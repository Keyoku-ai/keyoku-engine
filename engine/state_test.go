// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestStateManager_Register_Success(t *testing.T) {
	var created *storage.AgentState
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return nil, nil // not found
		},
		createAgentStateFn: func(_ context.Context, state *storage.AgentState) error {
			created = state
			return nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	schema := map[string]any{"mood": "string"}
	rules := map[string]any{"mood": []string{"happy", "sad"}}

	err := sm.Register(context.Background(), "entity-1", "agent-1", "mood-schema", schema, rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created == nil {
		t.Fatal("expected CreateAgentState to be called")
	}
	if created.EntityID != "entity-1" || created.AgentID != "agent-1" || created.SchemaName != "mood-schema" {
		t.Error("created state has wrong fields")
	}
}

func TestStateManager_Register_AlreadyExists(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{ID: "existing"}, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	err := sm.Register(context.Background(), "e", "a", "s", nil, nil)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestStateManager_Register_StoreError(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return nil, fmt.Errorf("db down")
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	err := sm.Register(context.Background(), "e", "a", "s", nil, nil)
	if err == nil {
		t.Fatal("expected error propagation")
	}
}

func TestStateManager_Update_Success(t *testing.T) {
	var updatedID string
	var updatedState map[string]any
	var historyLogged bool

	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{
				ID:               "state-1",
				EntityID:         "entity-1",
				AgentID:          "agent-1",
				SchemaName:       "mood",
				CurrentState:     map[string]any{"mood": "neutral"},
				SchemaDefinition: map[string]any{"mood": "string"},
			}, nil
		},
		updateAgentStateFn: func(_ context.Context, id string, newState map[string]any) error {
			updatedID = id
			updatedState = newState
			return nil
		},
		logAgentStateHistoryFn: func(_ context.Context, _ *storage.AgentStateHistory) error {
			historyLogged = true
			return nil
		},
	}
	provider := &mockProvider{
		extractStateFn: func(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
			return &llm.StateExtractionResponse{
				ChangedFields:  []string{"mood"},
				ExtractedState: map[string]any{"mood": "happy"},
				Confidence:     0.9,
				Reasoning:      "user expressed happiness",
			}, nil
		},
	}
	sm := NewStateManager(store, provider)
	result, err := sm.Update(context.Background(), "entity-1", "agent-1", "mood", "I'm feeling great!", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ChangedFields) != 1 || result.ChangedFields[0] != "mood" {
		t.Error("expected ChangedFields=[mood]")
	}
	if result.Confidence != 0.9 {
		t.Errorf("expected Confidence=0.9, got %f", result.Confidence)
	}
	if updatedID != "state-1" {
		t.Errorf("expected UpdateAgentState called with id state-1, got %s", updatedID)
	}
	if updatedState["mood"] != "happy" {
		t.Error("expected state updated to mood=happy")
	}
	if !historyLogged {
		t.Error("expected history to be logged")
	}
}

func TestStateManager_Update_NotRegistered(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return nil, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	_, err := sm.Update(context.Background(), "e", "a", "s", "content", nil)
	if err == nil {
		t.Fatal("expected error for unregistered state")
	}
}

func TestStateManager_Update_ValidationError(t *testing.T) {
	var updateCalled bool
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{ID: "state-1", CurrentState: map[string]any{}}, nil
		},
		updateAgentStateFn: func(_ context.Context, _ string, _ map[string]any) error {
			updateCalled = true
			return nil
		},
	}
	provider := &mockProvider{
		extractStateFn: func(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
			return &llm.StateExtractionResponse{
				ValidationError: "invalid field value",
			}, nil
		},
	}
	sm := NewStateManager(store, provider)
	result, err := sm.Update(context.Background(), "e", "a", "s", "content", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ValidationError != "invalid field value" {
		t.Error("expected validation error in result")
	}
	if updateCalled {
		t.Error("UpdateAgentState should NOT be called when validation fails")
	}
}

func TestStateManager_Update_NoChanges(t *testing.T) {
	var updateCalled bool
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{ID: "state-1", CurrentState: map[string]any{}}, nil
		},
		updateAgentStateFn: func(_ context.Context, _ string, _ map[string]any) error {
			updateCalled = true
			return nil
		},
	}
	provider := &mockProvider{
		extractStateFn: func(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
			return &llm.StateExtractionResponse{
				ChangedFields: []string{}, // no changes
			}, nil
		},
	}
	sm := NewStateManager(store, provider)
	_, err := sm.Update(context.Background(), "e", "a", "s", "content", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updateCalled {
		t.Error("UpdateAgentState should NOT be called when nothing changed")
	}
}

func TestStateManager_Update_ExtractionError(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{ID: "state-1", CurrentState: map[string]any{}}, nil
		},
	}
	provider := &mockProvider{
		extractStateFn: func(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
			return nil, fmt.Errorf("LLM timeout")
		},
	}
	sm := NewStateManager(store, provider)
	_, err := sm.Update(context.Background(), "e", "a", "s", "content", nil)
	if err == nil {
		t.Fatal("expected error propagation from LLM")
	}
}

func TestStateManager_Update_EmitsEvent(t *testing.T) {
	var emittedType string
	var emittedData map[string]any

	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{
				ID:           "state-1",
				EntityID:     "entity-1",
				AgentID:      "agent-1",
				SchemaName:   "mood",
				CurrentState: map[string]any{"mood": "neutral"},
			}, nil
		},
		updateAgentStateFn: func(_ context.Context, _ string, _ map[string]any) error {
			return nil
		},
		logAgentStateHistoryFn: func(_ context.Context, _ *storage.AgentStateHistory) error {
			return nil
		},
	}
	provider := &mockProvider{
		extractStateFn: func(_ context.Context, _ llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
			return &llm.StateExtractionResponse{
				ChangedFields:  []string{"mood"},
				ExtractedState: map[string]any{"mood": "happy"},
				Confidence:     0.9,
			}, nil
		},
	}
	sm := NewStateManager(store, provider)
	sm.SetEmitter(func(eventType, entityID, agentID, _ string, data map[string]any) {
		emittedType = eventType
		emittedData = data
	})

	_, err := sm.Update(context.Background(), "entity-1", "agent-1", "mood", "feeling great", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emittedType != "state.changed" {
		t.Errorf("expected event type 'state.changed', got %q", emittedType)
	}
	if emittedData["schema_name"] != "mood" {
		t.Error("expected schema_name in emitted data")
	}
}

func TestStateManager_Get_Exists(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{
				CurrentState: map[string]any{"mood": "happy"},
			}, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	state, err := sm.Get(context.Background(), "e", "a", "s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state["mood"] != "happy" {
		t.Error("expected mood=happy")
	}
}

func TestStateManager_Get_NotFound(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return nil, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	state, err := sm.Get(context.Background(), "e", "a", "s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != nil {
		t.Error("expected nil state for not found")
	}
}

func TestStateManager_History_Success(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return &storage.AgentState{ID: "state-1"}, nil
		},
		getAgentStateHistoryFn: func(_ context.Context, stateID string, limit int) ([]*storage.AgentStateHistory, error) {
			if stateID != "state-1" {
				t.Errorf("expected stateID state-1, got %s", stateID)
			}
			if limit != 5 {
				t.Errorf("expected limit 5, got %d", limit)
			}
			return []*storage.AgentStateHistory{{ID: "h1"}, {ID: "h2"}}, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	history, err := sm.History(context.Background(), "e", "a", "s", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}
}

func TestStateManager_History_NotFound(t *testing.T) {
	store := &mockStore{
		getAgentStateFn: func(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
			return nil, nil
		},
	}
	sm := NewStateManager(store, &mockProvider{})
	history, err := sm.History(context.Background(), "e", "a", "s", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if history != nil {
		t.Error("expected nil history for not found")
	}
}
