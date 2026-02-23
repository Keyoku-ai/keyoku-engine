package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/keyoku-ai/keyoku-embedded/engine"
	"github.com/keyoku-ai/keyoku-embedded/storage"
)

// DecayProcessor evaluates memories and transitions states based on the Ebbinghaus decay curve.
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

	var totalProcessed, totalAffected, transitionsToStale, transitionsToArchive int
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

			decayFactor := engine.CalculateDecayFactor(mem.LastAccessedAt, mem.Stability)
			targetState := engine.DetermineDecayState(decayFactor)
			newState := storage.MemoryState(targetState)

			if mem.State != newState {
				var reason string
				switch newState {
				case storage.StateStale:
					reason = fmt.Sprintf("decay factor %.3f below stale threshold %.3f", decayFactor, p.config.ThresholdStale)
					transitionsToStale++
				case storage.StateArchived:
					reason = fmt.Sprintf("decay factor %.3f below archive threshold %.3f", decayFactor, p.config.ThresholdArchive)
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
	)

	return &JobResult{
		ItemsProcessed: totalProcessed,
		ItemsAffected:  totalAffected,
		Details: map[string]any{
			"transitions_to_stale":   transitionsToStale,
			"transitions_to_archive": transitionsToArchive,
		},
	}, nil
}
