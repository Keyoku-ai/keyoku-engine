// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"time"
)

// pstLocation is the US/Pacific timezone used as default for quiet hours.
var pstLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.FixedZone("PST", -8*60*60)
	}
	return loc
}()

// TimePeriod constants for time-of-day awareness.
const (
	PeriodMorning   = "morning"    // 7-10: proactive window
	PeriodWorking   = "working"    // 10-17: normal operations
	PeriodEvening   = "evening"    // 17-21: wind-down, less proactive
	PeriodLateNight = "late_night" // 21-23: minimal interruption
	PeriodQuiet     = "quiet"      // 23-7: only immediate urgency
)

// currentTimePeriod returns the time period for the current wall clock.
func (k *Keyoku) currentTimePeriod() string {
	return k.currentTimePeriodAt(time.Now())
}

// currentTimePeriodAt returns the time period for a specific instant.
func (k *Keyoku) currentTimePeriodAt(now time.Time) string {
	if k.timePeriodOverride != "" {
		return k.timePeriodOverride
	}
	loc := pstLocation
	if k.quietHours.Location != nil {
		loc = k.quietHours.Location
	}
	hour := now.In(loc).Hour()
	switch {
	case hour >= 7 && hour < 10:
		return PeriodMorning
	case hour >= 10 && hour < 17:
		return PeriodWorking
	case hour >= 17 && hour < 21:
		return PeriodEvening
	case hour >= 21 && hour < 23:
		return PeriodLateNight
	default: // 23-7
		if !k.quietHours.Enabled {
			return PeriodLateNight // no quiet hours = treat as late night
		}
		return PeriodQuiet
	}
}

// timePeriodMinTier returns the minimum urgency tier required for a time period.
func timePeriodMinTier(period string) string {
	switch period {
	case PeriodMorning, PeriodWorking:
		return TierLow // everything allowed
	case PeriodEvening:
		return TierNormal
	case PeriodLateNight:
		return TierElevated
	case PeriodQuiet:
		return TierImmediate
	default:
		return TierLow
	}
}

// timePeriodCooldownMultiplier returns the cooldown multiplier for a time period.
func timePeriodCooldownMultiplier(period string) float64 {
	switch period {
	case PeriodMorning:
		return 0.5
	case PeriodWorking:
		return 1.0
	case PeriodEvening:
		return 1.5
	case PeriodLateNight:
		return 3.0
	case PeriodQuiet:
		return 3.0 // was 10x, but compound with response rate caused permanent silence
	default:
		return 1.0
	}
}

// tierRank returns a numeric rank for urgency tier comparison.
func tierRank(tier string) int {
	switch tier {
	case TierImmediate:
		return 4
	case TierElevated:
		return 3
	case TierNormal:
		return 2
	case TierLow:
		return 1
	default:
		return 0
	}
}

// isUserTypicallyActive checks if the user has historically been active at the current hour.
// Returns true if current hour accounts for >= 2% of total message volume over the last 14 days,
// or if there's insufficient data to determine a pattern.
func (k *Keyoku) isUserTypicallyActive(ctx context.Context, entityID string) bool {
	return k.isUserTypicallyActiveAt(ctx, entityID, time.Now())
}

func (k *Keyoku) isUserTypicallyActiveAt(ctx context.Context, entityID string, now time.Time) bool {
	dist, err := k.store.GetMessageHourDistribution(ctx, entityID, 14)
	if err != nil || len(dist) == 0 {
		return true // no data = assume active
	}
	total := 0
	for _, count := range dist {
		total += count
	}
	// Need substantial data before suppressing based on rhythm:
	// at least 100 messages across at least 5 distinct hours.
	// Otherwise the pattern is too sparse to be reliable.
	if total < 100 || len(dist) < 5 {
		return true // too few messages to determine pattern
	}
	loc := pstLocation
	if k.quietHours.Location != nil {
		loc = k.quietHours.Location
	}
	currentHour := now.In(loc).Hour()
	hourCount := dist[currentHour]
	return float64(hourCount)/float64(total) >= 0.02
}
