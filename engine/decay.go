// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"math"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// Decay thresholds for state transitions.
const (
	DecayThresholdStale   = 0.3
	DecayThresholdArchive = 0.1
	DecayThresholdDelete  = 0.01
)

// CalculateDecayFactor calculates the current decay factor for a memory
// using an enhanced Ebbinghaus forgetting curve that accounts for access frequency.
//
// Base formula: retention(t) = e^(-t/effective_stability)
//
// Access-frequency modifier:
//   effective_stability = base_stability × (1 + ln(1 + access_count) × 0.5)
//
// A memory accessed 10 times has ~1.7x the stability of one accessed once.
// A memory accessed 50 times has ~2.46x stability. This means frequently
// retrieved memories (which AI agents do constantly) resist decay much harder.
func CalculateDecayFactor(lastAccessedAt *time.Time, stability float64) float64 {
	return CalculateDecayFactorWithAccess(lastAccessedAt, stability, 0)
}

// CalculateDecayFactorWithAccess is the full decay calculation that factors in access count.
func CalculateDecayFactorWithAccess(lastAccessedAt *time.Time, stability float64, accessCount int) float64 {
	if stability <= 0 {
		stability = 60
	}

	// Access-frequency modifier: frequently accessed memories decay slower.
	// ln(1 + 0) = 0, so zero accesses = no modifier (backward compatible).
	// ln(1 + 10) × 0.5 ≈ 1.20 → effective = stability × 2.20
	// ln(1 + 50) × 0.5 ≈ 1.96 → effective = stability × 2.96
	effectiveStability := stability * (1.0 + math.Log(1.0+float64(accessCount))*0.5)

	var daysSinceAccess float64
	if lastAccessedAt == nil {
		daysSinceAccess = 0
	} else {
		daysSinceAccess = time.Since(*lastAccessedAt).Hours() / 24
	}

	if daysSinceAccess < 0 {
		daysSinceAccess = 0
	}

	return math.Exp(-daysSinceAccess / effectiveStability)
}

// GetStabilityForType returns the default stability in days for a memory type.
func GetStabilityForType(memType storage.MemoryType) float64 {
	return memType.StabilityDays()
}

// StabilityGrowthFactor calculates how much to increase stability after an access.
//
// Enhanced for AI agents: stronger growth factors because AI retrieves memories
// far more frequently than humans. A memory that keeps getting pulled up by
// semantic search is clearly important and should be reinforced aggressively.
//
// Also adds an access-count multiplier: the more a memory has been accessed,
// the stronger each subsequent access reinforces it (compounding strength).
func StabilityGrowthFactor(daysSinceLastAccess float64) float64 {
	switch {
	case daysSinceLastAccess < 1:
		return 1.10 // was 1.05 — same-day re-access still meaningful for AI
	case daysSinceLastAccess < 7:
		return 1.20 // was 1.10 — weekly access = strong signal
	case daysSinceLastAccess < 30:
		return 1.35 // was 1.20 — monthly recall = very strong
	default:
		return 1.60 // was 1.40 — long-term recall = critical memory
	}
}

// StabilityGrowthFactorWithAccess applies a compounding bonus based on total access count.
// Memories accessed many times grow stability faster with each subsequent access.
func StabilityGrowthFactorWithAccess(daysSinceLastAccess float64, accessCount int) float64 {
	base := StabilityGrowthFactor(daysSinceLastAccess)

	// Compounding bonus: each access makes future growth slightly stronger.
	// accessCount 1: ×1.0, 5: ×1.04, 10: ×1.06, 50: ×1.10, 100: ×1.12
	// Capped at 1.15x to prevent runaway growth.
	compoundBonus := 1.0 + math.Min(0.15, math.Log(1.0+float64(accessCount))*0.025)

	return base * compoundBonus
}

// CalculateNewStability calculates the new stability after an access.
func CalculateNewStability(currentStability float64, lastAccessedAt *time.Time) float64 {
	return CalculateNewStabilityWithAccess(currentStability, lastAccessedAt, 0)
}

// CalculateNewStabilityWithAccess calculates new stability factoring in total access count.
func CalculateNewStabilityWithAccess(currentStability float64, lastAccessedAt *time.Time, accessCount int) float64 {
	var daysSince float64
	if lastAccessedAt != nil {
		daysSince = time.Since(*lastAccessedAt).Hours() / 24
	}
	growthFactor := StabilityGrowthFactorWithAccess(daysSince, accessCount)
	return currentStability * growthFactor
}

// CalculateAccessBurstImportanceBoost checks if a memory has been accessed
// frequently in a short window and returns an importance boost.
//
// If accessCount accesses happened within the last accessWindowDays days,
// the memory is clearly significant to the AI's current work.
//
// Returns: importance boost (0.0 to 0.3), to be added to current importance.
func CalculateAccessBurstImportanceBoost(accessCount int, lastAccessedAt *time.Time) float64 {
	if lastAccessedAt == nil || accessCount < 3 {
		return 0
	}

	daysSince := time.Since(*lastAccessedAt).Hours() / 24
	if daysSince > 1 {
		return 0 // Only boost if recently accessed
	}

	// 3 accesses in a day: +0.05, 10: +0.15, 20+: +0.25 (capped at 0.3)
	boost := math.Min(0.3, float64(accessCount)*0.015)
	return boost
}

// DecayState represents what state a memory should be in based on its decay.
type DecayState string

const (
	DecayStateActive   DecayState = "active"
	DecayStateStale    DecayState = "stale"
	DecayStateArchived DecayState = "archived"
	DecayStateDeleted  DecayState = "deleted"
)

// DetermineDecayState determines what state a memory should be in based on decay.
func DetermineDecayState(decayFactor float64) DecayState {
	switch {
	case decayFactor >= DecayThresholdStale:
		return DecayStateActive
	case decayFactor >= DecayThresholdArchive:
		return DecayStateStale
	case decayFactor >= DecayThresholdDelete:
		return DecayStateArchived
	default:
		return DecayStateDeleted
	}
}

// HalfLife calculates the half-life in days for a given stability.
func HalfLife(stability float64) float64 {
	return stability * math.Ln2
}

// TimeUntilDecay calculates how many days until memory reaches a threshold.
func TimeUntilDecay(stability float64, targetDecay float64) float64 {
	if targetDecay <= 0 || targetDecay >= 1 {
		return 0
	}
	return -stability * math.Log(targetDecay)
}
