// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"context"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// Delete removes a memory by ID.
func (e *Engine) Delete(ctx context.Context, id string) error {
	return e.store.DeleteMemory(ctx, id, false)
}

// DeleteAll removes all memories, entities, and relationships for the entity.
func (e *Engine) DeleteAll(ctx context.Context, entityID string) error {
	// Delete relationships first (FK constraints)
	if _, err := e.store.DeleteAllRelationshipsForOwner(ctx, entityID); err != nil {
		return err
	}
	if _, err := e.store.DeleteAllEntitiesForOwner(ctx, entityID); err != nil {
		return err
	}

	memories, err := e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: entityID,
		Limit:    10000,
	})
	if err != nil {
		return err
	}

	for _, m := range memories {
		if err := e.store.DeleteMemory(ctx, m.ID, true); err != nil {
			return err
		}
	}

	return nil
}

// Stats contains memory statistics.
type Stats struct {
	TotalMemories int
	ByType        map[storage.MemoryType]int
	ByState       map[storage.MemoryState]int
}

// GetStats returns statistics about stored memories.
func (e *Engine) GetStats(ctx context.Context, entityID string) (*Stats, error) {
	memories, err := e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: entityID,
		Limit:    10000,
		States:   []storage.MemoryState{storage.StateActive, storage.StateStale, storage.StateResolved, storage.StateArchived},
	})
	if err != nil {
		return nil, err
	}

	stats := &Stats{
		TotalMemories: len(memories),
		ByType:        make(map[storage.MemoryType]int),
		ByState:       make(map[storage.MemoryState]int),
	}

	for _, m := range memories {
		stats.ByType[m.Type]++
		stats.ByState[m.State]++
	}

	return stats, nil
}

// GetGlobalStats returns SQL-aggregated stats. Empty entityID = global.
func (e *Engine) GetGlobalStats(ctx context.Context, entityID string) (*storage.AggregatedStats, error) {
	return e.store.AggregateStats(ctx, entityID)
}

// GetSampleMemories returns a representative sample using server-side SQL.
func (e *Engine) GetSampleMemories(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	return e.store.SampleMemories(ctx, entityID, limit)
}

// Close closes the engine and releases resources.
func (e *Engine) Close() error {
	if closer, ok := e.provider.(interface{ Close() error }); ok {
		_ = closer.Close() //nolint:errcheck
	}
	return nil
}
