// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// --- P0: Core Fixes ---

func TestGoalProgressFilter_NoActivity(t *testing.T) {
	// GoalProgress with status "no_activity" should be filtered out
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Build feature X", Type: storage.TypePlan, Importance: 0.8, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckGoalProgress))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// GoalProgress should be empty since there are no activity memories → status = "no_activity" → filtered
	if len(result.GoalProgress) > 0 {
		for _, g := range result.GoalProgress {
			if g.Status == "no_activity" {
				t.Error("GoalProgress with no_activity status should be filtered out")
			}
		}
	}
}

func TestImportanceFloor_LoweredTo04(t *testing.T) {
	// Memories with importance 0.5 should now pass the floor (was 0.7, now 0.4)
	memReturned := false
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				memReturned = true
				return []*storage.Memory{
					{ID: "plan-1", Content: "Important plan", Type: storage.TypePlan,
						Importance: 0.5, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !memReturned {
		t.Skip("query was not called for plans")
	}
	if len(result.PendingWork) == 0 {
		t.Error("memory with importance 0.5 should pass the lowered floor of 0.4")
	}
}

// --- P1: Time-of-Day Tiers ---

func TestCurrentTimePeriod(t *testing.T) {
	k := &Keyoku{}
	period := k.currentTimePeriod()
	validPeriods := map[string]bool{
		PeriodMorning: true, PeriodWorking: true, PeriodEvening: true,
		PeriodLateNight: true, PeriodQuiet: true,
	}
	if !validPeriods[period] {
		t.Errorf("currentTimePeriod() = %q, want one of morning/working/evening/late_night/quiet", period)
	}
}

func TestTimePeriodMinTier(t *testing.T) {
	tests := []struct {
		period  string
		wantMin string
	}{
		{PeriodMorning, TierLow},
		{PeriodWorking, TierLow},
		{PeriodEvening, TierNormal},
		{PeriodLateNight, TierElevated},
		{PeriodQuiet, TierImmediate},
	}
	for _, tt := range tests {
		got := timePeriodMinTier(tt.period)
		if got != tt.wantMin {
			t.Errorf("timePeriodMinTier(%q) = %q, want %q", tt.period, got, tt.wantMin)
		}
	}
}

func TestTimePeriodCooldownMultiplier(t *testing.T) {
	tests := []struct {
		period string
		want   float64
	}{
		{PeriodMorning, 0.5},
		{PeriodWorking, 1.0},
		{PeriodEvening, 1.5},
		{PeriodLateNight, 3.0},
		{PeriodQuiet, 3.0},
	}
	for _, tt := range tests {
		got := timePeriodCooldownMultiplier(tt.period)
		if got != tt.want {
			t.Errorf("timePeriodCooldownMultiplier(%q) = %v, want %v", tt.period, got, tt.want)
		}
	}
}

func TestTierRank(t *testing.T) {
	if tierRank(TierImmediate) <= tierRank(TierElevated) {
		t.Error("immediate should rank higher than elevated")
	}
	if tierRank(TierElevated) <= tierRank(TierNormal) {
		t.Error("elevated should rank higher than normal")
	}
	if tierRank(TierNormal) <= tierRank(TierLow) {
		t.Error("normal should rank higher than low")
	}
}

func TestTimePeriod_InResult(t *testing.T) {
	store := &testStore{}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.TimePeriod == "" {
		t.Error("TimePeriod should be set in HeartbeatResult")
	}
}

// --- P1: Elevated Cooldown as Param ---

func TestElevatedCooldownParam(t *testing.T) {
	tests := []struct {
		autonomy string
		wantSet  bool
	}{
		{"act", true},
		{"suggest", true},
		{"observe", true},
	}
	for _, tt := range tests {
		params := DefaultHeartbeatParams(tt.autonomy)
		if tt.wantSet && params.SignalCooldownElevated == 0 {
			t.Errorf("DefaultHeartbeatParams(%q).SignalCooldownElevated should be set", tt.autonomy)
		}
	}
}

// --- P1: First-Contact Mode ---

func TestFirstContactMode(t *testing.T) {
	k := &Keyoku{store: &firstContactStore{}}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.ShouldAct {
		t.Error("first contact (< 5 memories) should set ShouldAct = true")
	}
	if result.DecisionReason != "first_contact" {
		t.Errorf("DecisionReason = %q, want 'first_contact'", result.DecisionReason)
	}
}

// firstContactStore returns a low memory count to trigger first-contact mode.
type firstContactStore struct {
	testStore
}

func (s *firstContactStore) GetMemoryCount(_ context.Context) (int, error) {
	return 2, nil
}

// --- P2: Content Rotation ---

func TestCollectSignalMemoryIDs(t *testing.T) {
	result := &HeartbeatResult{
		PendingWork: []*Memory{{ID: "pw-1"}, {ID: "pw-2"}},
		Deadlines:   []*Memory{{ID: "dl-1"}},
		Scheduled:   []*Memory{{ID: "pw-1"}}, // duplicate with PendingWork
	}
	ids := collectSignalMemoryIDs(result)
	if len(ids) != 3 {
		t.Errorf("collectSignalMemoryIDs: got %d IDs, want 3 (deduplicated)", len(ids))
	}
	// Check dedup
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID in result: %s", id)
		}
		seen[id] = true
	}
}

func TestFilterSurfacedMemories(t *testing.T) {
	surfacedIDs := []string{"mem-1", "mem-3"}
	store := &testStore{} // GetRecentlySurfacedMemoryIDs returns nil by default

	k := &Keyoku{store: store}

	memories := []*Memory{
		{ID: "mem-1", Content: "old"},
		{ID: "mem-2", Content: "new"},
		{ID: "mem-3", Content: "old too"},
	}

	// With no surfaced IDs, all memories should pass through
	filtered := k.filterSurfacedMemories(context.Background(), "e1", "a1", memories, time.Hour)
	if len(filtered) != 3 {
		t.Errorf("with no surfaced IDs, got %d, want 3", len(filtered))
	}

	// Now with a store that returns surfaced IDs
	k2 := &Keyoku{store: &surfacedStore{surfacedIDs: surfacedIDs}}
	filtered = k2.filterSurfacedMemories(context.Background(), "e1", "a1", memories, time.Hour)
	if len(filtered) != 1 {
		t.Errorf("after filtering surfaced, got %d, want 1", len(filtered))
	}
	if filtered[0].ID != "mem-2" {
		t.Errorf("remaining memory should be mem-2, got %s", filtered[0].ID)
	}
}

func TestFilterSurfacedMemories_FallbackWhenAllFiltered(t *testing.T) {
	surfacedIDs := []string{"mem-1", "mem-2"}
	k := &Keyoku{store: &surfacedStore{surfacedIDs: surfacedIDs}}

	memories := []*Memory{
		{ID: "mem-1", Content: "a"},
		{ID: "mem-2", Content: "b"},
	}

	// When ALL would be filtered, fall back to original
	filtered := k.filterSurfacedMemories(context.Background(), "e1", "a1", memories, time.Hour)
	if len(filtered) != 2 {
		t.Errorf("fallback should return all %d memories when everything filtered", len(filtered))
	}
}

type surfacedStore struct {
	testStore
	surfacedIDs []string
}

func (s *surfacedStore) GetRecentlySurfacedMemoryIDs(_ context.Context, _, _ string, _ time.Duration) ([]string, error) {
	return s.surfacedIDs, nil
}

// --- P2: Conversation Rhythm ---

func TestIsUserTypicallyActive_NoData(t *testing.T) {
	store := &testStore{}
	k := &Keyoku{store: store}
	// No data → assume active
	if !k.isUserTypicallyActive(context.Background(), "entity-1") {
		t.Error("with no data, should assume user is active")
	}
}

func TestIsUserTypicallyActive_TooFewMessages(t *testing.T) {
	store := &rhythmStore{dist: map[int]int{10: 5, 14: 3}, total: 8}
	k := &Keyoku{store: store}
	// < 20 messages → assume active
	if !k.isUserTypicallyActive(context.Background(), "entity-1") {
		t.Error("with < 20 messages, should assume user is active")
	}
}

type rhythmStore struct {
	testStore
	dist  map[int]int
	total int // not used directly, dist values sum to this
}

func (s *rhythmStore) GetMessageHourDistribution(_ context.Context, _ string, _ int) (map[int]int, error) {
	return s.dist, nil
}

// --- P2: Positive Deltas as Signal ---

func TestPositiveDeltasClassifiedAsSignal(t *testing.T) {
	result := &HeartbeatResult{
		PositiveDeltas: []PositiveDelta{
			{Type: "goal_improved", Description: "Project X moved to on_track"},
		},
	}

	k := &Keyoku{store: &testStore{}}
	active := k.classifyActiveSignals(result)

	if _, ok := active[CheckPositiveDeltas]; !ok {
		t.Error("positive deltas should be classified as an active signal")
	}
	if active[CheckPositiveDeltas] != TierNormal {
		t.Errorf("positive deltas tier = %q, want %q", active[CheckPositiveDeltas], TierNormal)
	}
}

// --- P2: Escalation ---

func TestBuildTopicLabel(t *testing.T) {
	k := &Keyoku{store: &testStore{}}

	result := &HeartbeatResult{
		PendingWork: []*Memory{{ID: "1", Content: "Fix the authentication bug in the login flow"}},
	}
	label := k.buildTopicLabel(result)
	if label != "Fix the authentication bug in the login flow" {
		t.Errorf("unexpected label: %q", label)
	}

	// Long content should be truncated
	longContent := make([]byte, 200)
	for i := range longContent {
		longContent[i] = 'x'
	}
	result2 := &HeartbeatResult{
		PendingWork: []*Memory{{ID: "1", Content: string(longContent)}},
	}
	label2 := k.buildTopicLabel(result2)
	if len(label2) > 80 {
		t.Errorf("label should be truncated to 80 chars, got %d", len(label2))
	}
}

// --- Snapshot Testing ---

// TestHeartbeatSnapshot_FullPipeline runs the complete heartbeat pipeline with known state
// and asserts the full result structure. This is the "snapshot" approach.
func TestHeartbeatSnapshot_FullPipeline(t *testing.T) {
	now := time.Now()
	twoDaysAgo := now.Add(-48 * time.Hour)

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			// Return pending work
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{
						ID: "plan-1", Content: "Ship v2 release",
						Type: storage.TypePlan, Importance: 0.8,
						State: storage.StateActive, CreatedAt: twoDaysAgo,
					},
				}, nil
			}
			return nil, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithAutonomy("act"),
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Verify result structure
	if result.TimePeriod == "" {
		t.Error("TimePeriod should be set")
	}
	if len(result.PendingWork) == 0 {
		t.Error("PendingWork should contain the plan")
	}
	if result.Summary == "" {
		t.Error("Summary should be built")
	}

	// Log full result for snapshot review
	t.Logf("Snapshot result:")
	t.Logf("  ShouldAct: %v", result.ShouldAct)
	t.Logf("  DecisionReason: %s", result.DecisionReason)
	t.Logf("  TimePeriod: %s", result.TimePeriod)
	t.Logf("  EscalationLevel: %d", result.EscalationLevel)
	t.Logf("  HighestUrgencyTier: %s", result.HighestUrgencyTier)
	t.Logf("  PendingWork: %d", len(result.PendingWork))
	t.Logf("  PositiveDeltas: %d", len(result.PositiveDeltas))
	t.Logf("  Summary: %s", result.Summary[:min(len(result.Summary), 100)])
}

// TestHeartbeatSnapshot_NoSignals verifies clean result when nothing to report.
func TestHeartbeatSnapshot_NoSignals(t *testing.T) {
	store := &testStore{}
	k := &Keyoku{store: store}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithAutonomy("suggest"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if result.ShouldAct {
		t.Error("should not act with no signals")
	}
	if result.TimePeriod == "" {
		t.Error("TimePeriod should always be set")
	}
	if result.EscalationLevel != 0 {
		t.Errorf("EscalationLevel should be 0 with no signals, got %d", result.EscalationLevel)
	}

	t.Logf("No-signal snapshot:")
	t.Logf("  ShouldAct: %v", result.ShouldAct)
	t.Logf("  DecisionReason: %s", result.DecisionReason)
	t.Logf("  TimePeriod: %s", result.TimePeriod)
}

// TestHeartbeatSnapshot_FirstContact verifies the first-contact flow.
func TestHeartbeatSnapshot_FirstContact(t *testing.T) {
	k := &Keyoku{store: &firstContactStore{}}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithAutonomy("act"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !result.ShouldAct {
		t.Error("first contact should act")
	}
	if result.DecisionReason != "first_contact" {
		t.Errorf("DecisionReason = %q, want first_contact", result.DecisionReason)
	}
	if result.HighestUrgencyTier != TierNormal {
		t.Errorf("HighestUrgencyTier = %q, want %q", result.HighestUrgencyTier, TierNormal)
	}

	t.Logf("First-contact snapshot:")
	t.Logf("  ShouldAct: %v", result.ShouldAct)
	t.Logf("  DecisionReason: %s", result.DecisionReason)
	t.Logf("  TimePeriod: %s", result.TimePeriod)
}

// --- Enhancement 1: Memory Velocity ---

func TestMemoryVelocity_HighWhenDeltaGte5(t *testing.T) {
	// Setup: previous act had 10 memories, now entity has 18 → delta=8 → velocity high
	prevSnapshot := StateSnapshot{
		GoalStatuses:         map[string]string{},
		RelationshipSilences: map[string]int{},
		MemoryCount:          10,
		MemoryCountAt:        time.Now().Add(-30 * time.Minute),
	}
	prevSnapshotJSON, _ := json.Marshal(prevSnapshot)

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Build feature X", Type: storage.TypePlan, Importance: 0.8, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
		getLastHeartbeatActionFn: func(_ context.Context, _, _, decision string) (*storage.HeartbeatAction, error) {
			if decision == "act" {
				return &storage.HeartbeatAction{
					ActedAt:       time.Now().Add(-30 * time.Minute),
					Decision:      "act",
					StateSnapshot: string(prevSnapshotJSON),
				}, nil
			}
			return nil, nil
		},
		getMemoryCountForEntityFn: func(_ context.Context, _ string) (int, error) {
			return 18, nil // 18 - 10 = 8 → high velocity
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork, CheckMemoryVelocity))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if result.MemoryVelocity != 8 {
		t.Errorf("MemoryVelocity = %d, want 8", result.MemoryVelocity)
	}
	if !result.MemoryVelocityHigh {
		t.Error("MemoryVelocityHigh should be true when delta >= 5")
	}
	t.Logf("MemoryVelocity=%d, MemoryVelocityHigh=%v", result.MemoryVelocity, result.MemoryVelocityHigh)
}

func TestMemoryVelocity_LowWhenDeltaLt5(t *testing.T) {
	prevSnapshot := StateSnapshot{
		GoalStatuses:         map[string]string{},
		RelationshipSilences: map[string]int{},
		MemoryCount:          10,
		MemoryCountAt:        time.Now().Add(-30 * time.Minute),
	}
	prevSnapshotJSON, _ := json.Marshal(prevSnapshot)

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		getLastHeartbeatActionFn: func(_ context.Context, _, _, decision string) (*storage.HeartbeatAction, error) {
			if decision == "act" {
				return &storage.HeartbeatAction{
					ActedAt:       time.Now().Add(-30 * time.Minute),
					Decision:      "act",
					StateSnapshot: string(prevSnapshotJSON),
				}, nil
			}
			return nil, nil
		},
		getMemoryCountForEntityFn: func(_ context.Context, _ string) (int, error) {
			return 13, nil // 13 - 10 = 3 → low velocity
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork, CheckMemoryVelocity))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if result.MemoryVelocity != 3 {
		t.Errorf("MemoryVelocity = %d, want 3", result.MemoryVelocity)
	}
	if result.MemoryVelocityHigh {
		t.Error("MemoryVelocityHigh should be false when delta < 5")
	}
}

func TestMemoryVelocity_ZeroWhenNoPreviousSnapshot(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		// No previous act → getLastHeartbeatActionFn returns nil (default)
		getMemoryCountForEntityFn: func(_ context.Context, _ string) (int, error) {
			return 20, nil
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if result.MemoryVelocity != 0 {
		t.Errorf("MemoryVelocity = %d, want 0 (no previous snapshot)", result.MemoryVelocity)
	}
	if result.MemoryVelocityHigh {
		t.Error("MemoryVelocityHigh should be false when no previous snapshot")
	}
}

// --- Enhancement 2: Signal Freshness Weighting ---

func TestSignalFreshness_BoostsPendingWork(t *testing.T) {
	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{ID: "m-1", Content: "Fresh task", CreatedAt: time.Now().Add(-10 * time.Minute), UpdatedAt: time.Now().Add(-10 * time.Minute)},
		},
	}
	activeSignals := map[HeartbeatCheckType]string{
		CheckPendingWork: TierNormal,
	}

	boostSignalFreshness(activeSignals, result, time.Now())

	if activeSignals[CheckPendingWork] != TierElevated {
		t.Errorf("PendingWork tier = %q, want %q (fresh memory should boost Normal→Elevated)", activeSignals[CheckPendingWork], TierElevated)
	}
}

func TestSignalFreshness_NoBoostWhenStale(t *testing.T) {
	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{ID: "m-1", Content: "Old task", CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour)},
		},
	}
	activeSignals := map[HeartbeatCheckType]string{
		CheckPendingWork: TierNormal,
	}

	boostSignalFreshness(activeSignals, result, time.Now())

	if activeSignals[CheckPendingWork] != TierNormal {
		t.Errorf("PendingWork tier = %q, want %q (stale memory should not boost)", activeSignals[CheckPendingWork], TierNormal)
	}
}

func TestSignalFreshness_BoostsDecaying(t *testing.T) {
	result := &HeartbeatResult{
		Decaying: []*Memory{
			{ID: "m-1", Content: "Decaying fresh", CreatedAt: time.Now().Add(-1 * time.Hour), UpdatedAt: time.Now().Add(-5 * time.Minute)},
		},
	}
	activeSignals := map[HeartbeatCheckType]string{
		CheckDecaying: TierLow,
	}

	boostSignalFreshness(activeSignals, result, time.Now())

	if activeSignals[CheckDecaying] != TierNormal {
		t.Errorf("Decaying tier = %q, want %q (fresh memory should boost Low→Normal)", activeSignals[CheckDecaying], TierNormal)
	}
}

func TestSignalFreshness_NeverBootsAboveElevated(t *testing.T) {
	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{ID: "m-1", Content: "Fresh", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
	}
	activeSignals := map[HeartbeatCheckType]string{
		CheckPendingWork: TierElevated, // already elevated
	}

	boostSignalFreshness(activeSignals, result, time.Now())

	if activeSignals[CheckPendingWork] != TierElevated {
		t.Errorf("PendingWork tier = %q, want %q (should not boost above elevated)", activeSignals[CheckPendingWork], TierElevated)
	}
}

// --- Enhancement 3: Conversation Awareness Gradient ---

func TestConversationGradient_ElevatedOnlyByDefault(t *testing.T) {
	// During conversation without high velocity, Normal signals should be filtered out
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Some plan", Type: storage.TypePlan, Importance: 0.8, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
		getMemoryCountForEntityFn: func(_ context.Context, _ string) (int, error) {
			return 5, nil // low count, no velocity
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork),
		WithInConversation(true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// PendingWork is TierNormal → should be filtered during conversation (no velocity)
	if result.ShouldAct {
		t.Logf("DecisionReason: %s", result.DecisionReason)
		// It's OK if first_contact fires, but normal signals should be suppressed
		if result.DecisionReason != "first_contact" {
			t.Error("Normal-tier signals should be suppressed during conversation without high velocity")
		}
	}
}

func TestConversationGradient_NormalAllowedWithHighVelocity(t *testing.T) {
	// During conversation WITH high velocity, Normal signals should pass through
	prevSnapshot := StateSnapshot{
		GoalStatuses:         map[string]string{},
		RelationshipSilences: map[string]int{},
		MemoryCount:          5,
		MemoryCountAt:        time.Now().Add(-30 * time.Minute),
	}
	prevSnapshotJSON, _ := json.Marshal(prevSnapshot)

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Active plan", Type: storage.TypePlan, Importance: 0.8, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
		getLastHeartbeatActionFn: func(_ context.Context, _, _, decision string) (*storage.HeartbeatAction, error) {
			if decision == "act" {
				return &storage.HeartbeatAction{
					ActedAt:       time.Now().Add(-30 * time.Minute),
					Decision:      "act",
					StateSnapshot: string(prevSnapshotJSON),
				}, nil
			}
			return nil, nil
		},
		getMemoryCountForEntityFn: func(_ context.Context, _ string) (int, error) {
			return 15, nil // 15 - 5 = 10 → high velocity
		},
	}
	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork, CheckMemoryVelocity),
		WithInConversation(true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !result.MemoryVelocityHigh {
		t.Fatal("Expected MemoryVelocityHigh=true for delta=10")
	}
	t.Logf("ShouldAct=%v, DecisionReason=%s, MemoryVelocity=%d", result.ShouldAct, result.DecisionReason, result.MemoryVelocity)
	// With high velocity, Normal-tier PendingWork should NOT be filtered during conversation
	// (it may still be suppressed by other checks like cooldown/quiet, but not by conversation filter)
	if result.DecisionReason == "suppress_conversation_low" {
		t.Error("Normal-tier signals should NOT be suppressed during conversation with high velocity")
	}
}

// --- Enhancement 4: Content-Based Dedup ---

func TestContentDedup_SameHashSuppresses(t *testing.T) {
	summary := "PENDING WORK (1):\n- [PLAN] Ship v2 release"
	hash := hashSignalSummary(summary)

	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, _ time.Duration) ([]*storage.HeartbeatAction, error) {
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-20 * time.Minute),
					Decision:          "act",
					SignalSummaryHash: hash,
					TopicEntities:     []string{}, // no entity overlap
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Same hash, no entity overlap → Layer 1 should catch it
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "e1", "a1", []string{"entity-999"}, hash, "fp-any")
	if !suppressed {
		t.Error("shouldSuppressTopicRepeat should return true when summary hash matches (Layer 1)")
	}
}

func TestContentDedup_DifferentHashDifferentEntities_NoSuppress(t *testing.T) {
	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, _ time.Duration) ([]*storage.HeartbeatAction, error) {
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-20 * time.Minute),
					Decision:          "act",
					SignalSummaryHash: "oldhash1234567890",
					SignalFingerprint: "fp-old",
					TopicEntities:     []string{"entity-A", "entity-B"},
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Different hash AND different entities → should NOT suppress
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "e1", "a1", []string{"entity-C", "entity-D"}, "newhash9876543210", "fp-new")
	if suppressed {
		t.Error("shouldSuppressTopicRepeat should return false when both hash and entities differ")
	}
}

func TestContentDedup_EntityOverlapSameFingerprint(t *testing.T) {
	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, _ time.Duration) ([]*storage.HeartbeatAction, error) {
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-20 * time.Minute),
					Decision:          "act",
					SignalSummaryHash: "differenthash12345",
					SignalFingerprint: "fp-same",
					TopicEntities:     []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"},
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Entity overlap 100% + same fingerprint → suppress (truly same topic)
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "owner", "agent", []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}, "anotherhash9999999", "fp-same")
	if !suppressed {
		t.Error("shouldSuppressTopicRepeat should suppress when entity overlap > 85% AND fingerprint matches")
	}
}

func TestContentDedup_EntityOverlapDifferentFingerprint_NoSuppress(t *testing.T) {
	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, _ time.Duration) ([]*storage.HeartbeatAction, error) {
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-20 * time.Minute),
					Decision:          "act",
					SignalSummaryHash: "differenthash12345",
					SignalFingerprint: "fp-old-work",
					TopicEntities:     []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"},
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Entity overlap 100% BUT different fingerprint → allow through (new work on same project)
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "owner", "agent", []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}, "anotherhash9999999", "fp-new-work")
	if suppressed {
		t.Error("shouldSuppressTopicRepeat should NOT suppress when fingerprint differs (new work on same project)")
	}
}

func TestContentDedup_SuppressionFootprintSameFingerprintSuppresses(t *testing.T) {
	var seenActWindow time.Duration
	var seenSuppressionWindow time.Duration

	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, window time.Duration) ([]*storage.HeartbeatAction, error) {
			seenActWindow = window
			return nil, nil
		},
		getRecentDecisionsFn: func(_ context.Context, _, _ string, window time.Duration) ([]*storage.HeartbeatAction, error) {
			seenSuppressionWindow = window
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-75 * time.Minute),
					Decision:          "suppress_topic_repeat",
					SignalFingerprint: "fp-zombie",
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	suppressed := k.shouldSuppressTopicRepeat(
		context.Background(),
		"owner",
		"agent",
		[]string{"project-a"},
		"newhash-after-state-change",
		"fp-zombie",
		1*time.Hour,
	)
	if !suppressed {
		t.Fatal("shouldSuppressTopicRepeat should suppress when a recent non-act decision has the same fingerprint")
	}
	if seenActWindow != 1*time.Hour {
		t.Errorf("act window = %v, want 1h", seenActWindow)
	}
	if seenSuppressionWindow != 2*time.Hour {
		t.Errorf("suppression window = %v, want 2h", seenSuppressionWindow)
	}
}

func TestIsMetaProcessContent_StrictPrefixMatching(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "internal monitoring prefix", content: "Monitoring release risk for regressions", want: true},
		{name: "internal cleanup prefix after trim", content: "  cleanup stale reminders from earlier runs", want: true},
		{name: "user mention of monitoring in middle", content: "Plan: build a monitoring dashboard for the team", want: false},
		{name: "user mention of cleanup in middle", content: "Need to finish cleanup on the kitchen project", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMetaProcessContent(tt.content); got != tt.want {
				t.Errorf("isMetaProcessContent(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// --- Helper function tests ---

func TestHashSignalSummary_Deterministic(t *testing.T) {
	summary := "PENDING WORK (1):\n- [PLAN] Ship v2 release"
	h1 := hashSignalSummary(summary)
	h2 := hashSignalSummary(summary)
	if h1 != h2 {
		t.Errorf("hashSignalSummary not deterministic: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Error("hashSignalSummary should not return empty string for non-empty input")
	}
	if len(h1) != 16 {
		t.Errorf("hashSignalSummary should return 16 hex chars, got %d", len(h1))
	}
}

func TestHashSignalSummary_DifferentInputsDifferentHashes(t *testing.T) {
	h1 := hashSignalSummary("PENDING WORK (1): Ship v2")
	h2 := hashSignalSummary("PENDING WORK (1): Fix bug")
	if h1 == h2 {
		t.Error("Different summaries should produce different hashes")
	}
}

func TestHashSignalSummary_EmptyReturnsEmpty(t *testing.T) {
	if h := hashSignalSummary(""); h != "" {
		t.Errorf("hashSignalSummary(\"\") = %q, want empty", h)
	}
}

func TestHasFreshMemory_ReturnsTrue(t *testing.T) {
	memories := []*Memory{
		{ID: "old", CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "fresh", CreatedAt: time.Now().Add(-10 * time.Minute), UpdatedAt: time.Now().Add(-10 * time.Minute)},
	}
	if !hasFreshMemory(memories, 30*time.Minute, time.Now()) {
		t.Error("hasFreshMemory should return true when a memory was created within window")
	}
}

func TestHasFreshMemory_ReturnsFalse(t *testing.T) {
	memories := []*Memory{
		{ID: "old1", CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "old2", CreatedAt: time.Now().Add(-3 * time.Hour), UpdatedAt: time.Now().Add(-3 * time.Hour)},
	}
	if hasFreshMemory(memories, 30*time.Minute, time.Now()) {
		t.Error("hasFreshMemory should return false when no memory is within window")
	}
}

func TestHasFreshMemory_UpdatedAtCounts(t *testing.T) {
	memories := []*Memory{
		{ID: "m1", CreatedAt: time.Now().Add(-5 * time.Hour), UpdatedAt: time.Now().Add(-5 * time.Minute)}, // old creation, fresh update
	}
	if !hasFreshMemory(memories, 30*time.Minute, time.Now()) {
		t.Error("hasFreshMemory should return true when UpdatedAt is within window")
	}
}

func TestHasFreshMemory_EmptySlice(t *testing.T) {
	if hasFreshMemory(nil, 30*time.Minute, time.Now()) {
		t.Error("hasFreshMemory should return false for nil slice")
	}
	if hasFreshMemory([]*Memory{}, 30*time.Minute, time.Now()) {
		t.Error("hasFreshMemory should return false for empty slice")
	}
}

// --- Nudge Protocol Tests ---

// nudgeTestKeyoku creates a Keyoku with the given store and optional time period override.
func nudgeTestKeyoku(store storage.Store, timePeriod string) *Keyoku {
	k := &Keyoku{store: store, logger: slog.Default()}
	if timePeriod != "" {
		k.timePeriodOverride = timePeriod
	}
	return k
}

// nudgeTestParams returns HeartbeatParams suitable for nudge testing.
func nudgeTestParams() HeartbeatParams {
	return HeartbeatParams{
		NudgeAfterSilence: 2 * time.Hour,
		MaxNudgesPerDay:   3,
		NudgeMaxInterval:  48 * time.Hour,
	}
}

func TestEvaluateNudge_ObserveMode(t *testing.T) {
	k := nudgeTestKeyoku(&testStore{}, PeriodWorking)
	result := &HeartbeatResult{}
	k.evaluateNudge(context.Background(), "entity-1", "default", "observe", nudgeTestParams(), result)

	if result.ShouldAct {
		t.Error("observe mode should not nudge")
	}
	if result.DecisionReason != "no_signals" {
		t.Errorf("DecisionReason = %q, want no_signals", result.DecisionReason)
	}
}

func TestEvaluateNudge_Disabled(t *testing.T) {
	k := nudgeTestKeyoku(&testStore{}, PeriodWorking)
	result := &HeartbeatResult{}
	params := nudgeTestParams()
	params.NudgeAfterSilence = 0 // disabled

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", params, result)

	if result.ShouldAct {
		t.Error("NudgeAfterSilence=0 should not nudge")
	}
	if result.DecisionReason != "no_signals" {
		t.Errorf("DecisionReason = %q, want no_signals", result.DecisionReason)
	}
}

func TestEvaluateNudge_InsufficientSilence(t *testing.T) {
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: time.Now().Add(-30 * time.Minute)}, // only 30m silence
			}, nil
		},
	}
	k := nudgeTestKeyoku(store, PeriodWorking)
	result := &HeartbeatResult{}
	params := nudgeTestParams() // requires 2h silence

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", params, result)

	if result.ShouldAct {
		t.Error("insufficient silence should not nudge")
	}
	if result.DecisionReason != "no_signals" {
		t.Errorf("DecisionReason = %q, want no_signals", result.DecisionReason)
	}
}

func TestEvaluateNudge_DailyCapReached(t *testing.T) {
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: time.Now().Add(-3 * time.Hour)}, // 3h silence
			}, nil
		},
		getNudgeCountTodayFn: func(_ context.Context, _, _ string) (int, error) {
			return 3, nil // at cap (MaxNudgesPerDay=3)
		},
	}
	k := nudgeTestKeyoku(store, PeriodWorking)
	result := &HeartbeatResult{}

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", nudgeTestParams(), result)

	if result.ShouldAct {
		t.Error("should not nudge when daily cap reached")
	}
	if result.DecisionReason != "suppress_nudge_cap" {
		t.Errorf("DecisionReason = %q, want suppress_nudge_cap", result.DecisionReason)
	}
}

func TestEvaluateNudge_BackoffSuppression(t *testing.T) {
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: time.Now().Add(-5 * time.Hour)}, // 5h silence
			}, nil
		},
		getNudgeCountTodayFn: func(_ context.Context, _, _ string) (int, error) {
			return 1, nil // 1 nudge already → backoff: 2h * 2^1 = 4h required
		},
		getLastHeartbeatActionFn: func(_ context.Context, _, _, decision string) (*storage.HeartbeatAction, error) {
			if decision == "act" {
				return &storage.HeartbeatAction{
					ActedAt:         time.Now().Add(-3 * time.Hour), // 3h ago (< 4h required)
					Decision:        "act",
					TriggerCategory: "nudge",
				}, nil
			}
			return nil, nil
		},
	}
	k := nudgeTestKeyoku(store, PeriodWorking)
	result := &HeartbeatResult{}

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", nudgeTestParams(), result)

	if result.ShouldAct {
		t.Error("should not nudge during backoff period")
	}
	if result.DecisionReason != "suppress_nudge_backoff" {
		t.Errorf("DecisionReason = %q, want suppress_nudge_backoff", result.DecisionReason)
	}
}

func TestEvaluateNudge_TimePeriodSuppression(t *testing.T) {
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: time.Now().Add(-3 * time.Hour)},
			}, nil
		},
	}
	// Evening period has min tier = Normal, nudge is Low tier → suppressed
	k := nudgeTestKeyoku(store, PeriodEvening)
	result := &HeartbeatResult{}

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", nudgeTestParams(), result)

	if result.ShouldAct {
		t.Error("should not nudge during evening")
	}
	if result.DecisionReason != "suppress_time_period" {
		t.Errorf("DecisionReason = %q, want suppress_time_period", result.DecisionReason)
	}
}

func TestEvaluateNudge_SuccessfulNudge(t *testing.T) {
	now := time.Now()
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: now.Add(-3 * time.Hour)}, // 3h silence > 2h threshold
			}, nil
		},
		// getNudgeCountTodayFn defaults to 0
		// getLastHeartbeatActionFn defaults to nil (no previous nudge)
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			// Return a plan for findNudgeContent continuity-first path
			if len(q.Types) > 0 && (q.Types[0] == storage.TypePlan || q.Types[0] == storage.TypeActivity) {
				return []*storage.Memory{{
					ID:          "plan-1",
					Content:     "Complete the API integration",
					Type:        storage.TypePlan,
					Importance:  0.8,
					AccessCount: 1,
					State:       storage.StateActive,
				}}, nil
			}
			return nil, nil
		},
	}
	k := nudgeTestKeyoku(store, PeriodWorking)
	result := &HeartbeatResult{}

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", nudgeTestParams(), result)

	if !result.ShouldAct {
		t.Errorf("should nudge with sufficient silence and content, got DecisionReason=%q", result.DecisionReason)
	}
	if result.DecisionReason != "nudge" {
		t.Errorf("DecisionReason = %q, want nudge", result.DecisionReason)
	}
	if result.NudgeContext == "" {
		t.Error("NudgeContext should be populated")
	}
	if result.NudgeContext != "Complete the API integration" {
		t.Errorf("NudgeContext = %q, want 'Complete the API integration'", result.NudgeContext)
	}
}

func TestEvaluateNudge_NoContent(t *testing.T) {
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{CreatedAt: time.Now().Add(-3 * time.Hour)},
			}, nil
		},
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil // no memories at all
		},
	}
	k := nudgeTestKeyoku(store, PeriodWorking)
	result := &HeartbeatResult{}

	k.evaluateNudge(context.Background(), "entity-1", "default", "act", nudgeTestParams(), result)

	if result.ShouldAct {
		t.Error("should not nudge when no content available")
	}
	if result.DecisionReason != "no_signals" {
		t.Errorf("DecisionReason = %q, want no_signals", result.DecisionReason)
	}
}

func TestFindNudgeContent_ContinuityFirst(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && (q.Types[0] == storage.TypePlan || q.Types[0] == storage.TypeActivity) {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Ship the release", Type: storage.TypePlan, Importance: 0.7, AccessCount: 1},
					{ID: "plan-2", Content: "Low importance", Type: storage.TypePlan, Importance: 0.3, AccessCount: 0},
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store, logger: slog.Default()}

	content := k.findNudgeContent(context.Background(), "entity-1", "default")
	if content != "Ship the release" {
		t.Errorf("findNudgeContent = %q, want 'Ship the release' (continuity-first, importance >= 0.5)", content)
	}
}

func TestFindNudgeContent_HighImportanceFallback(t *testing.T) {
	callCount := 0
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			callCount++
			// First call: continuity types → empty
			if len(q.Types) > 0 {
				return nil, nil
			}
			// Second call: all active memories by importance
			return []*storage.Memory{
				{ID: "mem-1", Content: "Very important fact", Importance: 0.9, AccessCount: 0},
				{ID: "mem-2", Content: "Moderately important", Importance: 0.5, AccessCount: 0},
			}, nil
		},
	}
	k := &Keyoku{store: store, logger: slog.Default()}

	content := k.findNudgeContent(context.Background(), "entity-1", "default")
	if content != "Very important fact" {
		t.Errorf("findNudgeContent = %q, want 'Very important fact' (high-importance fallback)", content)
	}
}

func TestFindNudgeContent_EmptyStore(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}
	k := &Keyoku{store: store, logger: slog.Default()}

	content := k.findNudgeContent(context.Background(), "entity-1", "default")
	if content != "" {
		t.Errorf("findNudgeContent = %q, want empty for empty store", content)
	}
}

// --- Signals-Only Mode ---

func TestSignalsOnly_SkipsDecisionPipeline(t *testing.T) {
	now := time.Now()
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && (q.Types[0] == storage.TypePlan || q.Types[0] == storage.TypeActivity) {
				return []*storage.Memory{
					{ID: "plan-1", Content: "Deploy v2", Type: storage.TypePlan,
						Importance: 0.8, State: storage.StateActive, CreatedAt: now, UpdatedAt: now},
				}, nil
			}
			return nil, nil
		},
		// Simulate a recent act (watcher just acted milliseconds ago)
		getLastHeartbeatActionFn: func(_ context.Context, _, _, decision string) (*storage.HeartbeatAction, error) {
			if decision == "act" {
				return &storage.HeartbeatAction{
					Decision:          "act",
					ActedAt:           now.Add(-100 * time.Millisecond),
					SignalFingerprint: "abc123",
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{
		store:              store,
		eventBus:           NewEventBus(false),
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	// Without signals_only: should be suppressed by cooldown
	result1, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork), WithAutonomy("suggest"))
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	// The recent act should cause suppress_cooldown (or similar suppression)
	if result1.ShouldAct && result1.DecisionReason != "first_contact" {
		t.Logf("Without signals_only: ShouldAct=%v reason=%s (may vary by store state)", result1.ShouldAct, result1.DecisionReason)
	}

	// With signals_only: should always return true with signals
	result2, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork), WithAutonomy("suggest"), WithSignalsOnly(true))
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	if !result2.ShouldAct {
		t.Error("signals_only=true should force ShouldAct=true")
	}
	if result2.DecisionReason != "signals_only" {
		t.Errorf("DecisionReason = %q, want 'signals_only'", result2.DecisionReason)
	}
	if len(result2.PendingWork) == 0 {
		t.Error("signals_only should still return pending work signals")
	}
	if result2.TimePeriod == "" {
		t.Error("signals_only should set TimePeriod")
	}
}

func TestSignalsOnly_NoSignals_StillReturnsTrue(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}
	k := &Keyoku{
		store:              store,
		eventBus:           NewEventBus(false),
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithSignalsOnly(true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.ShouldAct {
		t.Error("signals_only should always set ShouldAct=true even with no signals")
	}
	if result.DecisionReason != "signals_only" {
		t.Errorf("DecisionReason = %q, want 'signals_only'", result.DecisionReason)
	}
}

func TestSignalsOnly_PropagatesInConversation(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}
	k := &Keyoku{
		store:              store,
		eventBus:           NewEventBus(false),
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithSignalsOnly(true), WithInConversation(true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !result.InConversation {
		t.Error("signals_only should propagate inConversation to result")
	}
}

func TestSignalsOnly_SkipsSurfacedMemoryFilter(t *testing.T) {
	now := time.Now()
	store := &surfacedStore{
		testStore: testStore{
			queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
				if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
					return []*storage.Memory{
						{
							ID:         "mem-1",
							Content:    "Deploy v3",
							Type:       storage.TypePlan,
							Importance: 0.8,
							State:      storage.StateActive,
							CreatedAt:  now,
							UpdatedAt:  now,
						},
					}, nil
				}
				return nil, nil
			},
		},
		surfacedIDs: []string{"mem-1"},
	}
	k := &Keyoku{
		store:              store,
		eventBus:           NewEventBus(false),
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork), WithSignalsOnly(true))
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	if len(result.PendingWork) != 1 {
		t.Fatalf("signals_only should keep surfaced pending work, got %d", len(result.PendingWork))
	}
	if result.PendingWork[0].ID != "mem-1" {
		t.Errorf("pending work ID = %q, want mem-1", result.PendingWork[0].ID)
	}
}

// --- Issue 2: allChecks contains CheckPositiveDeltas and CheckMemoryVelocity ---

func TestAllChecks_ContainsPositiveDeltasAndVelocity(t *testing.T) {
	found := map[HeartbeatCheckType]bool{}
	for _, c := range allChecks {
		found[c] = true
	}
	if !found[CheckPositiveDeltas] {
		t.Error("allChecks missing CheckPositiveDeltas")
	}
	if !found[CheckMemoryVelocity] {
		t.Error("allChecks missing CheckMemoryVelocity")
	}
}

func TestPositiveDeltas_GatedByCheckType(t *testing.T) {
	now := time.Now()
	prevSnapshot := StateSnapshot{MemoryCount: 5, MemoryCountAt: now.Add(-1 * time.Hour)}
	snapshotJSON, _ := json.Marshal(prevSnapshot)

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		getLastHeartbeatActionFn: func(_ context.Context, entityID, agentID, triggerCategory string) (*storage.HeartbeatAction, error) {
			return &storage.HeartbeatAction{
				ActedAt:       now.Add(-1 * time.Hour),
				StateSnapshot: string(snapshotJSON),
			}, nil
		},
		getMemoryCountForEntityFn: func(_ context.Context, entityID string) (int, error) {
			return 15, nil // +10 velocity
		},
	}
	k := &Keyoku{store: store, eventBus: NewEventBus(false), logger: slog.Default(), timePeriodOverride: PeriodWorking}

	// Without CheckPositiveDeltas and CheckMemoryVelocity, should not compute them
	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(result.PositiveDeltas) > 0 {
		t.Error("PositiveDeltas should be empty when CheckPositiveDeltas not in checks")
	}
	if result.MemoryVelocity != 0 {
		t.Errorf("MemoryVelocity = %d, want 0 when CheckMemoryVelocity not in checks", result.MemoryVelocity)
	}
}

// --- Issue 3: Stale escape with real signals → act, not nudge ---

func TestStaleEscape_ReActsWithRealSignals(t *testing.T) {
	now := time.Now()
	pending := &Memory{
		ID:         "plan-1",
		Content:    "Build feature X",
		Type:       storage.TypePlan,
		Importance: 0.8,
		State:      storage.StateActive,
		Confidence: 0.9,
		UpdatedAt:  now.Add(-1 * time.Hour),
	}
	expectedFingerprint := (&Keyoku{}).computeSignalFingerprint(&HeartbeatResult{
		PendingWork: []*Memory{pending},
	})
	var recorded *storage.HeartbeatAction
	nudgeEvaluated := false
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					pending,
				}, nil
			}
			return nil, nil
		},
		getLastHeartbeatActionFn: func(_ context.Context, entityID, agentID, triggerCategory string) (*storage.HeartbeatAction, error) {
			// Same fingerprint, but past 2x cooldown (stale escape territory)
			return &storage.HeartbeatAction{
				SignalFingerprint: expectedFingerprint,
				ActedAt:           now.Add(-5 * time.Hour), // well past 2x cooldown
			}, nil
		},
		recordHeartbeatActionFn: func(_ context.Context, action *storage.HeartbeatAction) error {
			recorded = action
			return nil
		},
		getRecentSessionMessagesFn: func(_ context.Context, entityID string, limit int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{Role: "user", CreatedAt: now.Add(-5 * time.Hour)},
			}, nil
		},
		getNudgeCountTodayFn: func(_ context.Context, entityID, agentID string) (int, error) {
			nudgeEvaluated = true
			return 0, nil
		},
	}

	k := &Keyoku{store: store, eventBus: NewEventBus(false), logger: slog.Default(), timePeriodOverride: PeriodWorking}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithAutonomy("suggest"),
		WithVirtualNow(now))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// With real signals (PendingWork), stale escape should act instead of nudge
	if result.DecisionReason == "nudge" {
		t.Error("stale escape with real signals should act, not nudge")
	}
	if result.DecisionReason == "suppress_stale" {
		t.Fatal("stale escape with real signals should not be overwritten to suppress_stale")
	}
	if result.DecisionReason != "act_stale_escalated" {
		t.Errorf("DecisionReason = %q, want %q", result.DecisionReason, "act_stale_escalated")
	}
	if !result.ShouldAct {
		t.Error("ShouldAct = false, want true for stale escape with real signals")
	}
	if nudgeEvaluated {
		t.Fatal("stale escape act path should not evaluate nudge flow")
	}
	if recorded == nil {
		t.Fatal("expected stale escape act to be recorded")
	}
	if recorded.Decision != "act" {
		t.Errorf("recorded.Decision = %q, want %q", recorded.Decision, "act")
	}
	if recorded.SignalFingerprint != expectedFingerprint {
		t.Errorf("recorded.SignalFingerprint = %q, want %q", recorded.SignalFingerprint, expectedFingerprint)
	}
}

// --- Issue 4: PendingWork recency gate ---

func TestPendingWork_StalePlansFiltered(t *testing.T) {
	now := time.Now()
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					// Stale plan: 30 days old, no deadline → should be filtered
					{ID: "old-plan", Content: "Old plan", Type: storage.TypePlan,
						Importance: 0.9, State: storage.StateActive, Confidence: 0.9,
						UpdatedAt: now.Add(-30 * 24 * time.Hour)},
					// Recent plan: 2 days old → should be kept
					{ID: "recent-plan", Content: "Recent plan", Type: storage.TypePlan,
						Importance: 0.5, State: storage.StateActive, Confidence: 0.9,
						UpdatedAt: now.Add(-2 * 24 * time.Hour)},
					// Old plan with upcoming deadline → should be kept
					{ID: "deadline-plan", Content: "Deadline plan", Type: storage.TypePlan,
						Importance: 0.6, State: storage.StateActive, Confidence: 0.9,
						UpdatedAt:  now.Add(-20 * 24 * time.Hour),
						ExpiresAt:  timePtr(now.Add(7 * 24 * time.Hour))},
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store, eventBus: NewEventBus(false), logger: slog.Default()}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Should have 2 items: recent-plan and deadline-plan (not old-plan)
	if len(result.PendingWork) != 2 {
		t.Fatalf("PendingWork count = %d, want 2", len(result.PendingWork))
	}
	for _, m := range result.PendingWork {
		if m.ID == "old-plan" {
			t.Error("stale plan without deadline should be filtered out")
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// --- Issue 5: Topic dedup window uses SignalCooldownNormal ---

func TestTopicDedupWindow_UsesParams(t *testing.T) {
	// Verify the shouldSuppressTopicRepeat function accepts a window parameter
	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, entityID, agentID string, window time.Duration) ([]*storage.HeartbeatAction, error) {
			// Verify the window is what we passed, not hardcoded 1h
			if window != 3*time.Hour {
				t.Errorf("window = %v, want 3h", window)
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store}
	k.shouldSuppressTopicRepeat(context.Background(), "entity-1", "agent-1", nil, "", "", 3*time.Hour)
}

// --- Issue 6: Confidence gating ---

func TestConfidenceGating_LowConfidenceFiltered(t *testing.T) {
	now := time.Now()
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{ID: "low-conf", Content: "Low confidence", Type: storage.TypePlan,
						Importance: 0.8, State: storage.StateActive, Confidence: 0.3,
						UpdatedAt: now.Add(-1 * time.Hour)},
					{ID: "high-conf", Content: "High confidence", Type: storage.TypePlan,
						Importance: 0.8, State: storage.StateActive, Confidence: 0.7,
						UpdatedAt: now.Add(-1 * time.Hour)},
				}, nil
			}
			return nil, nil
		},
	}
	k := &Keyoku{store: store, eventBus: NewEventBus(false), logger: slog.Default()}

	result, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(result.PendingWork) != 1 {
		t.Fatalf("PendingWork count = %d, want 1 (low-confidence filtered)", len(result.PendingWork))
	}
	if result.PendingWork[0].ID != "high-conf" {
		t.Errorf("expected high-conf memory, got %s", result.PendingWork[0].ID)
	}
}

// --- Issue 7: Tag-based tier adjustment ---

func TestTagBasedTierAdjustment(t *testing.T) {
	tests := []struct {
		name     string
		baseTier string
		tags     []string
		want     string
	}{
		{"critical boosts Normal→Elevated", TierNormal, []string{"critical"}, TierElevated},
		{"urgent boosts Normal→Elevated", TierNormal, []string{"urgent"}, TierElevated},
		{"backlog demotes Normal→Low", TierNormal, []string{"backlog"}, TierLow},
		{"low-priority demotes Elevated→Normal", TierElevated, []string{"low-priority"}, TierNormal},
		{"critical at Immediate stays Immediate", TierImmediate, []string{"critical"}, TierImmediate},
		{"backlog at Low stays Low", TierLow, []string{"backlog"}, TierLow},
		{"no tags = no change", TierNormal, nil, TierNormal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memories := []*Memory{{Tags: tt.tags}}
			got := adjustTierByTags(tt.baseTier, memories)
			if got != tt.want {
				t.Errorf("adjustTierByTags(%q, tags=%v) = %q, want %q", tt.baseTier, tt.tags, got, tt.want)
			}
		})
	}
}

// --- Issue 8: Stability weighting in confluence ---

func TestStabilityWeighting_HighStabilityBoostsConfluence(t *testing.T) {
	highStabResult := &HeartbeatResult{
		PendingWork: []*Memory{{Stability: 0.9}},
		Decaying:    []*Memory{{Stability: 0.8}},
	}
	lowStabResult := &HeartbeatResult{
		PendingWork: []*Memory{{Stability: 0.1}},
		Decaying:    []*Memory{{Stability: 0.1}},
	}

	signals := map[HeartbeatCheckType]string{
		CheckPendingWork: TierNormal,
		CheckDecaying:    TierLow,
	}

	highScore, _ := calculateSignalConfluence(signals, "suggest", highStabResult)
	lowScore, _ := calculateSignalConfluence(signals, "suggest", lowStabResult)

	if highScore <= lowScore {
		t.Errorf("high stability score (%d) should be > low stability score (%d)", highScore, lowScore)
	}
}

// --- Issue 9: Response rate multiplier smooth curve ---

func TestResponseCooldownMultiplier_SmoothCurve(t *testing.T) {
	tests := []struct {
		rate    float64
		wantMin float64
		wantMax float64
	}{
		{0.0, 1.9, 2.1},     // rate 0 → 2x (capped to prevent silence)
		{0.1, 1.5, 1.9},     // rate 0.1 → ~1.8x
		{0.25, 1.4, 1.6},    // rate 0.25 → ~1.5x
		{0.3, 1.3, 1.5},     // rate 0.3 → ~1.4x
		{0.5, 0.9, 1.1},     // rate 0.5 → 1x
		{0.8, 0.9, 1.1},     // rate 0.8 → 1x
		{1.0, 0.9, 1.1},     // rate 1.0 → 1x
	}
	for _, tt := range tests {
		got := responseCooldownMultiplier(tt.rate)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("responseCooldownMultiplier(%v) = %v, want [%v, %v]",
				tt.rate, got, tt.wantMin, tt.wantMax)
		}
	}
}

func TestShiftTier(t *testing.T) {
	tests := []struct {
		tier  string
		delta int
		want  string
	}{
		{TierNormal, 1, TierElevated},
		{TierNormal, -1, TierLow},
		{TierImmediate, 1, TierImmediate}, // clamped
		{TierLow, -1, TierLow},           // clamped
		{TierLow, 2, TierElevated},
	}
	for _, tt := range tests {
		got := shiftTier(tt.tier, tt.delta)
		if got != tt.want {
			t.Errorf("shiftTier(%q, %d) = %q, want %q", tt.tier, tt.delta, got, tt.want)
		}
	}
}

// --- Stale Signal Suppression Tests ---

func TestGoalProgressFilter_ExpiredPlanFiltered(t *testing.T) {
	now := time.Now().UTC()
	expiresAt := now.Add(-24 * time.Hour)
	result, err := runGoalProgressCheck(t, now,
		&storage.Memory{
			ID:         "plan-expired",
			Content:    "Submit launch checklist",
			Type:       storage.TypePlan,
			Importance: 0.9,
			State:      storage.StateActive,
			ExpiresAt:  &expiresAt,
		},
		&storage.Memory{
			ID:        "activity-old",
			Content:   "Worked on launch checklist",
			Type:      storage.TypeActivity,
			State:     storage.StateActive,
			CreatedAt: now.Add(-10 * 24 * time.Hour),
		},
	)
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	if len(result.allGoalProgress) != 1 {
		t.Fatalf("allGoalProgress count = %d, want 1", len(result.allGoalProgress))
	}
	if result.allGoalProgress[0].Status != "stalled" {
		t.Errorf("expired plan status = %q, want stalled before filtering", result.allGoalProgress[0].Status)
	}
	if len(result.GoalProgress) != 0 {
		t.Fatalf("GoalProgress count = %d, want 0 for expired stale goal", len(result.GoalProgress))
	}
}

func TestGoalProgressFilter_FutureExpiryPlanKept(t *testing.T) {
	now := time.Now().UTC()
	expiresAt := now.Add(7 * 24 * time.Hour)
	result, err := runGoalProgressCheck(t, now,
		&storage.Memory{
			ID:         "plan-future",
			Content:    "Ship onboarding update",
			Type:       storage.TypePlan,
			Importance: 0.9,
			State:      storage.StateActive,
			ExpiresAt:  &expiresAt,
		},
		&storage.Memory{
			ID:        "activity-old",
			Content:   "Outlined onboarding changes",
			Type:      storage.TypeActivity,
			State:     storage.StateActive,
			CreatedAt: now.Add(-10 * 24 * time.Hour),
		},
	)
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	if len(result.GoalProgress) != 1 {
		t.Fatalf("GoalProgress count = %d, want 1", len(result.GoalProgress))
	}
	if result.GoalProgress[0].Plan.ID != "plan-future" {
		t.Errorf("GoalProgress plan ID = %q, want plan-future", result.GoalProgress[0].Plan.ID)
	}
	if result.GoalProgress[0].Status != "at_risk" {
		t.Errorf("future-expiry status = %q, want at_risk", result.GoalProgress[0].Status)
	}
}

func TestGoalProgressFilter_NoExpiryPlanNotMisclassified(t *testing.T) {
	now := time.Now().UTC()
	result, err := runGoalProgressCheck(t, now,
		&storage.Memory{
			ID:         "plan-no-expiry",
			Content:    "Refactor heartbeat prompts",
			Type:       storage.TypePlan,
			Importance: 0.9,
			State:      storage.StateActive,
		},
		&storage.Memory{
			ID:        "activity-old",
			Content:   "Started prompt refactor",
			Type:      storage.TypeActivity,
			State:     storage.StateActive,
			CreatedAt: now.Add(-10 * 24 * time.Hour),
		},
	)
	if err != nil {
		t.Fatalf("HeartbeatCheck error: %v", err)
	}
	if len(result.GoalProgress) != 1 {
		t.Fatalf("GoalProgress count = %d, want 1", len(result.GoalProgress))
	}
	if result.GoalProgress[0].Plan.ID != "plan-no-expiry" {
		t.Errorf("GoalProgress plan ID = %q, want plan-no-expiry", result.GoalProgress[0].Plan.ID)
	}
	if result.GoalProgress[0].Status != "stalled" {
		t.Errorf("no-expiry status = %q, want stalled", result.GoalProgress[0].Status)
	}
	if result.GoalProgress[0].DaysLeft != -1 {
		t.Errorf("DaysLeft = %v, want -1 sentinel", result.GoalProgress[0].DaysLeft)
	}
}

func runGoalProgressCheck(t *testing.T, now time.Time, plan *storage.Memory, activity *storage.Memory) (*HeartbeatResult, error) {
	t.Helper()

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{plan}, nil
			}
			return nil, nil
		},
		findSimilarFn: func(_ context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: activity, Similarity: 0.9},
			}, nil
		},
	}

	k := &Keyoku{
		store: store,
		emb:   &goalProgressTestEmbedder{},
	}

	return k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckGoalProgress),
		WithVirtualNow(now))
}

type goalProgressTestEmbedder struct{}

func (e *goalProgressTestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func (e *goalProgressTestEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{1, 0, 0}
	}
	return result, nil
}

func (e *goalProgressTestEmbedder) Dimensions() int {
	return 3
}

func TestEvaluateShouldAct_LowTierOnly_NoConfluence(t *testing.T) {
	// Low-tier-only signals (e.g., patterns) without confluence should NOT trigger act
	store := &testStore{}
	k := nudgeTestKeyoku(store, PeriodWorking)

	result := &HeartbeatResult{
		Patterns: []BehavioralPattern{
			{Description: "User usually asks about Go on Mondays", Confidence: 0.7},
		},
	}
	cfg := &heartbeatConfig{
		autonomy: "act",
	}
	k.evaluateShouldAct(context.Background(), "entity-1", cfg, result)

	if result.ShouldAct {
		t.Errorf("low-tier-only signals should not trigger act, got ShouldAct=true, reason=%s", result.DecisionReason)
	}
	if result.DecisionReason != "suppress_low_no_confluence" {
		t.Errorf("DecisionReason = %q, want suppress_low_no_confluence", result.DecisionReason)
	}
}

func TestEvaluateShouldAct_NormalTier_PassesThrough(t *testing.T) {
	// Normal-tier signals (e.g., pending_work) should pass through to act
	store := &testStore{}
	k := nudgeTestKeyoku(store, PeriodWorking)

	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{ID: "task-1", Content: "Review PR #42", Importance: 0.8},
		},
	}
	cfg := &heartbeatConfig{
		autonomy: "act",
	}
	k.evaluateShouldAct(context.Background(), "entity-1", cfg, result)

	if !result.ShouldAct {
		t.Errorf("normal-tier signals should trigger act, got ShouldAct=false, reason=%s", result.DecisionReason)
	}
}
