package engine

import (
	"math"
	"time"

	"github.com/keyoku-ai/keyoku-embedded/storage"
)

// Decay thresholds for state transitions.
const (
	DecayThresholdStale   = 0.3
	DecayThresholdArchive = 0.1
	DecayThresholdDelete  = 0.01
)

// CalculateDecayFactor calculates the current decay factor for a memory
// using the Ebbinghaus forgetting curve: retention(t) = e^(-t/stability).
func CalculateDecayFactor(lastAccessedAt *time.Time, stability float64) float64 {
	if stability <= 0 {
		stability = 60
	}

	var daysSinceAccess float64
	if lastAccessedAt == nil {
		daysSinceAccess = 0
	} else {
		daysSinceAccess = time.Since(*lastAccessedAt).Hours() / 24
	}

	if daysSinceAccess < 0 {
		daysSinceAccess = 0
	}

	return math.Exp(-daysSinceAccess / stability)
}

// GetStabilityForType returns the default stability in days for a memory type.
func GetStabilityForType(memType storage.MemoryType) float64 {
	return memType.StabilityDays()
}

// StabilityGrowthFactor calculates how much to increase stability after an access.
func StabilityGrowthFactor(daysSinceLastAccess float64) float64 {
	switch {
	case daysSinceLastAccess < 1:
		return 1.05
	case daysSinceLastAccess < 7:
		return 1.10
	case daysSinceLastAccess < 30:
		return 1.20
	default:
		return 1.40
	}
}

// CalculateNewStability calculates the new stability after an access.
func CalculateNewStability(currentStability float64, lastAccessedAt *time.Time) float64 {
	var daysSince float64
	if lastAccessedAt != nil {
		daysSince = time.Since(*lastAccessedAt).Hours() / 24
	}
	growthFactor := StabilityGrowthFactor(daysSince)
	return currentStability * growthFactor
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
