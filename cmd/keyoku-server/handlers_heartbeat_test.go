// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyoku "github.com/keyoku-ai/keyoku-engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// newTestHandlers creates Handlers backed by an in-memory SQLite store.
func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	store, err := storage.NewSQLite(":memory:", 8)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	k := keyoku.NewForTesting(store)
	return NewHandlers(k, nil)
}

// --- HandleHeartbeatCheck ---

func TestHandleHeartbeatCheck_Valid(t *testing.T) {
	h := newTestHandlers(t)

	body := `{"entity_id":"user-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp heartbeatCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	// With an empty store, should_act may be true (first_contact) or false.
	// Just verify the response decoded successfully — both empty and populated are valid.
}

func TestHandleHeartbeatCheck_MissingEntityID(t *testing.T) {
	h := newTestHandlers(t)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatCheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleHeartbeatCheck_MalformedJSON(t *testing.T) {
	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/check", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatCheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- HandleHeartbeatContext ---

func TestHandleHeartbeatContext_Basic(t *testing.T) {
	h := newTestHandlers(t)

	body := `{"entity_id":"user-1","autonomy":"suggest"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/context", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatContext(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp heartbeatContextResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.TimePeriod == "" {
		t.Error("expected TimePeriod to be set")
	}
}

func TestHandleHeartbeatContext_WithAutonomy(t *testing.T) {
	h := newTestHandlers(t)

	body := `{"entity_id":"user-1","autonomy":"act"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/context", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatContext(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// --- HandleRecordHeartbeatMessage ---

func TestHandleRecordHeartbeatMessage_Valid(t *testing.T) {
	h := newTestHandlers(t)

	body := `{"entity_id":"user-1","message":"Hey, checking in about that deadline."}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleRecordHeartbeatMessage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}
	if resp["id"] == "" {
		t.Error("expected non-empty id in response")
	}
}

func TestHandleRecordHeartbeatMessage_MissingMessage(t *testing.T) {
	h := newTestHandlers(t)

	body := `{"entity_id":"user-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleRecordHeartbeatMessage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
