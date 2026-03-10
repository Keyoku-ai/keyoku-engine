// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"strings"
	"testing"
	"time"

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

	k := &Keyoku{store: store}
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
	if len(allChecks) != 6 {
		t.Errorf("allChecks = %d, want 6", len(allChecks))
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
