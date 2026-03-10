// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package engine

import (
	"context"
	"log/slog"
	"math"
	"sort"

	"github.com/keyoku-ai/keyoku-engine/cache"
	"github.com/keyoku-ai/keyoku-engine/storage"
	"github.com/keyoku-ai/keyoku-engine/vectorindex"
)

// TieredRetrieverConfig configures the three-tier retrieval system.
type TieredRetrieverConfig struct {
	// Tier 1: Hot Cache
	HotCacheSize      int     // max cached items (default: 500)
	HotCacheThreshold float64 // min similarity to short-circuit (default: 0.7)

	// Tier 2: Bounded HNSW
	MaxHNSWEntries int // max vectors in HNSW index (default: 10000)

	// Tier 3: FTS fallback
	FTSFallbackThreshold float64 // search FTS if best HNSW score < this (default: 0.4)
	MaxStorageBytes      int64   // total storage cap in bytes (default: 500MB)

	// Eviction
	EvictionBatchSize int // how many to evict at once (default: 100)
}

// DefaultTieredRetrieverConfig returns sensible free-tier defaults.
func DefaultTieredRetrieverConfig() TieredRetrieverConfig {
	return TieredRetrieverConfig{
		HotCacheSize:         500,
		HotCacheThreshold:    0.7,
		MaxHNSWEntries:       10000,
		FTSFallbackThreshold: 0.4,
		MaxStorageBytes:      500 * 1024 * 1024, // 500 MB
		EvictionBatchSize:    100,
	}
}

// TieredRetriever orchestrates search across three tiers:
//   - Tier 1: LRU hot cache (brute-force cosine, ~1ms)
//   - Tier 2: Bounded HNSW index (ANN search, ~10ms)
//   - Tier 3: SQLite FTS5 fallback (keyword search, ~200ms)
type TieredRetriever struct {
	cache  *cache.LRU
	store  storage.Store
	config TieredRetrieverConfig
	ranker *RankCalculator
	logger *slog.Logger
}

// NewTieredRetriever creates a new tiered retriever.
func NewTieredRetriever(store storage.Store, config TieredRetrieverConfig, logger *slog.Logger) *TieredRetriever {
	if config.HotCacheSize <= 0 {
		config.HotCacheSize = 500
	}
	if config.HotCacheThreshold <= 0 {
		config.HotCacheThreshold = 0.7
	}
	if config.MaxHNSWEntries <= 0 {
		config.MaxHNSWEntries = 10000
	}
	if config.FTSFallbackThreshold <= 0 {
		config.FTSFallbackThreshold = 0.4
	}
	if logger == nil {
		logger = slog.Default()
	}

	lruCache := cache.NewLRU(cache.LRUConfig{
		MaxEntries:   config.HotCacheSize,
		HitThreshold: config.HotCacheThreshold,
	})

	return &TieredRetriever{
		cache:  lruCache,
		store:  store,
		config: config,
		ranker: &RankCalculator{},
		logger: logger,
	}
}

// TieredSearchResult wraps a memory with its similarity score and source tier.
type TieredSearchResult struct {
	Memory     *storage.Memory
	Similarity float64
	Tier       int // 1, 2, or 3
}

// Search performs tiered search across all three tiers.
// Returns results sorted by similarity descending.
func (t *TieredRetriever) Search(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// === Tier 1: Hot Cache ===
	cacheResults := t.cache.SearchWithEntityFilter(embedding, entityID, limit, minScore)
	if len(cacheResults) > 0 && cacheResults[0].Similarity >= t.config.HotCacheThreshold {
		// Cache hit with high confidence — short-circuit
		results := make([]*storage.SimilarityResult, len(cacheResults))
		for i, cr := range cacheResults {
			results[i] = &storage.SimilarityResult{
				Memory:     cr.Memory,
				Similarity: cr.Similarity,
			}
		}
		t.logger.Debug("tier1 cache hit",
			"entity", entityID,
			"results", len(results),
			"best_score", cacheResults[0].Similarity)
		return results, nil
	}

	// === Tier 2: HNSW Index ===
	var hnswResults []*storage.SimilarityResult
	var err error

	if opts.VisibilityFor != nil || opts.AgentID != "" {
		hnswResults, err = t.store.FindSimilarWithOptions(ctx, embedding, entityID, limit, minScore, opts)
	} else {
		hnswResults, err = t.store.FindSimilar(ctx, embedding, entityID, limit, minScore)
	}
	if err != nil {
		return nil, err
	}

	// Promote high-confidence HNSW results to hot cache
	for _, r := range hnswResults {
		if r.Similarity < t.config.FTSFallbackThreshold {
			continue // don't pollute cache with low-confidence results
		}
		vec := decodeEmbeddingFromMemory(r.Memory)
		if len(vec) > 0 {
			t.cache.Put(r.Memory, vec)
		}
	}

	// Check if HNSW results are good enough
	bestHNSWScore := 0.0
	if len(hnswResults) > 0 {
		bestHNSWScore = hnswResults[0].Similarity
	}

	if bestHNSWScore >= t.config.FTSFallbackThreshold {
		t.logger.Debug("tier2 HNSW hit",
			"entity", entityID,
			"results", len(hnswResults),
			"best_score", bestHNSWScore)
		return hnswResults, nil
	}

	// === Tier 3: FTS5 Fallback ===
	// HNSW returned low-confidence results — try full-text search
	// We need a text query for FTS. Extract key terms from the embedding context.
	// Since we don't have the original query text here, we fall through with whatever
	// HNSW found. The caller (Engine.Query) has the text and can call SearchFTS directly.
	// For now, return HNSW results even if low confidence — the engine layer handles FTS.
	t.logger.Debug("tier2 HNSW low confidence, returning best available",
		"entity", entityID,
		"results", len(hnswResults),
		"best_score", bestHNSWScore)

	return hnswResults, nil
}

// SearchWithFTSFallback performs tiered search with FTS fallback when a text query is available.
// This is the preferred entry point when the original query text is known.
func (t *TieredRetriever) SearchWithFTSFallback(ctx context.Context, embedding []float32, queryText string, entityID string, limit int, minScore float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
	// First try Tier 1 + Tier 2
	results, err := t.Search(ctx, embedding, entityID, limit, minScore, opts)
	if err != nil {
		return nil, err
	}

	// Check if we need Tier 3
	bestScore := 0.0
	if len(results) > 0 {
		bestScore = results[0].Similarity
	}

	if bestScore >= t.config.FTSFallbackThreshold || queryText == "" {
		return results, nil
	}

	// === Tier 3: FTS5 Fallback ===
	ftsMemories, err := t.store.SearchFTSWithOptions(ctx, queryText, entityID, limit, opts)
	if err != nil {
		t.logger.Debug("tier3 FTS failed, returning HNSW results", "error", err)
		return results, nil
	}

	if len(ftsMemories) == 0 {
		return results, nil
	}

	// Score FTS results using embedding similarity if available
	ftsResults := make([]*storage.SimilarityResult, 0, len(ftsMemories))
	for _, mem := range ftsMemories {
		sim := 0.0
		vec := decodeEmbeddingFromMemory(mem)
		if len(vec) > 0 && len(embedding) > 0 {
			sim = float64(vectorindex.CosineSimilarity(embedding, vec))
		} else {
			// No embedding available — assign a base score for keyword match
			sim = 0.3
		}
		if sim >= minScore {
			ftsResults = append(ftsResults, &storage.SimilarityResult{
				Memory:     mem,
				Similarity: sim,
			})
			// Promote to cache
			if len(vec) > 0 {
				t.cache.Put(mem, vec)
			}
		}
	}

	// Merge HNSW + FTS results, deduplicate by ID, keep highest score
	merged := mergeResults(results, ftsResults, limit)

	t.logger.Debug("tier3 FTS fallback",
		"entity", entityID,
		"hnsw_results", len(results),
		"fts_results", len(ftsResults),
		"merged", len(merged))

	return merged, nil
}

// OnMemoryCreated adds a newly created memory to the hot cache.
func (t *TieredRetriever) OnMemoryCreated(mem *storage.Memory, embedding []float32) {
	if len(embedding) > 0 {
		t.cache.Put(mem, embedding)
	}
}

// OnMemoryDeleted removes a memory from the hot cache.
func (t *TieredRetriever) OnMemoryDeleted(id string) {
	t.cache.Remove(id)
}

// OnMemoryAccessed promotes accessed memories in the hot cache.
func (t *TieredRetriever) OnMemoryAccessed(memories []*storage.Memory) {
	for _, mem := range memories {
		vec := decodeEmbeddingFromMemory(mem)
		if len(vec) > 0 {
			t.cache.Put(mem, vec)
		}
	}
}

// EnforceHNSWBounds evicts lowest-ranked memories from HNSW when over the cap.
// Returns the number of memories evicted.
func (t *TieredRetriever) EnforceHNSWBounds(ctx context.Context) (int, error) {
	currentSize := t.store.GetHNSWIndexSize()
	if currentSize <= t.config.MaxHNSWEntries {
		return 0, nil
	}

	excess := currentSize - t.config.MaxHNSWEntries
	if excess > t.config.EvictionBatchSize {
		excess = t.config.EvictionBatchSize
	}

	// Get all memories in HNSW, rank them, evict the lowest
	memories, err := t.store.GetLowestRankedInHNSW(ctx, 0)
	if err != nil {
		return 0, err
	}

	if len(memories) == 0 {
		return 0, nil
	}

	// Rank all memories
	ranked := t.ranker.RankMemories(memories)

	// Evict the lowest-ranked (at the end of the sorted list)
	evicted := 0
	for i := len(ranked) - 1; i >= 0 && evicted < excess; i-- {
		mem := ranked[i].Memory
		if err := t.store.RemoveFromHNSW(mem.ID); err != nil {
			t.logger.Warn("failed to evict from HNSW", "id", mem.ID, "error", err)
			continue
		}
		t.cache.Remove(mem.ID) // Also remove from hot cache
		evicted++
	}

	t.logger.Info("HNSW eviction completed",
		"evicted", evicted,
		"new_size", t.store.GetHNSWIndexSize(),
		"cap", t.config.MaxHNSWEntries)

	return evicted, nil
}

// CacheLen returns the current hot cache size.
func (t *TieredRetriever) CacheLen() int {
	return t.cache.Len()
}

// --- helpers ---

// decodeEmbeddingFromMemory extracts the float32 embedding from the Memory's BLOB field.
func decodeEmbeddingFromMemory(mem *storage.Memory) []float32 {
	if len(mem.Embedding) == 0 {
		return nil
	}
	// Embedding is stored as little-endian float32 BLOB
	if len(mem.Embedding)%4 != 0 {
		return nil
	}
	result := make([]float32, len(mem.Embedding)/4)
	for i := range result {
		bits := uint32(mem.Embedding[i*4+0]) |
			uint32(mem.Embedding[i*4+1])<<8 |
			uint32(mem.Embedding[i*4+2])<<16 |
			uint32(mem.Embedding[i*4+3])<<24
		result[i] = math.Float32frombits(bits)
	}
	return result
}

// mergeResults combines two result sets, deduplicating by memory ID and keeping highest score.
func mergeResults(a, b []*storage.SimilarityResult, limit int) []*storage.SimilarityResult {
	seen := make(map[string]int, len(a)+len(b))
	merged := make([]*storage.SimilarityResult, 0, len(a)+len(b))

	for _, r := range a {
		seen[r.Memory.ID] = len(merged)
		merged = append(merged, r)
	}

	for _, r := range b {
		if idx, exists := seen[r.Memory.ID]; exists {
			// Keep higher score
			if r.Similarity > merged[idx].Similarity {
				merged[idx] = r
			}
		} else {
			seen[r.Memory.ID] = len(merged)
			merged = append(merged, r)
		}
	}

	// Sort by similarity descending (O(n log n))
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Similarity > merged[j].Similarity
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}
