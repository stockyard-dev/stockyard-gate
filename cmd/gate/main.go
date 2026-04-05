// Stockyard Gate — Auth proxy for internal tools.
// Add login, API keys, and rate limiting to any app. Zero code changes.
// Single binary. Embedded SQLite. Apache 2.0.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stockyard-dev/stockyard-gate/internal/server"
	"github.com/stockyard-dev/stockyard-gate/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("stockyard-gate %s\n", version)
		os.Exit(0)
	}

	log.SetFlags(log.Ltime | log.Lshortfile)

	port := 8780
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}

	upstream := os.Getenv("GATE_UPSTREAM")
	if upstream == "" {
		upstream = "http://localhost:3000"
		log.Printf("[gate] GATE_UPSTREAM not set, defaulting to %s", upstream)
	}

	adminKey := os.Getenv("GATE_ADMIN_KEY")
	if adminKey == "" {
		log.Printf("[gate] WARNING: GATE_ADMIN_KEY not set — admin API is locked")
	}

	rpm := 60
	if r := os.Getenv("GATE_RPM"); r != "" {
		if n, err := strconv.Atoi(r); err == nil {
			rpm = n
		}
	}

	dataDir := "./data"
	if d := strings.TrimSpace(os.Getenv("DATA_DIR")); d != "" {
		dataDir = d
	}

	corsOrigins := os.Getenv("GATE_CORS_ORIGINS") // e.g. "https://app.example.com,https://admin.example.com" or "*"

		limits := server.DefaultLimits()
	db, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	srv, err := server.New(db.Conn(), server.Config{
		Limits: limits,
		Port:        port,
		UpstreamURL: upstream,
		AdminKey:    adminKey,
		RPM:         rpm,
		CORSOrigins: corsOrigins,
	})
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	log.Printf("")
	log.Printf("  Stockyard Gate (Auth Proxy)")
	log.Printf("  Proxy:     http://localhost:%d → %s", port, upstream)
	log.Printf("  Admin API: http://localhost:%d/gate/api (requires GATE_ADMIN_KEY)", port)
	log.Printf("  Health:    http://localhost:%d/gate/health", port)
	log.Printf("  Dashboard: http://localhost:%d/ui", port)
	log.Printf("  Questions? hello@stockyard.dev")
	log.Printf("  Rate limit: %d req/min", rpm)
	if corsOrigins != "" {
		log.Printf("  CORS origins: %s", corsOrigins)
	}
	log.Printf("")

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}
