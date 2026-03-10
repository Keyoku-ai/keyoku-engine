// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package jobs

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// JobTypeEviction is the job type for HNSW index eviction.
const JobTypeEviction JobType = "eviction"

// EvictionProcessor enforces HNSW index bounds and storage caps.
// It demotes lowest-ranked memories from Tier 2 (HNSW) to Tier 3 (SQLite-only)
// and hard-deletes lowest-ranked memories when storage exceeds the cap.
type EvictionProcessor struct {
	store       storage.Store
	logger      *slog.Logger
	config      EvictionJobConfig
	onEvicted   func(id string) // callback to invalidate hot cache on HNSW eviction
}

// EvictionJobConfig holds configuration for eviction processing.
type EvictionJobConfig struct {
	MaxHNSWEntries  int   // max vectors in HNSW index (default: 10000)
	MaxStorageBytes int64 // total storage cap in bytes (default: 500MB)
	BatchSize       int   // how many to evict per run (default: 100)
}

// DefaultEvictionJobConfig returns default eviction configuration.
func DefaultEvictionJobConfig() EvictionJobConfig {
	return EvictionJobConfig{
		MaxHNSWEntries:  10000,
		MaxStorageBytes: 500 * 1024 * 1024,
		BatchSize:       100,
	}
}

// SetOnEvicted sets a callback invoked for each memory evicted from HNSW.
// Use this to invalidate the hot cache when the eviction processor runs.
func (p *EvictionProcessor) SetOnEvicted(fn func(id string)) {
	p.onEvicted = fn
}

// NewEvictionProcessor creates a new eviction processor.
func NewEvictionProcessor(store storage.Store, logger *slog.Logger, config EvictionJobConfig) *EvictionProcessor {
	if config.MaxHNSWEntries <= 0 {
		config.MaxHNSWEntries = 10000
	}
	if config.MaxStorageBytes <= 0 {
		config.MaxStorageBytes = 500 * 1024 * 1024
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 100
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &EvictionProcessor{
		store:  store,
		logger: logger.With("processor", "eviction"),
		config: config,
	}
}

func (p *EvictionProcessor) Type() JobType { return JobTypeEviction }

func (p *EvictionProcessor) Process(ctx context.Context) (*JobResult, error) {
	var totalEvicted, totalDeleted int

	// === Phase 1: HNSW Index Eviction ===
	currentSize := p.store.GetHNSWIndexSize()
	if currentSize > p.config.MaxHNSWEntries {
		excess := currentSize - p.config.MaxHNSWEntries
		if excess > p.config.BatchSize {
			excess = p.config.BatchSize
		}

		// Get all memories in HNSW, rank them, evict lowest
		memories, err := p.store.GetLowestRankedInHNSW(ctx, 0)
		if err != nil {
			p.logger.Error("failed to get HNSW memories for eviction", "error", err)
		} else if len(memories) > 0 {
			ranked := rankMemories(memories)

			// Evict from the bottom (lowest rank)
			for i := len(ranked) - 1; i >= 0 && totalEvicted < excess; i-- {
				mem := ranked[i]
				if err := p.store.RemoveFromHNSW(mem.id); err != nil {
					p.logger.Warn("failed to evict from HNSW", "id", mem.id, "error", err)
					continue
				}
				if p.onEvicted != nil {
					p.onEvicted(mem.id)
				}
				totalEvicted++
			}

			p.logger.Info("HNSW eviction completed",
				"evicted", totalEvicted,
				"previous_size", currentSize,
				"new_size", p.store.GetHNSWIndexSize(),
				"cap", p.config.MaxHNSWEntries)
		}
	}

	// === Phase 2: Storage Cap Enforcement ===
	storageBytes, err := p.store.GetStorageSizeBytes(ctx)
	if err != nil {
		p.logger.Error("failed to get storage size", "error", err)
	} else if storageBytes > p.config.MaxStorageBytes {
		p.logger.Warn("storage exceeds cap",
			"current_mb", storageBytes/(1024*1024),
			"cap_mb", p.config.MaxStorageBytes/(1024*1024))

		// Delete lowest-ranked archived/stale memories to free space
		query := storage.MemoryQuery{
			States:     []storage.MemoryState{storage.StateArchived, storage.StateStale},
			Limit:      p.config.BatchSize,
			OrderBy:    "importance",
			Descending: false, // lowest importance first
		}

		entities, _ := p.store.GetAllEntities(ctx)
		for _, entityID := range entities {
			query.EntityID = entityID
			memories, err := p.store.QueryMemories(ctx, query)
			if err != nil {
				continue
			}

			for _, mem := range memories {
				if err := p.store.DeleteMemory(ctx, mem.ID, true); err != nil {
					p.logger.Warn("failed to hard-delete memory", "id", mem.ID, "error", err)
					continue
				}
				totalDeleted++

				// Re-check storage after each deletion batch
				if totalDeleted%10 == 0 {
					newSize, err := p.store.GetStorageSizeBytes(ctx)
					if err == nil && newSize <= p.config.MaxStorageBytes {
						break
					}
				}
			}
		}

		if totalDeleted > 0 {
			p.logger.Info("storage cap enforcement completed",
				"deleted", totalDeleted,
				"cap_mb", p.config.MaxStorageBytes/(1024*1024))
		}
	}

	return &JobResult{
		ItemsProcessed: currentSize,
		ItemsAffected:  totalEvicted + totalDeleted,
		Details: map[string]any{
			"hnsw_evicted":   totalEvicted,
			"storage_deleted": totalDeleted,
			"hnsw_size":      p.store.GetHNSWIndexSize(),
			"hnsw_cap":       p.config.MaxHNSWEntries,
		},
	}, nil
}

// rankedMem pairs a memory ID with its rank for sorting.
type rankedMem struct {
	id   string
	rank float64
}

// rankMemories computes eviction ranks for memories and returns sorted by rank descending.
func rankMemories(memories []*storage.Memory) []rankedMem {
	ranked := make([]rankedMem, len(memories))
	for i, m := range memories {
		accessFactor := 1.0 + math.Log(1.0+float64(m.AccessCount))
		boost := 1.0
		if m.LastAccessedAt != nil {
			hours := time.Since(*m.LastAccessedAt).Hours()
			switch {
			case hours < 24:
				boost = 2.0
			case hours < 168:
				boost = 1.5
			case hours < 720:
				boost = 1.2
			}
		}
		importance := math.Max(m.Importance, 0.01) // floor: prevent rank=0 dead zone
		// Age decay: gently penalize very old unaccessed memories
		decay := 1.0
		if m.LastAccessedAt != nil {
			days := time.Since(*m.LastAccessedAt).Hours() / 24.0
			if days > 30 {
				decay = 1.0 / (1.0 + math.Log(days/30.0))
			}
		} else {
			decay = 0.8 // unknown access time — slight penalty
		}
		ranked[i] = rankedMem{
			id:   m.ID,
			rank: importance * accessFactor * boost * decay,
		}
	}

	// Sort descending by rank (highest first, lowest at end for eviction, O(n log n))
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].rank > ranked[j].rank
	})
	return ranked
}
