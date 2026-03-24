// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestBuildDeliveryMessage_PriorityAction(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "act",
		PriorityAction: "Review the API design doc",
		Urgency:        "soon",
		TimePeriod:     "working",
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "Review the API design doc") {
		t.Errorf("expected priority action in message, got: %s", msg)
	}
	if !strings.Contains(msg, "Urgency: soon") {
		t.Errorf("expected urgency in message, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_Nudge(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "nudge",
		NudgeContext:   "Last discussed: migration to new API",
		TimePeriod:     "morning",
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "[Nudge]") {
		t.Errorf("expected nudge marker, got: %s", msg)
	}
	if !strings.Contains(msg, "migration to new API") {
		t.Errorf("expected nudge context, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_MemoryVelocity(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason:     "act",
		MemoryVelocity:     12,
		MemoryVelocityHigh: true,
		Summary:            "PENDING WORK: complete auth module",
		TimePeriod:         "working",
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "12 new memories") {
		t.Errorf("expected memory velocity note, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_EscalationTone(t *testing.T) {
	tests := []struct {
		level    int
		expected string
	}{
		{1, "casual"},
		{2, "direct"},
		{3, "offer help"},
	}
	for _, tt := range tests {
		result := &HeartbeatResult{
			DecisionReason:  "act",
			EscalationLevel: tt.level,
			Summary:         "test",
			TimePeriod:      "working",
		}
		msg := buildDeliveryMessage(result)
		if !strings.Contains(msg, tt.expected) {
			t.Errorf("escalation %d: expected %q in message, got: %s", tt.level, tt.expected, msg)
		}
	}
}

func TestBuildDeliveryMessage_PositiveDeltas(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "act",
		PositiveDeltas: []PositiveDelta{
			{Type: "goal_improved", Description: "Auth module moved from stalled to on_track"},
		},
		TimePeriod: "working",
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "[+]") {
		t.Errorf("expected positive delta marker, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_Empty(t *testing.T) {
	result := &HeartbeatResult{}
	msg := buildDeliveryMessage(result)
	if msg != "" {
		t.Errorf("expected empty message for empty result (skip delivery), got: %s", msg)
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	long := strings.Repeat("a", 100)
	result := truncate(long, 20)
	if len(result) != 20 {
		t.Errorf("expected length 20, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("truncated string should end with ...")
	}
}

func TestNewDeliverer_CLI(t *testing.T) {
	d := NewDeliverer(DeliveryConfig{Method: "cli", Command: "echo"})
	if d == nil {
		t.Fatal("expected CLI deliverer, got nil")
	}
	if _, ok := d.(*CLIDeliverer); !ok {
		t.Error("expected *CLIDeliverer")
	}
}

func TestNewDeliverer_Empty(t *testing.T) {
	d := NewDeliverer(DeliveryConfig{})
	if d != nil {
		t.Error("expected nil deliverer for empty method")
	}
}

// mockDeliverer records delivery calls for testing.
type mockDeliverer struct {
	calls atomic.Int32
}

func (m *mockDeliverer) Deliver(ctx context.Context, entityID string, result *HeartbeatResult) error {
	m.calls.Add(1)
	return nil
}

func TestWatcher_DeliveryOnAct(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				now := time.Now()
				return []*storage.Memory{{
					ID:         "mem-1",
					EntityID:   "entity-1",
					AgentID:    "agent-1",
					Content:    "Complete the integration",
					Type:       storage.TypePlan,
					State:      storage.StateActive,
					Importance: 0.9,
					Confidence: 0.8,
					CreatedAt:  now,
					UpdatedAt:  now,
				}}, nil
			}
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:              store,
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	mock := &mockDeliverer{}

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.deliverer = mock
	w.Start()

	time.Sleep(150 * time.Millisecond)
	w.Stop()

	if mock.calls.Load() == 0 {
		t.Error("expected at least one delivery call when ShouldAct=true")
	}
}

func TestWatcher_NoDeliveryWhenNoDeliverer(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				now := time.Now()
				return []*storage.Memory{{
					ID:         "mem-1",
					EntityID:   "entity-1",
					AgentID:    "agent-1",
					Content:    "Complete the integration",
					Type:       storage.TypePlan,
					State:      storage.StateActive,
					Importance: 0.9,
					Confidence: 0.8,
					CreatedAt:  now,
					UpdatedAt:  now,
				}}, nil
			}
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:              store,
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	// No deliverer set — events should still fire, no panic
	var signalCount atomic.Int32
	bus.On(EventHeartbeatSignal, func(e Event) {
		signalCount.Add(1)
	})

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	if signalCount.Load() == 0 {
		t.Error("expected events to still fire without deliverer")
	}
}

func TestComputeNextInterval_Defaults(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})

	// Working hours, no last result, no last act → should be base * 1.0
	interval := w.computeNextInterval()
	if interval != 5*time.Minute {
		t.Errorf("expected 5m base interval during working hours, got %v", interval)
	}
}

func TestComputeNextInterval_LateNight(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodLateNight,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})

	interval := w.computeNextInterval()
	// 5m * 3.0 (late night multiplier) = 15m
	if interval != 15*time.Minute {
		t.Errorf("expected 15m at late night, got %v", interval)
	}
}

func TestComputeNextInterval_QuietHours(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodQuiet,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})

	interval := w.computeNextInterval()
	// 5m * 3.0 (quiet hours) = 15m
	if interval != 15*time.Minute {
		t.Errorf("expected 15m during quiet hours, got %v", interval)
	}
}

func TestComputeNextInterval_RecentAct(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})
	w.lastActTime = time.Now().Add(-2 * time.Minute) // acted 2 min ago

	interval := w.computeNextInterval()
	// 5m * 0.5 (recent act) * 1.0 (working) = 2.5m
	expected := 2*time.Minute + 30*time.Second
	if interval != expected {
		t.Errorf("expected %v after recent act, got %v", expected, interval)
	}
}

func TestComputeNextInterval_HighSignalDensity(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})
	w.lastResult = &HeartbeatResult{
		PendingWork: make([]*Memory, 3),
		Deadlines:   make([]*Memory, 2),
		Scheduled:   make([]*Memory, 1),
	}

	interval := w.computeNextInterval()
	// 6 signals > 5, so 5m * 0.5 = 2.5m
	expected := 2*time.Minute + 30*time.Second
	if interval != expected {
		t.Errorf("expected %v with high signal density, got %v", expected, interval)
	}
}

func TestComputeNextInterval_HighMemoryVelocity(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})
	w.lastResult = &HeartbeatResult{
		MemoryVelocityHigh: true,
	}

	interval := w.computeNextInterval()
	// 5m * 1.5 (zero signals) * 0.5 (high velocity) = 3.75m
	expected := 3*time.Minute + 45*time.Second
	if interval != expected {
		t.Errorf("expected %v with high memory velocity, got %v", expected, interval)
	}
}

func TestComputeNextInterval_MinClamp(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodMorning,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 2 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
	})
	// Stack multipliers: recent act (0.5) + morning (0.5) + high signals (0.5) + high velocity (0.5)
	w.lastActTime = time.Now().Add(-2 * time.Minute)
	w.lastResult = &HeartbeatResult{
		PendingWork:        make([]*Memory, 6),
		MemoryVelocityHigh: true,
	}

	interval := w.computeNextInterval()
	// 2m * 0.5 * 0.5 * 0.5 * 0.5 = 7.5s, clamped to 1m
	if interval != 1*time.Minute {
		t.Errorf("expected 1m (min clamp), got %v", interval)
	}
}

func TestCountSignals(t *testing.T) {
	r := &HeartbeatResult{
		PendingWork: make([]*Memory, 2),
		Deadlines:   make([]*Memory, 1),
		Continuity:  &ContinuityItem{},
	}
	if n := countSignals(r); n != 4 {
		t.Errorf("expected 4 signals, got %d", n)
	}
}

func TestCLIDeliverer_BuildArgs(t *testing.T) {
	d := NewCLIDeliverer(DeliveryConfig{
		Method:    "cli",
		Command:   "openclaw",
		Channel:   "telegram",
		Recipient: "-4970078838",
	})

	args := d.buildArgs("hello world")
	expected := []string{"agent", "--message", "hello world", "--deliver", "--session-id", "telegram:group:-4970078838", "--channel", "telegram", "--reply-to", "-4970078838"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], a)
		}
	}
}

// --- CLIDeliverer.Deliver() tests via CommandRunner mock ---

type mockRunner struct {
	lastCmd  string
	lastArgs []string
	output   []byte
	err      error
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	m.lastCmd = name
	m.lastArgs = args
	return m.output, m.err
}

func TestCLIDeliverer_Deliver_Success(t *testing.T) {
	runner := &mockRunner{output: []byte("ok")}
	d := NewCLIDeliverer(DeliveryConfig{
		Command:   "openclaw",
		Channel:   "telegram",
		Recipient: "-123",
	})
	d.runner = runner

	result := &HeartbeatResult{
		DecisionReason: "act",
		Summary:        "test signals",
		TimePeriod:     "working",
	}

	err := d.Deliver(context.Background(), "user-1", result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.lastCmd != "openclaw" {
		t.Errorf("expected cmd=openclaw, got %q", runner.lastCmd)
	}
	// Verify args contain --message and --deliver
	found := false
	for _, a := range runner.lastArgs {
		if a == "--deliver" {
			found = true
		}
	}
	if !found {
		t.Error("expected --deliver in args")
	}
}

func TestCLIDeliverer_Deliver_CommandError(t *testing.T) {
	runner := &mockRunner{
		output: []byte("command not found"),
		err:    fmt.Errorf("exit status 1"),
	}
	d := NewCLIDeliverer(DeliveryConfig{Command: "openclaw"})
	d.runner = runner

	result := &HeartbeatResult{
		DecisionReason: "act",
		Summary:        "test",
		TimePeriod:     "working",
	}

	err := d.Deliver(context.Background(), "user-1", result)
	if err == nil {
		t.Fatal("expected error on command failure")
	}
	if !strings.Contains(err.Error(), "delivery:") {
		t.Errorf("expected 'delivery:' prefix in error, got: %v", err)
	}
}

func TestCLIDeliverer_Deliver_EmptyCommand(t *testing.T) {
	d := NewCLIDeliverer(DeliveryConfig{Command: ""}) // defaults to "openclaw"
	d.runner = &mockRunner{output: []byte("ok")}

	result := &HeartbeatResult{
		DecisionReason: "act",
		Summary:        "test",
		TimePeriod:     "working",
	}

	// Should succeed since default command is "openclaw"
	err := d.Deliver(context.Background(), "user-1", result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIDeliverer_Deliver_Timeout(t *testing.T) {
	// Create a runner that respects context cancellation
	runner := &mockRunner{}
	d := NewCLIDeliverer(DeliveryConfig{
		Command: "openclaw",
		Timeout: 50 * time.Millisecond,
	})
	// Replace runner with one that blocks until context is done
	d.runner = CommandRunnerFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	result := &HeartbeatResult{
		DecisionReason: "act",
		Summary:        "test",
		TimePeriod:     "working",
	}

	err := d.Deliver(context.Background(), "user-1", result)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	_ = runner // suppress unused
}

// --- Enhanced delivery message tests (v4: LLM analysis) ---

func TestBuildDeliveryMessage_EnhancedAnalysis(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "act",
		EnhancedAnalysis: &llm.HeartbeatAnalysisResponse{
			ShouldAct:          true,
			ActionBrief:        "Review PR #42 in keyoku-engine — stale for 3 days",
			RecommendedActions: []string{"Review PR #42", "Check CI status"},
			Urgency:            "medium",
			Autonomy:           "act",
			UserFacing:         "I'm going to review PR #42 since it's been open for 3 days.",
		},
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "Review PR #42") {
		t.Errorf("expected action brief in message, got: %s", msg)
	}
	if !strings.Contains(msg, "Check CI status") {
		t.Errorf("expected recommended action in message, got: %s", msg)
	}
	if !strings.Contains(msg, "Tell the User") {
		t.Errorf("expected user-facing section, got: %s", msg)
	}
	if !strings.Contains(msg, "Mode: act") {
		t.Errorf("expected mode in message, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_EnhancedAnalysis_ShouldNotAct(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "act",
		EnhancedAnalysis: &llm.HeartbeatAnalysisResponse{
			ShouldAct:   false,
			ActionBrief: "",
			Urgency:     "none",
		},
	}
	msg := buildDeliveryMessage(result)
	if msg != "" {
		t.Errorf("expected empty message when LLM says don't act, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_EnhancedAnalysis_EmptyBrief(t *testing.T) {
	result := &HeartbeatResult{
		DecisionReason: "act",
		EnhancedAnalysis: &llm.HeartbeatAnalysisResponse{
			ShouldAct:   true,
			ActionBrief: "", // LLM said act but provided no brief
		},
	}
	msg := buildDeliveryMessage(result)
	if msg != "" {
		t.Errorf("expected empty message when action brief is empty, got: %s", msg)
	}
}

func TestBuildDeliveryMessage_FallbackWithoutEnhanced(t *testing.T) {
	// When no EnhancedAnalysis, should use original programmatic assembly
	result := &HeartbeatResult{
		DecisionReason: "act",
		PriorityAction: "Check the build status",
		Urgency:        "high",
		TimePeriod:     "working",
	}
	msg := buildDeliveryMessage(result)
	if !strings.Contains(msg, "Check the build status") {
		t.Errorf("expected fallback to use PriorityAction, got: %s", msg)
	}
	if !strings.Contains(msg, "Urgency: high") {
		t.Errorf("expected urgency in fallback message, got: %s", msg)
	}
}

// --- Pipeline verification tests: real signals → non-empty delivery ---

func TestPipeline_PendingWorkProducesDeliveryMessage(t *testing.T) {
	// Verify: when PendingWork exists in HeartbeatResult, the programmatic
	// fallback (no LLM) produces a non-empty delivery message with signal content.
	now := time.Now()
	result := &HeartbeatResult{
		DecisionReason: "act",
		Urgency:        "medium",
		TimePeriod:     "working",
		PendingWork: []*Memory{{
			ID:         "mem-pending-1",
			Content:    "Review PR #42 for keyoku-engine",
			Type:       "plan",
			Importance: 0.9,
			Confidence: 0.8,
			CreatedAt:  now,
			UpdatedAt:  now,
		}},
	}
	result.Summary = buildSummary(result)

	msg := buildDeliveryMessage(result)
	if msg == "" {
		t.Fatal("expected non-empty delivery message when PendingWork signals exist")
	}
	if !strings.Contains(msg, "Review PR #42") {
		t.Errorf("expected pending work content in message, got: %s", msg)
	}
	if !strings.Contains(msg, "Signals:") {
		t.Errorf("expected Signals section in message, got: %s", msg)
	}
}

func TestPipeline_DeadlinesProduceDeliveryMessage(t *testing.T) {
	expires := time.Now().Add(2 * time.Hour)
	result := &HeartbeatResult{
		DecisionReason: "act",
		Urgency:        "high",
		TimePeriod:     "working",
		Deadlines: []*Memory{{
			ID:        "mem-deadline-1",
			Content:   "Ship v2.0 release",
			Type:      "plan",
			ExpiresAt: &expires,
		}},
	}
	result.Summary = buildSummary(result)

	msg := buildDeliveryMessage(result)
	if msg == "" {
		t.Fatal("expected non-empty delivery message when Deadline signals exist")
	}
	if !strings.Contains(msg, "Ship v2.0") {
		t.Errorf("expected deadline content in message, got: %s", msg)
	}
}

func TestPipeline_ScheduledProducesDeliveryMessage(t *testing.T) {
	now := time.Now()
	result := &HeartbeatResult{
		DecisionReason: "act",
		Urgency:        "critical",
		TimePeriod:     "morning",
		Scheduled: []*Memory{{
			ID:        "mem-sched-1",
			Content:   "Daily standup reminder",
			Type:      "plan",
			Tags:      []string{"cron:daily:09:00"},
			CreatedAt: now,
		}},
	}
	result.Summary = buildSummary(result)

	msg := buildDeliveryMessage(result)
	if msg == "" {
		t.Fatal("expected non-empty delivery message when Scheduled signals exist")
	}
	if !strings.Contains(msg, "Daily standup") {
		t.Errorf("expected scheduled content in message, got: %s", msg)
	}
}

func TestPipeline_MultipleSignalsProduceRichMessage(t *testing.T) {
	// Multiple signal categories should all appear in the delivery message.
	now := time.Now()
	expires := now.Add(6 * time.Hour)
	result := &HeartbeatResult{
		DecisionReason:     "act",
		Urgency:            "high",
		TimePeriod:         "working",
		MemoryVelocityHigh: true,
		MemoryVelocity:     8,
		EscalationLevel:    2,
		PendingWork: []*Memory{{
			ID: "pw-1", Content: "Implement auth module", Type: "plan",
			Importance: 0.9, CreatedAt: now, UpdatedAt: now,
		}},
		Deadlines: []*Memory{{
			ID: "dl-1", Content: "API redesign deadline", Type: "plan",
			ExpiresAt: &expires,
		}},
		PositiveDeltas: []PositiveDelta{{
			Type:        "goal_improved",
			Description: "Auth module moved from stalled to on_track",
		}},
	}
	result.Summary = buildSummary(result)

	msg := buildDeliveryMessage(result)
	if msg == "" {
		t.Fatal("expected non-empty delivery message with multiple signals")
	}
	// Verify all signal types appear
	if !strings.Contains(msg, "Implement auth module") {
		t.Errorf("missing pending work in message")
	}
	if !strings.Contains(msg, "8 new memories") {
		t.Errorf("missing memory velocity in message")
	}
	if !strings.Contains(msg, "[+]") {
		t.Errorf("missing positive delta marker in message")
	}
	if !strings.Contains(msg, "direct") {
		t.Errorf("missing escalation tone in message")
	}
}

func TestPipeline_EnhancedAnalysisTakesPrecedence(t *testing.T) {
	// When EnhancedAnalysis is present with ShouldAct=true, the LLM output
	// takes priority over the programmatic fallback.
	result := &HeartbeatResult{
		DecisionReason: "act",
		Summary:        "PENDING WORK: something", // would appear in fallback
		PendingWork: []*Memory{{
			ID: "pw-1", Content: "fallback content",
		}},
		EnhancedAnalysis: &llm.HeartbeatAnalysisResponse{
			ShouldAct:          true,
			ActionBrief:        "You have 3 open PRs that need review",
			RecommendedActions: []string{"Review PR #42", "Merge PR #38"},
			Urgency:            "high",
			Autonomy:           "suggest",
			UserFacing:         "Hey, a few PRs are waiting on you.",
		},
	}

	msg := buildDeliveryMessage(result)
	if msg == "" {
		t.Fatal("expected non-empty message with enhanced analysis")
	}
	// Should use LLM output, NOT fallback
	if !strings.Contains(msg, "3 open PRs") {
		t.Errorf("expected LLM action brief, got: %s", msg)
	}
	if strings.Contains(msg, "Signals:") {
		t.Errorf("should NOT fall back to programmatic Signals section when enhanced analysis present")
	}
	if !strings.Contains(msg, "Hey, a few PRs") {
		t.Errorf("expected user-facing message, got: %s", msg)
	}
}

func TestPipeline_WatcherDelivery_WithRealSignals(t *testing.T) {
	// End-to-end: watcher tick → HeartbeatCheck with real signals → delivery.
	// Verifies that when PendingWork memories exist, the watcher calls Deliver
	// and the message contains actual signal content.
	now := time.Now()

	var deliveredMessage string
	var deliveredEntityID string

	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				return []*storage.Memory{{
					ID:         "mem-work-1",
					EntityID:   "entity-1",
					Content:    "Deploy keyoku-engine v0.7.0 to production",
					Type:       storage.TypePlan,
					State:      storage.StateActive,
					Importance: 0.9,
					Confidence: 0.8,
					CreatedAt:  now,
					UpdatedAt:  now,
				}}, nil
			}
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:              store,
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	// Custom deliverer that captures the message
	capturingDeliverer := &capturingMockDeliverer{}

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.deliverer = capturingDeliverer
	w.Start()
	time.Sleep(200 * time.Millisecond)
	w.Stop()

	if capturingDeliverer.calls.Load() == 0 {
		t.Fatal("expected at least one delivery call")
	}
	deliveredMessage = capturingDeliverer.lastMessage
	deliveredEntityID = capturingDeliverer.lastEntityID

	if deliveredEntityID != "entity-1" {
		t.Errorf("expected entity-1, got %s", deliveredEntityID)
	}
	if deliveredMessage == "" {
		t.Fatal("delivery message was empty — signals not reaching delivery")
	}
	if !strings.Contains(deliveredMessage, "Deploy keyoku-engine v0.7.0") {
		t.Errorf("delivery message missing signal content, got: %s", deliveredMessage)
	}
}

func TestPipeline_WatcherDelivery_WithLLMProvider(t *testing.T) {
	// End-to-end: watcher with LLM provider → HeartbeatCheck with signals →
	// LLM analysis → delivery with EnhancedAnalysis content.
	now := time.Now()

	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				return []*storage.Memory{{
					ID:         "mem-pr-1",
					EntityID:   "entity-1",
					Content:    "Review Copilot suggestions on PR #15",
					Type:       storage.TypePlan,
					State:      storage.StateActive,
					Importance: 0.85,
					Confidence: 0.9,
					CreatedAt:  now,
					UpdatedAt:  now,
				}}, nil
			}
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	llmProvider := &testLLMProvider{
		analyzeHeartbeatFn: func(ctx context.Context, req llm.HeartbeatAnalysisRequest) (*llm.HeartbeatAnalysisResponse, error) {
			return &llm.HeartbeatAnalysisResponse{
				ShouldAct:          true,
				ActionBrief:        "PR #15 has Copilot suggestions waiting for review",
				RecommendedActions: []string{"Review and apply valid suggestions", "Tag author for unclear ones"},
				Urgency:            "medium",
				Autonomy:           "act",
				UserFacing:         "I'll review the Copilot suggestions on PR #15.",
			}, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:              store,
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	capturingDeliverer := &capturingMockDeliverer{}

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
		HeartbeatOpts: []HeartbeatOption{
			WithLLMPrioritization(llmProvider, "", ""),
		},
	})
	w.deliverer = capturingDeliverer
	w.Start()
	time.Sleep(200 * time.Millisecond)
	w.Stop()

	if capturingDeliverer.calls.Load() == 0 {
		t.Fatal("expected at least one delivery call with LLM provider")
	}

	msg := capturingDeliverer.lastMessage
	if msg == "" {
		t.Fatal("delivery message was empty with LLM provider")
	}
	// Should use LLM enhanced analysis output
	if !strings.Contains(msg, "Copilot suggestions") {
		t.Errorf("expected LLM analysis content in delivery, got: %s", msg)
	}
	if !strings.Contains(msg, "Tell the User") {
		t.Errorf("expected user-facing section from LLM, got: %s", msg)
	}
}

func TestPipeline_EmptyResultNoDelivery(t *testing.T) {
	// Verify: completely empty HeartbeatResult → no delivery (v0.6.2 fix).
	result := &HeartbeatResult{}
	msg := buildDeliveryMessage(result)
	if msg != "" {
		t.Errorf("empty result should produce empty message (no delivery), got: %s", msg)
	}

	// Also verify via CLIDeliverer — Deliver() should skip
	runner := &mockRunner{output: []byte("ok")}
	d := NewCLIDeliverer(DeliveryConfig{Command: "openclaw", Channel: "telegram", Recipient: "-123"})
	d.runner = runner

	err := d.Deliver(context.Background(), "user-1", result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.lastCmd != "" {
		t.Error("expected command NOT to be executed for empty result")
	}
}

func TestPipeline_SummaryAlwaysBuiltFromSignals(t *testing.T) {
	// Verify that buildSummary produces structured text from all signal types.
	now := time.Now()
	expires := now.Add(3 * time.Hour)
	result := &HeartbeatResult{
		PendingWork: []*Memory{
			{ID: "pw-1", Content: "Finish API migration", Type: "plan", Importance: 0.8, CreatedAt: now, UpdatedAt: now},
		},
		Deadlines: []*Memory{
			{ID: "dl-1", Content: "Client demo", ExpiresAt: &expires},
		},
		Scheduled: []*Memory{
			{ID: "sc-1", Content: "Weekly report", Tags: []string{"cron:weekly:mon:09:00"}},
		},
		StaleMonitors: []*Memory{
			{ID: "sm-1", Content: "Monitor CI pipeline"},
		},
	}

	summary := buildSummary(result)
	if summary == "" {
		t.Fatal("expected non-empty summary from signals")
	}
	checks := map[string]string{
		"PENDING WORK":    "Finish API migration",
		"DEADLINES":       "Client demo",
		"SCHEDULED TASKS": "Weekly report",
		"STALE MONITORS":  "Monitor CI pipeline",
	}
	for section, content := range checks {
		if !strings.Contains(summary, section) {
			t.Errorf("summary missing section %q", section)
		}
		if !strings.Contains(summary, content) {
			t.Errorf("summary missing content %q from section %q", content, section)
		}
	}
}

// capturingMockDeliverer records delivery calls AND captures the actual message.
type capturingMockDeliverer struct {
	calls        atomic.Int32
	lastMessage  string
	lastEntityID string
}

func (m *capturingMockDeliverer) Deliver(ctx context.Context, entityID string, result *HeartbeatResult) error {
	m.calls.Add(1)
	m.lastEntityID = entityID
	m.lastMessage = buildDeliveryMessage(result)
	return nil
}
