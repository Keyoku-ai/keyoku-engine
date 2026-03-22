// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// --- Memory CRUD ---

func (s *SQLiteStore) CreateMemory(ctx context.Context, mem *Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mem.ID == "" {
		mem.ID = ulid.Make().String()
	}
	if mem.AgentID == "" {
		mem.AgentID = "default"
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = time.Now().UTC()
	}
	mem.UpdatedAt = mem.CreatedAt
	now := mem.CreatedAt.UTC().Format(time.RFC3339)

	tags, _ := mem.Tags.Value()
	impFactors, _ := mem.ImportanceFactors.Value()
	confFactors, _ := mem.ConfidenceFactors.Value()
	derivedFrom, _ := mem.DerivedFrom.Value()

	var lastAccessed *string
	if mem.LastAccessedAt != nil {
		v := mem.LastAccessedAt.UTC().Format(time.RFC3339)
		lastAccessed = &v
	}

	var expiresAt *string
	if mem.ExpiresAt != nil {
		v := mem.ExpiresAt.UTC().Format(time.RFC3339)
		expiresAt = &v
	}

	visibility := string(mem.Visibility)
	if visibility == "" {
		visibility = string(VisibilityPrivate)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, source, session_id, extraction_provider, extraction_model,
			importance_factors, confidence_factors, sentiment, derived_from, visibility)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, mem.EntityID, mem.AgentID, mem.TeamID, mem.Content, mem.Hash, mem.Embedding,
		string(mem.Type), tags, mem.Importance, mem.Confidence, mem.Stability,
		mem.AccessCount, lastAccessed, string(mem.State), now, now,
		expiresAt, mem.Source, mem.SessionID, mem.ExtractionProvider, mem.ExtractionModel,
		impFactors, confFactors, mem.Sentiment, derivedFrom, visibility,
	)
	if err != nil {
		return err
	}

	// Add embedding to HNSW index
	if len(mem.Embedding) > 0 {
		vec := decodeEmbedding(mem.Embedding)
		if len(vec) > 0 {
			s.currentIndex().Add(mem.ID, vec)
		}
	}

	return nil
}

func (s *SQLiteStore) GetMemory(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE id = ?`, id)

	return scanMemory(row)
}

func (s *SQLiteStore) GetMemoriesByIDs(ctx context.Context, ids []string) ([]*Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemories(rows)
}

func (s *SQLiteStore) UpdateMemory(ctx context.Context, id string, updates MemoryUpdate) (*Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var setClauses []string
	var args []any

	if updates.Content != nil {
		setClauses = append(setClauses, "content = ?")
		args = append(args, *updates.Content)
	}
	if updates.Importance != nil {
		setClauses = append(setClauses, "importance = ?")
		args = append(args, *updates.Importance)
	}
	if updates.Confidence != nil {
		setClauses = append(setClauses, "confidence = ?")
		args = append(args, *updates.Confidence)
	}
	if updates.Tags != nil {
		tagsJSON, _ := json.Marshal(*updates.Tags)
		setClauses = append(setClauses, "tags = ?")
		args = append(args, string(tagsJSON))
	}
	if updates.State != nil {
		setClauses = append(setClauses, "state = ?")
		args = append(args, string(*updates.State))
	}
	if updates.ExpiresAt != nil {
		setClauses = append(setClauses, "expires_at = ?")
		args = append(args, updates.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if updates.Sentiment != nil {
		setClauses = append(setClauses, "sentiment = ?")
		args = append(args, *updates.Sentiment)
	}
	if updates.DerivedFrom != nil {
		derivedJSON, _ := json.Marshal(*updates.DerivedFrom)
		setClauses = append(setClauses, "derived_from = ?")
		args = append(args, string(derivedJSON))
	}
	if updates.Visibility != nil {
		setClauses = append(setClauses, "visibility = ?")
		args = append(args, *updates.Visibility)
	}

	if len(setClauses) == 0 {
		return s.GetMemory(ctx, id)
	}

	setClauses = append(setClauses, "updated_at = ?", "version = version + 1")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE memories SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...)
	if err != nil {
		return nil, err
	}

	return s.getMemoryUnlocked(ctx, id)
}

func (s *SQLiteStore) DeleteMemory(ctx context.Context, id string, hard bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hard {
		_, err := s.db.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", id)
		if err == nil {
			s.currentIndex().Remove(id)
		}
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET state = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ?",
		now, now, id)
	if err == nil {
		s.currentIndex().Remove(id)
	}
	return err
}

func (s *SQLiteStore) ResolveMemory(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET state = ?, updated_at = ? WHERE id = ?",
		string(StateResolved), now, id)
	return err
}

// --- Deduplication ---

func (s *SQLiteStore) FindByHash(ctx context.Context, entityID, hash string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE entity_id = ? AND content_hash = ? AND state != 'deleted' LIMIT 1`,
		entityID, hash)
	mem, err := scanMemory(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return mem, err
}

func (s *SQLiteStore) FindByHashWithAgent(ctx context.Context, entityID, agentID, hash string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE entity_id = ? AND agent_id = ? AND content_hash = ? AND state != 'deleted' LIMIT 1`,
		entityID, agentID, hash)
	mem, err := scanMemory(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return mem, err
}

// --- History ---

func (s *SQLiteStore) LogHistory(ctx context.Context, entry *HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = ulid.Make().String()
	}
	changesJSON, _ := json.Marshal(entry.Changes)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO history (id, memory_id, operation, changes, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.MemoryID, entry.Operation, string(changesJSON), entry.Reason, now)
	return err
}

func (s *SQLiteStore) GetHistory(ctx context.Context, memoryID string, limit int) ([]*HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, memory_id, operation, changes, reason, created_at
		FROM history WHERE memory_id = ? ORDER BY created_at DESC LIMIT ?`,
		memoryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var changesStr string
		var createdStr string
		if err := rows.Scan(&e.ID, &e.MemoryID, &e.Operation, &changesStr, &e.Reason, &createdStr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(changesStr), &e.Changes)
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		entries = append(entries, &e)
	}
	return entries, nil
}

// --- Access Tracking ---

func (s *SQLiteStore) UpdateAccessStats(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	ph := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, now)
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE memories SET access_count = access_count + 1, last_accessed_at = ? WHERE id IN (%s)",
			strings.Join(ph, ",")),
		args...)
	return err
}

func (s *SQLiteStore) UpdateStability(ctx context.Context, id string, newStability float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET stability = ? WHERE id = ?", newStability, id)
	return err
}
