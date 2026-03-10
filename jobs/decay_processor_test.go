// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package jobs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestDecayProcessor_Type(t *testing.T) {
	p := NewDecayProcessor(&mockStore{}, nil, DecayJobConfig{})
	if p.Type() != JobTypeDecay {
		t.Errorf("Type() = %q, want %q", p.Type(), JobTypeDecay)
	}
}

func TestDefaultDecayJobConfig(t *testing.T) {
	cfg := DefaultDecayJobConfig()
	if cfg.BatchSize != 1000 {
		t.Errorf("BatchSize = %d, want 1000", cfg.BatchSize)
	}
	if cfg.ThresholdStale <= 0 {
		t.Errorf("ThresholdStale = %v, want > 0", cfg.ThresholdStale)
	}
	if cfg.ThresholdArchive <= 0 {
		t.Errorf("ThresholdArchive = %v, want > 0", cfg.ThresholdArchive)
	}
}

func TestNewDecayProcessor_Defaults(t *testing.T) {
	p := NewDecayProcessor(&mockStore{}, nil, DecayJobConfig{})
	if p.config.BatchSize != 1000 {
		t.Errorf("default BatchSize = %d, want 1000", p.config.BatchSize)
	}
}

func TestDecayProcessor_Process_HappyPath(t *testing.T) {
	// Create memories with varying access times to trigger different states
	now := time.Now()
	recentAccess := now.Add(-1 * time.Hour) // very recent — should stay active
	oldAccess := now.Add(-365 * 24 * time.Hour) // 1 year ago — should decay

	memories := []*storage.Memory{
		{
			ID:             "mem-1",
			State:          storage.StateActive,
			LastAccessedAt: &recentAccess,
			Stability:      60,
		},
		{
			ID:             "mem-2",
			State:          storage.StateActive,
			LastAccessedAt: &oldAccess,
			Stability:      30, // low stability + old access = high decay
		},
	}

	var transitions []storage.StateTransition
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, batchSize, offset int) ([]*storage.Memory, error) {
			if offset > 0 {
				return nil, nil
			}
			return memories, nil
		},
		batchTransitionStatesFn: func(_ context.Context, t []storage.StateTransition) (int, error) {
			transitions = append(transitions, t...)
			return len(t), nil
		},
	}

	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}

	if result.ItemsProcessed != 2 {
		t.Errorf("ItemsProcessed = %d, want 2", result.ItemsProcessed)
	}
	// The old memory should have been transitioned
	if len(transitions) == 0 {
		t.Error("expected at least one transition for old memory")
	}

	// Check that the old memory was transitioned (not the recent one)
	for _, tr := range transitions {
		if tr.MemoryID == "mem-1" {
			t.Error("recent memory should not be transitioned")
		}
	}
}

func TestDecayProcessor_Process_MultipleBatches(t *testing.T) {
	callCount := 0
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, batchSize, offset int) ([]*storage.Memory, error) {
			callCount++
			if callCount == 1 {
				// First batch: return exactly batchSize items
				mems := make([]*storage.Memory, batchSize)
				now := time.Now()
				for i := range mems {
					mems[i] = &storage.Memory{
						ID:             fmt.Sprintf("mem-%d", i),
						State:          storage.StateActive,
						LastAccessedAt: &now,
						Stability:      365,
					}
				}
				return mems, nil
			}
			// Second batch: empty
			return nil, nil
		},
		batchTransitionStatesFn: func(_ context.Context, t []storage.StateTransition) (int, error) {
			return len(t), nil
		},
	}

	cfg := DefaultDecayJobConfig()
	cfg.BatchSize = 10
	p := NewDecayProcessor(store, nil, cfg)

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 10 {
		t.Errorf("ItemsProcessed = %d, want 10", result.ItemsProcessed)
	}
	if callCount != 2 {
		t.Errorf("batch calls = %d, want 2", callCount)
	}
}

func TestDecayProcessor_Process_Empty(t *testing.T) {
	store := &mockStore{}
	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestDecayProcessor_Process_StoreError(t *testing.T) {
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, _, _ int) ([]*storage.Memory, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())
	_, err := p.Process(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestDecayProcessor_Process_TransitionError(t *testing.T) {
	oldAccess := time.Now().Add(-365 * 24 * time.Hour)
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, _, offset int) ([]*storage.Memory, error) {
			if offset > 0 {
				return nil, nil
			}
			return []*storage.Memory{
				{ID: "mem-1", State: storage.StateActive, LastAccessedAt: &oldAccess, Stability: 1},
			}, nil
		},
		batchTransitionStatesFn: func(_ context.Context, _ []storage.StateTransition) (int, error) {
			return 0, fmt.Errorf("transition error")
		},
	}

	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())
	result, err := p.Process(context.Background())
	// Error is logged but not returned
	if err != nil {
		t.Fatalf("Process error = %v (expected nil, error is logged)", err)
	}
	if result.ItemsAffected != 0 {
		t.Errorf("ItemsAffected = %d, want 0 (transition failed)", result.ItemsAffected)
	}
}

func TestDecayProcessor_Process_CronTagExempt(t *testing.T) {
	// Cron-tagged memories should NEVER decay, even with old access times.
	oldAccess := time.Now().Add(-365 * 24 * time.Hour)
	memories := []*storage.Memory{
		{
			ID:             "cron-mem",
			State:          storage.StateActive,
			LastAccessedAt: &oldAccess,
			Stability:      1, // Very low stability — would normally decay immediately
			Tags:           storage.StringSlice{"cron:daily:08:00"},
		},
		{
			ID:             "normal-mem",
			State:          storage.StateActive,
			LastAccessedAt: &oldAccess,
			Stability:      1,
		},
	}

	var transitions []storage.StateTransition
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, _, offset int) ([]*storage.Memory, error) {
			if offset > 0 {
				return nil, nil
			}
			return memories, nil
		},
		batchTransitionStatesFn: func(_ context.Context, t []storage.StateTransition) (int, error) {
			transitions = append(transitions, t...)
			return len(t), nil
		},
	}

	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}

	// Both are counted as processed
	if result.ItemsProcessed != 2 {
		t.Errorf("ItemsProcessed = %d, want 2", result.ItemsProcessed)
	}

	// The cron-tagged memory should NOT appear in transitions
	for _, tr := range transitions {
		if tr.MemoryID == "cron-mem" {
			t.Error("cron-tagged memory should be exempt from decay transitions")
		}
	}

	// The normal memory should have been transitioned
	foundNormal := false
	for _, tr := range transitions {
		if tr.MemoryID == "normal-mem" {
			foundNormal = true
		}
	}
	if !foundNormal {
		t.Error("normal memory should have been transitioned")
	}
}

func TestDecayProcessor_Process_ResultDetails(t *testing.T) {
	oldAccess := time.Now().Add(-365 * 24 * time.Hour)
	store := &mockStore{
		getActiveMemoriesForDecayFn: func(_ context.Context, _, offset int) ([]*storage.Memory, error) {
			if offset > 0 {
				return nil, nil
			}
			return []*storage.Memory{
				{ID: "mem-1", State: storage.StateActive, LastAccessedAt: &oldAccess, Stability: 1},
			}, nil
		},
		batchTransitionStatesFn: func(_ context.Context, t []storage.StateTransition) (int, error) {
			return len(t), nil
		},
	}

	p := NewDecayProcessor(store, nil, DefaultDecayJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.Details == nil {
		t.Fatal("expected non-nil Details")
	}
	if _, ok := result.Details["transitions_to_stale"]; !ok {
		t.Error("expected transitions_to_stale in details")
	}
	if _, ok := result.Details["transitions_to_archive"]; !ok {
		t.Error("expected transitions_to_archive in details")
	}
}
