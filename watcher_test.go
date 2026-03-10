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
		store:    store,
		eventBus: bus,
		logger:   slog.Default(),
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
