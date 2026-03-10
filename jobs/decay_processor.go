// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// hasCronTag returns true if the memory's tags contain any cron:* schedule tag.
// Cron-tagged memories are exempt from decay — they represent explicit schedules
// (like writing on a calendar) and must remain active until explicitly cancelled.
func hasCronTag(tags storage.StringSlice) bool {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "cron:") {
			return true
		}
	}
	return false
}

// DecayProcessor evaluates memories and transitions states based on the Ebbinghaus decay curve.
// Enhanced to use access-frequency-aware decay and importance boosting.
type DecayProcessor struct {
	store  storage.Store
	logger *slog.Logger
	config DecayJobConfig
}

// DecayJobConfig holds configuration for decay processing.
type DecayJobConfig struct {
	BatchSize        int
	ThresholdStale   float64
	ThresholdArchive float64
}

// DefaultDecayJobConfig returns default decay job configuration.
func DefaultDecayJobConfig() DecayJobConfig {
	return DecayJobConfig{
		BatchSize:        1000,
		ThresholdStale:   engine.DecayThresholdStale,
		ThresholdArchive: engine.DecayThresholdArchive,
	}
}

// NewDecayProcessor creates a new decay processor.
func NewDecayProcessor(store storage.Store, logger *slog.Logger, config DecayJobConfig) *DecayProcessor {
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}
	if config.ThresholdStale <= 0 {
		config.ThresholdStale = engine.DecayThresholdStale
	}
	if config.ThresholdArchive <= 0 {
		config.ThresholdArchive = engine.DecayThresholdArchive
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DecayProcessor{
		store:  store,
		logger: logger.With("processor", "decay"),
		config: config,
	}
}

func (p *DecayProcessor) Type() JobType { return JobTypeDecay }

func (p *DecayProcessor) Process(ctx context.Context) (*JobResult, error) {
	p.logger.Info("starting decay processing")

	var totalProcessed, totalAffected, transitionsToStale, transitionsToArchive, importanceBoosted int
	offset := 0

	for {
		memories, err := p.store.GetActiveMemoriesForDecay(ctx, p.config.BatchSize, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to get memories for decay: %w", err)
		}
		if len(memories) == 0 {
			break
		}

		var transitions []storage.StateTransition

		for _, mem := range memories {
			totalProcessed++

			// Cron-tagged memories never decay. They represent explicit schedules
			// and must stay active until explicitly cancelled or archived.
			if hasCronTag(mem.Tags) {
				continue
			}

			// Use access-aware decay: frequently accessed memories resist decay.
			decayFactor := engine.CalculateDecayFactorWithAccess(mem.LastAccessedAt, mem.Stability, mem.AccessCount)
			targetState := engine.DetermineDecayState(decayFactor)
			newState := storage.MemoryState(targetState)

			// Access-burst importance boost: if a memory is being hammered by
			// the AI, it's clearly important — boost its importance score.
			boost := engine.CalculateAccessBurstImportanceBoost(mem.AccessCount, mem.LastAccessedAt)
			if boost > 0 && mem.Importance < 1.0 {
				newImportance := mem.Importance + boost
				if newImportance > 1.0 {
					newImportance = 1.0
				}
				_, err := p.store.UpdateMemory(ctx, mem.ID, storage.MemoryUpdate{
					Importance: &newImportance,
				})
				if err == nil {
					importanceBoosted++
				}
			}

			if mem.State != newState {
				var reason string
				switch newState {
				case storage.StateStale:
					reason = fmt.Sprintf("decay factor %.3f below stale threshold %.3f (access_count=%d)", decayFactor, p.config.ThresholdStale, mem.AccessCount)
					transitionsToStale++
				case storage.StateArchived:
					reason = fmt.Sprintf("decay factor %.3f below archive threshold %.3f (access_count=%d)", decayFactor, p.config.ThresholdArchive, mem.AccessCount)
					transitionsToArchive++
				}

				transitions = append(transitions, storage.StateTransition{
					MemoryID: mem.ID,
					NewState: newState,
					Reason:   reason,
				})
			}
		}

		if len(transitions) > 0 {
			affected, err := p.store.BatchTransitionStates(ctx, transitions)
			if err != nil {
				p.logger.Error("failed to apply transitions", "error", err)
			} else {
				totalAffected += affected
			}
		}

		offset += len(memories)
		if len(memories) < p.config.BatchSize {
			break
		}
	}

	p.logger.Info("decay processing complete",
		"processed", totalProcessed,
		"affected", totalAffected,
		"to_stale", transitionsToStale,
		"to_archive", transitionsToArchive,
		"importance_boosted", importanceBoosted,
	)

	return &JobResult{
		ItemsProcessed: totalProcessed,
		ItemsAffected:  totalAffected,
		Details: map[string]any{
			"transitions_to_stale":   transitionsToStale,
			"transitions_to_archive": transitionsToArchive,
			"importance_boosted":     importanceBoosted,
		},
	}, nil
}
