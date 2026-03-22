// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyoku "github.com/keyoku-ai/keyoku-engine"
	"github.com/keyoku-ai/keyoku-engine/llm"
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

type testHeartbeatLLMProvider struct {
	analyzeHeartbeatFn func(context.Context, llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error)
}

func (p *testHeartbeatLLMProvider) ExtractMemories(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
	return &llm.ExtractionResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ConsolidateMemories(_ context.Context, _ llm.ConsolidationRequest) (*llm.ConsolidationResponse, error) {
	return &llm.ConsolidationResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ExtractWithSchema(_ context.Context, _ llm.CustomExtractionRequest) (*llm.CustomExtractionResponse, error) {
	return &llm.CustomExtractionResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ExtractState(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
	return &llm.StateExtractionResponse{}, nil
}
func (p *testHeartbeatLLMProvider) DetectConflict(_ context.Context, _ llm.ConflictCheckRequest) (*llm.ConflictCheckResponse, error) {
	return &llm.ConflictCheckResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ReEvaluateImportance(_ context.Context, _ llm.ImportanceReEvalRequest) (*llm.ImportanceReEvalResponse, error) {
	return &llm.ImportanceReEvalResponse{}, nil
}
func (p *testHeartbeatLLMProvider) PrioritizeActions(_ context.Context, _ llm.ActionPriorityRequest) (*llm.ActionPriorityResponse, error) {
	return &llm.ActionPriorityResponse{}, nil
}
func (p *testHeartbeatLLMProvider) AnalyzeHeartbeatContext(ctx context.Context, req llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
	if p.analyzeHeartbeatFn != nil {
		return p.analyzeHeartbeatFn(ctx, req)
	}
	return &llm.HeartbeatAnalysisResponse{}, nil
}
func (p *testHeartbeatLLMProvider) SummarizeGraph(_ context.Context, _ llm.GraphSummaryRequest) (*llm.GraphSummaryResponse, error) {
	return &llm.GraphSummaryResponse{}, nil
}
func (p *testHeartbeatLLMProvider) RerankMemories(_ context.Context, _ llm.RerankRequest) (*llm.RerankResponse, error) {
	return &llm.RerankResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ExtractMemoriesCore(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
	return &llm.ExtractionResponse{}, nil
}
func (p *testHeartbeatLLMProvider) ExtractGraph(_ context.Context, _ llm.ExtractionRequest) (*llm.GraphExtractionResponse, error) {
	return &llm.GraphExtractionResponse{}, nil
}
func (p *testHeartbeatLLMProvider) IsLite() bool  { return false }
func (p *testHeartbeatLLMProvider) Name() string  { return "test" }
func (p *testHeartbeatLLMProvider) Model() string { return "test-model" }

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

func TestHandleHeartbeatContext_DeveloperTraceVerbosity(t *testing.T) {
	tests := []struct {
		name      string
		verbosity string
		wantTrace bool
	}{
		{name: "conversational omitted", verbosity: "conversational"},
		{name: "standard omitted", verbosity: "standard"},
		{name: "detailed populated", verbosity: "detailed", wantTrace: true},
		{name: "debug populated", verbosity: "debug", wantTrace: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newTestHandlersWithStore(t)
			if err := store.CreateMemory(context.Background(), &storage.Memory{
				ID:         "plan-1",
				EntityID:   "user-1",
				Content:    "Ship release",
				Type:       storage.TypePlan,
				State:      storage.StateActive,
				Importance: 0.9,
				Confidence: 0.9,
				Stability:  60,
			}); err != nil {
				t.Fatalf("failed to seed plan memory: %v", err)
			}

			h.k.SetProvider(&testHeartbeatLLMProvider{
				analyzeHeartbeatFn: func(_ context.Context, _ llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
					return &llm.HeartbeatAnalysisResponse{
						ShouldAct:   true,
						ActionBrief: "Follow up",
						Urgency:     "medium",
						Autonomy:    "suggest",
						UserFacing:  "Follow up on the release.",
					}, nil
				},
			})

			body := `{"entity_id":"user-1","autonomy":"suggest","analyze":true,"verbosity":"` + tt.verbosity + `"}`
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

			if resp.Analysis == nil {
				t.Fatal("expected analysis in response")
			}
			if tt.wantTrace {
				if resp.DeveloperTrace == nil {
					t.Fatal("expected developer_trace")
				}
				if resp.DeveloperTrace.CooldownState == "" {
					t.Error("expected cooldown_state to be populated")
				}
				if resp.DeveloperTrace.ConfluenceThreshold == 0 {
					t.Error("expected confluence_threshold to be populated")
				}
				return
			}
			if resp.DeveloperTrace != nil {
				t.Fatalf("developer_trace = %+v, want nil", resp.DeveloperTrace)
			}
		})
	}
}

func TestHandleHeartbeatContext_UsesActualSignalCount(t *testing.T) {
	h, store := newTestHandlersWithStore(t)
	if err := store.CreateMemory(context.Background(), &storage.Memory{
		ID:         "plan-1",
		EntityID:   "user-1",
		Content:    "Ship release",
		Type:       storage.TypePlan,
		State:      storage.StateActive,
		Importance: 0.9,
		Confidence: 0.9,
		Stability:  60,
	}); err != nil {
		t.Fatalf("failed to seed plan memory: %v", err)
	}

	var capturedReq llm.HeartbeatAnalysisRequest
	h.k.SetProvider(&testHeartbeatLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, req llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			capturedReq = req
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct:   true,
				ActionBrief: "Follow up",
				Urgency:     "medium",
				Autonomy:    "suggest",
				UserFacing:  "Follow up on the release.",
			}, nil
		},
	})

	body := `{"entity_id":"user-1","autonomy":"suggest","analyze":true,"verbosity":"debug"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heartbeat/context", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleHeartbeatContext(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if capturedReq.SignalCount != 1 {
		t.Fatalf("SignalCount = %d, want 1 active signal", capturedReq.SignalCount)
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
