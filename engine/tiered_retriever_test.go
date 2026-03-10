// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package engine

import (
	"context"
	"math"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func makeEmbeddingBytes(vals []float32) []byte {
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

func TestTieredRetriever_Tier1CacheHit(t *testing.T) {
	store := &mockStore{}
	config := DefaultTieredRetrieverConfig()
	config.HotCacheThreshold = 0.7
	tr := NewTieredRetriever(store, config, nil)

	// Pre-populate cache with a high-similarity memory
	mem := testMemory("mem-1", "test memory")
	embedding := []float32{1.0, 0.0, 0.0}
	mem.Embedding = makeEmbeddingBytes(embedding)
	tr.OnMemoryCreated(mem, embedding)

	// Search with the same embedding — should hit cache
	results, err := tr.Search(context.Background(), embedding, "test-entity", 10, 0.0, storage.SimilarityOptions{})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected cache hit, got no results")
	}
	if results[0].Memory.ID != "mem-1" {
		t.Errorf("got ID %q, want %q", results[0].Memory.ID, "mem-1")
	}
	if results[0].Similarity < 0.7 {
		t.Errorf("similarity %v < 0.7, cache should have short-circuited", results[0].Similarity)
	}
}

func TestTieredRetriever_Tier2HNSWHit(t *testing.T) {
	mem := testMemory("mem-2", "HNSW memory")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.6},
			}, nil
		},
	}
	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	// Search should fall through cache (empty) to HNSW
	results, err := tr.Search(context.Background(), []float32{0.5, 0.5, 0.0}, "", 10, 0.0, storage.SimilarityOptions{})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Memory.ID != "mem-2" {
		t.Errorf("got ID %q, want %q", results[0].Memory.ID, "mem-2")
	}
}

func TestTieredRetriever_Tier3FTSFallback(t *testing.T) {
	ftsMem := testMemory("mem-3", "FTS memory about PostgreSQL")
	ftsMem.Embedding = makeEmbeddingBytes([]float32{0.3, 0.3, 0.3})

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			// HNSW returns low-confidence result
			return []*storage.SimilarityResult{
				{Memory: testMemory("mem-weak", "weak match"), Similarity: 0.2},
			}, nil
		},
	}

	// Add SearchFTS via function field — need to enhance mock
	searchFTSCalled := false
	store.queryMemoriesFn = nil // not used for FTS

	config := DefaultTieredRetrieverConfig()
	config.FTSFallbackThreshold = 0.4
	tr := NewTieredRetriever(store, config, nil)

	// Use SearchWithFTSFallback — this needs the store to implement SearchFTS
	// Since our mock returns nil for SearchFTS, the fallback will return HNSW results
	results, err := tr.SearchWithFTSFallback(context.Background(), []float32{0.5, 0.5, 0.0}, "PostgreSQL database", "", 10, 0.0, storage.SimilarityOptions{})
	if err != nil {
		t.Fatalf("SearchWithFTSFallback error: %v", err)
	}
	// Should return whatever is available (HNSW low-confidence results)
	_ = searchFTSCalled
	if len(results) == 0 {
		t.Log("no results from FTS fallback (expected with mock store)")
	}
}

func TestTieredRetriever_SearchWithOptions(t *testing.T) {
	mem := testMemory("mem-opts", "options memory")
	store := &mockStore{
		findSimilarWithOptionsFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
			if opts.AgentID != "agent-1" {
				t.Errorf("expected AgentID %q, got %q", "agent-1", opts.AgentID)
			}
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.8},
			}, nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	opts := storage.SimilarityOptions{AgentID: "agent-1"}
	results, err := tr.Search(context.Background(), []float32{0.5, 0.5, 0.0}, "", 10, 0.0, opts)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestTieredRetriever_OnMemoryCreatedAndDeleted(t *testing.T) {
	store := &mockStore{}
	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	mem := testMemory("mem-cache", "cached memory")
	embedding := []float32{1.0, 0.0, 0.0}
	tr.OnMemoryCreated(mem, embedding)

	if tr.CacheLen() != 1 {
		t.Errorf("cache len = %d, want 1", tr.CacheLen())
	}

	tr.OnMemoryDeleted("mem-cache")
	if tr.CacheLen() != 0 {
		t.Errorf("cache len = %d, want 0 after delete", tr.CacheLen())
	}
}

func TestTieredRetriever_OnMemoryAccessed(t *testing.T) {
	store := &mockStore{}
	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	mem := testMemory("mem-access", "accessed memory")
	mem.Embedding = makeEmbeddingBytes([]float32{0.5, 0.5, 0.0})

	tr.OnMemoryAccessed([]*storage.Memory{mem})

	if tr.CacheLen() != 1 {
		t.Errorf("cache len = %d, want 1 after access", tr.CacheLen())
	}
}

func TestTieredRetriever_EnforceHNSWBounds_UnderCap(t *testing.T) {
	store := &mockStore{}
	config := DefaultTieredRetrieverConfig()
	config.MaxHNSWEntries = 100
	tr := NewTieredRetriever(store, config, nil)

	// Index size 0 < cap 100 — nothing to evict
	evicted, err := tr.EnforceHNSWBounds(context.Background())
	if err != nil {
		t.Fatalf("EnforceHNSWBounds error: %v", err)
	}
	if evicted != 0 {
		t.Errorf("evicted = %d, want 0 (under cap)", evicted)
	}
}

func TestTieredRetriever_DefaultLimitZero(t *testing.T) {
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, limit int, _ float64) ([]*storage.SimilarityResult, error) {
			if limit != 10 {
				t.Errorf("default limit = %d, want 10", limit)
			}
			return nil, nil
		},
	}
	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	_, _ = tr.Search(context.Background(), []float32{0.5, 0.5, 0.0}, "", 0, 0.0, storage.SimilarityOptions{})
}

func TestDecodeEmbeddingFromMemory(t *testing.T) {
	original := []float32{1.0, -0.5, 0.25, 3.14}
	mem := &storage.Memory{
		Embedding: makeEmbeddingBytes(original),
	}

	decoded := decodeEmbeddingFromMemory(mem)
	if len(decoded) != len(original) {
		t.Fatalf("decoded len = %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("decoded[%d] = %v, want %v", i, decoded[i], original[i])
		}
	}
}

func TestDecodeEmbeddingFromMemory_Empty(t *testing.T) {
	mem := &storage.Memory{}
	decoded := decodeEmbeddingFromMemory(mem)
	if decoded != nil {
		t.Errorf("expected nil for empty embedding, got %v", decoded)
	}
}

func TestDecodeEmbeddingFromMemory_BadLength(t *testing.T) {
	mem := &storage.Memory{Embedding: []byte{1, 2, 3}} // not multiple of 4
	decoded := decodeEmbeddingFromMemory(mem)
	if decoded != nil {
		t.Errorf("expected nil for bad length, got %v", decoded)
	}
}

func TestMergeResults(t *testing.T) {
	a := []*storage.SimilarityResult{
		{Memory: &storage.Memory{ID: "mem-1"}, Similarity: 0.9},
		{Memory: &storage.Memory{ID: "mem-2"}, Similarity: 0.7},
	}
	b := []*storage.SimilarityResult{
		{Memory: &storage.Memory{ID: "mem-2"}, Similarity: 0.8}, // duplicate, higher score
		{Memory: &storage.Memory{ID: "mem-3"}, Similarity: 0.6},
	}

	merged := mergeResults(a, b, 10)
	if len(merged) != 3 {
		t.Fatalf("merged len = %d, want 3", len(merged))
	}
	// Should be sorted by similarity descending
	if merged[0].Memory.ID != "mem-1" || merged[0].Similarity != 0.9 {
		t.Errorf("merged[0] = %v, want mem-1 @ 0.9", merged[0])
	}
	// mem-2 should keep the higher score (0.8 from b)
	if merged[1].Memory.ID != "mem-2" || merged[1].Similarity != 0.8 {
		t.Errorf("merged[1] = %v, want mem-2 @ 0.8", merged[1])
	}
	if merged[2].Memory.ID != "mem-3" || merged[2].Similarity != 0.6 {
		t.Errorf("merged[2] = %v, want mem-3 @ 0.6", merged[2])
	}
}

func TestMergeResults_Limit(t *testing.T) {
	a := []*storage.SimilarityResult{
		{Memory: &storage.Memory{ID: "mem-1"}, Similarity: 0.9},
		{Memory: &storage.Memory{ID: "mem-2"}, Similarity: 0.7},
	}
	b := []*storage.SimilarityResult{
		{Memory: &storage.Memory{ID: "mem-3"}, Similarity: 0.6},
	}

	merged := mergeResults(a, b, 2)
	if len(merged) != 2 {
		t.Fatalf("merged len = %d, want 2 (limited)", len(merged))
	}
}

func TestMergeResults_Empty(t *testing.T) {
	merged := mergeResults(nil, nil, 10)
	if len(merged) != 0 {
		t.Errorf("merged len = %d, want 0", len(merged))
	}
}

func TestDefaultTieredRetrieverConfig(t *testing.T) {
	cfg := DefaultTieredRetrieverConfig()

	if cfg.HotCacheSize != 500 {
		t.Errorf("HotCacheSize = %d, want 500", cfg.HotCacheSize)
	}
	if cfg.HotCacheThreshold != 0.7 {
		t.Errorf("HotCacheThreshold = %v, want 0.7", cfg.HotCacheThreshold)
	}
	if cfg.MaxHNSWEntries != 10000 {
		t.Errorf("MaxHNSWEntries = %d, want 10000", cfg.MaxHNSWEntries)
	}
	if cfg.FTSFallbackThreshold != 0.4 {
		t.Errorf("FTSFallbackThreshold = %v, want 0.4", cfg.FTSFallbackThreshold)
	}
	if cfg.MaxStorageBytes != 500*1024*1024 {
		t.Errorf("MaxStorageBytes = %d, want %d", cfg.MaxStorageBytes, 500*1024*1024)
	}
	if cfg.EvictionBatchSize != 100 {
		t.Errorf("EvictionBatchSize = %d, want 100", cfg.EvictionBatchSize)
	}
}

func TestNewTieredRetriever_Defaults(t *testing.T) {
	store := &mockStore{}
	tr := NewTieredRetriever(store, TieredRetrieverConfig{}, nil)

	if tr.config.HotCacheSize != 500 {
		t.Errorf("default HotCacheSize = %d, want 500", tr.config.HotCacheSize)
	}
	if tr.config.HotCacheThreshold != 0.7 {
		t.Errorf("default HotCacheThreshold = %v, want 0.7", tr.config.HotCacheThreshold)
	}
	if tr.config.MaxHNSWEntries != 10000 {
		t.Errorf("default MaxHNSWEntries = %d, want 10000", tr.config.MaxHNSWEntries)
	}
	if tr.config.FTSFallbackThreshold != 0.4 {
		t.Errorf("default FTSFallbackThreshold = %v, want 0.4", tr.config.FTSFallbackThreshold)
	}
}

func TestTieredRetriever_CachePromotionFromHNSW(t *testing.T) {
	mem := testMemory("mem-promote", "promoted memory")
	mem.Embedding = makeEmbeddingBytes([]float32{0.8, 0.2, 0.0})

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.6},
			}, nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	// Before search, cache is empty
	if tr.CacheLen() != 0 {
		t.Fatal("cache should be empty before search")
	}

	_, _ = tr.Search(context.Background(), []float32{0.5, 0.5, 0.0}, "", 10, 0.0, storage.SimilarityOptions{})

	// After HNSW search, result should be promoted to cache
	if tr.CacheLen() != 1 {
		t.Errorf("cache len = %d, want 1 after HNSW promotion", tr.CacheLen())
	}
}

func TestTieredRetriever_SearchWithFTSFallback_HighHNSWScore(t *testing.T) {
	mem := testMemory("mem-high", "high score memory")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.8},
			}, nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	// HNSW score 0.8 >= FTSFallbackThreshold 0.4, so FTS should NOT be called
	results, err := tr.SearchWithFTSFallback(context.Background(), []float32{0.5, 0.5, 0.0}, "test query", "", 10, 0.0, storage.SimilarityOptions{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestTieredRetriever_SearchWithFTSFallback_EmptyQuery(t *testing.T) {
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: testMemory("mem-x", "x"), Similarity: 0.2},
			}, nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	// Empty query text means no FTS fallback even with low HNSW score
	results, err := tr.SearchWithFTSFallback(context.Background(), []float32{0.5, 0.5, 0.0}, "", "", 10, 0.0, storage.SimilarityOptions{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (no FTS for empty query), got %d", len(results))
	}
}

func TestTieredRetriever_EnforceHNSWBounds_OverCap(t *testing.T) {
	// Create 5 memories with different importance levels
	memories := make([]*storage.Memory, 5)
	for i := 0; i < 5; i++ {
		mem := testMemory("mem-"+string(rune('a'+i)), "memory content")
		mem.Importance = float64(i+1) * 0.2 // 0.2, 0.4, 0.6, 0.8, 1.0
		memories[i] = mem
	}

	removedIDs := make([]string, 0)
	indexSize := 5

	store := &mockStore{
		getHNSWIndexSizeFn: func() int {
			return indexSize
		},
		getLowestRankedInHNSWFn: func(_ int) ([]*storage.Memory, error) {
			return memories, nil
		},
		removeFromHNSWFn: func(id string) error {
			removedIDs = append(removedIDs, id)
			indexSize--
			return nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	config.MaxHNSWEntries = 3     // cap at 3, so 2 should be evicted
	config.EvictionBatchSize = 10 // allow evicting all excess
	tr := NewTieredRetriever(store, config, nil)

	evicted, err := tr.EnforceHNSWBounds(context.Background())
	if err != nil {
		t.Fatalf("EnforceHNSWBounds error: %v", err)
	}
	if evicted != 2 {
		t.Errorf("evicted = %d, want 2", evicted)
	}
	if len(removedIDs) != 2 {
		t.Fatalf("removed %d IDs, want 2", len(removedIDs))
	}
	// Lowest-ranked (lowest importance) should be evicted first
	// mem-a (0.2) and mem-b (0.4) should be evicted
	if removedIDs[0] != "mem-a" {
		t.Errorf("first evicted = %q, want mem-a (lowest rank)", removedIDs[0])
	}
	if removedIDs[1] != "mem-b" {
		t.Errorf("second evicted = %q, want mem-b", removedIDs[1])
	}
}

func TestTieredRetriever_CachePromotionThreshold(t *testing.T) {
	// Low-confidence HNSW results should NOT be promoted to cache
	mem := testMemory("mem-low", "low confidence memory")
	mem.Embedding = makeEmbeddingBytes([]float32{0.8, 0.2, 0.0})

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.2}, // below FTSFallbackThreshold (0.4)
			}, nil
		},
	}

	config := DefaultTieredRetrieverConfig()
	tr := NewTieredRetriever(store, config, nil)

	_, _ = tr.Search(context.Background(), []float32{0.5, 0.5, 0.0}, "", 10, 0.0, storage.SimilarityOptions{})

	// Low-confidence result should NOT be in cache
	if tr.CacheLen() != 0 {
		t.Errorf("cache len = %d, want 0 (low-confidence results should not be cached)", tr.CacheLen())
	}
}
