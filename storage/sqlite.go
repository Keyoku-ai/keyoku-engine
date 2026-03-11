// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/keyoku-ai/keyoku-engine/vectorindex"
	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite + HNSW vector index.
type SQLiteStore struct {
	db     *sql.DB
	mu     sync.Mutex // serialized writes
	index  *vectorindex.HNSW
	dbPath string
}

// NewSQLite creates a new SQLite-backed store with an HNSW vector index.
func NewSQLite(dbPath string, dimensions int) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Single writer, multiple readers
	db.SetMaxOpenConns(1)

	hnswCfg := vectorindex.DefaultHNSWConfig(dimensions)
	index := vectorindex.NewHNSW(hnswCfg)

	s := &SQLiteStore{
		db:     db,
		index:  index,
		dbPath: dbPath,
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	// Try to load HNSW from disk, rebuild from BLOBs if it fails
	hnswPath := dbPath + ".hnsw"
	if err := index.Load(hnswPath); err != nil {
		s.rebuildIndex()
	}

	return s, nil
}

func (s *SQLiteStore) Close() error {
	// Persist HNSW index
	if s.dbPath != "" && s.dbPath != ":memory:" {
		s.index.Save(s.dbPath + ".hnsw")
	}
	return s.db.Close()
}

func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// rebuildIndex reconstructs the HNSW index from embedding BLOBs in SQLite.
func (s *SQLiteStore) rebuildIndex() {
	rows, err := s.db.Query(
		`SELECT id, embedding FROM memories WHERE state IN ('active', 'stale') AND embedding IS NOT NULL`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			continue
		}
		vec := decodeEmbedding(embBytes)
		if len(vec) > 0 {
			s.index.Add(id, vec)
		}
	}
}

// --- Schema Migration ---

func (s *SQLiteStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			embedding BLOB,
			memory_type TEXT NOT NULL CHECK(memory_type IN ('IDENTITY','PREFERENCE','RELATIONSHIP','EVENT','ACTIVITY','PLAN','CONTEXT','EPHEMERAL')),
			tags TEXT DEFAULT '[]',
			importance REAL NOT NULL DEFAULT 0.5,
			confidence REAL NOT NULL DEFAULT 0.5,
			stability REAL NOT NULL DEFAULT 60,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_accessed_at TEXT,
			state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','stale','archived','deleted')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT,
			deleted_at TEXT,
			version INTEGER NOT NULL DEFAULT 1,
			source TEXT DEFAULT '',
			session_id TEXT DEFAULT '',
			extraction_provider TEXT DEFAULT '',
			extraction_model TEXT DEFAULT '',
			importance_factors TEXT DEFAULT '[]',
			confidence_factors TEXT DEFAULT '[]'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_entity_id ON memories(entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_agent_id ON memories(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_state ON memories(state)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_hash ON memories(content_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(memory_type)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_entity_state ON memories(entity_id, state)`,

		`CREATE TABLE IF NOT EXISTS history (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			changes TEXT DEFAULT '{}',
			reason TEXT DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_history_memory_id ON history(memory_id)`,

		`CREATE TABLE IF NOT EXISTS session_messages (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			session_id TEXT DEFAULT '',
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			turn_number INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_messages_entity ON session_messages(entity_id)`,

		`CREATE TABLE IF NOT EXISTS entities (
			id TEXT PRIMARY KEY,
			owner_entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			canonical_name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'other',
			description TEXT DEFAULT '',
			aliases TEXT DEFAULT '[]',
			embedding BLOB,
			attributes TEXT DEFAULT '{}',
			mention_count INTEGER NOT NULL DEFAULT 0,
			last_mentioned_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_owner ON entities(owner_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(owner_entity_id, canonical_name, type)`,

		`CREATE TABLE IF NOT EXISTS entity_mentions (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			mention_text TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0.5,
			context_snippet TEXT DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE,
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_entity_mentions_entity ON entity_mentions(entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_entity_mentions_memory ON entity_mentions(memory_id)`,

		`CREATE TABLE IF NOT EXISTS relationships (
			id TEXT PRIMARY KEY,
			owner_entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			source_entity_id TEXT NOT NULL,
			target_entity_id TEXT NOT NULL,
			relationship_type TEXT NOT NULL,
			description TEXT DEFAULT '',
			strength REAL NOT NULL DEFAULT 0.5,
			confidence REAL NOT NULL DEFAULT 0.5,
			is_bidirectional INTEGER NOT NULL DEFAULT 0,
			evidence_count INTEGER NOT NULL DEFAULT 0,
			attributes TEXT DEFAULT '{}',
			first_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (source_entity_id) REFERENCES entities(id) ON DELETE CASCADE,
			FOREIGN KEY (target_entity_id) REFERENCES entities(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relationships_owner ON relationships(owner_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_relationships_source ON relationships(source_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_relationships_target ON relationships(target_entity_id)`,

		`CREATE TABLE IF NOT EXISTS relationship_evidence (
			id TEXT PRIMARY KEY,
			relationship_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			evidence_text TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0.5,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (relationship_id) REFERENCES relationships(id) ON DELETE CASCADE,
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rel_evidence_relationship ON relationship_evidence(relationship_id)`,

		`CREATE TABLE IF NOT EXISTS extraction_schemas (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			version TEXT DEFAULT '1.0',
			schema_definition TEXT NOT NULL DEFAULT '{}',
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(entity_id, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_schemas_entity ON extraction_schemas(entity_id)`,

		`CREATE TABLE IF NOT EXISTS custom_extractions (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			memory_id TEXT NOT NULL DEFAULT '',
			schema_id TEXT NOT NULL,
			extracted_data TEXT NOT NULL DEFAULT '{}',
			extraction_provider TEXT DEFAULT '',
			extraction_model TEXT DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0.5,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (schema_id) REFERENCES extraction_schemas(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_extractions_entity ON custom_extractions(entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_extractions_schema ON custom_extractions(schema_id)`,
		`CREATE INDEX IF NOT EXISTS idx_extractions_memory ON custom_extractions(memory_id)`,

		`CREATE TABLE IF NOT EXISTS agent_states (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			schema_name TEXT NOT NULL,
			current_state TEXT NOT NULL DEFAULT '{}',
			schema_definition TEXT NOT NULL DEFAULT '{}',
			transition_rules TEXT DEFAULT '{}',
			last_updated_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(entity_id, agent_id, schema_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_states_entity ON agent_states(entity_id, agent_id)`,

		`CREATE TABLE IF NOT EXISTS agent_state_history (
			id TEXT PRIMARY KEY,
			state_id TEXT NOT NULL,
			previous_state TEXT DEFAULT '{}',
			new_state TEXT NOT NULL DEFAULT '{}',
			changed_fields TEXT DEFAULT '[]',
			trigger_content TEXT DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0.5,
			reasoning TEXT DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (state_id) REFERENCES agent_states(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_state_history_state ON agent_state_history(state_id)`,

		// Team tables
		`CREATE TABLE IF NOT EXISTS teams (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			default_visibility TEXT NOT NULL DEFAULT 'team',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,

		`CREATE TABLE IF NOT EXISTS team_members (
			team_id TEXT NOT NULL,
			agent_id TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL DEFAULT 'member',
			joined_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (team_id, agent_id),
			FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_team_members_agent ON team_members(agent_id)`,

		// Heartbeat action tracking (cooldown, novelty, nudge decisions)
		`CREATE TABLE IF NOT EXISTS heartbeat_actions (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			acted_at TEXT NOT NULL DEFAULT (datetime('now')),
			trigger_category TEXT NOT NULL,
			signal_fingerprint TEXT NOT NULL,
			decision TEXT NOT NULL DEFAULT 'act',
			urgency_tier TEXT NOT NULL,
			llm_should_act INTEGER,
			signal_summary TEXT DEFAULT '',
			total_signals INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hb_actions_entity ON heartbeat_actions(entity_id, agent_id, acted_at DESC)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, stmt)
		}
	}

	// Add new columns (ignore errors for already-existing columns)
	alterStmts := []string{
		`ALTER TABLE memories ADD COLUMN sentiment REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE memories ADD COLUMN derived_from TEXT DEFAULT '[]'`,
		// Team & visibility columns
		`ALTER TABLE memories ADD COLUMN team_id TEXT DEFAULT ''`,
		`ALTER TABLE memories ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private'`,
		`ALTER TABLE entities ADD COLUMN team_id TEXT DEFAULT ''`,
		`ALTER TABLE relationships ADD COLUMN team_id TEXT DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		s.db.Exec(stmt) // ignore "duplicate column" errors
	}

	// Team-related indexes (ignore errors if already exist)
	teamIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_team_visibility ON memories(team_id, visibility)`,
	}
	for _, stmt := range teamIndexes {
		s.db.Exec(stmt)
	}

	// FTS5 virtual table for full-text search (Tier 3 fallback)
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
		content,
		memory_id UNINDEXED,
		entity_id UNINDEXED
	)`)

	// FTS triggers to keep index in sync
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS memories_fts_insert AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts(content, memory_id, entity_id)
		VALUES (new.content, new.id, new.entity_id);
	END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS memories_fts_delete AFTER DELETE ON memories BEGIN
		DELETE FROM memories_fts WHERE memory_id = old.id;
	END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS memories_fts_update AFTER UPDATE OF content ON memories BEGIN
		DELETE FROM memories_fts WHERE memory_id = old.id;
		INSERT INTO memories_fts(content, memory_id, entity_id)
		VALUES (new.content, new.id, new.entity_id);
	END`)

	// Performance indexes for reporting & aggregation
	perfIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_importance ON memories(importance DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_entity_created ON memories(entity_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_entity_importance ON memories(entity_id, importance DESC)`,
	}
	for _, stmt := range perfIndexes {
		s.db.Exec(stmt)
	}

	return nil
}

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
	now := time.Now().UTC().Format(time.RFC3339)
	mem.CreatedAt = time.Now().UTC()
	mem.UpdatedAt = mem.CreatedAt

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
			s.index.Add(mem.ID, vec)
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
			s.index.Remove(id)
		}
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET state = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ?",
		now, now, id)
	if err == nil {
		s.index.Remove(id)
	}
	return err
}

// --- Vector Search (HNSW-backed) ---

func (s *SQLiteStore) FindSimilar(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*SimilarityResult, error) {
	candidateCount := limit * 5
	if candidateCount < 50 {
		candidateCount = 50
	}

	searchResults, err := s.index.Search(embedding, candidateCount)
	if err != nil {
		return nil, fmt.Errorf("HNSW search failed: %w", err)
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

	searchResults, err := s.index.Search(embedding, candidateCount)
	if err != nil {
		return nil, fmt.Errorf("HNSW search failed: %w", err)
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

// --- Queries ---

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
		for _, tag := range query.Tags {
			where = append(where, "tags LIKE ?")
			args = append(args, "%"+tag+"%")
		}
	}
	if query.TagPrefix != "" {
		where = append(where, "tags LIKE ?")
		args = append(args, "%"+query.TagPrefix+"%")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	orderBy := "created_at"
	if query.OrderBy != "" {
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

// --- Session Messages ---

func (s *SQLiteStore) AddSessionMessage(ctx context.Context, msg *SessionMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.ID == "" {
		msg.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session_messages (id, entity_id, agent_id, session_id, role, content, turn_number, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.EntityID, msg.AgentID, msg.SessionID, msg.Role, msg.Content, msg.TurnNumber, now)
	return err
}

func (s *SQLiteStore) GetRecentSessionMessages(ctx context.Context, entityID string, limit int) ([]*SessionMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, session_id, role, content, turn_number, created_at
		FROM session_messages WHERE entity_id = ?
		ORDER BY created_at DESC LIMIT ?`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*SessionMessage
	for rows.Next() {
		var m SessionMessage
		var createdStr string
		if err := rows.Scan(&m.ID, &m.EntityID, &m.AgentID, &m.SessionID, &m.Role, &m.Content, &m.TurnNumber, &createdStr); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		messages = append(messages, &m)
	}
	return messages, nil
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

// --- Lifecycle ---

func (s *SQLiteStore) TransitionState(ctx context.Context, id string, newState MemoryState, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE memories SET state = ?, updated_at = ? WHERE id = ?",
		string(newState), now, id)
	if err == nil && (newState == StateDeleted || newState == StateArchived) {
		s.index.Remove(id)
	}
	return err
}

func (s *SQLiteStore) GetStaleMemories(ctx context.Context, entityID string, decayThreshold float64) ([]*Memory, error) {
	return s.QueryMemories(ctx, MemoryQuery{
		EntityID: entityID,
		States:   []MemoryState{StateStale},
		Limit:    100,
	})
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
		FROM memories WHERE state IN ('active', 'stale')
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
			s.index.Remove(t.MemoryID)
		}
	}

	return affected, tx.Commit()
}

// --- Entity CRUD ---

func (s *SQLiteStore) CreateEntity(ctx context.Context, entity *Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entity.ID == "" {
		entity.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	aliases, _ := entity.Aliases.Value()
	attrs, _ := entity.Attributes.Value()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO entities (id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entity.ID, entity.OwnerEntityID, entity.AgentID, entity.CanonicalName,
		string(entity.Type), entity.Description, aliases, entity.Embedding,
		attrs, entity.MentionCount, now, now)
	return err
}

func (s *SQLiteStore) GetEntity(ctx context.Context, id string) (*Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities WHERE id = ?`, id)
	return scanEntity(row)
}

func (s *SQLiteStore) GetEntityByName(ctx context.Context, ownerEntityID, name string, entityType EntityType) (*Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities WHERE owner_entity_id = ? AND canonical_name = ? AND type = ?`,
		ownerEntityID, name, string(entityType))
	entity, err := scanEntity(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return entity, err
}

func (s *SQLiteStore) FindEntityByAlias(ctx context.Context, ownerEntityID, alias string) (*Entity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities WHERE owner_entity_id = ? AND canonical_name = ?`,
		ownerEntityID, alias)
	entity, err := scanEntity(row)
	if err == nil {
		return entity, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities WHERE owner_entity_id = ?`, ownerEntityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			continue
		}
		for _, a := range e.Aliases {
			if strings.EqualFold(a, alias) {
				return e, nil
			}
		}
	}
	return nil, nil
}

func (s *SQLiteStore) FindSimilarEntities(ctx context.Context, embedding []float32, ownerEntityID string, limit int, minScore float64) ([]*Entity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities WHERE owner_entity_id = ? AND embedding IS NOT NULL`,
		ownerEntityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		entity *Entity
		sim    float64
	}
	var candidates []scored

	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			continue
		}
		entityVec := decodeEmbedding(e.Embedding)
		if len(entityVec) == 0 || len(entityVec) != len(embedding) {
			continue
		}
		sim := float64(vectorindex.CosineSimilarity(embedding, entityVec))
		if sim >= minScore {
			candidates = append(candidates, scored{entity: e, sim: sim})
		}
	}

	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].sim > candidates[j-1].sim; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	result := make([]*Entity, len(candidates))
	for i, c := range candidates {
		result[i] = c.entity
	}
	return result, nil
}

func (s *SQLiteStore) QueryEntities(ctx context.Context, query EntityQuery) ([]*Entity, error) {
	var where []string
	var args []any

	if query.OwnerEntityID != "" {
		where = append(where, "owner_entity_id = ?")
		args = append(args, query.OwnerEntityID)
	}
	if query.AgentID != "" {
		where = append(where, "agent_id = ?")
		args = append(args, query.AgentID)
	}
	if len(query.Types) > 0 {
		ph := make([]string, len(query.Types))
		for i, t := range query.Types {
			ph[i] = "?"
			args = append(args, string(t))
		}
		where = append(where, fmt.Sprintf("type IN (%s)", strings.Join(ph, ",")))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	q := fmt.Sprintf(
		`SELECT id, owner_entity_id, agent_id, canonical_name, type, description,
			aliases, embedding, attributes, mention_count, last_mentioned_at, created_at, updated_at
		FROM entities %s ORDER BY mention_count DESC LIMIT ? OFFSET ?`,
		whereClause)
	args = append(args, limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	return entities, nil
}

func (s *SQLiteStore) UpdateEntity(ctx context.Context, id string, updates map[string]any) (*Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var setClauses []string
	var args []any

	for k, v := range updates {
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE entities SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...)
	if err != nil {
		return nil, err
	}
	return s.GetEntity(ctx, id)
}

func (s *SQLiteStore) UpdateEntityMentionCount(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE entities SET mention_count = mention_count + 1, last_mentioned_at = ?, updated_at = ? WHERE id = ?",
		now, now, id)
	return err
}

func (s *SQLiteStore) AddEntityAlias(ctx context.Context, id, alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var aliasesStr string
	err := s.db.QueryRowContext(ctx, "SELECT aliases FROM entities WHERE id = ?", id).Scan(&aliasesStr)
	if err != nil {
		return err
	}

	var aliases []string
	json.Unmarshal([]byte(aliasesStr), &aliases)

	for _, a := range aliases {
		if a == alias {
			return nil
		}
	}

	aliases = append(aliases, alias)
	newAliases, _ := json.Marshal(aliases)

	_, err = s.db.ExecContext(ctx,
		"UPDATE entities SET aliases = ?, updated_at = ? WHERE id = ?",
		string(newAliases), time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *SQLiteStore) DeleteEntity(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM entities WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) DeleteAllEntitiesForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx,
		"DELETE FROM entities WHERE owner_entity_id = ?", ownerEntityID)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) CreateEntityMention(ctx context.Context, mention *EntityMention) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if mention.ID == "" {
		mention.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO entity_mentions (id, entity_id, memory_id, mention_text, confidence, context_snippet, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		mention.ID, mention.EntityID, mention.MemoryID, mention.MentionText,
		mention.Confidence, mention.ContextSnippet, now)
	return err
}

func (s *SQLiteStore) GetEntityMentions(ctx context.Context, entityID string, limit int) ([]*EntityMention, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, memory_id, mention_text, confidence, context_snippet, created_at
		FROM entity_mentions WHERE entity_id = ? ORDER BY created_at DESC LIMIT ?`,
		entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mentions []*EntityMention
	for rows.Next() {
		var m EntityMention
		var createdStr string
		if err := rows.Scan(&m.ID, &m.EntityID, &m.MemoryID, &m.MentionText,
			&m.Confidence, &m.ContextSnippet, &createdStr); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		mentions = append(mentions, &m)
	}
	return mentions, nil
}

func (s *SQLiteStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*Entity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.owner_entity_id, e.agent_id, e.canonical_name, e.type, e.description,
			e.aliases, e.embedding, e.attributes, e.mention_count, e.last_mentioned_at, e.created_at, e.updated_at
		FROM entities e
		JOIN entity_mentions em ON em.entity_id = e.id
		WHERE em.memory_id = ?`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []*Entity
	for rows.Next() {
		e, err := scanEntityFromRows(rows)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	return entities, nil
}

// --- Relationship CRUD ---

func (s *SQLiteStore) CreateRelationship(ctx context.Context, rel *Relationship) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rel.ID == "" {
		rel.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	attrs, _ := rel.Attributes.Value()
	bidir := 0
	if rel.IsBidirectional {
		bidir = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO relationships (id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
			relationship_type, description, strength, confidence, is_bidirectional,
			evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rel.ID, rel.OwnerEntityID, rel.AgentID, rel.SourceEntityID, rel.TargetEntityID,
		rel.RelationshipType, rel.Description, rel.Strength, rel.Confidence, bidir,
		rel.EvidenceCount, attrs, now, now, now, now)
	return err
}

func (s *SQLiteStore) GetRelationship(ctx context.Context, id string) (*Relationship, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
			relationship_type, description, strength, confidence, is_bidirectional,
			evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
		FROM relationships WHERE id = ?`, id)
	return scanRelationship(row)
}

func (s *SQLiteStore) FindRelationship(ctx context.Context, ownerEntityID, sourceID, targetID, relType string) (*Relationship, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
			relationship_type, description, strength, confidence, is_bidirectional,
			evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
		FROM relationships
		WHERE owner_entity_id = ? AND source_entity_id = ? AND target_entity_id = ? AND relationship_type = ?`,
		ownerEntityID, sourceID, targetID, relType)
	rel, err := scanRelationship(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rel, err
}

func (s *SQLiteStore) GetEntityRelationships(ctx context.Context, ownerEntityID, entityID, direction string) ([]*Relationship, error) {
	var q string
	var args []any

	switch direction {
	case "outgoing":
		q = `SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
				relationship_type, description, strength, confidence, is_bidirectional,
				evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
			FROM relationships WHERE owner_entity_id = ? AND source_entity_id = ?`
		args = []any{ownerEntityID, entityID}
	case "incoming":
		q = `SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
				relationship_type, description, strength, confidence, is_bidirectional,
				evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
			FROM relationships WHERE owner_entity_id = ? AND target_entity_id = ?`
		args = []any{ownerEntityID, entityID}
	default:
		q = `SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
				relationship_type, description, strength, confidence, is_bidirectional,
				evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
			FROM relationships WHERE owner_entity_id = ? AND (source_entity_id = ? OR target_entity_id = ?)`
		args = []any{ownerEntityID, entityID, entityID}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRelationships(rows)
}

func (s *SQLiteStore) QueryRelationships(ctx context.Context, query RelationshipQuery) ([]*Relationship, error) {
	var where []string
	var args []any

	if query.OwnerEntityID != "" {
		where = append(where, "owner_entity_id = ?")
		args = append(args, query.OwnerEntityID)
	}
	if query.EntityID != "" {
		where = append(where, "(source_entity_id = ? OR target_entity_id = ?)")
		args = append(args, query.EntityID, query.EntityID)
	}
	if len(query.RelationshipTypes) > 0 {
		ph := make([]string, len(query.RelationshipTypes))
		for i, t := range query.RelationshipTypes {
			ph[i] = "?"
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf("relationship_type IN (%s)", strings.Join(ph, ",")))
	}
	if query.MinStrength > 0 {
		where = append(where, "strength >= ?")
		args = append(args, query.MinStrength)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	q := fmt.Sprintf(
		`SELECT id, owner_entity_id, agent_id, source_entity_id, target_entity_id,
			relationship_type, description, strength, confidence, is_bidirectional,
			evidence_count, attributes, first_seen_at, last_seen_at, created_at, updated_at
		FROM relationships %s ORDER BY strength DESC LIMIT ? OFFSET ?`,
		whereClause)
	args = append(args, limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationships(rows)
}

func (s *SQLiteStore) UpdateRelationship(ctx context.Context, id string, updates map[string]any) (*Relationship, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var setClauses []string
	var args []any
	for k, v := range updates {
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE relationships SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...)
	if err != nil {
		return nil, err
	}
	return s.GetRelationship(ctx, id)
}

func (s *SQLiteStore) IncrementRelationshipEvidence(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE relationships SET evidence_count = evidence_count + 1, last_seen_at = ?, updated_at = ? WHERE id = ?",
		now, now, id)
	return err
}

func (s *SQLiteStore) DeleteRelationship(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM relationships WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) CreateRelationshipEvidence(ctx context.Context, evidence *RelationshipEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if evidence.ID == "" {
		evidence.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO relationship_evidence (id, relationship_id, memory_id, evidence_text, confidence, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		evidence.ID, evidence.RelationshipID, evidence.MemoryID,
		evidence.EvidenceText, evidence.Confidence, now)
	return err
}

func (s *SQLiteStore) GetRelationshipEvidence(ctx context.Context, relationshipID string, limit int) ([]*RelationshipEvidence, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, relationship_id, memory_id, evidence_text, confidence, created_at
		FROM relationship_evidence WHERE relationship_id = ? ORDER BY created_at DESC LIMIT ?`,
		relationshipID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evidence []*RelationshipEvidence
	for rows.Next() {
		var e RelationshipEvidence
		var createdStr string
		if err := rows.Scan(&e.ID, &e.RelationshipID, &e.MemoryID,
			&e.EvidenceText, &e.Confidence, &createdStr); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		evidence = append(evidence, &e)
	}
	return evidence, nil
}

func (s *SQLiteStore) GetRelationshipPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string, maxDepth int) ([]string, error) {
	if fromEntityID == toEntityID {
		return []string{fromEntityID}, nil
	}

	visited := map[string]bool{fromEntityID: true}
	parent := map[string]string{}
	queue := []string{fromEntityID}

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		nextQueue := make([]string, 0)

		for _, current := range queue {
			rels, err := s.GetEntityRelationships(ctx, ownerEntityID, current, "both")
			if err != nil {
				continue
			}

			for _, rel := range rels {
				var targetID string
				if rel.SourceEntityID == current {
					targetID = rel.TargetEntityID
				} else {
					targetID = rel.SourceEntityID
				}

				if targetID == toEntityID {
					path := []string{toEntityID}
					curr := current
					for curr != fromEntityID {
						path = append([]string{curr}, path...)
						curr = parent[curr]
					}
					return append([]string{fromEntityID}, path...), nil
				}

				if !visited[targetID] {
					visited[targetID] = true
					parent[targetID] = current
					nextQueue = append(nextQueue, targetID)
				}
			}
		}

		queue = nextQueue
	}

	return nil, fmt.Errorf("no path found")
}

func (s *SQLiteStore) DeleteAllRelationshipsForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx,
		"DELETE FROM relationships WHERE owner_entity_id = ?", ownerEntityID)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// --- Schema CRUD ---

func (s *SQLiteStore) CreateSchema(ctx context.Context, schema *ExtractionSchema) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if schema.ID == "" {
		schema.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	schemaDef, _ := json.Marshal(schema.SchemaDefinition)
	isActive := 0
	if schema.IsActive {
		isActive = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO extraction_schemas (id, entity_id, name, description, version, schema_definition, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		schema.ID, schema.EntityID, schema.Name, schema.Description, schema.Version,
		string(schemaDef), isActive, now, now)
	if err != nil {
		return err
	}

	schema.CreatedAt, _ = time.Parse(time.RFC3339, now)
	schema.UpdatedAt = schema.CreatedAt
	return nil
}

func (s *SQLiteStore) GetSchema(ctx context.Context, id string) (*ExtractionSchema, error) {
	var es ExtractionSchema
	var schemaDefStr string
	var isActive int
	var createdStr, updatedStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, name, description, version, schema_definition, is_active, created_at, updated_at
		FROM extraction_schemas WHERE id = ?`, id).Scan(
		&es.ID, &es.EntityID, &es.Name, &es.Description, &es.Version,
		&schemaDefStr, &isActive, &createdStr, &updatedStr)
	if err != nil {
		return nil, err
	}

	es.IsActive = isActive != 0
	json.Unmarshal([]byte(schemaDefStr), &es.SchemaDefinition)
	es.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	es.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &es, nil
}

func (s *SQLiteStore) GetSchemaByName(ctx context.Context, entityID, name string) (*ExtractionSchema, error) {
	var es ExtractionSchema
	var schemaDefStr string
	var isActive int
	var createdStr, updatedStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, name, description, version, schema_definition, is_active, created_at, updated_at
		FROM extraction_schemas WHERE entity_id = ? AND name = ?`, entityID, name).Scan(
		&es.ID, &es.EntityID, &es.Name, &es.Description, &es.Version,
		&schemaDefStr, &isActive, &createdStr, &updatedStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	es.IsActive = isActive != 0
	json.Unmarshal([]byte(schemaDefStr), &es.SchemaDefinition)
	es.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	es.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &es, nil
}

func (s *SQLiteStore) QuerySchemas(ctx context.Context, query SchemaQuery) ([]*ExtractionSchema, error) {
	var where []string
	var args []any

	if query.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, query.EntityID)
	}
	if query.ActiveOnly {
		where = append(where, "is_active = 1")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	q := fmt.Sprintf(
		`SELECT id, entity_id, name, description, version, schema_definition, is_active, created_at, updated_at
		FROM extraction_schemas %s ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		whereClause)
	args = append(args, limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schemas []*ExtractionSchema
	for rows.Next() {
		var es ExtractionSchema
		var schemaDefStr string
		var isActive int
		var createdStr, updatedStr string
		if err := rows.Scan(&es.ID, &es.EntityID, &es.Name, &es.Description, &es.Version,
			&schemaDefStr, &isActive, &createdStr, &updatedStr); err != nil {
			return nil, err
		}
		es.IsActive = isActive != 0
		json.Unmarshal([]byte(schemaDefStr), &es.SchemaDefinition)
		es.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		es.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		schemas = append(schemas, &es)
	}
	return schemas, nil
}

func (s *SQLiteStore) UpdateSchema(ctx context.Context, id string, updates map[string]any) (*ExtractionSchema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var setClauses []string
	var args []any

	for k, v := range updates {
		if k == "schema_definition" {
			if def, ok := v.(map[string]any); ok {
				jsonBytes, _ := json.Marshal(def)
				setClauses = append(setClauses, "schema_definition = ?")
				args = append(args, string(jsonBytes))
				continue
			}
		}
		if k == "is_active" {
			if active, ok := v.(bool); ok {
				val := 0
				if active {
					val = 1
				}
				setClauses = append(setClauses, "is_active = ?")
				args = append(args, val)
				continue
			}
		}
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}

	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id)

	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE extraction_schemas SET %s WHERE id = ?", strings.Join(setClauses, ", ")),
		args...)
	if err != nil {
		return nil, err
	}
	return s.GetSchema(ctx, id)
}

func (s *SQLiteStore) DeleteSchema(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM extraction_schemas WHERE id = ?", id)
	return err
}

// --- Custom Extraction CRUD ---

func (s *SQLiteStore) CreateCustomExtraction(ctx context.Context, extraction *CustomExtraction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if extraction.ID == "" {
		extraction.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	dataJSON, _ := json.Marshal(extraction.ExtractedData)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO custom_extractions (id, entity_id, memory_id, schema_id, extracted_data, extraction_provider, extraction_model, confidence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		extraction.ID, extraction.EntityID, extraction.MemoryID, extraction.SchemaID,
		string(dataJSON), extraction.ExtractionProvider, extraction.ExtractionModel,
		extraction.Confidence, now)
	if err != nil {
		return err
	}

	extraction.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (s *SQLiteStore) GetCustomExtraction(ctx context.Context, id string) (*CustomExtraction, error) {
	var ce CustomExtraction
	var dataStr string
	var createdStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, memory_id, schema_id, extracted_data, extraction_provider, extraction_model, confidence, created_at
		FROM custom_extractions WHERE id = ?`, id).Scan(
		&ce.ID, &ce.EntityID, &ce.MemoryID, &ce.SchemaID,
		&dataStr, &ce.ExtractionProvider, &ce.ExtractionModel,
		&ce.Confidence, &createdStr)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(dataStr), &ce.ExtractedData)
	ce.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	return &ce, nil
}

func (s *SQLiteStore) GetCustomExtractionsByMemory(ctx context.Context, memoryID string) ([]*CustomExtraction, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, memory_id, schema_id, extracted_data, extraction_provider, extraction_model, confidence, created_at
		FROM custom_extractions WHERE memory_id = ? ORDER BY created_at DESC`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanCustomExtractions(rows)
}

func (s *SQLiteStore) QueryCustomExtractions(ctx context.Context, query CustomExtractionQuery) ([]*CustomExtraction, error) {
	var where []string
	var args []any

	if query.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, query.EntityID)
	}
	if query.MemoryID != "" {
		where = append(where, "memory_id = ?")
		args = append(args, query.MemoryID)
	}
	if query.SchemaID != "" {
		where = append(where, "schema_id = ?")
		args = append(args, query.SchemaID)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	q := fmt.Sprintf(
		`SELECT id, entity_id, memory_id, schema_id, extracted_data, extraction_provider, extraction_model, confidence, created_at
		FROM custom_extractions %s ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		whereClause)
	args = append(args, limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanCustomExtractions(rows)
}

func (s *SQLiteStore) DeleteCustomExtraction(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM custom_extractions WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) DeleteCustomExtractionsBySchema(ctx context.Context, schemaID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM custom_extractions WHERE schema_id = ?", schemaID)
	return err
}

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

// --- Agent State CRUD ---

func (s *SQLiteStore) CreateAgentState(ctx context.Context, state *AgentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state.ID == "" {
		state.ID = ulid.Make().String()
	}
	now := time.Now().UTC()
	state.CreatedAt = now
	state.LastUpdatedAt = &now

	currentStateJSON, _ := json.Marshal(state.CurrentState)
	schemaJSON, _ := json.Marshal(state.SchemaDefinition)
	rulesJSON, _ := json.Marshal(state.TransitionRules)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_states (id, entity_id, agent_id, schema_name, current_state, schema_definition, transition_rules, last_updated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		state.ID, state.EntityID, state.AgentID, state.SchemaName,
		string(currentStateJSON), string(schemaJSON), string(rulesJSON),
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) GetAgentState(ctx context.Context, entityID, agentID, schemaName string) (*AgentState, error) {
	var st AgentState
	var currentStateStr, schemaStr, rulesStr string
	var lastUpdatedStr, createdStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, schema_name, current_state, schema_definition, transition_rules, last_updated_at, created_at
		 FROM agent_states WHERE entity_id = ? AND agent_id = ? AND schema_name = ?`,
		entityID, agentID, schemaName,
	).Scan(&st.ID, &st.EntityID, &st.AgentID, &st.SchemaName,
		&currentStateStr, &schemaStr, &rulesStr,
		&lastUpdatedStr, &createdStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(currentStateStr), &st.CurrentState)
	json.Unmarshal([]byte(schemaStr), &st.SchemaDefinition)
	json.Unmarshal([]byte(rulesStr), &st.TransitionRules)

	if t, err := time.Parse(time.RFC3339, lastUpdatedStr); err == nil {
		st.LastUpdatedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
		st.CreatedAt = t
	}

	return &st, nil
}

func (s *SQLiteStore) UpdateAgentState(ctx context.Context, id string, newState map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stateJSON, _ := json.Marshal(newState)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_states SET current_state = ?, last_updated_at = ? WHERE id = ?`,
		string(stateJSON), now, id,
	)
	return err
}

func (s *SQLiteStore) GetAgentStateHistory(ctx context.Context, stateID string, limit int) ([]*AgentStateHistory, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, state_id, previous_state, new_state, changed_fields, trigger_content, confidence, reasoning, created_at
		 FROM agent_state_history WHERE state_id = ? ORDER BY created_at DESC LIMIT ?`,
		stateID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*AgentStateHistory
	for rows.Next() {
		var h AgentStateHistory
		var prevStr, newStr, fieldsStr, createdStr string
		if err := rows.Scan(&h.ID, &h.StateID, &prevStr, &newStr, &fieldsStr,
			&h.TriggerContent, &h.Confidence, &h.Reasoning, &createdStr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(prevStr), &h.PreviousState)
		json.Unmarshal([]byte(newStr), &h.NewState)
		h.ChangedFields.Scan(fieldsStr)
		if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
			h.CreatedAt = t
		}
		entries = append(entries, &h)
	}
	return entries, nil
}

func (s *SQLiteStore) LogAgentStateHistory(ctx context.Context, entry *AgentStateHistory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = ulid.Make().String()
	}
	now := time.Now().UTC()
	entry.CreatedAt = now

	prevJSON, _ := json.Marshal(entry.PreviousState)
	newJSON, _ := json.Marshal(entry.NewState)
	fieldsJSON, _ := entry.ChangedFields.Value()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_state_history (id, state_id, previous_state, new_state, changed_fields, trigger_content, confidence, reasoning, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.StateID, string(prevJSON), string(newJSON), fieldsJSON,
		entry.TriggerContent, entry.Confidence, entry.Reasoning, now.Format(time.RFC3339),
	)
	return err
}

// --- Team CRUD ---

func (s *SQLiteStore) CreateTeam(ctx context.Context, team *Team) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if team.ID == "" {
		team.ID = ulid.Make().String()
	}
	now := time.Now().UTC()
	team.CreatedAt = now
	team.UpdatedAt = now

	visibility := string(team.DefaultVisibility)
	if visibility == "" {
		visibility = string(VisibilityTeam)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO teams (id, name, description, default_visibility, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		team.ID, team.Name, team.Description, visibility,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) GetTeam(ctx context.Context, id string) (*Team, error) {
	var t Team
	var visStr, createdStr, updatedStr string

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, default_visibility, created_at, updated_at FROM teams WHERE id = ?`, id,
	).Scan(&t.ID, &t.Name, &t.Description, &visStr, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t.DefaultVisibility = MemoryVisibility(visStr)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &t, nil
}

func (s *SQLiteStore) DeleteTeam(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM teams WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) AddTeamMember(ctx context.Context, teamID, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_members (team_id, agent_id, role, joined_at) VALUES (?, ?, 'member', ?)`,
		teamID, agentID, now,
	)
	return err
}

func (s *SQLiteStore) RemoveTeamMember(ctx context.Context, teamID, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ? AND agent_id = ?`,
		teamID, agentID,
	)
	return err
}

func (s *SQLiteStore) GetTeamMembers(ctx context.Context, teamID string) ([]*TeamMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT team_id, agent_id, role, joined_at FROM team_members WHERE team_id = ?`, teamID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*TeamMember
	for rows.Next() {
		var m TeamMember
		var joinedStr string
		if err := rows.Scan(&m.TeamID, &m.AgentID, &m.Role, &joinedStr); err != nil {
			return nil, err
		}
		m.JoinedAt, _ = time.Parse(time.RFC3339, joinedStr)
		members = append(members, &m)
	}
	return members, nil
}

func (s *SQLiteStore) GetTeamForAgent(ctx context.Context, agentID string) (string, error) {
	var teamID string
	err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM team_members WHERE agent_id = ?`, agentID,
	).Scan(&teamID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return teamID, nil
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
	AND m.state IN ('active', 'stale')`

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
	return s.index.Len()
}

func (s *SQLiteStore) GetLowestRankedInHNSW(ctx context.Context, limit int) ([]*Memory, error) {
	// Get all IDs currently in the HNSW index
	ids := s.index.IDs()
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
	return s.index.Remove(id)
}

// --- Storage Metrics ---

func (s *SQLiteStore) GetMemoryCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories WHERE state != 'deleted'").Scan(&count)
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

// --- Heartbeat Action Tracking ---

func (s *SQLiteStore) RecordHeartbeatAction(ctx context.Context, action *HeartbeatAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if action.ID == "" {
		action.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var llmVal *int
	if action.LLMShouldAct != nil {
		v := 0
		if *action.LLMShouldAct {
			v = 1
		}
		llmVal = &v
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heartbeat_actions (id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ID, action.EntityID, action.AgentID, now, action.TriggerCategory,
		action.SignalFingerprint, action.Decision, action.UrgencyTier,
		llmVal, action.SignalSummary, action.TotalSignals)
	return err
}

func (s *SQLiteStore) GetLastHeartbeatAction(ctx context.Context, entityID, agentID, decision string) (*HeartbeatAction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals
		FROM heartbeat_actions
		WHERE entity_id = ? AND agent_id = ? AND decision = ?
		ORDER BY acted_at DESC LIMIT 1`,
		entityID, agentID, decision)

	var a HeartbeatAction
	var actedStr string
	var llmVal *int
	err := row.Scan(&a.ID, &a.EntityID, &a.AgentID, &actedStr, &a.TriggerCategory,
		&a.SignalFingerprint, &a.Decision, &a.UrgencyTier, &llmVal, &a.SignalSummary, &a.TotalSignals)
	if err != nil {
		return nil, err
	}
	a.ActedAt, _ = time.Parse(time.RFC3339, actedStr)
	if llmVal != nil {
		v := *llmVal == 1
		a.LLMShouldAct = &v
	}
	return &a, nil
}

func (s *SQLiteStore) GetNudgeCountToday(ctx context.Context, entityID, agentID string) (int, error) {
	// Count nudges in the last 24 hours
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM heartbeat_actions
		WHERE entity_id = ? AND agent_id = ? AND trigger_category = 'nudge' AND decision = 'act' AND acted_at > ?`,
		entityID, agentID, cutoff).Scan(&count)
	return count, err
}

func (s *SQLiteStore) CleanupOldHeartbeatActions(ctx context.Context, olderThan time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `DELETE FROM heartbeat_actions WHERE acted_at < ?`, cutoff)
	return err
}

func (s *SQLiteStore) GetMessageHourDistribution(ctx context.Context, entityID string, days int) (map[int]int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT created_at FROM session_messages WHERE entity_id = ? AND role = 'user' AND created_at > ?`,
		entityID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dist := make(map[int]int)
	for rows.Next() {
		var createdStr string
		if err := rows.Scan(&createdStr); err != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, createdStr)
		if err != nil {
			continue
		}
		dist[t.Hour()]++
	}
	return dist, rows.Err()
}
