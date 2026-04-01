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
	db          *sql.DB
	mux         *http.ServeMux
	port        int
	upstream    *url.URL
	proxy       *httputil.ReverseProxy
	adminKey    string
	limiter     *RateLimiter
	srv         *http.Server
	corsOrigins []string // empty = CORS disabled
	limits      Limits
}

type Config struct {
	Port        int
	UpstreamURL string
	AdminKey    string
	RPM         int // requests per minute (0 = disabled)
	CORSOrigins string // comma-separated origins, "*" for all
	Limits      Limits
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
	var origins []string
	if cfg.CORSOrigins != "" {
		for _, o := range strings.Split(cfg.CORSOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				origins = append(origins, o)
			}
		}
	}
	s := &Server{
		db: db, mux: mux, port: cfg.Port,
		upstream: upstream, proxy: proxy,
		adminKey:    cfg.AdminKey,
		limiter:     NewRateLimiter(cfg.RPM),
		corsOrigins: origins,
		limits:      cfg.Limits,
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
	s.mux.HandleFunc("POST /gate/api/ip-rules", s.adminOnly(s.handleIPRules))
	s.mux.HandleFunc("GET /gate/api/admin-keys", s.adminOnly(s.handleAdminKeys))

	// Session auth (no admin key required)
	s.mux.HandleFunc("GET /gate/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /gate/login", s.handleLogin)
	s.mux.HandleFunc("POST /gate/logout", s.handleLogout)

	// Health (no auth)
	s.mux.HandleFunc("GET /ui", s.handleUI)
	s.mux.HandleFunc("GET /gate/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	// Everything else goes through CORS + auth + proxy
	s.mux.HandleFunc("/", s.withCORS(s.handleProxy))
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

	// IP allow/deny check (Pro feature — enforced when rules exist)
	if s.limits.IPAllowDeny {
		s.db.Exec(`CREATE TABLE IF NOT EXISTS ip_rules (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL, action TEXT NOT NULL, note TEXT DEFAULT '', created_at DATETIME DEFAULT (datetime('now')))`)
		var action string
		cleanIP := strings.Split(sourceIP, ":")[0]
		err := s.db.QueryRow("SELECT action FROM ip_rules WHERE ip = ? ORDER BY id DESC LIMIT 1", cleanIP).Scan(&action)
		if err == nil && action == "deny" {
			s.logAccess(r.Method, r.URL.Path, 403, sourceIP, 0, "", int(time.Since(start).Milliseconds()))
			writeJSON(w, 403, map[string]string{"error": "IP address blocked"})
			return
		}
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
		// Redirect browsers to login page; return JSON for API clients
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/html") {
			http.Redirect(w, r, "/gate/login", http.StatusFound)
			return
		}
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

func (s *Server) handleAdminKeys(w http.ResponseWriter, r *http.Request) {
	if !s.limits.MultipleAdminKeys {
		writeJSON(w, 402, map[string]string{"error": "multiple admin keys require Pro — upgrade at https://stockyard.dev/gate/", "upgrade": "https://stockyard.dev/gate/"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "note": "multiple admin keys active (Pro)"})
}

func (s *Server) handleIPRules(w http.ResponseWriter, r *http.Request) {
	if !s.limits.IPAllowDeny {
		writeJSON(w, 402, map[string]string{"error": "IP allow/deny lists require Pro — upgrade at https://stockyard.dev/gate/", "upgrade": "https://stockyard.dev/gate/"})
		return
	}
	if r.Method == "GET" {
		rows, err := s.db.Query("SELECT id, ip, action, note, created_at FROM ip_rules ORDER BY created_at DESC")
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		type IPRule struct {
			ID        int    `json:"id"`
			IP        string `json:"ip"`
			Action    string `json:"action"`
			Note      string `json:"note"`
			CreatedAt string `json:"created_at"`
		}
		var rules []IPRule
		for rows.Next() {
			var rule IPRule
			rows.Scan(&rule.ID, &rule.IP, &rule.Action, &rule.Note, &rule.CreatedAt)
			rules = append(rules, rule)
		}
		writeJSON(w, 200, map[string]any{"rules": rules})
		return
	}
	var req struct {
		IP     string `json:"ip"`
		Action string `json:"action"` // "allow" or "deny"
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
		writeJSON(w, 400, map[string]string{"error": "ip and action required"})
		return
	}
	if req.Action != "allow" && req.Action != "deny" {
		writeJSON(w, 400, map[string]string{"error": "action must be allow or deny"})
		return
	}
	// Ensure table exists
	s.db.Exec(`CREATE TABLE IF NOT EXISTS ip_rules (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL, action TEXT NOT NULL, note TEXT DEFAULT '', created_at DATETIME DEFAULT (datetime('now')))`)
		res, err := s.db.Exec("INSERT INTO ip_rules (ip, action, note) VALUES (?,?,?)", req.IP, req.Action, req.Note)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 200, map[string]any{"id": id, "ip": req.IP, "action": req.Action})
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
	if s.limits.MaxUsers > 0 {
		var cnt int
		s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&cnt)
		if LimitReached(s.limits.MaxUsers, cnt) {
			writeJSON(w, 402, map[string]string{"error": "free tier limit: " + strconv.Itoa(s.limits.MaxUsers) + " users max — upgrade to Pro", "upgrade": "https://stockyard.dev/gate/"})
			return
		}
	}
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

	// Per-key rate limits require Pro
	if req.RateLimit > 0 && !s.limits.PerRouteRateLimits {
		req.RateLimit = 0 // silently drop — global limit still applies
	}

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
	// CSV export requires Pro; raw JSON always available
	if r.URL.Query().Get("format") == "csv" && !s.limits.LogExport {
		writeJSON(w, 402, map[string]string{"error": "log export requires Pro — upgrade at https://stockyard.dev/gate/", "upgrade": "https://stockyard.dev/gate/"})
		return
	}
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

// ──────────────────────────────────────────────────────────────────────
// CORS Middleware
// ──────────────────────────────────────────────────────────────────────

func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(s.corsOrigins) == 0 {
			next(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		allowed := false
		for _, o := range s.corsOrigins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}
		if allowed && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next(w, r)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Session Login / Logout
// ──────────────────────────────────────────────────────────────────────

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeJSON(w, 400, map[string]string{"error": "username and password required"})
		return
	}

	var userID int
	var storedHash, role string
	var enabled int
	err := s.db.QueryRow("SELECT id, password_hash, role, enabled FROM users WHERE username = ?", req.Username).
		Scan(&userID, &storedHash, &role, &enabled)
	if err != nil || enabled != 1 {
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	if !store.CheckPassword(req.Password, storedHash) {
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}

	// Create session
	sessionID := store.GenerateSessionID()
	expires := time.Now().Add(24 * time.Hour)
	_, err = s.db.Exec("INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)",
		sessionID, userID, expires.Format("2006-01-02 15:04:05"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "failed to create session"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "gate_session",
		Value:    sessionID,
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})
	// Redirect browsers to dashboard; return JSON for API clients
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/html") || r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		http.Redirect(w, r, "/ui", http.StatusFound)
		return
	}
	writeJSON(w, 200, map[string]any{"user_id": userID, "username": req.Username, "role": role})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("gate_session")
	if err == nil {
		s.db.Exec("DELETE FROM sessions WHERE id = ?", cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "gate_session",
		Value:   "",
		Expires: time.Unix(0, 0),
		Path:    "/",
	})
	writeJSON(w, 200, map[string]string{"status": "logged out"})
}

// Helpers

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// Ensure unused imports don't fail

// handleLoginPage serves the HTML login form for browser-based access.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginPageHTML))
}

const loginPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>Gate — Sign In</title>
<link href="https://fonts.googleapis.com/css2?family=Libre+Baskerville:wght@400;700&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">
<style>
:root{--bg:#1a1410;--bg2:#241e18;--bg3:#2e261e;--rust:#c45d2c;--rust-light:#e8753a;--leather:#a0845c;--cream:#f0e6d3;--cream-dim:#bfb5a3;--gold:#d4a843;--green:#5ba86e;--red:#c0392b;--font-serif:'Libre Baskerville',Georgia,serif;--font-mono:'JetBrains Mono',monospace}
*{margin:0;padding:0;box-sizing:border-box}
body{background:var(--bg);color:var(--cream);font-family:var(--font-serif);min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:2rem}
.card{background:var(--bg2);border:1px solid var(--bg3);border-top:2px solid var(--rust);max-width:400px;width:100%;padding:2.5rem}
.brand{display:flex;align-items:center;gap:.6rem;margin-bottom:2rem}
.brand-logo svg{display:block}
.brand-text{font-family:var(--font-mono);font-size:.8rem;color:var(--leather);letter-spacing:3px;text-transform:uppercase}
.brand-product{font-family:var(--font-mono);font-size:.8rem;color:var(--cream);margin-left:.3rem}
h1{font-size:1.2rem;margin-bottom:.4rem}
.sub{font-family:var(--font-mono);font-size:.72rem;color:var(--leather);margin-bottom:2rem}
.field{margin-bottom:1.2rem}
label{display:block;font-family:var(--font-mono);font-size:.65rem;letter-spacing:2px;text-transform:uppercase;color:var(--leather);margin-bottom:.4rem}
input{width:100%;background:var(--bg3);border:1px solid var(--bg3);color:var(--cream);font-family:var(--font-mono);font-size:.85rem;padding:.65rem .8rem;outline:none;transition:border-color .15s}
input:focus{border-color:var(--leather)}
.btn{width:100%;background:var(--rust);color:var(--cream);border:none;font-family:var(--font-mono);font-size:.85rem;padding:.75rem;cursor:pointer;transition:background .15s;margin-top:.5rem}
.btn:hover{background:var(--rust-light)}
.error{background:#2a1a1a;border-left:3px solid var(--red);padding:.7rem 1rem;font-family:var(--font-mono);font-size:.78rem;color:#e57373;margin-bottom:1rem;display:none}
.divider{border-top:1px solid var(--bg3);margin:1.5rem 0;position:relative}
.divider-text{position:absolute;top:-9px;left:50%;transform:translateX(-50%);background:var(--bg2);padding:0 .8rem;font-family:var(--font-mono);font-size:.65rem;color:var(--leather)}
.api-note{font-family:var(--font-mono);font-size:.7rem;color:var(--leather);line-height:1.6}
code{background:var(--bg3);padding:.1rem .4rem;color:var(--cream-dim)}
</style>
</head>
<body>
<div class="card">
  <div class="brand">
    <div class="brand-logo">
      <svg viewBox="0 0 64 64" width="28" height="28" fill="none">
        <rect x="8" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/>
        <rect x="28" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/>
        <rect x="48" y="8" width="8" height="48" rx="2.5" fill="#e8753a"/>
        <rect x="8" y="27" width="48" height="7" rx="2.5" fill="#c4a87a"/>
      </svg>
    </div>
    <span class="brand-text">Stockyard <span class="brand-product">/ Gate</span></span>
  </div>

  <h1>Sign in</h1>
  <p class="sub">Protected by Gate &mdash; auth proxy for internal tools</p>

  <div class="error" id="err"></div>

  <div class="field">
    <label>Username</label>
    <input type="text" id="username" autocomplete="username" autofocus placeholder="alice">
  </div>
  <div class="field">
    <label>Password</label>
    <input type="password" id="password" autocomplete="current-password" placeholder="••••••••">
  </div>
  <button class="btn" onclick="login()">Sign in &rarr;</button>

  <div class="divider"><span class="divider-text">or use an API key</span></div>
  <p class="api-note">
    For programmatic access, pass a bearer token:<br>
    <code>Authorization: Bearer sk-gate-...</code>
  </p>
</div>

<script>
document.addEventListener('keydown', function(e) {
  if (e.key === 'Enter') login();
});

async function login() {
  var username = document.getElementById('username').value.trim();
  var password = document.getElementById('password').value;
  var errEl = document.getElementById('err');
  errEl.style.display = 'none';

  if (!username || !password) {
    errEl.textContent = 'Username and password required.';
    errEl.style.display = 'block';
    return;
  }

  try {
    var r = await fetch('/gate/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
      body: JSON.stringify({username: username, password: password})
    });
    if (r.ok) {
      window.location.href = '/ui';
    } else {
      var d = await r.json().catch(function(){ return {}; });
      errEl.textContent = d.error || 'Invalid credentials.';
      errEl.style.display = 'block';
      document.getElementById('password').value = '';
    }
  } catch(e) {
    errEl.textContent = 'Network error — please try again.';
    errEl.style.display = 'block';
  }
}
</script>
</body>
</html>`
