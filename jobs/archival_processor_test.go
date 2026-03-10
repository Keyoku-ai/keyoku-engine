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

func TestArchivalProcessor_Type(t *testing.T) {
	p := NewArchivalProcessor(&mockStore{}, nil, ArchivalJobConfig{})
	if p.Type() != JobTypeArchival {
		t.Errorf("Type() = %q, want %q", p.Type(), JobTypeArchival)
	}
}

func TestDefaultArchivalJobConfig(t *testing.T) {
	cfg := DefaultArchivalJobConfig()
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", cfg.BatchSize)
	}
	if cfg.StaleThresholdDays != 30 {
		t.Errorf("StaleThresholdDays = %d, want 30", cfg.StaleThresholdDays)
	}
}

func TestNewArchivalProcessor_Defaults(t *testing.T) {
	p := NewArchivalProcessor(&mockStore{}, nil, ArchivalJobConfig{})
	if p.config.BatchSize != 500 {
		t.Errorf("default BatchSize = %d, want 500", p.config.BatchSize)
	}
	if p.config.StaleThresholdDays != 30 {
		t.Errorf("default StaleThresholdDays = %d, want 30", p.config.StaleThresholdDays)
	}
}

func TestArchivalProcessor_Process_ArchivesOldStaleMemories(t *testing.T) {
	// Memory stale for 60 days (past the 30-day threshold)
	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	oldMem := testMemory("mem-old", "old stale memory", storage.StateStale)
	oldMem.UpdatedAt = oldTime

	// Memory stale for only 5 days (within threshold)
	recentTime := time.Now().Add(-5 * 24 * time.Hour)
	recentMem := testMemory("mem-recent", "recent stale memory", storage.StateStale)
	recentMem.UpdatedAt = recentTime

	var transitions []storage.StateTransition

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{oldMem, recentMem}, nil
		},
		batchTransitionStatesFn: func(_ context.Context, t []storage.StateTransition) (int, error) {
			transitions = append(transitions, t...)
			return len(t), nil
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}

	if result.ItemsProcessed != 2 {
		t.Errorf("ItemsProcessed = %d, want 2", result.ItemsProcessed)
	}
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1 (only old memory)", result.ItemsAffected)
	}
	if len(transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(transitions))
	}
	if transitions[0].MemoryID != "mem-old" {
		t.Errorf("transitioned memory = %q, want %q", transitions[0].MemoryID, "mem-old")
	}
	if transitions[0].NewState != storage.StateArchived {
		t.Errorf("new state = %q, want %q", transitions[0].NewState, storage.StateArchived)
	}
}

func TestArchivalProcessor_Process_NoEntities(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, nil
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestArchivalProcessor_Process_Empty(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestArchivalProcessor_Process_GetEntitiesError(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	_, err := p.Process(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestArchivalProcessor_Process_QueryError(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, fmt.Errorf("query error")
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	// Query error is logged but not returned
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestArchivalProcessor_Process_TransitionError(t *testing.T) {
	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	mem := testMemory("mem-1", "old memory", storage.StateStale)
	mem.UpdatedAt = oldTime

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem}, nil
		},
		batchTransitionStatesFn: func(_ context.Context, _ []storage.StateTransition) (int, error) {
			return 0, fmt.Errorf("transition error")
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	// Error is logged, not returned
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsAffected != 0 {
		t.Errorf("ItemsAffected = %d, want 0", result.ItemsAffected)
	}
}

func TestArchivalProcessor_Process_ResultDetails(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, nil
		},
	}

	p := NewArchivalProcessor(store, nil, DefaultArchivalJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.Details == nil {
		t.Fatal("expected non-nil Details")
	}
	if days, ok := result.Details["stale_threshold_days"]; !ok {
		t.Error("expected stale_threshold_days in details")
	} else if days != 30 {
		t.Errorf("stale_threshold_days = %v, want 30", days)
	}
}
