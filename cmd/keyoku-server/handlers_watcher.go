// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"net/http"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-engine"
)

// HandleWatcherStart starts the proactive watcher.
func (h *Handlers) HandleWatcherStart(w http.ResponseWriter, r *http.Request) {
	var req watcherStartRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.EntityIDs) == 0 {
		writeError(w, http.StatusBadRequest, "entity_ids is required")
		return
	}
	for _, eid := range req.EntityIDs {
		if err := validateEntityID(eid); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	interval := 10 * time.Second
	if req.IntervalMs > 0 {
		interval = time.Duration(req.IntervalMs) * time.Millisecond
	}

	cfg := keyoku.WatcherConfig{
		Interval:  interval,
		EntityIDs: req.EntityIDs,
		Adaptive:  req.Adaptive,
	}

	// Adaptive interval settings
	if req.BaseIntervalMs > 0 {
		cfg.BaseInterval = time.Duration(req.BaseIntervalMs) * time.Millisecond
	}
	if req.MinIntervalMs > 0 {
		cfg.MinInterval = time.Duration(req.MinIntervalMs) * time.Millisecond
	}
	if req.MaxIntervalMs > 0 {
		cfg.MaxInterval = time.Duration(req.MaxIntervalMs) * time.Millisecond
	}

	// Delivery config
	if req.Delivery != nil && req.Delivery.Method != "" {
		dc := &keyoku.DeliveryConfig{
			Method:    req.Delivery.Method,
			Command:   req.Delivery.Command,
			Channel:   req.Delivery.Channel,
			Recipient: req.Delivery.Recipient,
			SessionID: req.Delivery.SessionID,
		}
		if req.Delivery.TimeoutMs > 0 {
			dc.Timeout = time.Duration(req.Delivery.TimeoutMs) * time.Millisecond
		}
		cfg.Delivery = dc
	}

	h.k.StartWatcher(cfg)

	mode := "fixed"
	if req.Adaptive {
		mode = "adaptive"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "started",
		"mode":     mode,
		"interval": interval.String(),
		"entities": req.EntityIDs,
		"delivery": req.Delivery != nil,
	})
}

// HandleWatcherStatus returns the current watcher state including next tick time and factors.
func (h *Handlers) HandleWatcherStatus(w http.ResponseWriter, r *http.Request) {
	watcher := h.k.Watcher()
	if watcher == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"running":  false,
			"adaptive": false,
			"entities": []string{},
		})
		return
	}
	writeJSON(w, http.StatusOK, watcher.Status())
}

// HandleWatcherStop stops the proactive watcher.
func (h *Handlers) HandleWatcherStop(w http.ResponseWriter, r *http.Request) {
	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// HandleWatcherHistory returns the tick history ring buffer for auditing.
func (h *Handlers) HandleWatcherHistory(w http.ResponseWriter, r *http.Request) {
	watcher := h.k.Watcher()
	if watcher == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ticks": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticks": watcher.History(),
	})
}

// HandleWatcherWatch adds an entity to the watch list.
func (h *Handlers) HandleWatcherWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Watch(req.EntityID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "watching", "entity_id": req.EntityID})
}

// HandleWatcherUnwatch removes an entity from the watch list.
func (h *Handlers) HandleWatcherUnwatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Unwatch(req.EntityID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "unwatched", "entity_id": req.EntityID})
}
