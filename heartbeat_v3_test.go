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
		{PeriodQuiet, 10.0},
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
		WithChecks(CheckPendingWork))
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
		WithChecks(CheckPendingWork))
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

	boostSignalFreshness(activeSignals, result)

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

	boostSignalFreshness(activeSignals, result)

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

	boostSignalFreshness(activeSignals, result)

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

	boostSignalFreshness(activeSignals, result)

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
		WithChecks(CheckPendingWork),
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
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "e1", "a1", []string{"entity-999"}, hash)
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
					TopicEntities:     []string{"entity-A", "entity-B"},
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Different hash AND different entities → should NOT suppress
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "e1", "a1", []string{"entity-C", "entity-D"}, "newhash9876543210")
	if suppressed {
		t.Error("shouldSuppressTopicRepeat should return false when both hash and entities differ")
	}
}

func TestContentDedup_EntityOverlapStillWorks(t *testing.T) {
	store := &testStore{
		getRecentActDecisionsFn: func(_ context.Context, _, _ string, _ time.Duration) ([]*storage.HeartbeatAction, error) {
			return []*storage.HeartbeatAction{
				{
					ActedAt:           time.Now().Add(-20 * time.Minute),
					Decision:          "act",
					SignalSummaryHash: "differenthash12345",                                  // different hash
					TopicEntities:     []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}, // same entities
				},
			}, nil
		},
	}
	k := &Keyoku{store: store}

	// Different hash but 100% entity overlap → Layer 2 should catch it
	suppressed := k.shouldSuppressTopicRepeat(context.Background(), "owner", "agent", []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}, "anotherhash9999999")
	if !suppressed {
		t.Error("shouldSuppressTopicRepeat should return true when entity overlap > 85% (Layer 2)")
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
	if !hasFreshMemory(memories, 30*time.Minute) {
		t.Error("hasFreshMemory should return true when a memory was created within window")
	}
}

func TestHasFreshMemory_ReturnsFalse(t *testing.T) {
	memories := []*Memory{
		{ID: "old1", CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{ID: "old2", CreatedAt: time.Now().Add(-3 * time.Hour), UpdatedAt: time.Now().Add(-3 * time.Hour)},
	}
	if hasFreshMemory(memories, 30*time.Minute) {
		t.Error("hasFreshMemory should return false when no memory is within window")
	}
}

func TestHasFreshMemory_UpdatedAtCounts(t *testing.T) {
	memories := []*Memory{
		{ID: "m1", CreatedAt: time.Now().Add(-5 * time.Hour), UpdatedAt: time.Now().Add(-5 * time.Minute)}, // old creation, fresh update
	}
	if !hasFreshMemory(memories, 30*time.Minute) {
		t.Error("hasFreshMemory should return true when UpdatedAt is within window")
	}
}

func TestHasFreshMemory_EmptySlice(t *testing.T) {
	if hasFreshMemory(nil, 30*time.Minute) {
		t.Error("hasFreshMemory should return false for nil slice")
	}
	if hasFreshMemory([]*Memory{}, 30*time.Minute) {
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
