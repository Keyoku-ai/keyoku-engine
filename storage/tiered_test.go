// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package storage

import (
	"context"
	"math"
	"testing"
)

func TestSearchFTS(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create memories with different content
	mem1 := &Memory{
		EntityID:   "entity-1",
		Content:    "PostgreSQL database with pgBouncer connection pooling",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-pg",
		Embedding:  makeTestEmbedding([]float32{0.5, 0.5, 0.0}),
	}
	mem2 := &Memory{
		EntityID:   "entity-1",
		Content:    "Redis caching layer with 5 minute TTL",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-redis",
		Embedding:  makeTestEmbedding([]float32{0.0, 0.5, 0.5}),
	}
	mem3 := &Memory{
		EntityID:   "entity-2",
		Content:    "PostgreSQL replication setup",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-pg2",
		Embedding:  makeTestEmbedding([]float32{0.5, 0.0, 0.5}),
	}

	for _, mem := range []*Memory{mem1, mem2, mem3} {
		if err := store.CreateMemory(ctx, mem); err != nil {
			t.Fatalf("CreateMemory error: %v", err)
		}
	}

	// Search for "PostgreSQL" scoped to entity-1
	results, err := store.SearchFTS(ctx, "PostgreSQL", "entity-1", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for PostgreSQL search")
	}
	// Should only return entity-1's PostgreSQL memory
	for _, r := range results {
		if r.EntityID != "entity-1" {
			t.Errorf("got entity %q, want entity-1", r.EntityID)
		}
	}

	// Search for "Redis"
	results, err = store.SearchFTS(ctx, "Redis", "entity-1", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for Redis search")
	}
	if results[0].ID != mem2.ID {
		t.Errorf("expected Redis memory, got %q", results[0].Content)
	}

	// Search with no results
	results, err = store.SearchFTS(ctx, "nonexistent_xyz_term", "entity-1", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent term, got %d", len(results))
	}
}

func TestSearchFTS_SpecificEntity(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	mem := &Memory{
		EntityID:   "entity-x",
		Content:    "unique search term xyzzy",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-xyzzy",
		Embedding:  makeTestEmbedding([]float32{0.1, 0.1, 0.1}),
	}
	if err := store.CreateMemory(ctx, mem); err != nil {
		t.Fatalf("CreateMemory error: %v", err)
	}

	// Search scoped to entity-x
	results, err := store.SearchFTS(ctx, "xyzzy", "entity-x", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results when searching entity-x")
	}
	if results[0].ID != mem.ID {
		t.Errorf("expected mem ID %q, got %q", mem.ID, results[0].ID)
	}

	// Search scoped to different entity should return nothing
	results, err = store.SearchFTS(ctx, "xyzzy", "entity-other", 10)
	if err != nil {
		t.Fatalf("SearchFTS error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for wrong entity, got %d", len(results))
	}
}

func TestGetStorageSizeBytes(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	size, err := store.GetStorageSizeBytes(ctx)
	if err != nil {
		t.Fatalf("GetStorageSizeBytes error: %v", err)
	}
	// SQLite should report some non-zero size even with empty tables
	if size <= 0 {
		t.Errorf("expected positive storage size, got %d", size)
	}
}

func TestGetMemoryCount(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Initially zero
	count, err := store.GetMemoryCount(ctx)
	if err != nil {
		t.Fatalf("GetMemoryCount error: %v", err)
	}
	if count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	// Add some memories
	for i := 0; i < 3; i++ {
		mem := &Memory{
			EntityID:   "entity-1",
			Content:    "test memory",
			Type:       TypeContext,
			State:      StateActive,
			Importance: 0.5,
			Confidence: 0.8,
			Stability:  60,
			Hash:       "hash-" + string(rune('a'+i)),
			Embedding:  makeTestEmbedding([]float32{0.1, 0.1, 0.1}),
		}
		if err := store.CreateMemory(ctx, mem); err != nil {
			t.Fatalf("CreateMemory error: %v", err)
		}
	}

	count, err = store.GetMemoryCount(ctx)
	if err != nil {
		t.Fatalf("GetMemoryCount error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestGetHNSWIndexSize(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()

	// Should return 0 or the index size
	size := store.GetHNSWIndexSize()
	if size < 0 {
		t.Errorf("HNSW index size = %d, want >= 0", size)
	}
}

func TestRemoveFromHNSW(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create a memory with embedding (gets added to HNSW)
	mem := &Memory{
		EntityID:   "entity-1",
		Content:    "test for removal",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-remove",
		Embedding:  makeTestEmbedding([]float32{0.5, 0.5, 0.0}),
	}
	if err := store.CreateMemory(ctx, mem); err != nil {
		t.Fatalf("CreateMemory error: %v", err)
	}

	initialSize := store.GetHNSWIndexSize()

	// Remove from HNSW
	err := store.RemoveFromHNSW(mem.ID)
	if err != nil {
		t.Fatalf("RemoveFromHNSW error: %v", err)
	}

	newSize := store.GetHNSWIndexSize()
	if newSize >= initialSize && initialSize > 0 {
		t.Errorf("HNSW size should decrease after removal: before=%d, after=%d", initialSize, newSize)
	}

	// Memory should still exist in SQLite (not deleted)
	retrieved, err := store.GetMemory(ctx, mem.ID)
	if err != nil {
		t.Fatalf("GetMemory error: %v", err)
	}
	if retrieved == nil {
		t.Error("memory should still exist in SQLite after HNSW removal")
	}
}

func TestGetLowestRankedInHNSW(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create some memories
	for i := 0; i < 3; i++ {
		mem := &Memory{
			EntityID:   "entity-1",
			Content:    "ranked memory",
			Type:       TypeContext,
			State:      StateActive,
			Importance: float64(i+1) * 0.3,
			Confidence: 0.8,
			Stability:  60,
			Hash:       "hash-ranked-" + string(rune('a'+i)),
			Embedding:  makeTestEmbedding([]float32{float32(i) * 0.1, 0.5, 0.0}),
		}
		if err := store.CreateMemory(ctx, mem); err != nil {
			t.Fatalf("CreateMemory error: %v", err)
		}
	}

	memories, err := store.GetLowestRankedInHNSW(ctx, 0)
	if err != nil {
		t.Fatalf("GetLowestRankedInHNSW error: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("expected some memories in HNSW")
	}
}

func TestSearchFTSWithOptions_VisibilityFilter(t *testing.T) {
	store := newTieredTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// Create private memory for agent-1
	mem1 := &Memory{
		EntityID:   "entity-1",
		AgentID:    "agent-1",
		Content:    "PostgreSQL secret config for agent-1",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-priv",
		Embedding:  makeTestEmbedding([]float32{0.5, 0.5, 0.0}),
		Visibility: VisibilityPrivate,
	}
	// Create global memory
	mem2 := &Memory{
		EntityID:   "entity-1",
		AgentID:    "agent-2",
		Content:    "PostgreSQL public docs reference",
		Type:       TypeContext,
		State:      StateActive,
		Importance: 0.5,
		Confidence: 0.8,
		Stability:  60,
		Hash:       "hash-pub",
		Embedding:  makeTestEmbedding([]float32{0.0, 0.5, 0.5}),
		Visibility: VisibilityGlobal,
	}

	for _, mem := range []*Memory{mem1, mem2} {
		if err := store.CreateMemory(ctx, mem); err != nil {
			t.Fatalf("CreateMemory error: %v", err)
		}
	}

	// Agent-2 searching for PostgreSQL should only see global, not agent-1's private
	results, err := store.SearchFTSWithOptions(ctx, "PostgreSQL", "entity-1", 10, SimilarityOptions{
		VisibilityFor: &VisibilityContext{AgentID: "agent-2"},
	})
	if err != nil {
		t.Fatalf("SearchFTSWithOptions error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (global only), got %d", len(results))
	}
	if results[0].ID != mem2.ID {
		t.Errorf("expected global memory %q, got %q", mem2.ID, results[0].ID)
	}

	// Agent-1 should see both their private + global
	results, err = store.SearchFTSWithOptions(ctx, "PostgreSQL", "entity-1", 10, SimilarityOptions{
		VisibilityFor: &VisibilityContext{AgentID: "agent-1"},
	})
	if err != nil {
		t.Fatalf("SearchFTSWithOptions error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (private + global), got %d", len(results))
	}

	// Filter by agent ID directly
	results, err = store.SearchFTSWithOptions(ctx, "PostgreSQL", "entity-1", 10, SimilarityOptions{
		AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("SearchFTSWithOptions error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (agent-1 only), got %d", len(results))
	}
	if results[0].AgentID != "agent-1" {
		t.Errorf("expected agent-1, got %q", results[0].AgentID)
	}
}

// --- helpers ---

func makeTestEmbedding(vals []float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func newTieredTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLite(":memory:", 3) // 3-dim embeddings for tests
	if err != nil {
		t.Fatalf("NewSQLite error: %v", err)
	}
	return store
}
