// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"sync"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// EventType represents the type of event emitted by Keyoku.
type EventType string

const (
	// Memory lifecycle events — fired from engine.Add() pipeline
	EventMemoryCreated EventType = "memory.created"
	EventMemoryUpdated EventType = "memory.updated"
	EventMemoryDeleted EventType = "memory.deleted"
	EventMemoriesMerged EventType = "memory.merged"

	// Conflict events — fired when contradictions are detected
	EventConflictDetected EventType = "conflict.detected"

	// Entity/relationship events — fired when knowledge graph changes
	EventEntityExtracted      EventType = "entity.extracted"
	EventRelationshipDetected EventType = "relationship.detected"

	// Importance events — fired when importance is re-evaluated
	EventImportanceChanged EventType = "importance.changed"

	// Agent state events — fired from state machine updates
	EventStateChanged EventType = "state.changed"

	// Heartbeat signal events — fired from proactive watcher
	EventHeartbeatSignal      EventType = "heartbeat.signal"
	EventDeadlineApproaching  EventType = "heartbeat.deadline"
	EventScheduledTaskDue     EventType = "heartbeat.scheduled"
	EventMemoryDecaying       EventType = "heartbeat.decaying"
	EventStaleMonitor         EventType = "heartbeat.stale_monitor"
	EventConflictUnresolved   EventType = "heartbeat.conflict_unresolved"
	EventPendingWork          EventType = "heartbeat.pending_work"

	// Team events — fired from team operations and team heartbeat
	EventTeamHeartbeatSignal EventType = "heartbeat.team_signal"
	EventTeamMemoryShared    EventType = "team.memory_shared"
	EventTeamMemberAdded     EventType = "team.member_added"
	EventTeamMemberRemoved   EventType = "team.member_removed"

	// Job events — fired from background scheduler
	EventJobCompleted EventType = "job.completed"
	EventJobFailed    EventType = "job.failed"
)

// Event is the payload delivered to event handlers.
type Event struct {
	Type      EventType
	EntityID  string
	AgentID   string
	TeamID    string
	Memory    *Memory
	Data      map[string]any
	Timestamp time.Time
}

// EventHandler is a callback function for events.
type EventHandler func(Event)

// EventBus manages event subscriptions and dispatching.
type EventBus struct {
	mu          sync.RWMutex
	handlers    map[EventType][]EventHandler
	anyHandlers []EventHandler
	async       bool // if true, handlers fire in goroutines (non-blocking)
}

// NewEventBus creates a new event bus.
// If async is true, handlers are invoked in separate goroutines so they never block the hot path.
func NewEventBus(async bool) *EventBus {
	return &EventBus{
		handlers: make(map[EventType][]EventHandler),
		async:    async,
	}
}

// On registers a handler for a specific event type.
func (eb *EventBus) On(eventType EventType, handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
}

// OnAny registers a handler that fires for all events.
func (eb *EventBus) OnAny(handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.anyHandlers = append(eb.anyHandlers, handler)
}

// Emit dispatches an event to all registered handlers.
func (eb *EventBus) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	eb.mu.RLock()
	typed := eb.handlers[event.Type]
	any := eb.anyHandlers
	eb.mu.RUnlock()

	for _, h := range typed {
		if eb.async {
			go h(event)
		} else {
			h(event)
		}
	}
	for _, h := range any {
		if eb.async {
			go h(event)
		} else {
			h(event)
		}
	}
}

// emitterFunc returns the engine-compatible callback that translates raw event data into typed Events.
func (eb *EventBus) emitterFunc() func(eventType string, entityID string, agentID string, teamID string, data map[string]any) {
	return func(eventType string, entityID string, agentID string, teamID string, data map[string]any) {
		event := Event{
			Type:      EventType(eventType),
			EntityID:  entityID,
			AgentID:   agentID,
			TeamID:    teamID,
			Data:      data,
			Timestamp: time.Now(),
		}

		// Extract memory from data if present
		if mem, ok := data["memory"]; ok {
			if m, ok := mem.(*storage.Memory); ok {
				event.Memory = m
			}
		}

		eb.Emit(event)
	}
}
