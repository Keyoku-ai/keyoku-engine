// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- Queries ---

// allowedOrderByColumns restricts ORDER BY to valid column names, preventing SQL injection.
var allowedOrderByColumns = map[string]bool{
	"created_at": true, "updated_at": true, "importance": true,
	"confidence": true, "access_count": true, "deleted_at": true,
	"last_accessed_at": true, "stability": true,
}

func (s *SQLiteStore) QueryMemories(ctx context.Context, query MemoryQuery) ([]*Memory, error) {
	var where []string
	var args []any

	if query.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, query.EntityID)
	}
	if query.AgentID != "" {
		where = append(where, "agent_id = ?")
		args = append(args, query.AgentID)
	}
	if query.TeamID != "" {
		where = append(where, "team_id = ?")
		args = append(args, query.TeamID)
	}
	// Visibility-based access control (private/team/global resolution)
	if query.VisibilityFor != nil {
		vc := query.VisibilityFor
		if vc.TeamID != "" {
			where = append(where, `((visibility = 'private' AND agent_id = ?) OR (visibility = 'team' AND team_id = ?) OR (visibility = 'global'))`)
			args = append(args, vc.AgentID, vc.TeamID)
		} else {
			where = append(where, `((visibility = 'private' AND agent_id = ?) OR (visibility = 'global'))`)
			args = append(args, vc.AgentID)
		}
	}
	if len(query.Visibility) > 0 {
		ph := make([]string, len(query.Visibility))
		for i, v := range query.Visibility {
			ph[i] = "?"
			args = append(args, string(v))
		}
		where = append(where, fmt.Sprintf("visibility IN (%s)", strings.Join(ph, ",")))
	}
	if len(query.Types) > 0 {
		ph := make([]string, len(query.Types))
		for i, t := range query.Types {
			ph[i] = "?"
			args = append(args, string(t))
		}
		where = append(where, fmt.Sprintf("memory_type IN (%s)", strings.Join(ph, ",")))
	}
	if len(query.States) > 0 {
		ph := make([]string, len(query.States))
		for i, st := range query.States {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, fmt.Sprintf("state IN (%s)", strings.Join(ph, ",")))
	}
	if len(query.Tags) > 0 {
		likeEscaper := strings.NewReplacer("%", "\\%", "_", "\\_")
		for _, tag := range query.Tags {
			escaped := likeEscaper.Replace(tag)
			where = append(where, "tags LIKE ? ESCAPE '\\'")
			args = append(args, "%"+escaped+"%")
		}
	}
	if query.TagPrefix != "" {
		escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(query.TagPrefix)
		where = append(where, "tags LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escaped+"%")
	}
	if !query.CreatedBefore.IsZero() {
		where = append(where, "created_at <= ?")
		args = append(args, query.CreatedBefore.UTC().Format(time.RFC3339))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	orderBy := "created_at"
	if query.OrderBy != "" {
		if !allowedOrderByColumns[query.OrderBy] {
			return nil, fmt.Errorf("invalid order_by column: %q", query.OrderBy)
		}
		orderBy = query.OrderBy
	}
	direction := "ASC"
	if query.Descending {
		direction = "DESC"
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	// Cursor-based pagination (keyset): use cursor ID to find the boundary
	if query.Cursor != "" {
		op := ">"
		if query.Descending {
			op = "<"
		}
		where = append(where, fmt.Sprintf("%s %s (SELECT %s FROM memories WHERE id = ?)", orderBy, op, orderBy))
		args = append(args, query.Cursor)
		// Rebuild where clause with cursor condition
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories %s ORDER BY %s %s LIMIT ?`,
		whereClause, orderBy, direction)

	args = append(args, limit)

	// Only use OFFSET when not using cursor pagination
	if query.Cursor == "" && query.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, query.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemories(rows)
}

// AggregatedStats holds SQL-aggregated memory statistics (no full object loading).
type AggregatedStats struct {
	TotalMemories  int            `json:"total_memories"`
	ActiveMemories int            `json:"active_memories"`
	EntityCount    int            `json:"entity_count"`
	ByType         map[string]int `json:"by_type"`
	ByState        map[string]int `json:"by_state"`
}

// AggregateStats returns memory statistics using SQL aggregation.
// If entityID is empty, returns global stats across all entities.
func (s *SQLiteStore) AggregateStats(ctx context.Context, entityID string) (*AggregatedStats, error) {
	stats := &AggregatedStats{
		ByType:  make(map[string]int),
		ByState: make(map[string]int),
	}

	// Count by type and state in a single query
	var where string
	var args []any
	if entityID != "" {
		where = "WHERE entity_id = ? AND deleted_at IS NULL"
		args = []any{entityID}
	} else {
		where = "WHERE deleted_at IS NULL"
	}

	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT memory_type, state, COUNT(*) as cnt FROM memories %s GROUP BY memory_type, state`, where),
		args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate stats query failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var memType, state string
		var cnt int
		if err := rows.Scan(&memType, &state, &cnt); err != nil {
			return nil, err
		}
		stats.ByType[memType] += cnt
		stats.ByState[state] += cnt
		stats.TotalMemories += cnt
		if state == "active" {
			stats.ActiveMemories += cnt
		}
	}

	// Count distinct entities
	entityRow := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(DISTINCT entity_id) FROM memories %s`, where),
		args...)
	if err := entityRow.Scan(&stats.EntityCount); err != nil {
		stats.EntityCount = 0
	}

	return stats, nil
}

// SampleMemories returns a representative sample of memories using server-side SQL.
// Strategy: high-importance first, then recent, then random fill.
func (s *SQLiteStore) SampleMemories(ctx context.Context, entityID string, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 150
	}
	third := limit / 3

	var entityFilter string
	var baseArgs []any
	if entityID != "" {
		entityFilter = "AND entity_id = ?"
		baseArgs = []any{entityID}
	}

	// High importance (top third)
	highQ := fmt.Sprintf(
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE deleted_at IS NULL AND state IN ('active','stale') %s
		ORDER BY importance DESC LIMIT ?`, entityFilter)
	highArgs := append(append([]any{}, baseArgs...), third)

	highRows, err := s.db.QueryContext(ctx, highQ, highArgs...)
	if err != nil {
		return nil, fmt.Errorf("sample high-importance query failed: %w", err)
	}
	highMems, err := scanMemories(highRows)
	highRows.Close()
	if err != nil {
		return nil, err
	}

	// Collect IDs to exclude
	seen := make(map[string]bool, len(highMems))
	for _, m := range highMems {
		seen[m.ID] = true
	}

	// Recent (last 24h, not already included)
	recentQ := fmt.Sprintf(
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE deleted_at IS NULL AND state IN ('active','stale')
		AND created_at > datetime('now', '-1 day') %s
		ORDER BY created_at DESC LIMIT ?`, entityFilter)
	recentArgs := append(append([]any{}, baseArgs...), third*2) // fetch extra to filter dupes

	recentRows, err := s.db.QueryContext(ctx, recentQ, recentArgs...)
	if err != nil {
		return nil, fmt.Errorf("sample recent query failed: %w", err)
	}
	recentMems, err := scanMemories(recentRows)
	recentRows.Close()
	if err != nil {
		return nil, err
	}

	var recentFiltered []*Memory
	for _, m := range recentMems {
		if !seen[m.ID] && len(recentFiltered) < third {
			seen[m.ID] = true
			recentFiltered = append(recentFiltered, m)
		}
	}

	// Random fill for remainder
	remaining := limit - len(highMems) - len(recentFiltered)
	if remaining <= 0 {
		result := append(highMems, recentFiltered...)
		return result, nil
	}

	randomQ := fmt.Sprintf(
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE deleted_at IS NULL AND state IN ('active','stale') %s
		ORDER BY RANDOM() LIMIT ?`, entityFilter)
	randomArgs := append(append([]any{}, baseArgs...), remaining*3) // fetch extra to filter dupes

	randomRows, err := s.db.QueryContext(ctx, randomQ, randomArgs...)
	if err != nil {
		return nil, fmt.Errorf("sample random query failed: %w", err)
	}
	randomMems, err := scanMemories(randomRows)
	randomRows.Close()
	if err != nil {
		return nil, err
	}

	var randomFiltered []*Memory
	for _, m := range randomMems {
		if !seen[m.ID] && len(randomFiltered) < remaining {
			seen[m.ID] = true
			randomFiltered = append(randomFiltered, m)
		}
	}

	result := make([]*Memory, 0, len(highMems)+len(recentFiltered)+len(randomFiltered))
	result = append(result, highMems...)
	result = append(result, recentFiltered...)
	result = append(result, randomFiltered...)
	return result, nil
}

func (s *SQLiteStore) GetRecentMemories(ctx context.Context, entityID string, hours int, limit int) ([]*Memory, error) {
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour).UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE entity_id = ? AND created_at > ? AND state = 'active'
		ORDER BY created_at DESC LIMIT ?`, entityID, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) GetStaleMemories(ctx context.Context, entityID string, decayThreshold float64) ([]*Memory, error) {
	return s.QueryMemories(ctx, MemoryQuery{
		EntityID: entityID,
		States:   []MemoryState{StateStale},
		Limit:    100,
	})
}

// --- Storage Metrics ---

func (s *SQLiteStore) GetMemoryCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories WHERE state != 'deleted'").Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetMemoryCountForEntity(ctx context.Context, entityID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories WHERE entity_id = ? AND state != 'deleted'", entityID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetStorageSizeBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}
