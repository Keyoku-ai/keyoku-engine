// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

//go:build !embed_dashboard

package main

import (
	"encoding/json"
	"net/http"
)

// dashboardHandler returns a handler that tells the user the dashboard is not embedded.
func dashboardHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><h2>Dashboard not embedded</h2><p>Build with <code>-tags embed_dashboard</code> to include it.</p></body></html>`))
	})
}

// dashboardConfigHandler returns the API configuration.
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
