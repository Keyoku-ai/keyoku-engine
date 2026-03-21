// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// --- Scan Helpers ---

func (s *SQLiteStore) getMemoryUnlocked(ctx context.Context, id string) (*Memory, error) {
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

type scanner interface {
	Scan(dest ...any) error
}

func scanMemory(row scanner) (*Memory, error) {
	var m Memory
	var tagsStr, impFactorsStr, confFactorsStr, derivedFromStr string
	var lastAccessedStr, createdStr, updatedStr sql.NullString
	var expiresStr, deletedStr sql.NullString
	var memType, stateStr, visibilityStr string

	err := row.Scan(
		&m.ID, &m.EntityID, &m.AgentID, &m.TeamID, &m.Content, &m.Hash, &m.Embedding,
		&memType, &tagsStr, &m.Importance, &m.Confidence, &m.Stability,
		&m.AccessCount, &lastAccessedStr, &stateStr, &createdStr, &updatedStr,
		&expiresStr, &deletedStr, &m.Version, &m.Source, &m.SessionID,
		&m.ExtractionProvider, &m.ExtractionModel, &impFactorsStr, &confFactorsStr,
		&m.Sentiment, &derivedFromStr, &visibilityStr,
	)
	if err != nil {
		return nil, err
	}

	m.Type = MemoryType(memType)
	m.State = MemoryState(stateStr)
	m.Visibility = MemoryVisibility(visibilityStr)
	if m.Visibility == "" {
		m.Visibility = VisibilityPrivate
	}

	m.Tags.Scan(tagsStr)
	m.ImportanceFactors.Scan(impFactorsStr)
	m.ConfidenceFactors.Scan(confFactorsStr)
	m.DerivedFrom.Scan(derivedFromStr)

	if createdStr.Valid {
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr.String)
	}
	if updatedStr.Valid {
		m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr.String)
	}
	if lastAccessedStr.Valid {
		t, _ := time.Parse(time.RFC3339, lastAccessedStr.String)
		m.LastAccessedAt = &t
	}
	if expiresStr.Valid {
		t, _ := time.Parse(time.RFC3339, expiresStr.String)
		m.ExpiresAt = &t
	}
	if deletedStr.Valid {
		t, _ := time.Parse(time.RFC3339, deletedStr.String)
		m.DeletedAt = &t
	}

	return &m, nil
}

func scanMemories(rows *sql.Rows) ([]*Memory, error) {
	var memories []*Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}

func scanEntity(row scanner) (*Entity, error) {
	var e Entity
	var aliasesStr, attrsStr string
	var lastMentionedStr, createdStr, updatedStr sql.NullString
	var entityType string

	err := row.Scan(
		&e.ID, &e.OwnerEntityID, &e.AgentID, &e.CanonicalName,
		&entityType, &e.Description, &aliasesStr, &e.Embedding,
		&attrsStr, &e.MentionCount, &lastMentionedStr, &createdStr, &updatedStr,
	)
	if err != nil {
		return nil, err
	}

	e.Type = EntityType(entityType)
	e.Aliases.Scan(aliasesStr)
	e.Attributes.Scan(attrsStr)

	if createdStr.Valid {
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdStr.String)
	}
	if updatedStr.Valid {
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr.String)
	}
	if lastMentionedStr.Valid {
		t, _ := time.Parse(time.RFC3339, lastMentionedStr.String)
		e.LastMentionedAt = &t
	}

	return &e, nil
}

func scanEntityFromRows(rows *sql.Rows) (*Entity, error) {
	return scanEntity(rows)
}

func scanRelationship(row scanner) (*Relationship, error) {
	var r Relationship
	var attrsStr string
	var bidir int
	var firstSeenStr, lastSeenStr, createdStr, updatedStr string

	err := row.Scan(
		&r.ID, &r.OwnerEntityID, &r.AgentID, &r.SourceEntityID, &r.TargetEntityID,
		&r.RelationshipType, &r.Description, &r.Strength, &r.Confidence, &bidir,
		&r.EvidenceCount, &attrsStr, &firstSeenStr, &lastSeenStr, &createdStr, &updatedStr,
	)
	if err != nil {
		return nil, err
	}

	r.IsBidirectional = bidir != 0
	r.Attributes.Scan(attrsStr)
	r.FirstSeenAt, _ = time.Parse(time.RFC3339, firstSeenStr)
	r.LastSeenAt, _ = time.Parse(time.RFC3339, lastSeenStr)
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)

	return &r, nil
}

func scanRelationships(rows *sql.Rows) ([]*Relationship, error) {
	var rels []*Relationship
	for rows.Next() {
		r, err := scanRelationship(rows)
		if err != nil {
			return nil, err
		}
		rels = append(rels, r)
	}
	return rels, nil
}

func scanCustomExtractions(rows *sql.Rows) ([]*CustomExtraction, error) {
	var extractions []*CustomExtraction
	for rows.Next() {
		var ce CustomExtraction
		var dataStr, createdStr string
		if err := rows.Scan(&ce.ID, &ce.EntityID, &ce.MemoryID, &ce.SchemaID,
			&dataStr, &ce.ExtractionProvider, &ce.ExtractionModel,
			&ce.Confidence, &createdStr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(dataStr), &ce.ExtractedData)
		ce.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		extractions = append(extractions, &ce)
	}
	return extractions, nil
}

// --- Lifecycle ---

func (s *SQLiteStore) TransitionState(ctx context.Context, id string, newState MemoryState, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET state = ?, updated_at = ? WHERE id = ?",
		string(newState), now, id)
	if err == nil && (newState == StateDeleted || newState == StateArchived) {
		s.currentIndex().Remove(id)
	}
	return err
}

func (s *SQLiteStore) GetAllEntities(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT DISTINCT entity_id FROM memories WHERE state != 'deleted'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *SQLiteStore) GetActiveMemoriesForDecay(ctx context.Context, batchSize, offset int) ([]*Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, team_id, content, content_hash, embedding,
			memory_type, tags, importance, confidence, stability,
			access_count, last_accessed_at, state, created_at, updated_at,
			expires_at, deleted_at, version, source, session_id,
			extraction_provider, extraction_model, importance_factors, confidence_factors,
			sentiment, derived_from, visibility
		FROM memories WHERE state IN ('active', 'stale', 'resolved')
		ORDER BY created_at ASC LIMIT ? OFFSET ?`, batchSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) BatchTransitionStates(ctx context.Context, transitions []StateTransition) (int, error) {
	if len(transitions) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	affected := 0

	stmt, err := tx.PrepareContext(ctx,
		"UPDATE memories SET state = ?, updated_at = ? WHERE id = ?")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, t := range transitions {
		result, err := stmt.ExecContext(ctx, string(t.NewState), now, t.MemoryID)
		if err != nil {
			continue
		}
		n, _ := result.RowsAffected()
		affected += int(n)
		if t.NewState == StateDeleted || t.NewState == StateArchived {
			s.currentIndex().Remove(t.MemoryID)
		}
	}

	return affected, tx.Commit()
}
