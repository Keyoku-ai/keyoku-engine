// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

// --- Heartbeat Action Tracking ---

func (s *SQLiteStore) RecordHeartbeatAction(ctx context.Context, action *HeartbeatAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if action.ID == "" {
		action.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if !action.ActedAt.IsZero() {
		now = action.ActedAt.UTC().Format(time.RFC3339)
	}

	var llmVal *int
	if action.LLMShouldAct != nil {
		v := 0
		if *action.LLMShouldAct {
			v = 1
		}
		llmVal = &v
	}

	var userRespondedVal *int
	if action.UserResponded != nil {
		v := 0
		if *action.UserResponded {
			v = 1
		}
		userRespondedVal = &v
	}

	topicEntitiesJSON := "[]"
	if len(action.TopicEntities) > 0 {
		if v, err := action.TopicEntities.Value(); err == nil {
			topicEntitiesJSON = v.(string)
		}
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heartbeat_actions (id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals, user_responded, topic_entities, state_snapshot, signal_summary_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ID, action.EntityID, action.AgentID, now, action.TriggerCategory,
		action.SignalFingerprint, action.Decision, action.UrgencyTier,
		llmVal, action.SignalSummary, action.TotalSignals,
		userRespondedVal, topicEntitiesJSON, action.StateSnapshot, action.SignalSummaryHash)
	return err
}

func (s *SQLiteStore) GetLastHeartbeatAction(ctx context.Context, entityID, agentID, decision string) (*HeartbeatAction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals, user_responded, topic_entities, state_snapshot, signal_summary_hash
		FROM heartbeat_actions
		WHERE entity_id = ? AND agent_id = ? AND decision = ?
		ORDER BY acted_at DESC LIMIT 1`,
		entityID, agentID, decision)

	return s.scanHeartbeatAction(row)
}

// scanHeartbeatAction scans a single row into a HeartbeatAction.
func (s *SQLiteStore) scanHeartbeatAction(row interface{ Scan(...any) error }) (*HeartbeatAction, error) {
	var a HeartbeatAction
	var actedStr string
	var llmVal *int
	var userRespondedVal *int
	var topicEntitiesStr *string
	var stateSnapshotStr *string
	var summaryHashStr *string
	err := row.Scan(&a.ID, &a.EntityID, &a.AgentID, &actedStr, &a.TriggerCategory,
		&a.SignalFingerprint, &a.Decision, &a.UrgencyTier, &llmVal, &a.SignalSummary, &a.TotalSignals,
		&userRespondedVal, &topicEntitiesStr, &stateSnapshotStr, &summaryHashStr)
	if err != nil {
		return nil, err
	}
	a.ActedAt, _ = time.Parse(time.RFC3339, actedStr)
	if llmVal != nil {
		v := *llmVal == 1
		a.LLMShouldAct = &v
	}
	if userRespondedVal != nil {
		v := *userRespondedVal == 1
		a.UserResponded = &v
	}
	if topicEntitiesStr != nil {
		_ = a.TopicEntities.Scan(*topicEntitiesStr)
	}
	if stateSnapshotStr != nil {
		a.StateSnapshot = *stateSnapshotStr
	}
	if summaryHashStr != nil {
		a.SignalSummaryHash = *summaryHashStr
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

// --- Heartbeat v2: Intelligence Methods ---

func (s *SQLiteStore) GetHeartbeatActionsForResponseCheck(ctx context.Context, entityID string, minAge time.Duration) ([]*HeartbeatAction, error) {
	cutoff := time.Now().UTC().Add(-minAge).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals, user_responded, topic_entities, state_snapshot, signal_summary_hash
		FROM heartbeat_actions
		WHERE entity_id = ? AND decision = 'act' AND user_responded IS NULL AND acted_at < ?
		ORDER BY acted_at DESC LIMIT 20`,
		entityID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []*HeartbeatAction
	for rows.Next() {
		a, err := s.scanHeartbeatAction(rows)
		if err != nil {
			continue
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

func (s *SQLiteStore) UpdateHeartbeatActionResponse(ctx context.Context, actionID string, responded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := 0
	if responded {
		val = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE heartbeat_actions SET user_responded = ? WHERE id = ?`,
		val, actionID)
	return err
}

func (s *SQLiteStore) GetRecentActDecisions(ctx context.Context, entityID, agentID string, since time.Duration) ([]*HeartbeatAction, error) {
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, acted_at, trigger_category, signal_fingerprint, decision, urgency_tier, llm_should_act, signal_summary, total_signals, user_responded, topic_entities, state_snapshot, signal_summary_hash
		FROM heartbeat_actions
		WHERE entity_id = ? AND agent_id = ? AND decision = 'act' AND acted_at > ?
		ORDER BY acted_at DESC`,
		entityID, agentID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []*HeartbeatAction
	for rows.Next() {
		a, err := s.scanHeartbeatAction(rows)
		if err != nil {
			continue
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

func (s *SQLiteStore) GetResponseRate(ctx context.Context, entityID, agentID string, days int) (float64, int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	var total, responded int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN user_responded = 1 THEN 1 ELSE 0 END), 0)
		FROM heartbeat_actions
		WHERE entity_id = ? AND agent_id = ? AND decision = 'act' AND user_responded IS NOT NULL AND acted_at > ?`,
		entityID, agentID, cutoff).Scan(&total, &responded)
	if err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 1.0, 0, nil // no data = assume responsive
	}
	return float64(responded) / float64(total), total, nil
}

// --- Content rotation (surfaced memory tracking) ---

func (s *SQLiteStore) RecordSurfacedMemories(ctx context.Context, entityID, agentID string, memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO surfaced_memories (id, entity_id, agent_id, memory_id, surfaced_at)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, mid := range memoryIDs {
		id := ulid.Make().String()
		if _, err := stmt.ExecContext(ctx, id, entityID, agentID, mid, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetRecentlySurfacedMemoryIDs(ctx context.Context, entityID, agentID string, since time.Duration) ([]string, error) {
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT memory_id FROM surfaced_memories
		WHERE entity_id = ? AND agent_id = ? AND surfaced_at > ?`,
		entityID, agentID, cutoff)
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
	return ids, rows.Err()
}

func (s *SQLiteStore) CleanupOldSurfacedMemories(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `DELETE FROM surfaced_memories WHERE surfaced_at < ?`, cutoff)
	return err
}

// --- Topic escalation tracking ---

func (s *SQLiteStore) UpsertTopicSurfacing(ctx context.Context, surfacing *TopicSurfacing) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if surfacing.ID == "" {
		surfacing.ID = ulid.Make().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO topic_surfacings (id, entity_id, agent_id, topic_hash, topic_label, times_surfaced, last_surfaced_at, user_responded)
		VALUES (?, ?, ?, ?, ?, 1, ?, 0)
		ON CONFLICT(entity_id, agent_id, topic_hash) DO UPDATE SET
			times_surfaced = times_surfaced + 1,
			last_surfaced_at = ?,
			topic_label = CASE WHEN excluded.topic_label != '' THEN excluded.topic_label ELSE topic_label END`,
		surfacing.ID, surfacing.EntityID, surfacing.AgentID, surfacing.TopicHash, surfacing.TopicLabel, now, now)
	return err
}

func (s *SQLiteStore) GetTopicSurfacing(ctx context.Context, entityID, agentID, topicHash string) (*TopicSurfacing, error) {
	var ts TopicSurfacing
	var lastSurfacedStr string
	var droppedStr *string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, entity_id, agent_id, topic_hash, topic_label, times_surfaced, last_surfaced_at, user_responded, dropped_at
		FROM topic_surfacings WHERE entity_id = ? AND agent_id = ? AND topic_hash = ?`,
		entityID, agentID, topicHash).Scan(
		&ts.ID, &ts.EntityID, &ts.AgentID, &ts.TopicHash, &ts.TopicLabel,
		&ts.TimesSurfaced, &lastSurfacedStr, &ts.UserResponded, &droppedStr)
	if err != nil {
		return nil, err
	}
	ts.LastSurfacedAt, _ = time.Parse(time.RFC3339, lastSurfacedStr)
	if droppedStr != nil {
		t, _ := time.Parse(time.RFC3339, *droppedStr)
		ts.DroppedAt = &t
	}
	return &ts, nil
}

func (s *SQLiteStore) GetActiveTopicSurfacings(ctx context.Context, entityID, agentID string, limit int) ([]*TopicSurfacing, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, topic_hash, topic_label, times_surfaced, last_surfaced_at, user_responded, dropped_at
		FROM topic_surfacings
		WHERE entity_id = ? AND agent_id = ? AND dropped_at IS NULL
		ORDER BY last_surfaced_at DESC LIMIT ?`,
		entityID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*TopicSurfacing
	for rows.Next() {
		var ts TopicSurfacing
		var lastSurfacedStr string
		var droppedStr *string
		if err := rows.Scan(&ts.ID, &ts.EntityID, &ts.AgentID, &ts.TopicHash, &ts.TopicLabel,
			&ts.TimesSurfaced, &lastSurfacedStr, &ts.UserResponded, &droppedStr); err != nil {
			return nil, err
		}
		ts.LastSurfacedAt, _ = time.Parse(time.RFC3339, lastSurfacedStr)
		if droppedStr != nil {
			t, _ := time.Parse(time.RFC3339, *droppedStr)
			ts.DroppedAt = &t
		}
		results = append(results, &ts)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) MarkTopicDropped(ctx context.Context, entityID, agentID, topicHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE topic_surfacings SET dropped_at = ? WHERE entity_id = ? AND agent_id = ? AND topic_hash = ?`,
		now, entityID, agentID, topicHash)
	return err
}

// --- Heartbeat message history ---

func (s *SQLiteStore) RecordHeartbeatMessage(ctx context.Context, msg *HeartbeatMessage) error {
	if msg.ID == "" {
		msg.ID = ulid.Make().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heartbeat_messages (id, entity_id, agent_id, action_id, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.EntityID, msg.AgentID, msg.ActionID, msg.Message, now)
	return err
}

func (s *SQLiteStore) GetRecentHeartbeatMessages(ctx context.Context, entityID, agentID string, limit int) ([]*HeartbeatMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id, agent_id, action_id, message, created_at
		FROM heartbeat_messages
		WHERE entity_id = ? AND agent_id = ?
		ORDER BY created_at DESC LIMIT ?`,
		entityID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*HeartbeatMessage
	for rows.Next() {
		var m HeartbeatMessage
		var createdStr string
		if err := rows.Scan(&m.ID, &m.EntityID, &m.AgentID, &m.ActionID, &m.Message, &createdStr); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		msgs = append(msgs, &m)
	}
	return msgs, rows.Err()
}
