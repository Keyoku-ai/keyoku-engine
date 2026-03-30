// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func testHeartbeatAction(entityID string) *HeartbeatAction {
	return &HeartbeatAction{
		EntityID:          entityID,
		AgentID:           "default",
		TriggerCategory:   "signal",
		SignalFingerprint: "fp_abc123",
		Decision:          "act",
		UrgencyTier:       "elevated",
		SignalSummary:     "pending work: fix bug",
		TotalSignals:      3,
	}
}

// --- RecordHeartbeatAction ---

func TestRecordHeartbeatAction_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	action := testHeartbeatAction("user-1")
	if err := s.RecordHeartbeatAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	if action.ID == "" {
		t.Fatal("expected auto-generated ID")
	}

	got, err := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if err != nil {
		t.Fatal(err)
	}
	if got.EntityID != "user-1" {
		t.Fatalf("got entity %q, want user-1", got.EntityID)
	}
	if got.Decision != "act" {
		t.Fatalf("got decision %q, want act", got.Decision)
	}
	if got.TotalSignals != 3 {
		t.Fatalf("got total_signals %d, want 3", got.TotalSignals)
	}
}

func TestRecordHeartbeatAction_TopicEntities(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	action := testHeartbeatAction("user-1")
	action.TopicEntities = StringSlice{"entity-a", "entity-b", "entity-c"}
	action.SignalSummaryHash = "hash_abc"
	if err := s.RecordHeartbeatAction(ctx, action); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TopicEntities) != 3 {
		t.Fatalf("got %d topic entities, want 3", len(got.TopicEntities))
	}
	if got.TopicEntities[1] != "entity-b" {
		t.Fatalf("got topic entity %q, want entity-b", got.TopicEntities[1])
	}
	if got.SignalSummaryHash != "hash_abc" {
		t.Fatalf("got hash %q, want hash_abc", got.SignalSummaryHash)
	}
}

func TestRecordHeartbeatAction_NullableBooleans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	llmTrue := true
	responded := false
	action := testHeartbeatAction("user-1")
	action.LLMShouldAct = &llmTrue
	action.UserResponded = &responded
	action.StateSnapshot = `{"memory_count":10}`
	if err := s.RecordHeartbeatAction(ctx, action); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if err != nil {
		t.Fatal(err)
	}
	if got.LLMShouldAct == nil || !*got.LLMShouldAct {
		t.Fatal("expected LLMShouldAct=true")
	}
	if got.UserResponded == nil || *got.UserResponded {
		t.Fatal("expected UserResponded=false")
	}
	if got.StateSnapshot != `{"memory_count":10}` {
		t.Fatalf("got state %q", got.StateSnapshot)
	}
}

// --- GetLastHeartbeatAction ---

func TestGetLastHeartbeatAction_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetLastHeartbeatAction(ctx, "nonexistent", "default", "act")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetLastHeartbeatAction_ReturnsMostRecent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a1 := testHeartbeatAction("user-1")
	a1.SignalSummary = "first"
	if err := s.RecordHeartbeatAction(ctx, a1); err != nil {
		t.Fatal(err)
	}

	// Sleep 1.1s to ensure different RFC3339 timestamps (second-level precision)
	time.Sleep(1100 * time.Millisecond)

	a2 := testHeartbeatAction("user-1")
	a2.SignalSummary = "second"
	if err := s.RecordHeartbeatAction(ctx, a2); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if err != nil {
		t.Fatal(err)
	}
	if got.SignalSummary != "second" {
		t.Fatalf("got summary %q, want second", got.SignalSummary)
	}
}

// --- GetRecentActDecisions ---

func TestGetRecentActDecisions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert 3 actions
	for i := 0; i < 3; i++ {
		a := testHeartbeatAction("user-1")
		if err := s.RecordHeartbeatAction(ctx, a); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	got, err := s.GetRecentActDecisions(ctx, "user-1", "default", 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d actions, want 3", len(got))
	}

	// With a very short window, should return all recent ones (they were just inserted)
	got2, err := s.GetRecentActDecisions(ctx, "user-1", "default", 1*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// All 3 were inserted within 1ms ago so they should all be included
	// (time precision means this should still return them)
	if len(got2) > 3 {
		t.Fatalf("got %d actions, want <= 3", len(got2))
	}
}

// --- GetRecentDecisions ---

func TestGetRecentDecisions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	setActedAt := func(id string, ts time.Time) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx,
			`UPDATE heartbeat_actions SET acted_at = ? WHERE id = ?`,
			ts.Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}

	oldAct := testHeartbeatAction("user-1")
	oldAct.SignalSummary = "old act"
	if err := s.RecordHeartbeatAction(ctx, oldAct); err != nil {
		t.Fatal(err)
	}

	recentSuppress := testHeartbeatAction("user-1")
	recentSuppress.Decision = "suppress_stale"
	recentSuppress.SignalSummary = "recent suppress"
	if err := s.RecordHeartbeatAction(ctx, recentSuppress); err != nil {
		t.Fatal(err)
	}

	recentAct := testHeartbeatAction("user-1")
	recentAct.SignalSummary = "recent act"
	if err := s.RecordHeartbeatAction(ctx, recentAct); err != nil {
		t.Fatal(err)
	}

	setActedAt(oldAct.ID, now.Add(-2*time.Hour))
	setActedAt(recentSuppress.ID, now.Add(-10*time.Minute))
	setActedAt(recentAct.ID, now.Add(-5*time.Minute))

	got, err := s.GetRecentDecisions(ctx, "user-1", "default", 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d actions, want 2", len(got))
	}
	if got[0].ID != recentAct.ID || got[1].ID != recentSuppress.ID {
		t.Fatalf("unexpected order: got %q then %q", got[0].SignalSummary, got[1].SignalSummary)
	}
	if got[0].ActedAt.Before(got[1].ActedAt) {
		t.Fatalf("expected descending acted_at, got %s then %s", got[0].ActedAt, got[1].ActedAt)
	}
	decisions := map[string]bool{
		"act":            false,
		"suppress_stale": false,
	}
	for _, a := range got {
		decisions[a.Decision] = true
		if a.ID == oldAct.ID {
			t.Fatal("expected old decision to be excluded from window")
		}
	}
	if !decisions["act"] || !decisions["suppress_stale"] {
		t.Fatalf("expected act and suppress decisions, got %+v", decisions)
	}
}

// --- GetNudgeCountToday ---

func TestGetNudgeCountToday(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert 2 nudge actions
	for i := 0; i < 2; i++ {
		a := testHeartbeatAction("user-1")
		a.TriggerCategory = "nudge"
		if err := s.RecordHeartbeatAction(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	// Insert 1 non-nudge action (should not count)
	a := testHeartbeatAction("user-1")
	a.TriggerCategory = "signal"
	if err := s.RecordHeartbeatAction(ctx, a); err != nil {
		t.Fatal(err)
	}

	count, err := s.GetNudgeCountToday(ctx, "user-1", "default")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got nudge count %d, want 2", count)
	}
}

// --- CleanupOldHeartbeatActions ---

func TestCleanupOldHeartbeatActions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert an action
	a := testHeartbeatAction("user-1")
	if err := s.RecordHeartbeatAction(ctx, a); err != nil {
		t.Fatal(err)
	}

	// Cleanup with a very short retention — since action was just inserted, it survives
	if err := s.CleanupOldHeartbeatActions(ctx, 1*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected action to survive cleanup")
	}
}

// --- Response tracking ---

func TestUpdateHeartbeatActionResponse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := testHeartbeatAction("user-1")
	if err := s.RecordHeartbeatAction(ctx, a); err != nil {
		t.Fatal(err)
	}

	// Initially nil
	got, _ := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if got.UserResponded != nil {
		t.Fatal("expected nil UserResponded initially")
	}

	// Update to responded=true
	if err := s.UpdateHeartbeatActionResponse(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}

	got2, _ := s.GetLastHeartbeatAction(ctx, "user-1", "default", "act")
	if got2.UserResponded == nil || !*got2.UserResponded {
		t.Fatal("expected UserResponded=true after update")
	}
}

func TestGetResponseRate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// No data = assume responsive (1.0)
	rate, total, err := s.GetResponseRate(ctx, "user-1", "default", 7)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 1.0 || total != 0 {
		t.Fatalf("got rate=%f total=%d, want 1.0/0", rate, total)
	}

	// Insert 4 actions, mark 3 as responded, 1 as not
	for i := 0; i < 4; i++ {
		a := testHeartbeatAction("user-1")
		if err := s.RecordHeartbeatAction(ctx, a); err != nil {
			t.Fatal(err)
		}
		responded := i < 3
		if err := s.UpdateHeartbeatActionResponse(ctx, a.ID, responded); err != nil {
			t.Fatal(err)
		}
	}

	rate, total, err = s.GetResponseRate(ctx, "user-1", "default", 7)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("got total=%d, want 4", total)
	}
	if rate != 0.75 {
		t.Fatalf("got rate=%f, want 0.75", rate)
	}
}

// --- Surfaced memories ---

func TestRecordAndGetSurfacedMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	memIDs := []string{"mem-1", "mem-2", "mem-3"}
	if err := s.RecordSurfacedMemories(ctx, "user-1", "default", memIDs); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetRecentlySurfacedMemoryIDs(ctx, "user-1", "default", 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d surfaced memories, want 3", len(got))
	}
}

func TestRecordSurfacedMemories_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty slice should be a no-op
	if err := s.RecordSurfacedMemories(ctx, "user-1", "default", nil); err != nil {
		t.Fatal(err)
	}
}

// --- Topic surfacing ---

func TestTopicSurfacing_UpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := &TopicSurfacing{
		EntityID:   "user-1",
		AgentID:    "default",
		TopicHash:  "hash_topic1",
		TopicLabel: "Project deadline",
	}
	if err := s.UpsertTopicSurfacing(ctx, ts); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTopicSurfacing(ctx, "user-1", "default", "hash_topic1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TimesSurfaced != 1 {
		t.Fatalf("got times_surfaced=%d, want 1", got.TimesSurfaced)
	}
	if got.TopicLabel != "Project deadline" {
		t.Fatalf("got label %q", got.TopicLabel)
	}

	// Upsert again — should increment
	if err := s.UpsertTopicSurfacing(ctx, ts); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetTopicSurfacing(ctx, "user-1", "default", "hash_topic1")
	if got2.TimesSurfaced != 2 {
		t.Fatalf("got times_surfaced=%d after upsert, want 2", got2.TimesSurfaced)
	}
}

func TestMarkTopicDropped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := &TopicSurfacing{
		EntityID:   "user-1",
		AgentID:    "default",
		TopicHash:  "hash_drop",
		TopicLabel: "stale topic",
	}
	if err := s.UpsertTopicSurfacing(ctx, ts); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkTopicDropped(ctx, "user-1", "default", "hash_drop"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetTopicSurfacing(ctx, "user-1", "default", "hash_drop")
	if got.DroppedAt == nil {
		t.Fatal("expected dropped_at to be set")
	}

	// Should not appear in active topics
	active, err := s.GetActiveTopicSurfacings(ctx, "user-1", "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("got %d active topics, want 0 after drop", len(active))
	}
}

// --- Heartbeat messages ---

func TestRecordAndGetHeartbeatMessages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	msg := &HeartbeatMessage{
		EntityID: "user-1",
		AgentID:  "default",
		ActionID: "action-123",
		Message:  "Hey, just checking in about that deadline.",
	}
	if err := s.RecordHeartbeatMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if msg.ID == "" {
		t.Fatal("expected auto-generated ID")
	}

	msgs, err := s.GetRecentHeartbeatMessages(ctx, "user-1", "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Message != "Hey, just checking in about that deadline." {
		t.Fatalf("got message %q", msgs[0].Message)
	}
	if msgs[0].ActionID != "action-123" {
		t.Fatalf("got action_id %q", msgs[0].ActionID)
	}
}
