// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestParseCronTag(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		wantDur  time.Duration
		wantBool bool
	}{
		{"hourly", []string{"cron:hourly"}, time.Hour, true},
		{"daily", []string{"cron:daily"}, 24 * time.Hour, true},
		{"daily with time", []string{"cron:daily:09:00"}, 24 * time.Hour, true},
		{"weekly", []string{"cron:weekly"}, 7 * 24 * time.Hour, true},
		{"weekly with day", []string{"cron:weekly:monday"}, 7 * 24 * time.Hour, true},
		{"monthly", []string{"cron:monthly"}, 30 * 24 * time.Hour, true},
		{"every 30m", []string{"cron:every:30m"}, 30 * time.Minute, true},
		{"every 2h", []string{"cron:every:2h"}, 2 * time.Hour, true},
		{"no cron tag", []string{"other", "tag"}, 0, false},
		{"empty tags", nil, 0, false},
		{"invalid every", []string{"cron:every:invalid"}, 0, false},
		{"mixed tags", []string{"monitor", "cron:daily"}, 24 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dur, ok := parseCronTag(tt.tags)
			if ok != tt.wantBool {
				t.Errorf("parseCronTag(%v) ok = %v, want %v", tt.tags, ok, tt.wantBool)
			}
			if dur != tt.wantDur {
				t.Errorf("parseCronTag(%v) dur = %v, want %v", tt.tags, dur, tt.wantDur)
			}
		})
	}
}

func TestHasTag(t *testing.T) {
	if !hasTag([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for existing tag")
	}
	if hasTag([]string{"a", "b"}, "c") {
		t.Error("expected false for missing tag")
	}
	if hasTag(nil, "a") {
		t.Error("expected false for nil slice")
	}
}

func TestBuildSummary(t *testing.T) {
	now := time.Now()
	expires := now.Add(2 * time.Hour)

	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{Content: "finish task", Type: storage.TypePlan, Importance: 0.9},
		},
		Deadlines: []*Memory{
			{Content: "deadline approaching", ExpiresAt: &expires},
		},
		Scheduled: []*Memory{
			{Content: "daily check", Tags: storage.StringSlice{"cron:daily:08:00"}},
		},
		Decaying: []*Memory{
			{Content: "important fact", Importance: 0.95},
		},
		Conflicts: []ConflictPair{
			{MemoryA: &storage.Memory{Content: "conflicting info"}, Reason: "contradicts another"},
		},
		StaleMonitors: []*Memory{
			{Content: "monitor task"},
		},
	}

	summary := buildSummary(result)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "PENDING WORK") {
		t.Error("summary missing PENDING WORK")
	}
	if !strings.Contains(summary, "APPROACHING DEADLINES") {
		t.Error("summary missing APPROACHING DEADLINES")
	}
	if !strings.Contains(summary, "SCHEDULED TASKS DUE") {
		t.Error("summary missing SCHEDULED TASKS DUE")
	}
	if !strings.Contains(summary, "IMPORTANT MEMORIES DECAYING") {
		t.Error("summary missing IMPORTANT MEMORIES DECAYING")
	}
	if !strings.Contains(summary, "UNRESOLVED CONFLICTS") {
		t.Error("summary missing UNRESOLVED CONFLICTS")
	}
	if !strings.Contains(summary, "STALE MONITORS") {
		t.Error("summary missing STALE MONITORS")
	}
	if !strings.Contains(summary, "[schedule: cron:daily:08:00]") {
		t.Error("summary missing schedule tag annotation")
	}
}

func TestBuildSummary_Empty(t *testing.T) {
	result := &HeartbeatResult{}
	summary := buildSummary(result)
	if summary != "" {
		t.Errorf("expected empty summary for empty result, got %q", summary)
	}
}

func TestHeartbeatCheck_PendingWork(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{Content: "important plan", Type: storage.TypePlan, Importance: 0.9, State: storage.StateActive},
				}, nil
			}
			return nil, nil
		},
	}

	k := &Keyoku{store: store, timePeriodOverride: PeriodWorking}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if !result.ShouldAct {
		t.Error("expected ShouldAct = true")
	}
	if len(result.PendingWork) != 1 {
		t.Errorf("PendingWork = %d, want 1", len(result.PendingWork))
	}
}

func TestHeartbeatCheck_PendingWork_ExpiredDeadlineDoesNotBypassRecency(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	expired := now.Add(-2 * time.Hour)
	staleUpdated := now.Add(-8 * 24 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:   "old plan",
					Type:      storage.TypePlan,
					Importance: 0.9,
					State:     storage.StateActive,
					UpdatedAt: staleUpdated,
					ExpiresAt: &expired,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store, timePeriodOverride: PeriodWorking}
	result, err := k.HeartbeatCheck(
		context.Background(),
		"entity-1",
		WithChecks(CheckPendingWork),
		WithVirtualNow(now),
	)
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.PendingWork) != 0 {
		t.Errorf("PendingWork = %d, want 0", len(result.PendingWork))
	}
}

func TestHeartbeatCheck_PendingWork_ResolvedMemoryExcluded(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.States) != 1 || q.States[0] != storage.StateActive {
				t.Fatalf("states = %v, want [active]", q.States)
			}
			return nil, nil
		},
	}

	k := &Keyoku{store: store, timePeriodOverride: PeriodWorking}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckPendingWork))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.PendingWork) != 0 {
		t.Errorf("PendingWork = %d, want 0", len(result.PendingWork))
	}
}

func TestHeartbeatCheck_Deadlines(t *testing.T) {
	expires := time.Now().Add(6 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{Content: "deadline soon", ExpiresAt: &expires, State: storage.StateActive},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckDeadlines))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Deadlines) != 1 {
		t.Errorf("Deadlines = %d, want 1", len(result.Deadlines))
	}
}

func TestHeartbeatCheck_Scheduled(t *testing.T) {
	oldAccess := time.Now().Add(-25 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:        "daily task",
					Tags:           storage.StringSlice{"cron:daily"},
					LastAccessedAt: &oldAccess,
					CreatedAt:      oldAccess,
					State:          storage.StateActive,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckScheduled))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Scheduled) != 1 {
		t.Errorf("Scheduled = %d, want 1", len(result.Scheduled))
	}
}

func TestHeartbeatCheck_Decaying(t *testing.T) {
	store := &testStore{
		getStaleMemoriesFn: func(_ context.Context, _ string, _ float64) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{Content: "decaying important memory", Importance: 0.9},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckDecaying))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Decaying) != 1 {
		t.Errorf("Decaying = %d, want 1", len(result.Decaying))
	}
}

func TestHeartbeatCheck_Conflicts(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:           "conflicting memory",
					ConfidenceFactors: storage.StringSlice{"conflict_flagged: contradicts other info"},
					State:             storage.StateActive,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckConflicts))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("Conflicts = %d, want 1", len(result.Conflicts))
	}
}

func TestHeartbeatCheck_StaleMonitors(t *testing.T) {
	oldAccess := time.Now().Add(-48 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.Types) > 0 && q.Types[0] == storage.TypePlan {
				return []*storage.Memory{
					{
						Content:        "monitor something",
						Type:           storage.TypePlan,
						Tags:           storage.StringSlice{"monitor"},
						LastAccessedAt: &oldAccess,
						CreatedAt:      oldAccess,
						State:          storage.StateActive,
					},
				}, nil
			}
			return nil, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckStale))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.StaleMonitors) != 1 {
		t.Errorf("StaleMonitors = %d, want 1", len(result.StaleMonitors))
	}
}

func TestHeartbeatCheck_ShouldAct_False(t *testing.T) {
	store := &testStore{}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if result.ShouldAct {
		t.Error("expected ShouldAct = false when nothing needs attention")
	}
	if result.Summary != "" {
		t.Errorf("expected empty summary, got %q", result.Summary)
	}
}

func TestHeartbeatOptions(t *testing.T) {
	cfg := &heartbeatConfig{}

	WithDeadlineWindow(48 * time.Hour)(cfg)
	if cfg.deadlineWindow != 48*time.Hour {
		t.Errorf("deadlineWindow = %v", cfg.deadlineWindow)
	}

	WithDecayThreshold(0.5)(cfg)
	if cfg.decayThreshold != 0.5 {
		t.Errorf("decayThreshold = %v", cfg.decayThreshold)
	}

	WithImportanceFloor(0.8)(cfg)
	if cfg.importanceFloor != 0.8 {
		t.Errorf("importanceFloor = %v", cfg.importanceFloor)
	}

	WithMaxResults(50)(cfg)
	if cfg.maxResults != 50 {
		t.Errorf("maxResults = %d", cfg.maxResults)
	}

	WithHeartbeatAgentID("agent-1")(cfg)
	if cfg.agentID != "agent-1" {
		t.Errorf("agentID = %q", cfg.agentID)
	}

	WithChecks(CheckPendingWork, CheckDeadlines)(cfg)
	if len(cfg.checks) != 2 {
		t.Errorf("checks = %d, want 2", len(cfg.checks))
	}
}

func TestHeartbeatCheckTypes(t *testing.T) {
	if CheckPendingWork != "pending_work" {
		t.Errorf("CheckPendingWork = %q", CheckPendingWork)
	}
	if CheckDeadlines != "deadlines" {
		t.Errorf("CheckDeadlines = %q", CheckDeadlines)
	}
	if CheckScheduled != "scheduled" {
		t.Errorf("CheckScheduled = %q", CheckScheduled)
	}
	if CheckDecaying != "decaying" {
		t.Errorf("CheckDecaying = %q", CheckDecaying)
	}
	if CheckConflicts != "conflicts" {
		t.Errorf("CheckConflicts = %q", CheckConflicts)
	}
	if CheckStale != "stale_monitors" {
		t.Errorf("CheckStale = %q", CheckStale)
	}
	if len(allChecks) != 14 {
		t.Errorf("allChecks = %d, want 14", len(allChecks))
	}
}

func TestHeartbeatCheck_Scheduled_TimeAnchored(t *testing.T) {
	// Simulate: cron:daily:08:00, last accessed yesterday at 8:01am,
	// current time is today at 8:05am → should be due
	yesterday8am := time.Date(2026, 2, 25, 8, 1, 0, 0, time.Local)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:        "check the news",
					Tags:           storage.StringSlice{"cron:daily:08:00"},
					LastAccessedAt: &yesterday8am,
					CreatedAt:      yesterday8am.Add(-48 * time.Hour),
					State:          storage.StateActive,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckScheduled))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}

	// The schedule parser uses time.Now() internally so we can't exactly control
	// the outcome, but cron:daily:08:00 with last run >24h ago should be due
	// at any time today after 8:00am (and current time is well past 8am on 2/26)
	if len(result.Scheduled) != 1 {
		t.Errorf("Scheduled = %d, want 1 (time-anchored daily schedule should be due)", len(result.Scheduled))
	}
}

func TestHeartbeatCheck_Scheduled_NotDueYet(t *testing.T) {
	// cron:daily with last access 12 hours ago → not due yet (interval-based)
	recent := time.Now().Add(-12 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:        "daily task",
					Tags:           storage.StringSlice{"cron:daily"},
					LastAccessedAt: &recent,
					CreatedAt:      recent.Add(-48 * time.Hour),
					State:          storage.StateActive,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckScheduled))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Scheduled) != 0 {
		t.Errorf("Scheduled = %d, want 0 (cron:daily accessed 12h ago shouldn't be due)", len(result.Scheduled))
	}
}

func TestHeartbeatCheck_Scheduled_EveryInterval(t *testing.T) {
	// cron:every:2h with last access 3 hours ago → should be due
	threeHoursAgo := time.Now().Add(-3 * time.Hour)
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{
					Content:        "frequent check",
					Tags:           storage.StringSlice{"cron:every:2h"},
					LastAccessedAt: &threeHoursAgo,
					CreatedAt:      threeHoursAgo.Add(-24 * time.Hour),
					State:          storage.StateActive,
				},
			}, nil
		},
	}

	k := &Keyoku{store: store}
	result, err := k.HeartbeatCheck(context.Background(), "entity-1", WithChecks(CheckScheduled))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
	if len(result.Scheduled) != 1 {
		t.Errorf("Scheduled = %d, want 1 (cron:every:2h with 3h since last run)", len(result.Scheduled))
	}
}

// --- gatherHeartbeatContext tests ---

func TestGatherHeartbeatContext_WithMessages(t *testing.T) {
	now := time.Now()
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, entityID string, limit int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{ID: "msg-1", EntityID: entityID, Role: "user", Content: "I resolved the auth bug", CreatedAt: now},
				{ID: "msg-2", EntityID: entityID, Role: "assistant", Content: "Great, marking it as done", CreatedAt: now},
			}, nil
		},
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", Content: "Auth bug in login flow", Importance: 0.9, State: storage.StateActive},
			}, nil
		},
	}

	k := NewForTesting(store)
	result := &HeartbeatResult{Summary: "test signals"}
	hctx := k.gatherHeartbeatContext(context.Background(), "user-1", result)

	if len(hctx.RecentMessages) != 2 {
		t.Errorf("RecentMessages = %d, want 2", len(hctx.RecentMessages))
	}
	if len(hctx.RelevantMemories) != 1 {
		t.Errorf("RelevantMemories = %d, want 1", len(hctx.RelevantMemories))
	}
	if hctx.SignalSummary != "test signals" {
		t.Errorf("SignalSummary = %q, want 'test signals'", hctx.SignalSummary)
	}
}

func TestGatherHeartbeatContext_EmptyStore(t *testing.T) {
	store := &testStore{}
	k := NewForTesting(store)
	result := &HeartbeatResult{}
	hctx := k.gatherHeartbeatContext(context.Background(), "user-1", result)

	if len(hctx.RecentMessages) != 0 {
		t.Errorf("RecentMessages = %d, want 0", len(hctx.RecentMessages))
	}
	if len(hctx.RelevantMemories) != 0 {
		t.Errorf("RelevantMemories = %d, want 0", len(hctx.RelevantMemories))
	}
}

func TestResolvedMemoryExcludedFromHeartbeat(t *testing.T) {
	// StateResolved should not be included in heartbeat queries that filter for StateActive
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			// Verify that queries only ask for active memories
			for _, s := range q.States {
				if s == storage.StateResolved {
					t.Error("heartbeat query should NOT include resolved state")
				}
			}
			return nil, nil
		},
	}

	k := NewForTesting(store)
	_, err := k.HeartbeatCheck(context.Background(), "entity-1",
		WithChecks(CheckPendingWork, CheckDeadlines, CheckDecaying))
	if err != nil {
		t.Fatalf("HeartbeatCheck error = %v", err)
	}
}

// --- runEnhancedLLMAnalysis tests ---

func TestRunEnhancedLLMAnalysis_Success(t *testing.T) {
	now := time.Now()
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{ID: "msg-1", EntityID: "user-1", Role: "user", Content: "How is PR #42 going?", CreatedAt: now},
				{ID: "msg-2", EntityID: "user-1", Role: "assistant", Content: "PR #42 is ready for review", CreatedAt: now},
			}, nil
		},
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", Content: "PR #42 needs code review", Importance: 0.9, State: storage.StateActive},
			}, nil
		},
	}

	var capturedReq llm.HeartbeatAnalysisRequest
	provider := &testLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, req llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			capturedReq = req
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct:          true,
				ActionBrief:        "Follow up on deployment after PR merge",
				RecommendedActions: []string{"Check CI status", "Notify team"},
				Urgency:            "medium",
				Reasoning:          "PR discussed but deployment not covered",
				Autonomy:           "suggest",
				UserFacing:         "Your PR #42 was merged. Want me to check the deployment?",
			}, nil
		},
	}

	k := NewForTesting(store)
	cfg := &heartbeatConfig{
		llmProvider: provider,
		autonomy:    "suggest",
		agentID:     "agent-1",
	}
	result := &HeartbeatResult{
		Summary:    "PR #42 merged, deployment pending",
		TimePeriod: "last 2 hours",
		PendingWork: []*storage.Memory{
			{Content: "Deploy after PR merge"},
		},
	}

	resp := k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)

	// Verify the LLM was called
	if resp == nil {
		t.Fatal("expected non-nil response from LLM analysis")
	}

	// Verify conversation history was passed
	if len(capturedReq.ConversationHistory) != 2 {
		t.Errorf("ConversationHistory = %d items, want 2", len(capturedReq.ConversationHistory))
	}
	if len(capturedReq.ConversationHistory) > 0 && !strings.Contains(capturedReq.ConversationHistory[0], "PR #42") {
		t.Errorf("ConversationHistory[0] = %q, expected to contain 'PR #42'", capturedReq.ConversationHistory[0])
	}

	// Verify relevant memories were passed
	if len(capturedReq.RelevantMemories) != 1 {
		t.Errorf("RelevantMemories = %d, want 1", len(capturedReq.RelevantMemories))
	}

	// Verify signals were passed
	if len(capturedReq.PendingWork) != 1 {
		t.Errorf("PendingWork = %d, want 1", len(capturedReq.PendingWork))
	}

	// Verify autonomy was forwarded
	if capturedReq.Autonomy != "suggest" {
		t.Errorf("Autonomy = %q, want 'suggest'", capturedReq.Autonomy)
	}

	// Verify result was updated from LLM response
	if result.PriorityAction != "Follow up on deployment after PR merge" {
		t.Errorf("PriorityAction = %q, want LLM's action brief", result.PriorityAction)
	}
	if len(result.ActionItems) != 2 {
		t.Errorf("ActionItems = %d, want 2", len(result.ActionItems))
	}
	if result.EnhancedAnalysis == nil {
		t.Error("EnhancedAnalysis should be set on result")
	}
	// "medium" is not in mapPriorityUrgency's mapping (immediate→critical, soon→high, can_wait→low),
	// so urgency stays unchanged. The EnhancedAnalysis still holds the original LLM urgency.
	if result.EnhancedAnalysis.Urgency != "medium" {
		t.Errorf("EnhancedAnalysis.Urgency = %q, want 'medium'", result.EnhancedAnalysis.Urgency)
	}
}

func TestRunEnhancedLLMAnalysis_NilProvider(t *testing.T) {
	k := NewForTesting(&testStore{})
	cfg := &heartbeatConfig{llmProvider: nil}
	result := &HeartbeatResult{}

	resp := k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)
	if resp != nil {
		t.Error("expected nil response when provider is nil")
	}
}

func TestRunEnhancedLLMAnalysis_LLMError_FallsBack(t *testing.T) {
	store := &testStore{}
	provider := &testLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, _ llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			return nil, errors.New("LLM unavailable")
		},
	}

	k := NewForTesting(store)
	cfg := &heartbeatConfig{llmProvider: provider}
	result := &HeartbeatResult{
		Summary:            "test signals",
		HighestUrgencyTier: "tier_2",
	}

	resp := k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)

	if resp != nil {
		t.Error("expected nil response on LLM error (fallback path)")
	}
	// Fallback uses programmatic tierToUrgency instead of another LLM call
	if result.Urgency == "" {
		t.Error("expected fallback to set urgency from tier (programmatic, no LLM)")
	}
}

func TestRunEnhancedLLMAnalysis_ConversationSuppression(t *testing.T) {
	// Verify that conversation history is included so the LLM can suppress
	// topics that were already discussed.
	now := time.Now()
	store := &testStore{
		getRecentSessionMessagesFn: func(_ context.Context, _ string, _ int) ([]*storage.SessionMessage, error) {
			return []*storage.SessionMessage{
				{ID: "msg-1", Role: "user", Content: "I already fixed the auth bug", CreatedAt: now},
				{ID: "msg-2", Role: "assistant", Content: "Great, I'll mark it as resolved", CreatedAt: now},
			}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", Content: "Auth bug in login flow", Importance: 0.9, State: storage.StateActive},
			}, nil
		},
	}

	var capturedReq llm.HeartbeatAnalysisRequest
	provider := &testLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, req llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			capturedReq = req
			// LLM decides not to act because auth bug was already discussed
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct:   false,
				ActionBrief: "",
				Urgency:     "none",
				Reasoning:   "Auth bug already discussed and resolved by user",
			}, nil
		},
	}

	k := NewForTesting(store)
	cfg := &heartbeatConfig{llmProvider: provider}
	result := &HeartbeatResult{
		Summary: "Auth bug detected in login flow",
		PendingWork: []*storage.Memory{
			{Content: "Auth bug in login flow"},
		},
	}

	resp := k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)

	// LLM should receive conversation history for suppression
	if len(capturedReq.ConversationHistory) != 2 {
		t.Fatalf("ConversationHistory = %d, want 2", len(capturedReq.ConversationHistory))
	}

	// Verify "auth bug" appears in conversation history
	found := false
	for _, msg := range capturedReq.ConversationHistory {
		if strings.Contains(strings.ToLower(msg), "auth bug") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected conversation history to contain 'auth bug' for suppression")
	}

	// LLM decided not to act — verify this flows through
	if resp.ShouldAct {
		t.Error("LLM should have decided not to act (topic already discussed)")
	}
}

func TestRunEnhancedLLMAnalysis_EndToEnd_DeliveryMessage(t *testing.T) {
	// Test the full pipeline: runEnhancedLLMAnalysis → result.EnhancedAnalysis → buildDeliveryMessage
	store := &testStore{}
	provider := &testLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, _ llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct:          true,
				ActionBrief:        "Deploy v2.1 to staging",
				RecommendedActions: []string{"Run integration tests", "Notify QA team"},
				Urgency:            "high",
				Autonomy:           "act",
				UserFacing:         "v2.1 is ready for staging deployment",
			}, nil
		},
	}

	k := NewForTesting(store)
	cfg := &heartbeatConfig{llmProvider: provider, autonomy: "act"}
	result := &HeartbeatResult{Summary: "v2.1 tagged and ready"}

	k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)

	// Now test buildDeliveryMessage with the enhanced result
	msg := buildDeliveryMessage(result)

	if msg == "" {
		t.Fatal("expected non-empty delivery message")
	}
	if !strings.Contains(msg, "Deploy v2.1 to staging") {
		t.Errorf("message missing action brief, got: %s", msg)
	}
	if !strings.Contains(msg, "Run integration tests") {
		t.Errorf("message missing recommended action, got: %s", msg)
	}
	if !strings.Contains(msg, "v2.1 is ready for staging deployment") {
		t.Errorf("message missing user-facing text, got: %s", msg)
	}
	if !strings.Contains(msg, "high") {
		t.Errorf("message missing urgency, got: %s", msg)
	}
}

func TestRunEnhancedLLMAnalysis_ShouldNotAct_EmptyDelivery(t *testing.T) {
	// When LLM says don't act, delivery message should be empty
	store := &testStore{}
	provider := &testLLMProvider{
		analyzeHeartbeatFn: func(_ context.Context, _ llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct: false,
				Urgency:   "none",
				Reasoning: "Nothing worth mentioning",
			}, nil
		},
	}

	k := NewForTesting(store)
	cfg := &heartbeatConfig{llmProvider: provider}
	result := &HeartbeatResult{}

	k.runEnhancedLLMAnalysis(context.Background(), "user-1", cfg, result)

	msg := buildDeliveryMessage(result)
	if msg != "" {
		t.Errorf("expected empty delivery message when LLM says don't act, got: %q", msg)
	}
}
