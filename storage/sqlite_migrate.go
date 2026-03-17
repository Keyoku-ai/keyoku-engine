// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"fmt"

	"github.com/keyoku-ai/keyoku-engine/vectorindex"
)

// rebuildIndex reconstructs the HNSW index from embedding BLOBs in SQLite.
// Returns counts for rebuilt and skipped vectors.
func (s *SQLiteStore) rebuildIndex(index *vectorindex.HNSW) (rebuilt int, skipped int, err error) {
	rows, err := s.db.Query(
		`SELECT id, embedding FROM memories WHERE state IN ('active', 'stale') AND embedding IS NOT NULL`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			skipped++
			continue
		}
		vec := decodeEmbedding(embBytes)
		if len(vec) == 0 {
			skipped++
			continue
		}
		if err := index.Add(id, vec); err != nil {
			skipped++
			continue
		}
		rebuilt++
	}

	if err := rows.Err(); err != nil {
		return rebuilt, skipped, err
	}

	return rebuilt, skipped, nil
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

		// Content rotation tracking (which memories were surfaced in heartbeats)
		`CREATE TABLE IF NOT EXISTS surfaced_memories (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			memory_id TEXT NOT NULL,
			surfaced_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_surfaced_entity ON surfaced_memories(entity_id, agent_id, surfaced_at DESC)`,

		// Topic escalation tracking (how many times a topic has been surfaced)
		`CREATE TABLE IF NOT EXISTS topic_surfacings (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			topic_hash TEXT NOT NULL,
			topic_label TEXT NOT NULL DEFAULT '',
			times_surfaced INTEGER NOT NULL DEFAULT 1,
			last_surfaced_at TEXT NOT NULL DEFAULT (datetime('now')),
			user_responded INTEGER NOT NULL DEFAULT 0,
			dropped_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_topic_entity ON topic_surfacings(entity_id, agent_id, topic_hash)`,

		// Heartbeat message history (what the AI actually said)
		`CREATE TABLE IF NOT EXISTS heartbeat_messages (
			id TEXT PRIMARY KEY,
			entity_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT 'default',
			action_id TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hb_messages_entity ON heartbeat_messages(entity_id, agent_id, created_at DESC)`,
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
		// Heartbeat v2: intelligence fields
		`ALTER TABLE heartbeat_actions ADD COLUMN user_responded INTEGER DEFAULT NULL`,
		`ALTER TABLE heartbeat_actions ADD COLUMN topic_entities TEXT DEFAULT '[]'`,
		`ALTER TABLE heartbeat_actions ADD COLUMN state_snapshot TEXT DEFAULT ''`,
		`ALTER TABLE heartbeat_actions ADD COLUMN signal_summary_hash TEXT DEFAULT ''`,
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
