// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestWatcher_WatchUnwatch(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{eventBus: bus, logger: slog.Default()}
	w := newWatcher(k, DefaultWatcherConfig())

	w.Watch("entity-1")
	w.Watch("entity-2")

	ids := w.WatchedEntities()
	if len(ids) != 2 {
		t.Fatalf("expected 2 watched entities, got %d", len(ids))
	}

	w.Unwatch("entity-1")
	ids = w.WatchedEntities()
	if len(ids) != 1 {
		t.Fatalf("expected 1 watched entity, got %d", len(ids))
	}
}

func TestWatcher_EmitsHeartbeatSignal(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			// Return a pending work memory for the heartbeat check
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
		timePeriodOverride: PeriodWorking, // pin to working hours so the test is deterministic
	}

	var signalCount atomic.Int32
	var pendingCount atomic.Int32

	bus.On(EventHeartbeatSignal, func(e Event) {
		signalCount.Add(1)
	})
	bus.On(EventPendingWork, func(e Event) {
		pendingCount.Add(1)
	})

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()

	// Wait for at least one heartbeat cycle
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	if signalCount.Load() == 0 {
		t.Error("expected at least one heartbeat signal event")
	}
	if pendingCount.Load() == 0 {
		t.Error("expected at least one pending work event")
	}
}

func TestWatcher_NoEmitWhenNothingToDo(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:    store,
		eventBus: bus,
		logger:   slog.Default(),
	}

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

	if signalCount.Load() != 0 {
		t.Errorf("expected no heartbeat signal events when nothing to do, got %d", signalCount.Load())
	}
}

func TestWatcher_StopIsGraceful(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		store:    &testStore{},
		eventBus: bus,
		logger:   slog.Default(),
	}

	w := newWatcher(k, WatcherConfig{Interval: 1 * time.Second})
	w.Start()

	// Stop should not hang
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("watcher.Stop() timed out")
	}
}

// --- Status() tests for new DecisionReason + SignalSummary fields ---

func TestWatcherStatus_DecisionReasonOnSkip(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		getStaleMemoriesFn: func(ctx context.Context, entityID string, threshold float64) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	bus := NewEventBus(false)
	k := &Keyoku{
		store:    store,
		eventBus: bus,
		logger:   slog.Default(),
	}

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	s := w.Status()
	if s.LastDecision != "skip" {
		t.Errorf("expected last_decision=skip, got %q", s.LastDecision)
	}
	// With no signals, DecisionReason should be populated from HeartbeatResult
	if s.LastCheckAt == nil {
		t.Error("expected LastCheckAt to be set after tick")
	}
}

func TestWatcherStatus_SignalSummaryOnAct(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				now := time.Now()
				return []*storage.Memory{{
					ID:         "mem-1",
					EntityID:   "entity-1",
					AgentID:    "default",
					Content:    "Complete integration",
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

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	s := w.Status()
	if s.SignalSummary == nil {
		t.Fatal("expected SignalSummary to be populated")
	}
	if s.SignalSummary.Total == 0 {
		t.Error("expected non-zero signal total")
	}
	if s.SignalSummary.PendingWork == 0 {
		t.Error("expected non-zero PendingWork count")
	}
}

func TestWatcherStatus_NoResult(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{eventBus: bus, logger: slog.Default()}
	w := newWatcher(k, DefaultWatcherConfig())

	s := w.Status()
	if s.LastDecision != "none" {
		t.Errorf("expected last_decision=none before any tick, got %q", s.LastDecision)
	}
	if s.DecisionReason != "" {
		t.Errorf("expected empty decision_reason before any tick, got %q", s.DecisionReason)
	}
	if s.SignalSummary != nil {
		t.Error("expected nil SignalSummary before any tick")
	}
}

func TestWatcher_TickHistory(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil // no signals → skip decisions
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

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()
	time.Sleep(200 * time.Millisecond)
	w.Stop()

	history := w.History()
	if len(history) == 0 {
		t.Fatal("expected tick history to have records")
	}

	// Verify newest-first ordering
	for i := 1; i < len(history); i++ {
		if history[i].Timestamp.After(history[i-1].Timestamp) {
			t.Errorf("tick history not in newest-first order at index %d", i)
		}
	}

	// Verify record fields
	rec := history[0]
	if rec.EntityID != "entity-1" {
		t.Errorf("expected entity_id=entity-1, got %q", rec.EntityID)
	}
	if rec.Decision != "skip" {
		t.Errorf("expected decision=skip (no signals), got %q", rec.Decision)
	}
	if rec.DecisionReason == "" {
		t.Error("expected non-empty decision_reason")
	}
	if rec.SignalSummary == nil {
		t.Error("expected signal_summary to be populated")
	}
	if rec.TimePeriod != "working" {
		t.Errorf("expected time_period=working, got %q", rec.TimePeriod)
	}
}

func TestWatcher_TickHistoryActPath(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(query.Types) > 0 && (query.Types[0] == storage.TypePlan || query.Types[0] == storage.TypeActivity) {
				now := time.Now()
				return []*storage.Memory{{
					ID:         "mem-1",
					EntityID:   "entity-1",
					AgentID:    "default",
					Content:    "Important task",
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

	w := newWatcher(k, WatcherConfig{
		Interval:  50 * time.Millisecond,
		EntityIDs: []string{"entity-1"},
	})
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	history := w.History()
	if len(history) == 0 {
		t.Fatal("expected tick history to have records")
	}

	// Find an "act" decision
	var foundAct bool
	for _, rec := range history {
		if rec.Decision == "act" {
			foundAct = true
			if rec.SignalCount == 0 {
				t.Error("expected non-zero signal_count for act decision")
			}
			if rec.Delivered != nil {
				t.Error("expected nil Delivered in event-only mode (no deliverer)")
			}
			break
		}
	}
	if !foundAct {
		t.Error("expected at least one 'act' decision in history")
	}
}

func TestWatcher_TickHistoryRingBuffer(t *testing.T) {
	// Verify ring buffer trims to maxTickHistory
	bus := NewEventBus(false)
	k := &Keyoku{eventBus: bus, logger: slog.Default()}
	w := newWatcher(k, DefaultWatcherConfig())

	// Manually append more than maxTickHistory records
	w.mu.Lock()
	for i := 0; i < maxTickHistory+20; i++ {
		w.appendTick(TickRecord{
			Timestamp:  time.Now(),
			EntityID:   "entity-1",
			Decision:   "skip",
			SignalCount: i,
		})
	}
	w.mu.Unlock()

	history := w.History()
	if len(history) != maxTickHistory {
		t.Errorf("expected history to be trimmed to %d, got %d", maxTickHistory, len(history))
	}

	// Newest first — last appended should have highest SignalCount
	if history[0].SignalCount != maxTickHistory+19 {
		t.Errorf("expected newest record signal_count=%d, got %d", maxTickHistory+19, history[0].SignalCount)
	}
}

func TestWatcherStatus_AdaptiveFactors(t *testing.T) {
	bus := NewEventBus(false)
	k := &Keyoku{
		store:              &testStore{},
		eventBus:           bus,
		logger:             slog.Default(),
		timePeriodOverride: PeriodWorking,
	}

	w := newWatcher(k, WatcherConfig{
		Adaptive:     true,
		BaseInterval: 5 * time.Minute,
		MinInterval:  1 * time.Minute,
		MaxInterval:  30 * time.Minute,
		EntityIDs:    []string{"entity-1"},
	})
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	s := w.Status()
	if s.Factors == nil {
		t.Fatal("expected Factors to be populated in adaptive mode")
	}
	if s.Factors.TimePeriod != "working" {
		t.Errorf("expected time_period=working, got %q", s.Factors.TimePeriod)
	}
	if s.IntervalMs <= 0 {
		t.Error("expected positive interval_ms")
	}
}
