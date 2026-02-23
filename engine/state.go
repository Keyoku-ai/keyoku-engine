package engine

import (
	"context"
	"fmt"

	"github.com/keyoku-ai/keyoku-embedded/llm"
	"github.com/keyoku-ai/keyoku-embedded/storage"
)

// StateManager handles agent state extraction and persistence.
type StateManager struct {
	store    storage.Store
	provider llm.Provider
}

// NewStateManager creates a new state manager.
func NewStateManager(store storage.Store, provider llm.Provider) *StateManager {
	return &StateManager{store: store, provider: provider}
}

// StateUpdateResult contains the result of a state update.
type StateUpdateResult struct {
	ChangedFields   []string       `json:"changed_fields"`
	NewState        map[string]any `json:"new_state"`
	Confidence      float64        `json:"confidence"`
	Reasoning       string         `json:"reasoning"`
	SuggestedAction string         `json:"suggested_action"`
	ValidationError string         `json:"validation_error"`
}

// Register creates a new agent state schema registration.
func (sm *StateManager) Register(ctx context.Context, entityID, agentID, schemaName string, schema, transitionRules map[string]any) error {
	existing, err := sm.store.GetAgentState(ctx, entityID, agentID, schemaName)
	if err != nil {
		return fmt.Errorf("failed to check existing state: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("agent state already registered: %s/%s/%s", entityID, agentID, schemaName)
	}

	state := &storage.AgentState{
		EntityID:         entityID,
		AgentID:          agentID,
		SchemaName:       schemaName,
		CurrentState:     map[string]any{},
		SchemaDefinition: schema,
		TransitionRules:  transitionRules,
	}
	return sm.store.CreateAgentState(ctx, state)
}

// Update processes content through LLM to extract state changes.
func (sm *StateManager) Update(ctx context.Context, entityID, agentID, schemaName, content string, conversationCtx []string) (*StateUpdateResult, error) {
	agentState, err := sm.store.GetAgentState(ctx, entityID, agentID, schemaName)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent state: %w", err)
	}
	if agentState == nil {
		return nil, fmt.Errorf("agent state not registered: %s/%s/%s", entityID, agentID, schemaName)
	}

	resp, err := sm.provider.ExtractState(ctx, llm.StateExtractionRequest{
		Content:         content,
		Schema:          agentState.SchemaDefinition,
		SchemaName:      schemaName,
		CurrentState:    agentState.CurrentState,
		TransitionRules: agentState.TransitionRules,
		ConversationCtx: conversationCtx,
		AgentID:         agentID,
	})
	if err != nil {
		return nil, fmt.Errorf("state extraction failed: %w", err)
	}

	result := &StateUpdateResult{
		ChangedFields:   resp.ChangedFields,
		NewState:        resp.ExtractedState,
		Confidence:      resp.Confidence,
		Reasoning:       resp.Reasoning,
		SuggestedAction: resp.SuggestedAction,
		ValidationError: resp.ValidationError,
	}

	// Don't persist if validation failed
	if resp.ValidationError != "" {
		return result, nil
	}

	// Don't persist if nothing changed
	if len(resp.ChangedFields) == 0 {
		return result, nil
	}

	// Log history before updating
	sm.store.LogAgentStateHistory(ctx, &storage.AgentStateHistory{
		StateID:        agentState.ID,
		PreviousState:  agentState.CurrentState,
		NewState:       resp.ExtractedState,
		ChangedFields:  storage.StringSlice(resp.ChangedFields),
		TriggerContent: content,
		Confidence:     resp.Confidence,
		Reasoning:      resp.Reasoning,
	})

	// Update the state
	if err := sm.store.UpdateAgentState(ctx, agentState.ID, resp.ExtractedState); err != nil {
		return nil, fmt.Errorf("failed to update agent state: %w", err)
	}

	return result, nil
}

// Get retrieves the current state.
func (sm *StateManager) Get(ctx context.Context, entityID, agentID, schemaName string) (map[string]any, error) {
	state, err := sm.store.GetAgentState(ctx, entityID, agentID, schemaName)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return state.CurrentState, nil
}

// History retrieves state change history.
func (sm *StateManager) History(ctx context.Context, entityID, agentID, schemaName string, limit int) ([]*storage.AgentStateHistory, error) {
	state, err := sm.store.GetAgentState(ctx, entityID, agentID, schemaName)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return sm.store.GetAgentStateHistory(ctx, state.ID, limit)
}
