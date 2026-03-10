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

// PurgeProcessor permanently deletes soft-deleted memories past their retention period.
type PurgeProcessor struct {
	store  storage.Store
	logger *slog.Logger
	config PurgeJobConfig
}

// PurgeJobConfig holds configuration for purge processing.
type PurgeJobConfig struct {
	BatchSize     int
	RetentionDays int
}

// DefaultPurgeJobConfig returns default purge configuration.
func DefaultPurgeJobConfig() PurgeJobConfig {
	return PurgeJobConfig{
		BatchSize:     500,
		RetentionDays: 90,
	}
}

// NewPurgeProcessor creates a new purge processor.
func NewPurgeProcessor(store storage.Store, logger *slog.Logger, config PurgeJobConfig) *PurgeProcessor {
	if config.BatchSize <= 0 {
		config.BatchSize = 500
	}
	if config.RetentionDays <= 0 {
		config.RetentionDays = 90
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PurgeProcessor{
		store:  store,
		logger: logger.With("processor", "purge"),
		config: config,
	}
}

func (p *PurgeProcessor) Type() JobType { return JobTypePurge }

func (p *PurgeProcessor) Process(ctx context.Context) (*JobResult, error) {
	p.logger.Info("starting purge processing")

	entities, err := p.store.GetAllEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get entities: %w", err)
	}

	var totalProcessed, totalPurged int

	query := storage.MemoryQuery{
		States:     []storage.MemoryState{storage.StateDeleted},
		Limit:      p.config.BatchSize,
		OrderBy:    "deleted_at",
		Descending: false,
	}

	retentionDuration := time.Duration(p.config.RetentionDays) * 24 * time.Hour

	for _, entityID := range entities {
		query.EntityID = entityID

		memories, err := p.store.QueryMemories(ctx, query)
		if err != nil {
			p.logger.Error("failed to query deleted memories", "entity", entityID, "error", err)
			continue
		}

		for _, mem := range memories {
			totalProcessed++
			if mem.DeletedAt != nil && time.Since(*mem.DeletedAt) > retentionDuration {
				if err := p.store.DeleteMemory(ctx, mem.ID, true); err != nil {
					p.logger.Error("failed to purge memory", "id", mem.ID, "error", err)
				} else {
					totalPurged++
				}
			}
		}
	}

	p.logger.Info("purge processing complete",
		"processed", totalProcessed,
		"purged", totalPurged,
	)

	return &JobResult{
		ItemsProcessed: totalProcessed,
		ItemsAffected:  totalPurged,
		Details: map[string]any{
			"retention_days": p.config.RetentionDays,
		},
	}, nil
}
