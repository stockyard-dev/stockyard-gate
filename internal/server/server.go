package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stockyard-dev/stockyard-gate/internal/store"
)

type Server struct {
	db       *sql.DB
	mux      *http.ServeMux
	port     int
	upstream *url.URL
	proxy    *httputil.ReverseProxy
	adminKey string
	limiter  *RateLimiter
	srv      *http.Server
}

type Config struct {
	Port        int
	UpstreamURL string
	AdminKey    string
	RPM         int // requests per minute (0 = disabled)
}

// RateLimiter tracks per-key request counts.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rpm     int
}

type bucket struct {
	count    int
	windowAt time.Time
}

func NewRateLimiter(rpm int) *RateLimiter {
	return &RateLimiter{buckets: make(map[string]*bucket), rpm: rpm}
}

func (rl *RateLimiter) Allow(key string) bool {
	if rl.rpm <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok || now.Sub(b.windowAt) > time.Minute {
		rl.buckets[key] = &bucket{count: 1, windowAt: now}
		return true
	}
	if b.count >= rl.rpm {
		return false
	}
	b.count++
	return true
}

func New(db *sql.DB, cfg Config) (*Server, error) {
	upstream, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[proxy] upstream error: %v", err)
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"upstream unavailable"}`))
	}

	mux := http.NewServeMux()
	s := &Server{
		db: db, mux: mux, port: cfg.Port,
		upstream: upstream, proxy: proxy,
		adminKey: cfg.AdminKey,
		limiter:  NewRateLimiter(cfg.RPM),
	}
	s.registerRoutes()
	return s, nil
}

func (s *Server) registerRoutes() {
	// Admin API (requires admin key)
	s.mux.HandleFunc("GET /gate/api/users", s.adminOnly(s.handleListUsers))
	s.mux.HandleFunc("POST /gate/api/users", s.adminOnly(s.handleCreateUser))
	s.mux.HandleFunc("DELETE /gate/api/users/{id}", s.adminOnly(s.handleDeleteUser))
	s.mux.HandleFunc("GET /gate/api/keys", s.adminOnly(s.handleListKeys))
	s.mux.HandleFunc("POST /gate/api/keys", s.adminOnly(s.handleCreateKey))
	s.mux.HandleFunc("DELETE /gate/api/keys/{id}", s.adminOnly(s.handleDeleteKey))
	s.mux.HandleFunc("GET /gate/api/logs", s.adminOnly(s.handleListLogs))
	s.mux.HandleFunc("GET /gate/api/stats", s.adminOnly(s.handleStats))

	// Health (no auth)
	s.mux.HandleFunc("GET /gate/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	// Everything else goes through auth + proxy
	s.mux.HandleFunc("/", s.handleProxy)
}

func (s *Server) Start() error {
	s.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}
	log.Printf("[gate] listening on :%d → %s", s.port, s.upstream)
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// ──────────────────────────────────────────────────────────────────────
// Auth + Proxy Handler
// ──────────────────────────────────────────────────────────────────────

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sourceIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		sourceIP = strings.Split(fwd, ",")[0]
	}

	// Check API key auth
	var keyPrefix string
	var userID int
	apiKey := extractAPIKey(r)
	if apiKey != "" {
		hash := store.HashKey(apiKey)
		var enabled int
		var role string
		err := s.db.QueryRow("SELECT id, key_prefix, role, enabled FROM api_keys WHERE key_hash = ?", hash).
			Scan(&userID, &keyPrefix, &role, &enabled)
		if err != nil || enabled != 1 {
			s.logAccess(r.Method, r.URL.Path, 401, sourceIP, 0, "", int(time.Since(start).Milliseconds()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"invalid API key"}`))
			return
		}
		// Update last_used
		s.db.Exec("UPDATE api_keys SET last_used = datetime('now') WHERE key_hash = ?", hash)
	} else {
		// Check session cookie
		cookie, err := r.Cookie("gate_session")
		if err == nil {
			var uid int
			var expires string
			err := s.db.QueryRow("SELECT user_id, expires_at FROM sessions WHERE id = ?", cookie.Value).Scan(&uid, &expires)
			if err == nil {
				if t, err := time.Parse("2006-01-02 15:04:05", expires); err == nil && t.After(time.Now()) {
					userID = uid
				}
			}
		}
	}

	// No auth found
	if apiKey == "" && userID == 0 {
		s.logAccess(r.Method, r.URL.Path, 401, sourceIP, 0, "", int(time.Since(start).Milliseconds()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"authentication required","methods":["API key (Authorization: Bearer sk-gate-...)","Session cookie"]}`))
		return
	}

	// Rate limiting
	limitKey := keyPrefix
	if limitKey == "" {
		limitKey = sourceIP
	}
	if !s.limiter.Allow(limitKey) {
		s.logAccess(r.Method, r.URL.Path, 429, sourceIP, userID, keyPrefix, int(time.Since(start).Milliseconds()))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":"rate limit exceeded"}`))
		return
	}

	// Proxy to upstream
	r.Host = s.upstream.Host
	recorder := &statusRecorder{ResponseWriter: w, statusCode: 200}
	s.proxy.ServeHTTP(recorder, r)

	latency := int(time.Since(start).Milliseconds())
	s.logAccess(r.Method, r.URL.Path, recorder.statusCode, sourceIP, userID, keyPrefix, latency)
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func extractAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}
	return ""
}

func (s *Server) logAccess(method, path string, status int, ip string, userID int, keyPrefix string, latencyMs int) {
	s.db.Exec(`INSERT INTO access_log (method, path, status, source_ip, user_id, key_prefix, latency_ms) VALUES (?,?,?,?,?,?,?)`,
		method, path, status, ip, userID, keyPrefix, latencyMs)
}

// ──────────────────────────────────────────────────────────────────────
// Admin API
// ──────────────────────────────────────────────────────────────────────

func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if s.adminKey == "" || key != s.adminKey {
			writeJSON(w, 403, map[string]string{"error": "admin key required"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id, username, role, enabled, created_at FROM users ORDER BY created_at DESC")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var users []map[string]any
	for rows.Next() {
		var id, enabled int
		var username, role, created string
		if rows.Scan(&id, &username, &role, &enabled, &created) != nil {
			continue
		}
		users = append(users, map[string]any{
			"id": id, "username": username, "role": role,
			"enabled": enabled == 1, "created_at": created,
		})
	}
	if users == nil {
		users = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]string{"error": "username and password required"})
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	hash := store.HashPassword(req.Password)
	res, err := s.db.Exec("INSERT INTO users (username, password_hash, role) VALUES (?,?,?)", req.Username, hash, req.Role)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "username already exists"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id, "username": req.Username, "role": req.Role})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.db.Exec("DELETE FROM sessions WHERE user_id = ?", id)
	s.db.Exec("DELETE FROM users WHERE id = ?", id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query("SELECT id, key_prefix, name, role, rate_limit, enabled, created_at, last_used FROM api_keys ORDER BY created_at DESC")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	var keys []map[string]any
	for rows.Next() {
		var id, rateLimit, enabled int
		var prefix, name, role, created, lastUsed string
		if rows.Scan(&id, &prefix, &name, &role, &rateLimit, &enabled, &created, &lastUsed) != nil {
			continue
		}
		keys = append(keys, map[string]any{
			"id": id, "key_prefix": prefix, "name": name, "role": role,
			"rate_limit": rateLimit, "enabled": enabled == 1,
			"created_at": created, "last_used": lastUsed,
		})
	}
	if keys == nil {
		keys = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"keys": keys})
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		RateLimit int    `json:"rate_limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Role == "" {
		req.Role = "user"
	}

	key, hash := store.GenerateAPIKey()
	prefix := key[:16] + "..."

	_, err := s.db.Exec("INSERT INTO api_keys (key_hash, key_prefix, name, role, rate_limit) VALUES (?,?,?,?,?)",
		hash, prefix, req.Name, req.Role, req.RateLimit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	// Return the full key ONCE — it's never shown again
	writeJSON(w, 201, map[string]any{
		"key":        key,
		"key_prefix": prefix,
		"name":       req.Name,
		"role":       req.Role,
		"note":       "Save this key — it will not be shown again",
	})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.db.Exec("DELETE FROM api_keys WHERE id = ?", id)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	rows, err := s.db.Query(`SELECT method, path, status, source_ip, user_id, key_prefix, latency_ms, created_at
		FROM access_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var logs []map[string]any
	for rows.Next() {
		var method, path, ip, prefix, created string
		var status, userID, latency int
		if rows.Scan(&method, &path, &status, &ip, &userID, &prefix, &latency, &created) != nil {
			continue
		}
		logs = append(logs, map[string]any{
			"method": method, "path": path, "status": status, "source_ip": ip,
			"user_id": userID, "key_prefix": prefix, "latency_ms": latency, "created_at": created,
		})
	}
	if logs == nil {
		logs = []map[string]any{}
	}
	writeJSON(w, 200, map[string]any{"logs": logs, "count": len(logs)})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	var total24h, errors24h, users, keys int
	s.db.QueryRow("SELECT COUNT(*) FROM access_log WHERE created_at >= datetime('now', '-1 day')").Scan(&total24h)
	s.db.QueryRow("SELECT COUNT(*) FROM access_log WHERE created_at >= datetime('now', '-1 day') AND status >= 400").Scan(&errors24h)
	s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users)
	s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE enabled = 1").Scan(&keys)

	var avgLatency float64
	s.db.QueryRow("SELECT COALESCE(AVG(latency_ms), 0) FROM access_log WHERE created_at >= datetime('now', '-1 day')").Scan(&avgLatency)

	writeJSON(w, 200, map[string]any{
		"requests_24h":    total24h,
		"errors_24h":      errors24h,
		"avg_latency_ms":  avgLatency,
		"users":           users,
		"active_keys":     keys,
		"upstream":        s.upstream.String(),
	})
}

// Helpers

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// Ensure unused imports don't fail
