// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	keyoku "github.com/keyoku-ai/keyoku-engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

var testMemCounter atomic.Int64

// newTestHandlersWithStore creates test handlers and returns the underlying store
// so tests can seed data directly.
func newTestHandlersWithStore(t *testing.T) (*Handlers, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLite(":memory:", 8)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	k := keyoku.NewForTesting(store)
	return NewHandlers(k, nil), store
}

// seedTestMemory creates a memory directly in the store for testing.
func seedTestMemory(t *testing.T, store *storage.SQLiteStore, entityID, content string, importance float64) string {
	t.Helper()
	mem := &storage.Memory{
		ID:         fmt.Sprintf("test-mem-%03d", testMemCounter.Add(1)),
		EntityID:   entityID,
		Content:    content,
		Type:       storage.TypeContext,
		State:      storage.StateActive,
		Importance: importance,
		Confidence: 0.9,
		Stability:  60,
	}
	if err := store.CreateMemory(context.Background(), mem); err != nil {
		t.Fatalf("failed to seed memory: %v", err)
	}
	return mem.ID
}

// --- HandleUpdateMemory ---

func TestHandleUpdateMemory_Valid(t *testing.T) {
	h, store := newTestHandlersWithStore(t)
	memID := seedTestMemory(t, store, "user-1", "User likes Python", 0.7)

	updateBody := `{"content":"User prefers Go now","importance":0.9}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/memories/{id}", h.HandleUpdateMemory)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/memories/"+memID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp memoryJSON
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Content != "User prefers Go now" {
		t.Errorf("content = %q, want 'User prefers Go now'", resp.Content)
	}
	if resp.Importance != 0.9 {
		t.Errorf("importance = %f, want 0.9", resp.Importance)
	}
}

func TestHandleUpdateMemory_InvalidID(t *testing.T) {
	h, _ := newTestHandlersWithStore(t)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/memories/{id}", h.HandleUpdateMemory)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/memories/", strings.NewReader(`{"content":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Empty ID path value should get 405 (no route match) or 400
	if w.Code == http.StatusOK {
		t.Errorf("expected error status for empty ID, got 200")
	}
}

func TestHandleUpdateMemory_StateChange(t *testing.T) {
	h, store := newTestHandlersWithStore(t)
	memID := seedTestMemory(t, store, "user-1", "Test memory content", 0.5)

	updateBody := `{"state":"resolved"}`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/memories/{id}", h.HandleUpdateMemory)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/memories/"+memID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp memoryJSON
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.State != "resolved" {
		t.Errorf("state = %q, want 'resolved'", resp.State)
	}
}

// --- HandleResolveMemory ---

func TestHandleResolveMemory_Valid(t *testing.T) {
	h, store := newTestHandlersWithStore(t)
	memID := seedTestMemory(t, store, "user-1", "Fix auth bug in login", 0.8)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/memories/{id}/resolve", h.HandleResolveMemory)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories/"+memID+"/resolve", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp memoryJSON
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.State != "resolved" {
		t.Errorf("state = %q, want 'resolved'", resp.State)
	}
	if resp.Importance != 0.1 {
		t.Errorf("importance = %f, want 0.1", resp.Importance)
	}
}

func TestHandleResolveMemory_InvalidID(t *testing.T) {
	h, _ := newTestHandlersWithStore(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/memories/{id}/resolve", h.HandleResolveMemory)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories//resolve", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Empty ID should not match route or return 400
	if w.Code == http.StatusOK {
		t.Errorf("expected error status for empty ID, got 200")
	}
}

// Verify resolved memories can still be fetched from the store (not deleted)
func TestResolvedMemoryStillAccessible(t *testing.T) {
	h, store := newTestHandlersWithStore(t)
	memID := seedTestMemory(t, store, "user-1", "Old task to complete", 0.8)

	// Resolve it via the handler
	resolveMux := http.NewServeMux()
	resolveMux.HandleFunc("POST /api/v1/memories/{id}/resolve", h.HandleResolveMemory)
	resolveReq := httptest.NewRequest(http.MethodPost, "/api/v1/memories/"+memID+"/resolve", nil)
	resolveW := httptest.NewRecorder()
	resolveMux.ServeHTTP(resolveW, resolveReq)

	if resolveW.Code != http.StatusOK {
		t.Fatalf("resolve status = %d; body: %s", resolveW.Code, resolveW.Body.String())
	}

	// Verify the resolved response
	var resolveResp memoryJSON
	json.NewDecoder(resolveW.Body).Decode(&resolveResp)
	if resolveResp.State != "resolved" {
		t.Errorf("state = %q, want 'resolved'", resolveResp.State)
	}

	// Verify the memory still exists in the store (not deleted)
	mem, err := store.GetMemory(context.Background(), memID)
	if err != nil {
		t.Fatalf("failed to get memory from store: %v", err)
	}
	if mem == nil {
		t.Fatal("resolved memory was deleted from store, expected it to still exist")
	}
	if mem.State != storage.StateResolved {
		t.Errorf("store state = %q, want 'resolved'", mem.State)
	}
}
