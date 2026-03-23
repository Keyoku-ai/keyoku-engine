// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// TestAutoResolve_Pass2_PlanSupersededByEvent tests the key fix: when raw input
// similarity is LOW (e.g. webhook payload), pass 2 uses the extracted EVENT's
// own embedding to match against the PLAN.
func TestAutoResolve_Pass2_PlanSupersededByEvent(t *testing.T) {
	resolvedIDs := map[string]bool{}

	plan := &storage.Memory{
		ID:       "plan-1",
		EntityID: "test-entity",
		AgentID:  "default",
		Content:  "User plans to fix PR #32 regression",
		Type:     storage.TypePlan,
		State:    storage.StateActive,
	}

	store := &mockStore{
		resolveMemoryFn: func(_ context.Context, id string) error {
			resolvedIDs[id] = true
			return nil
		},
		findSimilarWithOptionsFn: func(_ context.Context, embedding []float32, _ string, _ int, _ float64, _ storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
			// Only return match when called with the completion memory's embedding (pass 2)
			if embedding[0] == 0.9 {
				return []*storage.SimilarityResult{
					{Memory: plan, Similarity: 0.85},
				}, nil
			}
			return nil, nil
		},
	}

	e := &Engine{store: store}

	created := []MemoryDetail{
		{
			ID:        "event-1",
			Content:   "PR #32 merged, regression fixed",
			Type:      storage.TypeEvent,
			Action:    "created",
			Embedding: []float32{0.9, 0.1, 0.1},
		},
	}

	// Pass 1: raw input similarity is below threshold
	similar := []*storage.SimilarityResult{
		{Memory: plan, Similarity: 0.55},
	}

	resolved := e.autoResolveSuperseded(context.Background(), "test-entity", created, similar, storage.SimilarityOptions{})

	if resolved != 1 {
		t.Errorf("expected 1 resolved, got %d", resolved)
	}
	if !resolvedIDs["plan-1"] {
		t.Error("expected plan-1 to be resolved via pass 2")
	}
}

// TestAutoResolve_Pass2_NoDoubleResolve ensures that if pass 1 resolves a plan,
// pass 2 doesn't resolve it again.
func TestAutoResolve_Pass2_NoDoubleResolve(t *testing.T) {
	resolveCount := 0
	plan := &storage.Memory{
		ID:       "plan-1",
		EntityID: "test-entity",
		AgentID:  "default",
		Content:  "User plans to fix the bug",
		Type:     storage.TypePlan,
		State:    storage.StateActive,
	}

	store := &mockStore{
		resolveMemoryFn: func(_ context.Context, _ string) error {
			resolveCount++
			return nil
		},
		findSimilarWithOptionsFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64, _ storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
			// Pass 2 also finds the same plan
			return []*storage.SimilarityResult{
				{Memory: plan, Similarity: 0.90},
			}, nil
		},
	}

	e := &Engine{store: store}

	created := []MemoryDetail{
		{
			ID:        "event-1",
			Content:   "User fixed the bug",
			Type:      storage.TypeEvent,
			Action:    "created",
			Embedding: []float32{0.5, 0.5, 0.5},
		},
	}

	// Pass 1 also matches
	similar := []*storage.SimilarityResult{
		{Memory: plan, Similarity: 0.80},
	}

	resolved := e.autoResolveSuperseded(context.Background(), "test-entity", created, similar, storage.SimilarityOptions{})

	if resolved != 1 {
		t.Errorf("expected 1 resolved (not double), got %d", resolved)
	}
	if resolveCount != 1 {
		t.Errorf("expected ResolveMemory called once, called %d times", resolveCount)
	}
}

// TestAutoResolve_Pass2_MultipleCompletions tests resolving multiple PLANs
// when multiple completion memories are created in one batch.
func TestAutoResolve_Pass2_MultipleCompletions(t *testing.T) {
	resolvedIDs := map[string]bool{}

	plan1 := &storage.Memory{
		ID: "plan-1", EntityID: "test-entity", AgentID: "default",
		Content: "User plans to fix PR #30", Type: storage.TypePlan, State: storage.StateActive,
	}
	plan2 := &storage.Memory{
		ID: "plan-2", EntityID: "test-entity", AgentID: "default",
		Content: "User plans to review PR #32", Type: storage.TypePlan, State: storage.StateActive,
	}

	store := &mockStore{
		resolveMemoryFn: func(_ context.Context, id string) error {
			resolvedIDs[id] = true
			return nil
		},
		findSimilarWithOptionsFn: func(_ context.Context, embedding []float32, _ string, _ int, _ float64, _ storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
			// Each completion memory matches its corresponding plan
			if embedding[0] == 0.1 {
				return []*storage.SimilarityResult{{Memory: plan1, Similarity: 0.82}}, nil
			}
			if embedding[0] == 0.2 {
				return []*storage.SimilarityResult{{Memory: plan2, Similarity: 0.79}}, nil
			}
			return nil, nil
		},
	}

	e := &Engine{store: store}

	created := []MemoryDetail{
		{ID: "event-1", Content: "PR #30 completed", Type: storage.TypeEvent, Action: "created", Embedding: []float32{0.1, 0.5, 0.5}},
		{ID: "event-2", Content: "PR #32 reviewed and merged", Type: storage.TypeEvent, Action: "created", Embedding: []float32{0.2, 0.5, 0.5}},
	}

	resolved := e.autoResolveSuperseded(context.Background(), "test-entity", created, nil, storage.SimilarityOptions{})

	if resolved != 2 {
		t.Errorf("expected 2 resolved, got %d", resolved)
	}
	if !resolvedIDs["plan-1"] {
		t.Error("expected plan-1 to be resolved")
	}
	if !resolvedIDs["plan-2"] {
		t.Error("expected plan-2 to be resolved")
	}
}

// TestAutoResolve_NoCompletionMemory verifies no-op when only non-completion types created.
func TestAutoResolve_NoCompletionCreated(t *testing.T) {
	store := &mockStore{
		resolveMemoryFn: func(_ context.Context, _ string) error {
			t.Error("ResolveMemory should not be called")
			return nil
		},
	}

	e := &Engine{store: store}

	created := []MemoryDetail{
		{ID: "id-1", Content: "User's name is Alice", Type: storage.TypeIdentity, Action: "created", Embedding: []float32{0.5, 0.5, 0.5}},
	}

	resolved := e.autoResolveSuperseded(context.Background(), "test-entity", created, nil, storage.SimilarityOptions{})

	if resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", resolved)
	}
}
