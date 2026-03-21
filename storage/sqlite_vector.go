// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// --- Vector Search (HNSW-backed) ---

func (s *SQLiteStore) FindSimilar(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*SimilarityResult, error) {
	candidateCount := limit * 5
	if candidateCount < 50 {
		candidateCount = 50
	}

	searchResults, err := s.currentIndex().Search(embedding, candidateCount)
	if err != nil {
		if !shouldAttemptHNSWRebuild(err) {
			return nil, fmt.Errorf("HNSW search failed: %w", err)
		}
		if rebuildErr := s.rebuildIndexWithLogging("runtime-search", err); rebuildErr != nil {
			return nil, fmt.Errorf("HNSW search failed: %w", err)
		}
		searchResults, err = s.currentIndex().Search(embedding, candidateCount)
		if err != nil {
			return nil, fmt.Errorf("HNSW search failed after rebuild: %w", err)
		}
	}

	if len(searchResults) == 0 {
		return nil, nil
	}

	ids := make([]string, len(searchResults))
	distByID := make(map[string]float32, len(searchResults))
	for i, sr := range searchResults {
		ids[i] = sr.ID
		distByID[sr.ID] = sr.Distance
	}

	memories, err := s.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	var results []*SimilarityResult
	for _, m := range memories {
		if m.EntityID != entityID {
			continue
		}
		if m.State == StateDeleted || m.State == StateArchived {
			continue
		}
		sim := float64(1 - distByID[m.ID])
		if sim < minScore {
			continue
		}
		results = append(results, &SimilarityResult{Memory: m, Similarity: sim})
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

func (s *SQLiteStore) FindSimilarWithOptions(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64, opts SimilarityOptions) ([]*SimilarityResult, error) {
	candidateCount := limit * 5
	if candidateCount < 50 {
		candidateCount = 50
	}

	searchResults, err := s.currentIndex().Search(embedding, candidateCount)
	if err != nil {
		if !shouldAttemptHNSWRebuild(err) {
			return nil, fmt.Errorf("HNSW search failed: %w", err)
		}
		if rebuildErr := s.rebuildIndexWithLogging("runtime-search", err); rebuildErr != nil {
			return nil, fmt.Errorf("HNSW search failed: %w", err)
		}
		searchResults, err = s.currentIndex().Search(embedding, candidateCount)
		if err != nil {
			return nil, fmt.Errorf("HNSW search failed after rebuild: %w", err)
		}
	}

	if len(searchResults) == 0 {
		return nil, nil
	}

	ids := make([]string, len(searchResults))
	distByID := make(map[string]float32, len(searchResults))
	for i, sr := range searchResults {
		ids[i] = sr.ID
		distByID[sr.ID] = sr.Distance
	}

	memories, err := s.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	var results []*SimilarityResult
	for _, m := range memories {
		if m.EntityID != entityID {
			continue
		}
		if m.State == StateDeleted || m.State == StateArchived {
			continue
		}
		if opts.AgentID != "" && m.AgentID != opts.AgentID {
			continue
		}
		if opts.VisibilityFor != nil && !IsVisibleTo(m.Visibility, m.AgentID, m.TeamID, opts.VisibilityFor) {
			continue
		}
		sim := float64(1 - distByID[m.ID])
		if sim < minScore {
			continue
		}
		results = append(results, &SimilarityResult{Memory: m, Similarity: sim})
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

// decodeEmbedding converts bytes from SQLite BLOB to float32 slice.
func decodeEmbedding(data []byte) []float32 {
	if len(data) == 0 || len(data)%4 != 0 {
		return nil
	}
	result := make([]float32, len(data)/4)
	for i := range result {
		bits := uint32(data[i*4+0]) |
			uint32(data[i*4+1])<<8 |
			uint32(data[i*4+2])<<16 |
			uint32(data[i*4+3])<<24
		result[i] = math.Float32frombits(bits)
	}
	return result
}

// --- Full-Text Search (Tier 3) ---

func (s *SQLiteStore) SearchFTS(ctx context.Context, query string, entityID string, limit int) ([]*Memory, error) {
	return s.SearchFTSWithOptions(ctx, query, entityID, limit, SimilarityOptions{})
}

func (s *SQLiteStore) SearchFTSWithOptions(ctx context.Context, query string, entityID string, limit int, opts SimilarityOptions) ([]*Memory, error) {
	if limit <= 0 {
		limit = 10
	}

	// Use FTS5 MATCH query
	sql := `SELECT m.id, m.entity_id, m.agent_id, m.team_id, m.content, m.content_hash, m.embedding,
		m.memory_type, m.tags, m.importance, m.confidence, m.stability,
		m.access_count, m.last_accessed_at, m.state, m.created_at, m.updated_at,
		m.expires_at, m.deleted_at, m.version, m.source, m.session_id,
		m.extraction_provider, m.extraction_model, m.importance_factors, m.confidence_factors,
		m.sentiment, m.derived_from, m.visibility
	FROM memories m
	JOIN memories_fts fts ON fts.memory_id = m.id
	WHERE fts.content MATCH ?
	AND m.entity_id = ?
	AND m.state IN ('active', 'stale', 'resolved')`

	args := []any{query, entityID}

	if opts.AgentID != "" {
		sql += ` AND m.agent_id = ?`
		args = append(args, opts.AgentID)
	}
	if opts.VisibilityFor != nil {
		vc := opts.VisibilityFor
		if vc.TeamID != "" {
			sql += ` AND ((m.visibility = 'private' AND m.agent_id = ?) OR (m.visibility = 'team' AND m.team_id = ?) OR (m.visibility = 'global'))`
			args = append(args, vc.AgentID, vc.TeamID)
		} else {
			sql += ` AND ((m.visibility = 'private' AND m.agent_id = ?) OR (m.visibility = 'global'))`
			args = append(args, vc.AgentID)
		}
	}

	sql += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// --- HNSW Index Management ---

func (s *SQLiteStore) GetHNSWIndexSize() int {
	return s.currentIndex().Len()
}

func (s *SQLiteStore) GetLowestRankedInHNSW(ctx context.Context, limit int) ([]*Memory, error) {
	// Get all IDs currently in the HNSW index
	ids := s.currentIndex().IDs()
	if len(ids) == 0 {
		return nil, nil
	}

	memories, err := s.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}

	return memories, nil
}

func (s *SQLiteStore) RemoveFromHNSW(id string) error {
	return s.currentIndex().Remove(id)
}

func shouldAttemptHNSWRebuild(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	if strings.Contains(errStr, "dimensions") && strings.Contains(errStr, "expected") {
		return false
	}
	return true
}
