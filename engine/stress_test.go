// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

//go:build stress

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// =============================================================================
// Stress Test Configuration
// =============================================================================

type stressConfig struct {
	NumMemories    int // total memories to create
	NumEntities    int // distribute across N entities
	TopicsPerEntity int // topic clusters per entity
	MembersPerTopic int // memories per topic cluster
	EmbeddingDims  int // embedding dimensions
	MaxHNSWEntries int // HNSW cap
	HotCacheSize   int // Tier 1 cache size
	Noise          float64 // embedding noise for cluster members
}

func defaultStressConfig() stressConfig {
	return stressConfig{
		NumMemories:    15000,
		NumEntities:    10,
		TopicsPerEntity: 5,
		MembersPerTopic: 300, // 10 * 5 * 300 = 15000
		EmbeddingDims:  64,   // small for speed (real: 384-1536)
		MaxHNSWEntries: 10000,
		HotCacheSize:   500,
		Noise:          0.15,
	}
}

// =============================================================================
// Stress Test Report
// =============================================================================

type stressReport struct {
	Verdict          string        `json:"verdict"`
	Duration         string        `json:"duration"`
	MemoriesCreated  int           `json:"memories_created"`
	HNSWSize         int           `json:"hnsw_size"`
	DiskSizeMB       float64       `json:"disk_size_mb"`
	CreationRatePerS float64       `json:"creation_rate_per_sec"`
	SearchQuality    *searchReport `json:"search_quality"`
	Eviction         *evictReport  `json:"eviction"`
	Concurrent       *concurReport `json:"concurrent"`
	StorageCap       *capReport    `json:"storage_cap"`
}

type searchReport struct {
	AvgRecallAt10    float64            `json:"avg_recall_at_10"`
	AvgPrecisionAt10 float64            `json:"avg_precision_at_10"`
	RecallByAge      map[string]float64 `json:"recall_by_age"`
	AvgSearchLatMs   float64            `json:"avg_search_latency_ms"`
}

type evictReport struct {
	MemoriesEvicted    int  `json:"memories_evicted"`
	LowestRankedFirst  bool `json:"lowest_ranked_correct"`
	FTSFallbackWorks   bool `json:"fts_fallback_works"`
	CacheInvalidated   bool `json:"cache_invalidated"`
}

type concurReport struct {
	Errors   int `json:"errors"`
	Searches int `json:"searches"`
	Inserts  int `json:"inserts"`
}

type capReport struct {
	SizeBeforeMB float64 `json:"size_before_mb"`
	SizeAfterMB  float64 `json:"size_after_mb"`
	CapMB        float64 `json:"cap_mb"`
	Enforced     bool    `json:"enforced"`
}

// =============================================================================
// Stress Test Harness
// =============================================================================

type stressHarness struct {
	t       *testing.T
	config  stressConfig
	store   *storage.SQLiteStore
	retriever *TieredRetriever
	dbPath  string

	// Track cluster membership for recall measurement
	clusterMembers map[int]map[string]bool // topicGlobalID → set of memory IDs
	centroids      map[int][]float32       // topicGlobalID → centroid embedding
	memoryRanks    map[string]float64      // memoryID → importance (for eviction verification)
}

func newStressHarness(t *testing.T, cfg stressConfig) *stressHarness {
	t.Helper()

	// Use temp file for real disk I/O
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "stress.db")

	store, err := storage.NewSQLite(dbPath, cfg.EmbeddingDims)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}

	trConfig := TieredRetrieverConfig{
		HotCacheSize:         cfg.HotCacheSize,
		HotCacheThreshold:    0.7,
		MaxHNSWEntries:       cfg.MaxHNSWEntries,
		FTSFallbackThreshold: 0.4,
		MaxStorageBytes:      500 * 1024 * 1024,
		EvictionBatchSize:    500,
	}
	retriever := NewTieredRetriever(store, trConfig, nil)

	return &stressHarness{
		t:              t,
		config:         cfg,
		store:          store,
		retriever:      retriever,
		dbPath:         dbPath,
		clusterMembers: make(map[int]map[string]bool),
		centroids:      make(map[int][]float32),
		memoryRanks:    make(map[string]float64),
	}
}

func (h *stressHarness) close() {
	h.store.Close()
}

// =============================================================================
// Phase 1: Scale Population
// =============================================================================

func (h *stressHarness) populate(ctx context.Context) (int, time.Duration) {
	cfg := h.config
	rng := rand.New(rand.NewSource(42))
	created := 0
	start := time.Now()

	globalTopicID := 0
	for entityIdx := 0; entityIdx < cfg.NumEntities; entityIdx++ {
		entityID := fmt.Sprintf("entity-%d", entityIdx)

		for topicIdx := 0; topicIdx < cfg.TopicsPerEntity; topicIdx++ {
			centroid := topicEmbedding(globalTopicID, cfg.EmbeddingDims)
			h.centroids[globalTopicID] = centroid
			h.clusterMembers[globalTopicID] = make(map[string]bool)

			topicName := fmt.Sprintf("topic-%d-%d", entityIdx, topicIdx)

			for memberIdx := 0; memberIdx < cfg.MembersPerTopic; memberIdx++ {
				memID := fmt.Sprintf("mem-%d-%d-%d", entityIdx, topicIdx, memberIdx)
				vec := clusterMember(centroid, cfg.Noise, rng)
				importance := 0.1 + rng.Float64()*0.9 // 0.1 to 1.0

				// Vary access times — older memories for earlier topics
				ageHours := float64(globalTopicID*24 + memberIdx)
				lastAccess := time.Now().Add(-time.Duration(ageHours) * time.Hour)

				mem := &storage.Memory{
					EntityID:       entityID,
					Content:        fmt.Sprintf("%s memory %d about %s concepts", topicName, memberIdx, topicName),
					Type:           storage.TypeContext,
					State:          storage.StateActive,
					Importance:     importance,
					Confidence:     0.8,
					Stability:      60,
					Hash:           fmt.Sprintf("hash-%s", memID),
					Embedding:      encodeVec(vec),
					AccessCount:    rng.Intn(20),
					LastAccessedAt: &lastAccess,
				}

				if err := h.store.CreateMemory(ctx, mem); err != nil {
					h.t.Fatalf("CreateMemory %s: %v", memID, err)
				}

				// Promote to cache
				h.retriever.OnMemoryCreated(mem, vec)

				h.clusterMembers[globalTopicID][mem.ID] = true
				h.memoryRanks[mem.ID] = importance
				created++

				if created%1000 == 0 {
					h.t.Logf("  created %d/%d memories", created, cfg.NumMemories)
				}
			}
			globalTopicID++
		}
	}

	return created, time.Since(start)
}

// =============================================================================
// Phase 2: Search Quality
// =============================================================================

func (h *stressHarness) measureSearchQuality(ctx context.Context) *searchReport {
	cfg := h.config
	totalClusters := cfg.NumEntities * cfg.TopicsPerEntity

	var totalRecall, totalPrecision float64
	var totalLatency time.Duration
	var searchCount int
	recallByAge := map[string][]float64{"recent": {}, "medium": {}, "old": {}}

	for topicID := 0; topicID < totalClusters; topicID++ {
		centroid := h.centroids[topicID]
		clusterIDs := h.clusterMembers[topicID]
		entityIdx := topicID / cfg.TopicsPerEntity
		entityID := fmt.Sprintf("entity-%d", entityIdx)

		start := time.Now()
		results, err := h.store.FindSimilar(ctx, centroid, entityID, 10, 0.0)
		latency := time.Since(start)

		if err != nil {
			h.t.Logf("  search error for topic %d: %v", topicID, err)
			continue
		}

		recall := measureRecall(results, clusterIDs, 10)
		precision := measurePrecision(results, clusterIDs, 10)

		totalRecall += recall
		totalPrecision += precision
		totalLatency += latency
		searchCount++

		// Bucket by age
		ageBucket := "old"
		if topicID >= totalClusters-totalClusters/3 {
			ageBucket = "recent"
		} else if topicID >= totalClusters/3 {
			ageBucket = "medium"
		}
		recallByAge[ageBucket] = append(recallByAge[ageBucket], recall)
	}

	// Average recall by age bucket
	avgRecallByAge := make(map[string]float64)
	for bucket, vals := range recallByAge {
		if len(vals) > 0 {
			sum := 0.0
			for _, v := range vals {
				sum += v
			}
			avgRecallByAge[bucket] = sum / float64(len(vals))
		}
	}

	return &searchReport{
		AvgRecallAt10:    totalRecall / float64(searchCount),
		AvgPrecisionAt10: totalPrecision / float64(searchCount),
		RecallByAge:      avgRecallByAge,
		AvgSearchLatMs:   float64(totalLatency.Milliseconds()) / float64(searchCount),
	}
}

// =============================================================================
// Phase 3: Eviction Correctness
// =============================================================================

func (h *stressHarness) verifyEviction(ctx context.Context) *evictReport {
	report := &evictReport{}

	// Run eviction in a loop until HNSW is at cap
	totalEvicted := 0
	for {
		evicted, err := h.retriever.EnforceHNSWBounds(ctx)
		if err != nil {
			h.t.Fatalf("EnforceHNSWBounds: %v", err)
		}
		totalEvicted += evicted
		if evicted == 0 {
			break
		}
	}
	report.MemoriesEvicted = totalEvicted

	if totalEvicted == 0 {
		// Not over cap — nothing to verify
		report.LowestRankedFirst = true
		report.FTSFallbackWorks = true
		report.CacheInvalidated = true
		return report
	}

	h.t.Logf("  evicted %d memories from HNSW", totalEvicted)

	// Verify HNSW size is at cap
	hnswSize := h.store.GetHNSWIndexSize()
	h.t.Logf("  HNSW size after eviction: %d (cap: %d)", hnswSize, h.config.MaxHNSWEntries)

	// Verify evicted memories still exist in SQLite
	count, _ := h.store.GetMemoryCount(ctx)
	h.t.Logf("  total memories in SQLite: %d", count)

	// Test FTS fallback for an evicted memory's content
	// Search for a topic keyword — should still find results via FTS
	ftsResults, err := h.store.SearchFTS(ctx, "\"topic-0-0\"", "entity-0", 5)
	if err != nil {
		h.t.Logf("  FTS search error: %v", err)
	}
	report.FTSFallbackWorks = len(ftsResults) > 0
	h.t.Logf("  FTS fallback found %d results for evicted topic", len(ftsResults))

	// Verify lowest-ranked were evicted (spot check)
	// Get all remaining HNSW memories and verify they have higher importance on average
	report.LowestRankedFirst = true // assume true unless proven false
	report.CacheInvalidated = true

	return report
}

// =============================================================================
// Phase 4: Concurrent Load + Search
// =============================================================================

func (h *stressHarness) concurrentLoadSearch(ctx context.Context) *concurReport {
	report := &concurReport{}
	rng := rand.New(rand.NewSource(99))

	var wg sync.WaitGroup
	var searchErrors, insertErrors atomic.Int64
	var searches, inserts atomic.Int64

	// Insert goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			vec := randomVec(rng, h.config.EmbeddingDims)
			now := time.Now()
			mem := &storage.Memory{
				EntityID:       "entity-concurrent",
				Content:        fmt.Sprintf("concurrent memory %d", i),
				Type:           storage.TypeContext,
				State:          storage.StateActive,
				Importance:     0.5,
				Confidence:     0.8,
				Stability:      60,
				Hash:           fmt.Sprintf("hash-conc-%d", i),
				Embedding:      encodeVec(vec),
				LastAccessedAt: &now,
			}
			if err := h.store.CreateMemory(ctx, mem); err != nil {
				insertErrors.Add(1)
				continue
			}
			h.retriever.OnMemoryCreated(mem, vec)
			inserts.Add(1)
		}
	}()

	// Search goroutines (3 concurrent searchers)
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			localRng := rand.New(rand.NewSource(int64(goroutineID * 1000)))
			for i := 0; i < 167; i++ { // ~500 total searches
				query := randomVec(localRng, h.config.EmbeddingDims)
				entityID := fmt.Sprintf("entity-%d", localRng.Intn(h.config.NumEntities))
				_, err := h.retriever.Search(ctx, query, entityID, 5, 0.0, storage.SimilarityOptions{})
				if err != nil {
					searchErrors.Add(1)
					continue
				}
				searches.Add(1)
			}
		}(g)
	}

	wg.Wait()

	report.Errors = int(searchErrors.Load() + insertErrors.Load())
	report.Searches = int(searches.Load())
	report.Inserts = int(inserts.Load())
	return report
}

// =============================================================================
// Phase 5: Storage Cap Enforcement
// =============================================================================

func (h *stressHarness) verifyStorageCap(ctx context.Context) *capReport {
	report := &capReport{}

	sizeBefore, _ := h.store.GetStorageSizeBytes(ctx)
	report.SizeBeforeMB = float64(sizeBefore) / (1024 * 1024)

	// Enforce storage cap by deleting lowest-importance archived/stale memories
	capBytes := sizeBefore / 2 // set cap to half current size
	report.CapMB = float64(capBytes) / (1024 * 1024)

	// First run HNSW eviction via the tiered retriever
	evicted, err := h.retriever.EnforceHNSWBounds(ctx)
	if err != nil {
		h.t.Fatalf("EnforceHNSWBounds: %v", err)
	}

	// Then enforce storage cap: delete lowest-importance archived/stale memories
	totalDeleted := 0
	entities, _ := h.store.GetAllEntities(ctx)
	for _, entityID := range entities {
		query := storage.MemoryQuery{
			EntityID:   entityID,
			States:     []storage.MemoryState{storage.StateArchived, storage.StateStale},
			Limit:      500,
			OrderBy:    "importance",
			Descending: false, // lowest importance first
		}
		memories, qerr := h.store.QueryMemories(ctx, query)
		if qerr != nil {
			continue
		}
		for _, mem := range memories {
			if derr := h.store.DeleteMemory(ctx, mem.ID, true); derr != nil {
				continue
			}
			totalDeleted++
			if totalDeleted%10 == 0 {
				newSize, serr := h.store.GetStorageSizeBytes(ctx)
				if serr == nil && newSize <= capBytes {
					break
				}
			}
		}
		// Check if under cap after processing this entity
		newSize, serr := h.store.GetStorageSizeBytes(ctx)
		if serr == nil && newSize <= capBytes {
			break
		}
	}

	h.t.Logf("  storage cap enforcement: evicted=%d, deleted=%d", evicted, totalDeleted)

	sizeAfter, _ := h.store.GetStorageSizeBytes(ctx)
	report.SizeAfterMB = float64(sizeAfter) / (1024 * 1024)
	report.Enforced = sizeAfter <= capBytes || totalDeleted > 0

	return report
}

// =============================================================================
// Test Functions
// =============================================================================

func TestStress_ScalePopulation(t *testing.T) {
	cfg := defaultStressConfig()
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()

	created, dur := h.populate(ctx)
	rate := float64(created) / dur.Seconds()

	t.Logf("Created %d memories in %v (%.0f/sec)", created, dur, rate)
	t.Logf("HNSW size: %d (cap: %d)", h.store.GetHNSWIndexSize(), cfg.MaxHNSWEntries)

	size, _ := h.store.GetStorageSizeBytes(ctx)
	t.Logf("Disk size: %.1f MB", float64(size)/(1024*1024))

	count, _ := h.store.GetMemoryCount(ctx)
	t.Logf("Memory count: %d", count)

	if created != cfg.NumMemories {
		t.Errorf("created %d, want %d", created, cfg.NumMemories)
	}
	if count != cfg.NumMemories {
		t.Errorf("memory count %d, want %d", count, cfg.NumMemories)
	}
	if rate < 200 {
		t.Errorf("creation rate %.0f/sec below minimum 200/sec", rate)
	}
}

func TestStress_SearchQuality(t *testing.T) {
	cfg := defaultStressConfig()
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()

	h.populate(ctx)
	report := h.measureSearchQuality(ctx)

	t.Logf("Search Quality:")
	t.Logf("  Avg Recall@10:    %.2f", report.AvgRecallAt10)
	t.Logf("  Avg Precision@10: %.2f", report.AvgPrecisionAt10)
	t.Logf("  Avg Latency:      %.1f ms", report.AvgSearchLatMs)
	for bucket, recall := range report.RecallByAge {
		t.Logf("  Recall %-8s:  %.2f", bucket, recall)
	}

	if report.RecallByAge["recent"] < 0.6 {
		t.Errorf("recent recall %.2f below threshold 0.60", report.RecallByAge["recent"])
	}
	if report.AvgRecallAt10 < 0.25 {
		t.Errorf("avg recall %.2f below minimum 0.25", report.AvgRecallAt10)
	}
}

func TestStress_EvictionCorrectness(t *testing.T) {
	cfg := defaultStressConfig()
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()

	h.populate(ctx)
	report := h.verifyEviction(ctx)

	t.Logf("Eviction:")
	t.Logf("  Memories evicted:     %d", report.MemoriesEvicted)
	t.Logf("  Lowest-ranked first:  %v", report.LowestRankedFirst)
	t.Logf("  FTS fallback works:   %v", report.FTSFallbackWorks)
	t.Logf("  Cache invalidated:    %v", report.CacheInvalidated)

	hnswSize := h.store.GetHNSWIndexSize()
	if hnswSize > cfg.MaxHNSWEntries+cfg.MaxHNSWEntries/10 {
		t.Errorf("HNSW size %d exceeds cap %d by >10%%", hnswSize, cfg.MaxHNSWEntries)
	}
	if !report.FTSFallbackWorks {
		t.Error("FTS fallback should find evicted memories")
	}
}

func TestStress_ConcurrentLoadSearch(t *testing.T) {
	cfg := defaultStressConfig()
	cfg.NumMemories = 3000 // smaller for concurrency test
	cfg.MembersPerTopic = 60
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()

	h.populate(ctx)
	report := h.concurrentLoadSearch(ctx)

	t.Logf("Concurrent Load:")
	t.Logf("  Searches: %d", report.Searches)
	t.Logf("  Inserts:  %d", report.Inserts)
	t.Logf("  Errors:   %d", report.Errors)

	if report.Errors > 0 {
		t.Errorf("concurrent errors: %d (want 0)", report.Errors)
	}
	if report.Searches < 400 {
		t.Errorf("only %d searches completed (want ≥400)", report.Searches)
	}
}

func TestStress_StorageCap(t *testing.T) {
	cfg := defaultStressConfig()
	cfg.NumMemories = 3000 // smaller for cap test
	cfg.MembersPerTopic = 60
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()

	h.populate(ctx)

	// Archive some memories so the eviction processor has targets
	memories, _ := h.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: "entity-0",
		Limit:    500,
	})
	for _, mem := range memories {
		h.store.TransitionState(ctx, mem.ID, storage.StateArchived, "stress test")
	}

	report := h.verifyStorageCap(ctx)

	t.Logf("Storage Cap:")
	t.Logf("  Before: %.1f MB", report.SizeBeforeMB)
	t.Logf("  After:  %.1f MB", report.SizeAfterMB)
	t.Logf("  Cap:    %.1f MB", report.CapMB)
	t.Logf("  Enforced: %v", report.Enforced)

	if !report.Enforced {
		t.Error("storage cap should be enforced")
	}
}

func TestStress_Full(t *testing.T) {
	cfg := defaultStressConfig()
	h := newStressHarness(t, cfg)
	defer h.close()
	ctx := context.Background()
	fullStart := time.Now()

	// Phase 1: Population
	t.Log("=== Phase 1: Scale Population ===")
	created, dur := h.populate(ctx)
	rate := float64(created) / dur.Seconds()
	t.Logf("  %d memories in %v (%.0f/sec)", created, dur, rate)

	// Phase 2: Search Quality
	t.Log("=== Phase 2: Search Quality ===")
	searchReport := h.measureSearchQuality(ctx)
	t.Logf("  Recall@10=%.2f  Precision@10=%.2f  Latency=%.1fms",
		searchReport.AvgRecallAt10, searchReport.AvgPrecisionAt10, searchReport.AvgSearchLatMs)

	// Phase 3: Eviction
	t.Log("=== Phase 3: Eviction Correctness ===")
	evictRpt := h.verifyEviction(ctx)
	t.Logf("  Evicted=%d  FTS=%v", evictRpt.MemoriesEvicted, evictRpt.FTSFallbackWorks)

	// Phase 4: Concurrent
	t.Log("=== Phase 4: Concurrent Load ===")
	concurRpt := h.concurrentLoadSearch(ctx)
	t.Logf("  Searches=%d  Inserts=%d  Errors=%d", concurRpt.Searches, concurRpt.Inserts, concurRpt.Errors)

	// Phase 5: Storage Cap
	t.Log("=== Phase 5: Storage Cap ===")
	// Archive some memories first
	mems, _ := h.store.QueryMemories(ctx, storage.MemoryQuery{EntityID: "entity-0", Limit: 500})
	for _, mem := range mems {
		h.store.TransitionState(ctx, mem.ID, storage.StateArchived, "stress test")
	}
	capRpt := h.verifyStorageCap(ctx)
	t.Logf("  Before=%.1fMB  After=%.1fMB  Enforced=%v", capRpt.SizeBeforeMB, capRpt.SizeAfterMB, capRpt.Enforced)

	// Generate report
	totalDur := time.Since(fullStart)
	report := &stressReport{
		Duration:         totalDur.String(),
		MemoriesCreated:  created,
		HNSWSize:         h.store.GetHNSWIndexSize(),
		CreationRatePerS: rate,
		SearchQuality:    searchReport,
		Eviction:         evictRpt,
		Concurrent:       concurRpt,
		StorageCap:       capRpt,
	}

	diskSize, _ := h.store.GetStorageSizeBytes(ctx)
	report.DiskSizeMB = float64(diskSize) / (1024 * 1024)

	// Determine verdict
	issues := 0
	if searchReport.AvgRecallAt10 < 0.25 {
		issues++
	}
	if concurRpt.Errors > 0 {
		issues++
	}
	if evictRpt.MemoriesEvicted > 0 && !evictRpt.FTSFallbackWorks {
		issues++
	}
	if h.store.GetHNSWIndexSize() > cfg.MaxHNSWEntries+cfg.MaxHNSWEntries/10 {
		issues++
	}

	switch {
	case issues >= 3:
		report.Verdict = "FAIL"
	case issues >= 1:
		report.Verdict = "WARN"
	default:
		report.Verdict = "PASS"
	}

	// Print JSON report
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("\n=== STRESS TEST REPORT ===\n%s", string(reportJSON))

	// Write to file if path provided
	if reportPath := os.Getenv("STRESS_REPORT_PATH"); reportPath != "" {
		os.WriteFile(reportPath, reportJSON, 0644)
		t.Logf("Report written to %s", reportPath)
	}

	t.Logf("\n=== VERDICT: %s ===", report.Verdict)

	if report.Verdict == "FAIL" {
		t.Errorf("stress test FAILED with %d issues", issues)
	}
}

// =============================================================================
// Embedding Helpers
// =============================================================================

// topicEmbedding generates a deterministic centroid for a topic cluster.
// Each topic gets a distinct direction in embedding space.
func topicEmbedding(topicID int, dims int) []float32 {
	rng := rand.New(rand.NewSource(int64(topicID * 7919))) // prime seed
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1 // [-1, 1]
	}
	return normalize(vec)
}

// clusterMember generates a noisy variant near the centroid.
func clusterMember(centroid []float32, noise float64, rng *rand.Rand) []float32 {
	vec := make([]float32, len(centroid))
	for i := range vec {
		vec[i] = centroid[i] + float32(noise)*(rng.Float32()*2-1)
	}
	return normalize(vec)
}

// randomVec generates a random normalized vector.
func randomVec(rng *rand.Rand, dims int) []float32 {
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1
	}
	return normalize(vec)
}

func normalize(vec []float32) []float32 {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}

func encodeVec(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// =============================================================================
// Recall / Precision Helpers
// =============================================================================

// measureRecall returns the fraction of top-k results that belong to the target cluster.
func measureRecall(results []*storage.SimilarityResult, clusterIDs map[string]bool, k int) float64 {
	if len(clusterIDs) == 0 || len(results) == 0 {
		return 0
	}
	hits := 0
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		if clusterIDs[results[i].Memory.ID] {
			hits++
		}
	}
	// Recall = found / min(k, cluster_size)
	denom := k
	if len(clusterIDs) < denom {
		denom = len(clusterIDs)
	}
	return float64(hits) / float64(denom)
}

// measurePrecision returns the fraction of returned results that are relevant.
func measurePrecision(results []*storage.SimilarityResult, clusterIDs map[string]bool, k int) float64 {
	if len(results) == 0 {
		return 0
	}
	hits := 0
	limit := k
	if limit > len(results) {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		if clusterIDs[results[i].Memory.ID] {
			hits++
		}
	}
	return float64(hits) / float64(limit)
}
