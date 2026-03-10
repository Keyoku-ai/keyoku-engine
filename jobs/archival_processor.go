// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// ArchivalProcessor archives stale memories that have been stale for too long.
type ArchivalProcessor struct {
	store  storage.Store
	logger *slog.Logger
	config ArchivalJobConfig
}

// ArchivalJobConfig holds configuration for archival processing.
type ArchivalJobConfig struct {
	BatchSize          int
	StaleThresholdDays int
}

// DefaultArchivalJobConfig returns default archival configuration.
func DefaultArchivalJobConfig() ArchivalJobConfig {
	return ArchivalJobConfig{
		BatchSize:          500,
		StaleThresholdDays: 30,
	}
}

// NewArchivalProcessor creates a new archival processor.
func NewArchivalProcessor(store storage.Store, logger *slog.Logger, config ArchivalJobConfig) *ArchivalProcessor {
	if config.BatchSize <= 0 {
		config.BatchSize = 500
	}
	if config.StaleThresholdDays <= 0 {
		config.StaleThresholdDays = 30
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ArchivalProcessor{
		store:  store,
		logger: logger.With("processor", "archival"),
		config: config,
	}
}

func (p *ArchivalProcessor) Type() JobType { return JobTypeArchival }

func (p *ArchivalProcessor) Process(ctx context.Context) (*JobResult, error) {
	p.logger.Info("starting archival processing")

	entities, err := p.store.GetAllEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get entities: %w", err)
	}

	var totalProcessed, totalArchived int
	cutoffTime := time.Now().AddDate(0, 0, -p.config.StaleThresholdDays)

	query := storage.MemoryQuery{
		States:     []storage.MemoryState{storage.StateStale},
		Limit:      p.config.BatchSize,
		OrderBy:    "updated_at",
		Descending: false,
	}

	for _, entityID := range entities {
		query.EntityID = entityID

		memories, err := p.store.QueryMemories(ctx, query)
		if err != nil {
			p.logger.Error("failed to query stale memories", "entity", entityID, "error", err)
			continue
		}

		var transitions []storage.StateTransition

		for _, mem := range memories {
			totalProcessed++
			if mem.UpdatedAt.Before(cutoffTime) {
				transitions = append(transitions, storage.StateTransition{
					MemoryID: mem.ID,
					NewState: storage.StateArchived,
					Reason:   fmt.Sprintf("stale for more than %d days", p.config.StaleThresholdDays),
				})
			}
		}

		if len(transitions) > 0 {
			affected, err := p.store.BatchTransitionStates(ctx, transitions)
			if err != nil {
				p.logger.Error("failed to archive memories", "entity", entityID, "error", err)
			} else {
				totalArchived += affected
			}
		}
	}

	p.logger.Info("archival processing complete",
		"processed", totalProcessed,
		"archived", totalArchived,
	)

	return &JobResult{
		ItemsProcessed: totalProcessed,
		ItemsAffected:  totalArchived,
		Details: map[string]any{
			"stale_threshold_days": p.config.StaleThresholdDays,
		},
	}, nil
}
