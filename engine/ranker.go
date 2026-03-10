// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package engine

import (
	"math"
	"sort"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// RankCalculator computes eviction rank for memories.
// Higher rank = more valuable = keep in HNSW longer.
// Used to decide which memories get demoted from Tier 2 (HNSW) to Tier 3 (SQLite-only).
type RankCalculator struct{}

// Rank computes the eviction rank for a memory.
// Formula: max(importance, 0.01) × (1 + ln(1 + accessCount)) × recencyBoost × ageDecay
// The importance floor (0.01) prevents memories with importance=0 from being permanently stuck at rank=0.
// Age decay gently penalizes memories that haven't been accessed in a very long time.
func (r *RankCalculator) Rank(mem *storage.Memory) float64 {
	importance := math.Max(mem.Importance, 0.01) // floor: prevent rank=0 dead zone
	accessFactor := 1.0 + math.Log(1.0+float64(mem.AccessCount))
	boost := recencyBoostFor(mem.LastAccessedAt)
	decay := ageDecayFor(mem.LastAccessedAt)
	return importance * accessFactor * boost * decay
}

// ageDecayFor applies a gentle decay to memories that haven't been accessed in a very long time.
// This ensures that old, unaccessed memories gradually lose rank relative to active ones.
// Decay starts after 30 days and asymptotes to 0.5 (never fully kills rank).
func ageDecayFor(lastAccessed *time.Time) float64 {
	if lastAccessed == nil {
		return 0.8 // unknown access time — slight penalty
	}
	days := time.Since(*lastAccessed).Hours() / 24.0
	if days <= 30 {
		return 1.0
	}
	// Exponential decay: 1/(1 + ln(days/30)), bottoms out at ~0.5
	return 1.0 / (1.0 + math.Log(days/30.0))
}

// recencyBoostFor returns a multiplier based on how recently a memory was accessed.
// Recent access gives a temporary advantage, but old memories don't lose intrinsic rank.
func recencyBoostFor(lastAccessed *time.Time) float64 {
	if lastAccessed == nil {
		return 1.0
	}
	hours := time.Since(*lastAccessed).Hours()
	switch {
	case hours < 24:
		return 2.0
	case hours < 168: // 7 days
		return 1.5
	case hours < 720: // 30 days
		return 1.2
	default:
		return 1.0
	}
}

// RankMemories sorts memories by rank descending and returns the sorted slice.
// Does not modify the input slice.
func (r *RankCalculator) RankMemories(memories []*storage.Memory) []*RankedMemory {
	ranked := make([]*RankedMemory, len(memories))
	for i, m := range memories {
		ranked[i] = &RankedMemory{
			Memory: m,
			Rank:   r.Rank(m),
		}
	}
	// Sort descending by rank (highest first, O(n log n))
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Rank > ranked[j].Rank
	})
	return ranked
}

// RankedMemory pairs a memory with its computed rank.
type RankedMemory struct {
	Memory *storage.Memory
	Rank   float64
}
