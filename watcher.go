package keyoku

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// WatcherConfig configures the proactive heartbeat watcher.
type WatcherConfig struct {
	// Interval between heartbeat checks (default: 10s).
	Interval time.Duration

	// EntityIDs to watch. If empty, the watcher does nothing until entities are added.
	EntityIDs []string

	// Heartbeat options applied to every check.
	HeartbeatOpts []HeartbeatOption
}

// DefaultWatcherConfig returns a default watcher configuration.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		Interval: 10 * time.Second,
	}
}

// Watcher is a background goroutine that proactively runs HeartbeatCheck
// and emits events when action is needed. Instead of polling, consumers
// register event handlers and get pushed signals in real time.
type Watcher struct {
	keyoku *Keyoku
	config WatcherConfig
	logger *slog.Logger

	mu        sync.RWMutex
	entityIDs map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newWatcher creates a new proactive watcher (not started yet).
func newWatcher(k *Keyoku, config WatcherConfig) *Watcher {
	ctx, cancel := context.WithCancel(context.Background())

	entityMap := make(map[string]bool)
	for _, id := range config.EntityIDs {
		entityMap[id] = true
	}

	if config.Interval <= 0 {
		config.Interval = 10 * time.Second
	}

	return &Watcher{
		keyoku:    k,
		config:    config,
		logger:    k.logger.With("component", "watcher"),
		entityIDs: entityMap,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start begins the background heartbeat loop.
func (w *Watcher) Start() {
	w.wg.Add(1)
	go w.run()
	w.logger.Info("watcher started", "interval", w.config.Interval)
}

// Stop gracefully stops the watcher.
func (w *Watcher) Stop() {
	w.cancel()
	w.wg.Wait()
	w.logger.Info("watcher stopped")
}

// Watch adds an entity ID to the watch list.
func (w *Watcher) Watch(entityID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entityIDs[entityID] = true
}

// Unwatch removes an entity ID from the watch list.
func (w *Watcher) Unwatch(entityID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.entityIDs, entityID)
}

// WatchedEntities returns the current list of watched entity IDs.
func (w *Watcher) WatchedEntities() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	ids := make([]string, 0, len(w.entityIDs))
	for id := range w.entityIDs {
		ids = append(ids, id)
	}
	return ids
}

func (w *Watcher) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

func (w *Watcher) checkAll() {
	w.mu.RLock()
	ids := make([]string, 0, len(w.entityIDs))
	for id := range w.entityIDs {
		ids = append(ids, id)
	}
	w.mu.RUnlock()

	for _, entityID := range ids {
		// Respect context cancellation
		if w.ctx.Err() != nil {
			return
		}

		result, err := w.keyoku.HeartbeatCheck(w.ctx, entityID, w.config.HeartbeatOpts...)
		if err != nil {
			w.logger.Debug("heartbeat check failed", "entity", entityID, "error", err)
			continue
		}

		if !result.ShouldAct {
			continue
		}

		// Emit granular events for each category
		w.emitHeartbeatEvents(entityID, result)
	}
}

func (w *Watcher) emitHeartbeatEvents(entityID string, result *HeartbeatResult) {
	bus := w.keyoku.eventBus

	// Emit the umbrella signal first
	bus.Emit(Event{
		Type:     EventHeartbeatSignal,
		EntityID: entityID,
		Data: map[string]any{
			"summary":         result.Summary,
			"pending_work":    len(result.PendingWork),
			"deadlines":       len(result.Deadlines),
			"scheduled":       len(result.Scheduled),
			"decaying":        len(result.Decaying),
			"conflicts":       len(result.Conflicts),
			"stale_monitors":  len(result.StaleMonitors),
			"priority_action": result.PriorityAction,
			"urgency":         result.Urgency,
		},
	})

	// Emit per-category events so consumers can subscribe to specific signals
	for _, m := range result.PendingWork {
		bus.Emit(Event{
			Type:     EventPendingWork,
			EntityID: entityID,
			AgentID:  m.AgentID,
			Memory:   m,
			Data: map[string]any{
				"content":    m.Content,
				"type":       string(m.Type),
				"importance": m.Importance,
			},
		})
	}

	for _, m := range result.Deadlines {
		bus.Emit(Event{
			Type:     EventDeadlineApproaching,
			EntityID: entityID,
			AgentID:  m.AgentID,
			Memory:   m,
			Data: map[string]any{
				"content":    m.Content,
				"expires_at": m.ExpiresAt,
				"remaining":  time.Until(*m.ExpiresAt).String(),
			},
		})
	}

	for _, m := range result.Scheduled {
		bus.Emit(Event{
			Type:     EventScheduledTaskDue,
			EntityID: entityID,
			AgentID:  m.AgentID,
			Memory:   m,
			Data: map[string]any{
				"content": m.Content,
				"tags":    m.Tags,
			},
		})
	}

	for _, m := range result.Decaying {
		bus.Emit(Event{
			Type:     EventMemoryDecaying,
			EntityID: entityID,
			AgentID:  m.AgentID,
			Memory:   m,
			Data: map[string]any{
				"content":    m.Content,
				"importance": m.Importance,
			},
		})
	}

	for _, c := range result.Conflicts {
		bus.Emit(Event{
			Type:     EventConflictUnresolved,
			EntityID: entityID,
			Memory:   c.MemoryA,
			Data: map[string]any{
				"reason": c.Reason,
			},
		})
	}

	for _, m := range result.StaleMonitors {
		bus.Emit(Event{
			Type:     EventStaleMonitor,
			EntityID: entityID,
			AgentID:  m.AgentID,
			Memory:   m,
			Data: map[string]any{
				"content": m.Content,
			},
		})
	}
}
