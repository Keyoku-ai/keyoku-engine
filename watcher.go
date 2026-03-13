// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// WatcherConfig configures the proactive heartbeat watcher.
type WatcherConfig struct {
	// Interval between heartbeat checks (default: 5m).
	// Used in fixed mode (Adaptive=false).
	Interval time.Duration

	// Adaptive enables dynamic tick interval based on signal density,
	// time of day, memory velocity, and recency of last action.
	// When false (default), uses fixed Interval for backward compat.
	Adaptive bool

	// BaseInterval is the starting interval for adaptive mode (default: 5m).
	BaseInterval time.Duration

	// MinInterval is the floor for adaptive mode (default: 1m).
	MinInterval time.Duration

	// MaxInterval is the ceiling for adaptive mode (default: 30m).
	MaxInterval time.Duration

	// Delivery configures external delivery (e.g. via OpenClaw CLI).
	// nil = event-only mode (backward compatible).
	Delivery *DeliveryConfig

	// EntityIDs to watch. If empty, the watcher does nothing until entities are added.
	EntityIDs []string

	// TeamIDs to run team heartbeats for. Each team+entity pair gets a separate team heartbeat.
	TeamIDs []string

	// Heartbeat options applied to every check.
	HeartbeatOpts []HeartbeatOption
}

// IntervalFactors describes each factor that influenced the tick interval.
type IntervalFactors struct {
	BaseMs         int64   `json:"base_ms"`
	FinalMs        int64   `json:"final_ms"`
	TimePeriod     string  `json:"time_period"`
	TimePeriodMult float64 `json:"time_period_mult"`
	RecentActMult  float64 `json:"recent_act_mult"`
	RecentActAgoMs *int64  `json:"recent_act_ago_ms,omitempty"`
	SignalCount    int     `json:"signal_count"`
	SignalMult     float64 `json:"signal_mult"`
	VelocityHigh   bool    `json:"velocity_high"`
	VelocityMult   float64 `json:"velocity_mult"`
	Clamped        string  `json:"clamped,omitempty"` // "min", "max", or ""
}

// WatcherStatus is the full status snapshot returned by Status().
type WatcherStatus struct {
	Running       bool            `json:"running"`
	Adaptive      bool            `json:"adaptive"`
	NextTickAt    *time.Time      `json:"next_tick_at,omitempty"`
	IntervalMs    int64           `json:"current_interval_ms"`
	BaseMs        int64           `json:"base_interval_ms"`
	MinMs         int64           `json:"min_interval_ms"`
	MaxMs         int64           `json:"max_interval_ms"`
	Factors       *IntervalFactors `json:"factors,omitempty"`
	LastActAt     *time.Time      `json:"last_act_at,omitempty"`
	LastDecision  string          `json:"last_decision"`
	LastCheckAt   *time.Time      `json:"last_check_at,omitempty"`
	Entities      []string        `json:"entities"`
}

// DefaultWatcherConfig returns a default watcher configuration.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		Interval: 5 * time.Minute,
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
	teamIDs   map[string]bool

	// Delivery
	deliverer Deliverer

	// Adaptive interval state
	lastResult   *HeartbeatResult
	lastActTime  time.Time
	nextTickAt   time.Time
	lastFactors  *IntervalFactors
	lastCheckAt  time.Time
	lastDecision string

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

	teamMap := make(map[string]bool)
	for _, id := range config.TeamIDs {
		teamMap[id] = true
	}

	if config.Interval <= 0 {
		config.Interval = 5 * time.Minute
	}

	w := &Watcher{
		keyoku:    k,
		config:    config,
		logger:    k.logger.With("component", "watcher"),
		entityIDs: entityMap,
		teamIDs:   teamMap,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Wire up deliverer if configured
	if config.Delivery != nil {
		w.deliverer = NewDeliverer(*config.Delivery)
		if cli, ok := w.deliverer.(*CLIDeliverer); ok {
			cli.SetLogger(w.logger)
		}
	}

	return w
}

// Start begins the background heartbeat loop.
func (w *Watcher) Start() {
	w.wg.Add(1)
	go w.run()
	if w.config.Adaptive {
		w.logger.Info("watcher started",
			"mode", "adaptive",
			"base", w.config.BaseInterval,
			"min", w.config.MinInterval,
			"max", w.config.MaxInterval,
			"delivery", w.config.Delivery != nil,
		)
	} else {
		w.logger.Info("watcher started", "mode", "fixed", "interval", w.config.Interval)
	}
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

// WatchTeam adds a team ID to the watch list for team heartbeats.
func (w *Watcher) WatchTeam(teamID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.teamIDs[teamID] = true
}

// UnwatchTeam removes a team ID from the watch list.
func (w *Watcher) UnwatchTeam(teamID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.teamIDs, teamID)
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

// Status returns a full snapshot of the watcher's current state.
func (w *Watcher) Status() WatcherStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	entities := make([]string, 0, len(w.entityIDs))
	for id := range w.entityIDs {
		entities = append(entities, id)
	}

	base := w.config.BaseInterval
	if base <= 0 {
		base = 5 * time.Minute
	}
	minI := w.config.MinInterval
	if minI <= 0 {
		minI = 1 * time.Minute
	}
	maxI := w.config.MaxInterval
	if maxI <= 0 {
		maxI = 30 * time.Minute
	}

	s := WatcherStatus{
		Running:  true,
		Adaptive: w.config.Adaptive,
		BaseMs:   base.Milliseconds(),
		MinMs:    minI.Milliseconds(),
		MaxMs:    maxI.Milliseconds(),
		Factors:  w.lastFactors,
		Entities: entities,
	}

	if w.lastFactors != nil {
		s.IntervalMs = w.lastFactors.FinalMs
	} else if w.config.Adaptive {
		s.IntervalMs = base.Milliseconds()
	} else {
		s.IntervalMs = w.config.Interval.Milliseconds()
	}

	if !w.nextTickAt.IsZero() {
		t := w.nextTickAt
		s.NextTickAt = &t
	}
	if !w.lastActTime.IsZero() {
		t := w.lastActTime
		s.LastActAt = &t
	}
	if !w.lastCheckAt.IsZero() {
		t := w.lastCheckAt
		s.LastCheckAt = &t
	}

	s.LastDecision = w.lastDecision
	if s.LastDecision == "" {
		s.LastDecision = "none"
	}

	return s
}

func (w *Watcher) run() {
	defer w.wg.Done()

	if !w.config.Adaptive {
		// Legacy fixed-interval mode (backward compat)
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

	// Adaptive mode: compute interval dynamically after each tick
	for {
		interval := w.computeNextInterval()
		w.logger.Debug("adaptive tick scheduled", "interval", interval)
		timer := time.NewTimer(interval)
		select {
		case <-w.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			w.checkAll()
		}
	}
}

// computeNextInterval calculates the dynamic interval based on signal state.
func (w *Watcher) computeNextInterval() time.Duration {
	base := w.config.BaseInterval
	if base <= 0 {
		base = 5 * time.Minute
	}

	f := &IntervalFactors{
		BaseMs:         base.Milliseconds(),
		RecentActMult:  1.0,
		TimePeriodMult: 1.0,
		SignalMult:     1.0,
		VelocityMult:   1.0,
	}

	interval := float64(base)

	// Factor 1: Time since last action — check sooner after acting for follow-up
	w.mu.RLock()
	lastAct := w.lastActTime
	lastResult := w.lastResult
	w.mu.RUnlock()

	if !lastAct.IsZero() {
		sinceAct := time.Since(lastAct)
		agoMs := sinceAct.Milliseconds()
		f.RecentActAgoMs = &agoMs
		if sinceAct < 10*time.Minute {
			f.RecentActMult = 0.5
			interval *= 0.5
		}
	}

	// Factor 2: Time of day
	period := w.keyoku.currentTimePeriod()
	mult := timePeriodCooldownMultiplier(period)
	f.TimePeriod = string(period)
	f.TimePeriodMult = mult
	interval *= mult

	// Factor 3: Signal density from last check
	if lastResult != nil {
		signalCount := countSignals(lastResult)
		f.SignalCount = signalCount
		if signalCount > 5 {
			f.SignalMult = 0.5
			interval *= 0.5
		} else if signalCount == 0 {
			f.SignalMult = 1.5
			interval *= 1.5
		}
	}

	// Factor 4: Memory velocity
	if lastResult != nil && lastResult.MemoryVelocityHigh {
		f.VelocityHigh = true
		f.VelocityMult = 0.5
		interval *= 0.5
	}

	// Clamp to [min, max]
	minInterval := w.config.MinInterval
	if minInterval <= 0 {
		minInterval = 1 * time.Minute
	}
	maxInterval := w.config.MaxInterval
	if maxInterval <= 0 {
		maxInterval = 30 * time.Minute
	}

	result := time.Duration(interval)
	if result < minInterval {
		f.Clamped = "min"
		result = minInterval
	}
	if result > maxInterval {
		f.Clamped = "max"
		result = maxInterval
	}

	f.FinalMs = result.Milliseconds()

	// Store factors and next tick time
	now := time.Now()
	nextTick := now.Add(result)
	w.mu.Lock()
	w.lastFactors = f
	w.nextTickAt = nextTick
	w.mu.Unlock()

	return result
}

// countSignals returns the total number of active signals in a result.
func countSignals(r *HeartbeatResult) int {
	n := len(r.PendingWork) + len(r.Deadlines) + len(r.Scheduled) +
		len(r.Decaying) + len(r.Conflicts) + len(r.StaleMonitors) +
		len(r.GoalProgress) + len(r.Relationships) + len(r.KnowledgeGaps) +
		len(r.Patterns)
	if r.Continuity != nil {
		n++
	}
	if r.Sentiment != nil {
		n++
	}
	return n
}

func (w *Watcher) checkAll() {
	w.mu.RLock()
	ids := make([]string, 0, len(w.entityIDs))
	for id := range w.entityIDs {
		ids = append(ids, id)
	}
	teamIDs := make([]string, 0, len(w.teamIDs))
	for id := range w.teamIDs {
		teamIDs = append(teamIDs, id)
	}
	w.mu.RUnlock()

	// Per-entity heartbeats (agent-level)
	for _, entityID := range ids {
		if w.ctx.Err() != nil {
			return
		}

		result, err := w.keyoku.HeartbeatCheck(w.ctx, entityID, w.config.HeartbeatOpts...)
		if err != nil {
			w.logger.Debug("heartbeat check failed", "entity", entityID, "error", err)
			continue
		}

		// Cache last result for adaptive interval computation
		w.mu.Lock()
		w.lastResult = result
		w.lastCheckAt = time.Now()
		if result.ShouldAct {
			w.lastDecision = "act"
		} else {
			w.lastDecision = "skip"
		}
		w.mu.Unlock()

		if !result.ShouldAct {
			continue
		}

		w.emitHeartbeatEvents(entityID, result)

		// Deliver via external agent if configured
		if w.deliverer != nil {
			w.mu.Lock()
			w.lastActTime = time.Now()
			w.mu.Unlock()
			if err := w.deliverer.Deliver(w.ctx, entityID, result); err != nil {
				w.logger.Error("heartbeat delivery failed", "entity", entityID, "error", err)
			}
		}
	}

	// Team heartbeats — run for each team+entity combination
	if len(teamIDs) > 0 {
		for _, teamID := range teamIDs {
			for _, entityID := range ids {
				if w.ctx.Err() != nil {
					return
				}

				teamOpts := append(w.config.HeartbeatOpts, WithTeamHeartbeat(teamID))
				result, err := w.keyoku.HeartbeatCheck(w.ctx, entityID, teamOpts...)
				if err != nil {
					w.logger.Debug("team heartbeat check failed", "team", teamID, "entity", entityID, "error", err)
					continue
				}

				if !result.ShouldAct {
					continue
				}

				w.emitTeamHeartbeatEvents(entityID, teamID, result)
			}
		}
	}
}

func (w *Watcher) emitHeartbeatEvents(entityID string, result *HeartbeatResult) {
	bus := w.keyoku.eventBus

	// Emit the umbrella signal first
	bus.Emit(Event{
		Type:     EventHeartbeatSignal,
		EntityID: entityID,
		Data: map[string]any{
			"should_act":      true,
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
				"memory_id":  m.ID,
				"content":    m.Content,
				"type":       string(m.Type),
				"importance": m.Importance,
				"should_act": true,
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
				"memory_id":  m.ID,
				"content":    m.Content,
				"expires_at": m.ExpiresAt,
				"remaining":  time.Until(*m.ExpiresAt).String(),
				"should_act": true,
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
				"memory_id":  m.ID,
				"content":    m.Content,
				"tags":       m.Tags,
				"should_act": true,
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
				"memory_id":  m.ID,
				"content":    m.Content,
				"importance": m.Importance,
				"should_act": true,
			},
		})
	}

	for _, c := range result.Conflicts {
		bus.Emit(Event{
			Type:     EventConflictUnresolved,
			EntityID: entityID,
			Memory:   c.MemoryA,
			Data: map[string]any{
				"memory_id":  c.MemoryA.ID,
				"reason":     c.Reason,
				"should_act": true,
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
				"memory_id":  m.ID,
				"content":    m.Content,
				"should_act": true,
			},
		})
	}
}

func (w *Watcher) emitTeamHeartbeatEvents(entityID, teamID string, result *HeartbeatResult) {
	bus := w.keyoku.eventBus

	bus.Emit(Event{
		Type:     EventTeamHeartbeatSignal,
		EntityID: entityID,
		TeamID:   teamID,
		Data: map[string]any{
			"summary":        result.Summary,
			"pending_work":   len(result.PendingWork),
			"deadlines":      len(result.Deadlines),
			"scheduled":      len(result.Scheduled),
			"decaying":       len(result.Decaying),
			"conflicts":      len(result.Conflicts),
			"stale_monitors": len(result.StaleMonitors),
			"by_agent":       result.ByAgent,
		},
	})
}
