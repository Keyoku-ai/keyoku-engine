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

func TestEvictionProcessor_Type(t *testing.T) {
	p := NewEvictionProcessor(&mockStore{}, nil, DefaultEvictionJobConfig())
	if p.Type() != JobTypeEviction {
		t.Errorf("Type() = %q, want %q", p.Type(), JobTypeEviction)
	}
}

func TestDefaultEvictionJobConfig(t *testing.T) {
	cfg := DefaultEvictionJobConfig()
	if cfg.MaxHNSWEntries != 10000 {
		t.Errorf("MaxHNSWEntries = %d, want 10000", cfg.MaxHNSWEntries)
	}
	if cfg.MaxStorageBytes != 500*1024*1024 {
		t.Errorf("MaxStorageBytes = %d, want %d", cfg.MaxStorageBytes, int64(500*1024*1024))
	}
	if cfg.BatchSize != 100 {
		t.Errorf("BatchSize = %d, want 100", cfg.BatchSize)
	}
}

func TestNewEvictionProcessor_Defaults(t *testing.T) {
	p := NewEvictionProcessor(&mockStore{}, nil, EvictionJobConfig{})
	if p.config.MaxHNSWEntries != 10000 {
		t.Errorf("default MaxHNSWEntries = %d, want 10000", p.config.MaxHNSWEntries)
	}
	if p.config.MaxStorageBytes != 500*1024*1024 {
		t.Errorf("default MaxStorageBytes = %d, want %d", p.config.MaxStorageBytes, int64(500*1024*1024))
	}
	if p.config.BatchSize != 100 {
		t.Errorf("default BatchSize = %d, want 100", p.config.BatchSize)
	}
}

func TestEvictionProcessor_Process_UnderCap(t *testing.T) {
	store := &mockStore{}
	// Default mock returns 0 for GetHNSWIndexSize and 0 for GetStorageSizeBytes
	p := NewEvictionProcessor(store, nil, EvictionJobConfig{
		MaxHNSWEntries:  100,
		MaxStorageBytes: 1024 * 1024,
		BatchSize:       10,
	})

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if result.ItemsAffected != 0 {
		t.Errorf("ItemsAffected = %d, want 0 (under cap)", result.ItemsAffected)
	}
}

func TestEvictionProcessor_Process_HNSWEviction(t *testing.T) {
	now := time.Now()
	weekAgo := now.Add(-168 * time.Hour)

	var removedIDs []string
	store := &mockStore{}

	// Override GetHNSWIndexSize to return over-cap value
	hnswSize := 15
	store.closeFn = nil // just using store struct

	// We need a custom mock that returns dynamic HNSW size
	// Since the mock's GetHNSWIndexSize() returns 0 by default,
	// we need to create a wrapper. For simplicity, use a function-field mock approach.

	// Create a custom store that wraps mockStore with HNSW overrides
	customStore := &evictionTestStore{
		mockStore:     store,
		hnswIndexSize: hnswSize,
		lowestRankedMemories: []*storage.Memory{
			{ID: "high-rank", Importance: 0.9, AccessCount: 10, LastAccessedAt: &now},
			{ID: "mid-rank", Importance: 0.5, AccessCount: 3, LastAccessedAt: &weekAgo},
			{ID: "low-rank-1", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
			{ID: "low-rank-2", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
			{ID: "low-rank-3", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
			{ID: "low-rank-4", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
			{ID: "low-rank-5", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
		},
		removeFromHNSWFn: func(id string) error {
			removedIDs = append(removedIDs, id)
			return nil
		},
	}

	p := NewEvictionProcessor(customStore, nil, EvictionJobConfig{
		MaxHNSWEntries:  10,
		MaxStorageBytes: 1024 * 1024 * 1024, // large cap, no storage eviction
		BatchSize:       5,
	})

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}

	// Should have evicted 5 (excess = 15-10=5, capped by batchSize=5)
	if result.ItemsAffected != 5 {
		t.Errorf("ItemsAffected = %d, want 5", result.ItemsAffected)
	}
	if len(removedIDs) != 5 {
		t.Errorf("removedIDs len = %d, want 5", len(removedIDs))
	}

	// Should evict lowest-ranked first (from the end of the ranked list)
	// low-rank-5, low-rank-4, low-rank-3, low-rank-2, low-rank-1 in some order
	for _, id := range removedIDs {
		if id == "high-rank" || id == "mid-rank" {
			t.Errorf("should not evict high/mid-rank memory %q", id)
		}
	}
}

func TestEvictionProcessor_Process_StorageCapEnforcement(t *testing.T) {
	var deletedIDs []string
	store := &evictionTestStore{
		mockStore:       &mockStore{},
		hnswIndexSize:   0, // under HNSW cap
		storageSizeBytes: 600 * 1024 * 1024, // 600MB, over 500MB cap
		entities:        []string{"entity-1"},
		queryMemoriesFn: func(_ context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "archived-1", Importance: 0.1, State: storage.StateArchived},
				{ID: "archived-2", Importance: 0.2, State: storage.StateArchived},
			}, nil
		},
		deleteMemoryFn: func(_ context.Context, id string, _ bool) error {
			deletedIDs = append(deletedIDs, id)
			return nil
		},
	}

	p := NewEvictionProcessor(store, nil, EvictionJobConfig{
		MaxHNSWEntries:  10000,
		MaxStorageBytes: 500 * 1024 * 1024,
		BatchSize:       100,
	})

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if len(deletedIDs) == 0 {
		t.Error("expected some memories to be deleted for storage cap enforcement")
	}
	if result.ItemsAffected == 0 {
		t.Error("expected ItemsAffected > 0")
	}
}

func TestEvictionProcessor_Process_RemoveFromHNSWError(t *testing.T) {
	now := time.Now()
	store := &evictionTestStore{
		mockStore:     &mockStore{},
		hnswIndexSize: 15,
		lowestRankedMemories: []*storage.Memory{
			{ID: "mem-1", Importance: 0.1, AccessCount: 0, LastAccessedAt: &now},
			{ID: "mem-2", Importance: 0.1, AccessCount: 0, LastAccessedAt: &now},
		},
		removeFromHNSWFn: func(id string) error {
			if id == "mem-1" {
				return fmt.Errorf("simulated error")
			}
			return nil
		},
	}

	p := NewEvictionProcessor(store, nil, EvictionJobConfig{
		MaxHNSWEntries:  10,
		MaxStorageBytes: 1024 * 1024 * 1024,
		BatchSize:       10,
	})

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	// Only mem-2 should have been evicted (mem-1 failed)
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1 (one failed)", result.ItemsAffected)
	}
}

func TestRankMemories_Sorting(t *testing.T) {
	now := time.Now()
	weekAgo := now.Add(-168 * time.Hour)

	memories := []*storage.Memory{
		{ID: "low", Importance: 0.1, AccessCount: 0, LastAccessedAt: nil},
		{ID: "high", Importance: 0.9, AccessCount: 10, LastAccessedAt: &now},
		{ID: "mid", Importance: 0.5, AccessCount: 3, LastAccessedAt: &weekAgo},
	}

	ranked := rankMemories(memories)
	if len(ranked) != 3 {
		t.Fatalf("ranked len = %d, want 3", len(ranked))
	}

	// Should be sorted descending by rank
	if ranked[0].id != "high" {
		t.Errorf("ranked[0] = %q, want %q (highest rank)", ranked[0].id, "high")
	}
	if ranked[2].id != "low" {
		t.Errorf("ranked[2] = %q, want %q (lowest rank)", ranked[2].id, "low")
	}

	// Verify ranks are actually descending
	for i := 1; i < len(ranked); i++ {
		if ranked[i].rank > ranked[i-1].rank {
			t.Errorf("ranked[%d].rank (%v) > ranked[%d].rank (%v) — not descending", i, ranked[i].rank, i-1, ranked[i-1].rank)
		}
	}
}

func TestRankMemories_RecencyBoost(t *testing.T) {
	now := time.Now()
	monthAgo := now.Add(-30 * 24 * time.Hour)

	// Same importance and access count, different recency
	memories := []*storage.Memory{
		{ID: "recent", Importance: 0.5, AccessCount: 1, LastAccessedAt: &now},
		{ID: "old", Importance: 0.5, AccessCount: 1, LastAccessedAt: &monthAgo},
	}

	ranked := rankMemories(memories)
	// Recent memory should have higher rank due to recency boost
	if ranked[0].id != "recent" {
		t.Errorf("ranked[0] = %q, want %q (recent should rank higher)", ranked[0].id, "recent")
	}
	if ranked[0].rank <= ranked[1].rank {
		t.Errorf("recent rank (%v) should be > old rank (%v)", ranked[0].rank, ranked[1].rank)
	}
}

// --- evictionTestStore wraps mockStore with configurable tiered retrieval methods ---

type evictionTestStore struct {
	*mockStore
	hnswIndexSize        int
	lowestRankedMemories []*storage.Memory
	removeFromHNSWFn     func(string) error
	storageSizeBytes     int64
	entities             []string
	queryMemoriesFn      func(context.Context, storage.MemoryQuery) ([]*storage.Memory, error)
	deleteMemoryFn       func(context.Context, string, bool) error
}

func (s *evictionTestStore) GetHNSWIndexSize() int {
	return s.hnswIndexSize
}

func (s *evictionTestStore) GetLowestRankedInHNSW(_ context.Context, _ int) ([]*storage.Memory, error) {
	return s.lowestRankedMemories, nil
}

func (s *evictionTestStore) RemoveFromHNSW(id string) error {
	if s.removeFromHNSWFn != nil {
		return s.removeFromHNSWFn(id)
	}
	return nil
}

func (s *evictionTestStore) GetStorageSizeBytes(_ context.Context) (int64, error) {
	return s.storageSizeBytes, nil
}

func (s *evictionTestStore) GetAllEntities(_ context.Context) ([]string, error) {
	if s.entities != nil {
		return s.entities, nil
	}
	return s.mockStore.GetAllEntities(context.Background())
}

func (s *evictionTestStore) QueryMemories(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
	if s.queryMemoriesFn != nil {
		return s.queryMemoriesFn(ctx, query)
	}
	return s.mockStore.QueryMemories(ctx, query)
}

func (s *evictionTestStore) DeleteMemory(ctx context.Context, id string, hard bool) error {
	if s.deleteMemoryFn != nil {
		return s.deleteMemoryFn(ctx, id, hard)
	}
	return s.mockStore.DeleteMemory(ctx, id, hard)
}
