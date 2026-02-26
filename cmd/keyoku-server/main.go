package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-embedded"
)

func main() {
	configPath := flag.String("config", "", "path to config JSON file")
	port := flag.Int("port", 0, "override port (default: 18900)")
	dbPath := flag.String("db", "", "override database path")
	flag.Parse()

	// Load config
	cfg, err := LoadServerConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// CLI flag overrides
	if *port > 0 {
		cfg.Port = *port
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	// Env var port override
	if v := os.Getenv("KEYOKU_PORT"); v != "" && *port == 0 {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}

	// Initialize Keyoku
	k, err := keyoku.New(cfg.ToKeyokuConfig())
	if err != nil {
		log.Fatalf("failed to initialize keyoku: %v", err)
	}
	defer k.Close()

	// Create SSE hub and bridge events
	hub := NewSSEHub()
	hub.BridgeKeyokuEvents(k)

	// Create handlers
	handlers := NewHandlers(k, hub)

	// Build router
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/v1/health", handlers.HandleHealth)

	// Memory CRUD
	mux.HandleFunc("POST /api/v1/remember", handlers.HandleRemember)
	mux.HandleFunc("POST /api/v1/search", handlers.HandleSearch)
	mux.HandleFunc("GET /api/v1/memories", handlers.HandleListMemories)
	mux.HandleFunc("GET /api/v1/memories/", handlers.HandleGetMemory)
	mux.HandleFunc("DELETE /api/v1/memories/", handlers.HandleDeleteMemory)
	mux.HandleFunc("DELETE /api/v1/memories", handlers.HandleDeleteAllMemories)

	// Stats
	mux.HandleFunc("GET /api/v1/stats/", handlers.HandleStats)

	// Heartbeat
	mux.HandleFunc("POST /api/v1/heartbeat/check", handlers.HandleHeartbeatCheck)

	// Watcher
	mux.HandleFunc("POST /api/v1/watcher/start", handlers.HandleWatcherStart)
	mux.HandleFunc("POST /api/v1/watcher/stop", handlers.HandleWatcherStop)
	mux.HandleFunc("POST /api/v1/watcher/watch", handlers.HandleWatcherWatch)
	mux.HandleFunc("POST /api/v1/watcher/unwatch", handlers.HandleWatcherUnwatch)

	// Schedule
	mux.HandleFunc("POST /api/v1/schedule/ack", handlers.HandleScheduleAck)
	mux.HandleFunc("GET /api/v1/scheduled", handlers.HandleListScheduled)
	mux.HandleFunc("PUT /api/v1/memories/{id}/tags", handlers.HandleUpdateTags)

	// Teams
	mux.HandleFunc("POST /api/v1/teams", handlers.HandleCreateTeam)
	mux.HandleFunc("GET /api/v1/teams/", handlers.HandleGetTeam)
	mux.HandleFunc("DELETE /api/v1/teams/", handlers.HandleDeleteTeam)
	mux.HandleFunc("POST /api/v1/teams/{id}/members", handlers.HandleAddTeamMember)
	mux.HandleFunc("GET /api/v1/teams/{id}/members", handlers.HandleListTeamMembers)
	mux.HandleFunc("DELETE /api/v1/teams/{id}/members/{agent_id}", handlers.HandleRemoveTeamMember)

	// SSE events
	mux.HandleFunc("GET /api/v1/events", hub.HandleSSE)

	// CORS middleware
	handler := corsMiddleware(mux)

	// Create server
	addr := fmt.Sprintf(":%d", cfg.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disabled for SSE
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("keyoku-server listening on %s", addr)
		log.Printf("  db: %s", cfg.DBPath)
		log.Printf("  provider: %s (%s)", cfg.ExtractionProvider, cfg.ExtractionModel)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	log.Println("keyoku-server stopped")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Log requests (skip health checks for cleaner logs)
		if !strings.HasSuffix(r.URL.Path, "/health") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}

		next.ServeHTTP(w, r)
	})
}
