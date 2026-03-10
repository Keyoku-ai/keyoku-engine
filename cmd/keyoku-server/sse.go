// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	keyoku "github.com/keyoku-ai/keyoku-engine"
)

// SSEHub manages SSE client connections and broadcasts keyoku events.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan []byte]bool
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan []byte]bool),
	}
}

// AddClient registers a new SSE client.
func (h *SSEHub) AddClient() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.clients[ch] = true
	h.mu.Unlock()
	return ch
}

// RemoveClient unregisters an SSE client.
func (h *SSEHub) RemoveClient(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	close(ch)
	h.mu.Unlock()
}

// Broadcast sends data to all connected SSE clients.
func (h *SSEHub) Broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- data:
		default:
			// Client buffer full, skip
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// sseEventPayload is the JSON structure sent over SSE.
type sseEventPayload struct {
	Type      string         `json:"type"`
	EntityID  string         `json:"entity_id"`
	AgentID   string         `json:"agent_id,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Timestamp string         `json:"timestamp"`
}

// BridgeKeyokuEvents registers an OnAnyEvent handler that forwards events to SSE clients.
func (h *SSEHub) BridgeKeyokuEvents(k *keyoku.Keyoku) {
	k.OnAnyEvent(func(e keyoku.Event) {
		payload := sseEventPayload{
			Type:      string(e.Type),
			EntityID:  e.EntityID,
			AgentID:   e.AgentID,
			Data:      e.Data,
			Timestamp: e.Timestamp.Format("2006-01-02T15:04:05.000Z"),
		}

		data, err := json.Marshal(payload)
		if err != nil {
			return
		}

		h.Broadcast(data)
	})
}

// HandleSSE is the HTTP handler for the SSE endpoint.
func (h *SSEHub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.AddClient()
	defer h.RemoveClient(ch)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
