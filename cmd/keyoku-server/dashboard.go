// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

//go:build embed_dashboard

package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dashboard/*
var dashboardFS embed.FS

// dashboardHandler serves the embedded dashboard SPA.
// All requests under /dashboard/ are served from the embedded filesystem.
// Unknown paths fall back to index.html for client-side routing.
func dashboardHandler() http.Handler {
	// Strip the "dashboard" prefix from the embedded FS so files are at root
	sub, err := fs.Sub(dashboardFS, "dashboard")
	if err != nil {
		panic("failed to access embedded dashboard: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip /dashboard prefix for file lookup
		path := strings.TrimPrefix(r.URL.Path, "/dashboard")
		if path == "" {
			path = "/"
		}

		// Try to serve the file directly
		// For paths with extensions (assets), serve directly
		// For paths without extensions (SPA routes), serve index.html
		if path != "/" && !strings.Contains(path[strings.LastIndex(path, "/")+1:], ".") {
			// SPA fallback: serve index.html for client-side routes
			r.URL.Path = "/index.html"
		} else {
			r.URL.Path = path
		}

		fileServer.ServeHTTP(w, r)
	})
}

// dashboardConfigHandler returns the API configuration the dashboard needs.
// This endpoint is exempt from auth and provides the token so the dashboard
// can make authenticated API calls to the same server.
func dashboardConfigHandler(token string, port int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"api_url": "",
			"token":   token,
			"port":    port,
		})
	}
}
