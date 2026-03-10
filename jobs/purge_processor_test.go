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

func TestPurgeProcessor_Type(t *testing.T) {
	p := NewPurgeProcessor(&mockStore{}, nil, PurgeJobConfig{})
	if p.Type() != JobTypePurge {
		t.Errorf("Type() = %q, want %q", p.Type(), JobTypePurge)
	}
}

func TestDefaultPurgeJobConfig(t *testing.T) {
	cfg := DefaultPurgeJobConfig()
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", cfg.BatchSize)
	}
	if cfg.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want 90", cfg.RetentionDays)
	}
}

func TestNewPurgeProcessor_Defaults(t *testing.T) {
	p := NewPurgeProcessor(&mockStore{}, nil, PurgeJobConfig{})
	if p.config.BatchSize != 500 {
		t.Errorf("default BatchSize = %d, want 500", p.config.BatchSize)
	}
	if p.config.RetentionDays != 90 {
		t.Errorf("default RetentionDays = %d, want 90", p.config.RetentionDays)
	}
}

func TestPurgeProcessor_Process_PurgesOldDeletedMemories(t *testing.T) {
	// Memory deleted 120 days ago (past 90-day retention)
	oldDeletedAt := time.Now().Add(-120 * 24 * time.Hour)
	oldMem := testMemory("mem-old", "old deleted memory", storage.StateDeleted)
	oldMem.DeletedAt = &oldDeletedAt

	// Memory deleted 10 days ago (within retention)
	recentDeletedAt := time.Now().Add(-10 * 24 * time.Hour)
	recentMem := testMemory("mem-recent", "recent deleted memory", storage.StateDeleted)
	recentMem.DeletedAt = &recentDeletedAt

	var hardDeletedIDs []string

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{oldMem, recentMem}, nil
		},
		deleteMemoryFn: func(_ context.Context, id string, hard bool) error {
			if hard {
				hardDeletedIDs = append(hardDeletedIDs, id)
			}
			return nil
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}

	if result.ItemsProcessed != 2 {
		t.Errorf("ItemsProcessed = %d, want 2", result.ItemsProcessed)
	}
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1 (only old)", result.ItemsAffected)
	}
	if len(hardDeletedIDs) != 1 || hardDeletedIDs[0] != "mem-old" {
		t.Errorf("hard deleted = %v, want [mem-old]", hardDeletedIDs)
	}
}

func TestPurgeProcessor_Process_NilDeletedAt(t *testing.T) {
	// Memory with nil DeletedAt should not be purged
	mem := testMemory("mem-1", "deleted no timestamp", storage.StateDeleted)
	mem.DeletedAt = nil

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem}, nil
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsAffected != 0 {
		t.Errorf("ItemsAffected = %d, want 0 (nil DeletedAt)", result.ItemsAffected)
	}
}

func TestPurgeProcessor_Process_NoEntities(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, nil
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestPurgeProcessor_Process_Empty(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestPurgeProcessor_Process_GetEntitiesError(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	_, err := p.Process(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestPurgeProcessor_Process_DeleteError(t *testing.T) {
	oldDeletedAt := time.Now().Add(-120 * 24 * time.Hour)
	mem := testMemory("mem-1", "old deleted memory", storage.StateDeleted)
	mem.DeletedAt = &oldDeletedAt

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem}, nil
		},
		deleteMemoryFn: func(_ context.Context, _ string, _ bool) error {
			return fmt.Errorf("delete error")
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	// Delete error is logged, not returned
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsAffected != 0 {
		t.Errorf("ItemsAffected = %d, want 0 (delete failed)", result.ItemsAffected)
	}
}

func TestPurgeProcessor_Process_QueryError(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, fmt.Errorf("query error")
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	// Query error is logged, not returned
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestPurgeProcessor_Process_ResultDetails(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, nil
		},
	}

	p := NewPurgeProcessor(store, nil, DefaultPurgeJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.Details == nil {
		t.Fatal("expected non-nil Details")
	}
	if days, ok := result.Details["retention_days"]; !ok {
		t.Error("expected retention_days in details")
	} else if days != 90 {
		t.Errorf("retention_days = %v, want 90", days)
	}
}
