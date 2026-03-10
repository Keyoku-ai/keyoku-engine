// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventBus_On(t *testing.T) {
	bus := NewEventBus(false) // synchronous for testing

	var received []Event
	bus.On(EventMemoryCreated, func(e Event) {
		received = append(received, e)
	})

	bus.Emit(Event{
		Type:     EventMemoryCreated,
		EntityID: "user-1",
		AgentID:  "agent-1",
		Data:     map[string]any{"content": "test memory"},
	})

	// Should not fire for different event type
	bus.Emit(Event{
		Type:     EventMemoryDeleted,
		EntityID: "user-1",
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].EntityID != "user-1" {
		t.Errorf("expected entityID 'user-1', got %q", received[0].EntityID)
	}
	if received[0].Data["content"] != "test memory" {
		t.Errorf("expected content 'test memory', got %v", received[0].Data["content"])
	}
}

func TestEventBus_OnAny(t *testing.T) {
	bus := NewEventBus(false)

	var count int
	bus.OnAny(func(e Event) {
		count++
	})

	bus.Emit(Event{Type: EventMemoryCreated})
	bus.Emit(Event{Type: EventMemoryDeleted})
	bus.Emit(Event{Type: EventConflictDetected})

	if count != 3 {
		t.Fatalf("expected 3 events, got %d", count)
	}
}

func TestEventBus_MultipleHandlers(t *testing.T) {
	bus := NewEventBus(false)

	var count1, count2 int
	bus.On(EventMemoryCreated, func(e Event) { count1++ })
	bus.On(EventMemoryCreated, func(e Event) { count2++ })

	bus.Emit(Event{Type: EventMemoryCreated})

	if count1 != 1 || count2 != 1 {
		t.Fatalf("expected both handlers called, got %d and %d", count1, count2)
	}
}

func TestEventBus_TimestampAutoSet(t *testing.T) {
	bus := NewEventBus(false)

	var received Event
	bus.On(EventMemoryCreated, func(e Event) {
		received = e
	})

	bus.Emit(Event{Type: EventMemoryCreated})

	if received.Timestamp.IsZero() {
		t.Error("expected timestamp to be auto-set")
	}
}

func TestEventBus_AsyncMode(t *testing.T) {
	bus := NewEventBus(true) // async

	var called atomic.Int32
	bus.On(EventMemoryCreated, func(e Event) {
		called.Add(1)
	})

	bus.Emit(Event{Type: EventMemoryCreated})

	// Wait a bit for async handler
	time.Sleep(50 * time.Millisecond)

	if called.Load() != 1 {
		t.Fatalf("expected async handler to be called, got %d", called.Load())
	}
}

func TestEventBus_ConcurrentEmit(t *testing.T) {
	bus := NewEventBus(true)

	var count atomic.Int32
	bus.On(EventMemoryCreated, func(e Event) {
		count.Add(1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit(Event{Type: EventMemoryCreated})
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	if count.Load() != 100 {
		t.Fatalf("expected 100 events, got %d", count.Load())
	}
}

func TestEmitterFunc_TranslatesRawEvents(t *testing.T) {
	bus := NewEventBus(false)

	var received Event
	bus.On(EventMemoryCreated, func(e Event) {
		received = e
	})

	fn := bus.emitterFunc()
	fn("memory.created", "entity-1", "agent-1", "", map[string]any{
		"content": "hello world",
	})

	if received.Type != EventMemoryCreated {
		t.Errorf("expected type %q, got %q", EventMemoryCreated, received.Type)
	}
	if received.EntityID != "entity-1" {
		t.Errorf("expected entityID 'entity-1', got %q", received.EntityID)
	}
	if received.AgentID != "agent-1" {
		t.Errorf("expected agentID 'agent-1', got %q", received.AgentID)
	}
}
