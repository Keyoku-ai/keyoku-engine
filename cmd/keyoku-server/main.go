// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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

	keyoku "github.com/keyoku-ai/keyoku-engine"
)

func main() {
	configPath := flag.String("config", "", "path to config JSON file")
	port := flag.Int("port", 0, "override port (default: 18900)")
	dbPath := flag.String("db", "", "override database path")
	flag.Parse()

	// Session token check — binary only works when launched by a host application
	sessionToken := os.Getenv("KEYOKU_SESSION_TOKEN")
	if sessionToken == "" {
		log.Fatal("keyoku-server requires KEYOKU_SESSION_TOKEN to be set.\n" +
			"This binary is designed to be launched by the a host application.\n" +
			"Set KEYOKU_SESSION_TOKEN to any value to start.")
	}

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

	// Auto-start watcher if configured
	if cfg.WatcherAutoStart != nil && *cfg.WatcherAutoStart {
		wcfg := keyoku.WatcherConfig{
			Interval: 5 * time.Minute,
		}

		// Parse entity IDs
		if cfg.WatcherEntityIDs != "" {
			for _, id := range strings.Split(cfg.WatcherEntityIDs, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					wcfg.EntityIDs = append(wcfg.EntityIDs, id)
				}
			}
		}
		if len(wcfg.EntityIDs) == 0 {
			wcfg.EntityIDs = []string{"default"}
		}

		// Adaptive mode
		if cfg.AdaptiveHeartbeat != nil && *cfg.AdaptiveHeartbeat {
			wcfg.Adaptive = true
			if cfg.WatcherBaseInterval > 0 {
				wcfg.BaseInterval = time.Duration(cfg.WatcherBaseInterval) * time.Millisecond
			} else {
				wcfg.BaseInterval = 5 * time.Minute
			}
			if cfg.WatcherMinInterval > 0 {
				wcfg.MinInterval = time.Duration(cfg.WatcherMinInterval) * time.Millisecond
			} else {
				wcfg.MinInterval = 1 * time.Minute
			}
			if cfg.WatcherMaxInterval > 0 {
				wcfg.MaxInterval = time.Duration(cfg.WatcherMaxInterval) * time.Millisecond
			} else {
				wcfg.MaxInterval = 30 * time.Minute
			}
		}

		// Delivery config
		if cfg.DeliveryMethod != "" {
			wcfg.Delivery = &keyoku.DeliveryConfig{
				Method:    cfg.DeliveryMethod,
				Command:   cfg.DeliveryCommand,
				Channel:   cfg.DeliveryChannel,
				Recipient: cfg.DeliveryRecipient,
				SessionID: cfg.DeliverySessionID,
			}
		}

		k.StartWatcher(wcfg)
		log.Printf("  watcher: auto-started (entities: %v, adaptive: %v, delivery: %v)",
			wcfg.EntityIDs, wcfg.Adaptive, wcfg.Delivery != nil)
	}

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
	mux.HandleFunc("POST /api/v1/seed", handlers.HandleSeedMemories)
	mux.HandleFunc("POST /api/v1/search", handlers.HandleSearch)
	mux.HandleFunc("GET /api/v1/memories", handlers.HandleListMemories)
	mux.HandleFunc("GET /api/v1/memories/", handlers.HandleGetMemory)
	mux.HandleFunc("DELETE /api/v1/memories/", handlers.HandleDeleteMemory)
	mux.HandleFunc("DELETE /api/v1/memories", handlers.HandleDeleteAllMemories)

	// Entities
	mux.HandleFunc("GET /api/v1/entities", handlers.HandleListEntities)

	// Stats
	mux.HandleFunc("GET /api/v1/stats", handlers.HandleGlobalStats) // global (no trailing slash)
	mux.HandleFunc("GET /api/v1/stats/", handlers.HandleStats)      // per-entity

	// Sampling (server-side representative sample)
	mux.HandleFunc("GET /api/v1/memories/sample", handlers.HandleSampleMemories)

	// Heartbeat
	mux.HandleFunc("POST /api/v1/heartbeat/check", handlers.HandleHeartbeatCheck)
	mux.HandleFunc("POST /api/v1/heartbeat/context", handlers.HandleHeartbeatContext)
	mux.HandleFunc("POST /api/v1/heartbeat/record-message", handlers.HandleRecordHeartbeatMessage)

	// Watcher
	mux.HandleFunc("GET /api/v1/watcher/status", handlers.HandleWatcherStatus)
	mux.HandleFunc("GET /api/v1/watcher/history", handlers.HandleWatcherHistory)
	mux.HandleFunc("POST /api/v1/watcher/start", handlers.HandleWatcherStart)
	mux.HandleFunc("POST /api/v1/watcher/stop", handlers.HandleWatcherStop)
	mux.HandleFunc("POST /api/v1/watcher/watch", handlers.HandleWatcherWatch)
	mux.HandleFunc("POST /api/v1/watcher/unwatch", handlers.HandleWatcherUnwatch)

	// Consolidation (lifecycle-triggered)
	mux.HandleFunc("POST /api/v1/consolidate", handlers.HandleConsolidate)

	// Schedule
	mux.HandleFunc("POST /api/v1/schedule", handlers.HandleCreateSchedule)
	mux.HandleFunc("POST /api/v1/schedule/ack", handlers.HandleScheduleAck)
	mux.HandleFunc("PUT /api/v1/schedule/", handlers.HandleUpdateSchedule)
	mux.HandleFunc("DELETE /api/v1/schedule/", handlers.HandleCancelSchedule)
	mux.HandleFunc("GET /api/v1/scheduled", handlers.HandleListScheduled)
	mux.HandleFunc("PUT /api/v1/memories/{id}/tags", handlers.HandleUpdateTags)
	mux.HandleFunc("PUT /api/v1/memories/{id}", handlers.HandleUpdateMemory)
	mux.HandleFunc("POST /api/v1/memories/{id}/resolve", handlers.HandleResolveMemory)

	// Teams
	mux.HandleFunc("POST /api/v1/teams", handlers.HandleCreateTeam)
	mux.HandleFunc("GET /api/v1/teams/", handlers.HandleGetTeam)
	mux.HandleFunc("DELETE /api/v1/teams/", handlers.HandleDeleteTeam)
	mux.HandleFunc("POST /api/v1/teams/{id}/members", handlers.HandleAddTeamMember)
	mux.HandleFunc("GET /api/v1/teams/{id}/members", handlers.HandleListTeamMembers)
	mux.HandleFunc("DELETE /api/v1/teams/{id}/members/{agent_id}", handlers.HandleRemoveTeamMember)

	// Graph
	mux.HandleFunc("GET /api/v1/graph/entities", handlers.HandleListGraphEntities)
	mux.HandleFunc("GET /api/v1/graph/relationships", handlers.HandleListRelationships)
	mux.HandleFunc("POST /api/v1/graph/traverse", handlers.HandleGraphTraverse)
	mux.HandleFunc("POST /api/v1/graph/path", handlers.HandleGraphPath)

	// SSE events
	mux.HandleFunc("GET /api/v1/events", hub.HandleSSE)

	// Dashboard (embedded SPA, no auth required)
	dashHandler := dashboardHandler()
	mux.Handle("/dashboard", http.RedirectHandler("/dashboard/", http.StatusMovedPermanently))
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard", dashHandler))
	mux.HandleFunc("GET /dashboard-config", dashboardConfigHandler(sessionToken, cfg.Port))

	// Build CORS allowlist
	allowedOrigins := buildCORSAllowlist()

	// Middleware stack: auth → CORS → router
	handler := authMiddleware(sessionToken, corsMiddleware(allowedOrigins, mux))

	// Create server
	addr := fmt.Sprintf(":%d", cfg.Port)
	readTimeout := readDurationEnv("KEYOKU_HTTP_READ_TIMEOUT", 30*time.Second)
	writeTimeout := readDurationEnv("KEYOKU_HTTP_WRITE_TIMEOUT", 120*time.Second)
	idleTimeout := readDurationEnv("KEYOKU_HTTP_IDLE_TIMEOUT", 120*time.Second)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("keyoku-server listening on %s", addr)
		log.Printf("  db: %s", cfg.DBPath)
		log.Printf("  provider: %s (%s)", cfg.ExtractionProvider, cfg.ExtractionModel)
		log.Printf("  dashboard: http://localhost:%d/dashboard/", cfg.Port)
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

// authMiddleware validates per-request bearer tokens using constant-time comparison.
// Health check endpoint is exempt to allow monitoring without credentials.
func authMiddleware(token string, next http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow health checks without auth
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow dashboard and its config without auth
		if strings.HasPrefix(r.URL.Path, "/dashboard") {
			next.ServeHTTP(w, r)
			return
		}

		// Allow CORS preflight without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		provided := []byte(strings.TrimPrefix(auth, "Bearer "))
		if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// buildCORSAllowlist builds the set of allowed CORS origins.
// Default: localhost origins. Override with KEYOKU_CORS_ORIGINS env var (comma-separated).
func readDurationEnv(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	log.Printf("invalid %s=%q; using default %s", name, v, fallback)
	return fallback
}

func buildCORSAllowlist() map[string]bool {
	allowed := map[string]bool{
		"http://localhost":      true,
		"http://localhost:3000": true,
		"http://localhost:5173": true,
		"http://localhost:8080": true,
		"http://127.0.0.1":      true,
		"http://127.0.0.1:3000": true,
		"http://127.0.0.1:5173": true,
		"http://127.0.0.1:8080": true,
		"https://sentai.dev":    true,
		"https://sentai.cloud":  true,
	}

	if origins := os.Getenv("KEYOKU_CORS_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowed[o] = true
			}
		}
	}

	return allowed
}

// corsMiddleware validates Origin against an allowlist instead of echoing any origin.
func corsMiddleware(allowedOrigins map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		// If origin is empty (same-origin) or not in allowlist, no CORS headers are set

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
