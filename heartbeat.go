// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// HeartbeatResult contains the result of a HeartbeatCheck.
type HeartbeatResult struct {
	ShouldAct     bool
	PendingWork   []*Memory       // Active plans/tasks needing attention
	Deadlines     []*Memory       // Approaching expiry
	Scheduled     []*Memory       // Cron-tagged memories that are due
	Decaying      []*Memory       // Important memories nearing decay threshold
	Conflicts     []ConflictPair  // Unresolved contradictions
	StaleMonitors []*Memory       // Monitoring tasks overdue for check
	Summary       string          // Pre-built context string for LLM

	// Extended heartbeat signals (populated by new check types)
	GoalProgress  []GoalProgressItem  // Plan vs activity cross-reference
	Continuity    *ContinuityItem     // Interrupted session detection
	Sentiment     *SentimentTrend     // Emotional trend analysis
	Relationships []RelationshipAlert // Silent entities near deadlines
	KnowledgeGaps []KnowledgeGap      // Unanswered questions
	Patterns      []BehavioralPattern // Day-of-week behavioral patterns

	// LLM prioritization fields (populated only when WithLLMPrioritization is set)
	PriorityAction string   // The single most important action
	ActionItems    []string // All items ordered by priority
	Urgency        string   // "immediate", "soon", "can_wait"

	// Intelligent heartbeat decision metadata
	DecisionReason     string // "act", "nudge", "suppress_cooldown", "suppress_stale", "suppress_quiet"
	HighestUrgencyTier string // "immediate", "elevated", "normal", "low"
	SignalFingerprint  string // for debugging
	NudgeContext       string // memory content selected for nudge

	// Team heartbeat fields (populated only in team heartbeat mode)
	ByAgent map[string]*AgentHeartbeatSummary // per-agent breakdown (team mode only)
}

// --- Signal Urgency Tiers ---

const (
	TierImmediate = "immediate" // Scheduled (cron due), Deadlines — 0 cooldown
	TierElevated  = "elevated"  // Conflicts, Continuity, StaleMonitors — 1h cooldown
	TierNormal    = "normal"    // PendingWork, GoalProgress, KnowledgeGaps — 2h cooldown
	TierLow       = "low"       // Decaying, Sentiment, Relationships, Patterns — 4h cooldown
)

// signalTierMap maps check types to urgency tiers.
var signalTierMap = map[HeartbeatCheckType]string{
	CheckScheduled:    TierImmediate,
	CheckDeadlines:    TierImmediate,
	CheckConflicts:    TierElevated,
	CheckContinuity:   TierElevated,
	CheckStale:        TierElevated,
	CheckPendingWork:  TierNormal,
	CheckGoalProgress: TierNormal,
	CheckKnowledge:    TierNormal,
	CheckDecaying:     TierLow,
	CheckSentiment:    TierLow,
	CheckRelationship: TierLow,
	CheckPatterns:     TierLow,
}

// tierPriority for comparison (lower = more urgent).
var tierPriority = map[string]int{
	TierImmediate: 0,
	TierElevated:  1,
	TierNormal:    2,
	TierLow:       3,
}

// HeartbeatParams holds configurable parameters for heartbeat evaluation.
// Defaults are set per autonomy level; individual fields can be overridden.
type HeartbeatParams struct {
	SignalCooldownNormal time.Duration
	SignalCooldownLow    time.Duration
	NudgeAfterSilence    time.Duration // 0 = disabled
	MaxNudgesPerDay      int           // safety cap per 24h
	NudgeMaxInterval     time.Duration // cap for backoff decay (e.g. 48h)
}

// DefaultHeartbeatParams returns defaults based on autonomy level.
func DefaultHeartbeatParams(autonomy string) HeartbeatParams {
	switch autonomy {
	case "observe":
		return HeartbeatParams{
			SignalCooldownNormal: 4 * time.Hour,
			SignalCooldownLow:    8 * time.Hour,
			NudgeAfterSilence:    0, // disabled
			MaxNudgesPerDay:      0,
			NudgeMaxInterval: 12 * time.Hour,
		}
	case "act":
		return HeartbeatParams{
			SignalCooldownNormal: 1 * time.Hour,
			SignalCooldownLow:    2 * time.Hour,
			NudgeAfterSilence:    2 * time.Hour,
			MaxNudgesPerDay:      6,
			NudgeMaxInterval: 48 * time.Hour,
		}
	default: // "suggest"
		return HeartbeatParams{
			SignalCooldownNormal: 2 * time.Hour,
			SignalCooldownLow:    4 * time.Hour,
			NudgeAfterSilence:    4 * time.Hour,
			MaxNudgesPerDay:      3,
			NudgeMaxInterval: 24 * time.Hour,
		}
	}
}

// --- Active Hours Detection ---

// ActiveHours represents the inferred quiet window for an entity.
type ActiveHours struct {
	QuietStart int       // UTC hour when quiet period begins
	QuietEnd   int       // UTC hour when quiet period ends
	ComputedAt time.Time
	HasData    bool      // false if insufficient messages to infer
}

// activeHoursCache stores computed active hours per entity.
var activeHoursCache = struct {
	sync.RWMutex
	m map[string]*ActiveHours
}{m: make(map[string]*ActiveHours)}

// getActiveHours returns cached active hours or computes them.
func getActiveHours(ctx context.Context, store storage.Store, entityID string) *ActiveHours {
	activeHoursCache.RLock()
	cached, ok := activeHoursCache.m[entityID]
	activeHoursCache.RUnlock()

	if ok && time.Since(cached.ComputedAt) < 24*time.Hour {
		return cached
	}

	ah := computeActiveHours(ctx, store, entityID)

	activeHoursCache.Lock()
	activeHoursCache.m[entityID] = ah
	activeHoursCache.Unlock()

	return ah
}

// computeActiveHours analyzes message timestamps to find quiet hours.
func computeActiveHours(ctx context.Context, store storage.Store, entityID string) *ActiveHours {
	dist, err := store.GetMessageHourDistribution(ctx, entityID, 14)
	if err != nil {
		return &ActiveHours{ComputedAt: time.Now(), HasData: false}
	}

	// Need minimum 20 messages for reliable inference
	total := 0
	for _, count := range dist {
		total += count
	}
	if total < 20 {
		return &ActiveHours{ComputedAt: time.Now(), HasData: false}
	}

	// Find the 6-hour contiguous window with least activity (quiet window)
	// Use circular sliding window over 24 hours
	minSum := total + 1
	bestStart := 0
	windowSize := 6 // 6 hours of quiet time

	for start := 0; start < 24; start++ {
		sum := 0
		for i := 0; i < windowSize; i++ {
			h := (start + i) % 24
			sum += dist[h]
		}
		if sum < minSum {
			minSum = sum
			bestStart = start
		}
	}

	return &ActiveHours{
		QuietStart: bestStart,
		QuietEnd:   (bestStart + windowSize) % 24,
		ComputedAt: time.Now(),
		HasData:    true,
	}
}

// isQuietHour checks if the current time falls within the quiet window.
func (ah *ActiveHours) isQuietHour() bool {
	if !ah.HasData {
		return false // not enough data, assume always active
	}
	hour := time.Now().UTC().Hour()
	if ah.QuietStart < ah.QuietEnd {
		return hour >= ah.QuietStart && hour < ah.QuietEnd
	}
	// Wraps midnight (e.g., 22:00 - 06:00)
	return hour >= ah.QuietStart || hour < ah.QuietEnd
}

// GoalProgressItem tracks a plan's progress based on related activity memories.
type GoalProgressItem struct {
	Plan       *Memory
	Activities []*Memory
	Progress   float64 // 0.0-1.0 heuristic based on activity count weighted by recency
	DaysLeft   float64 // days until plan expires (-1 if no expiry)
	Status     string  // "on_track", "at_risk", "stalled", "no_activity"
}

// ContinuityItem detects interrupted sessions for resume suggestions.
type ContinuityItem struct {
	LastSessionMemories []*Memory
	SessionAge          time.Duration
	WasInterrupted      bool
	ResumeSuggestion    string
}

// SentimentTrend analyzes emotional direction across recent memories.
type SentimentTrend struct {
	RecentAvg   float64   // avg sentiment of most recent memories
	PreviousAvg float64   // avg sentiment of earlier memories
	Direction   string    // "improving", "declining", "stable"
	Delta       float64   // absolute change
	Notable     []*Memory // memories with extreme sentiment (|s| > 0.7)
}

// RelationshipAlert flags entities that have gone silent but have active plans.
type RelationshipAlert struct {
	Entity       *storage.Entity
	LastMentioned time.Time
	DaysSilent    int
	RelatedPlans  []*Memory
	Urgency       string // "info", "attention", "urgent"
}

// KnowledgeGap represents an unanswered question found in memories.
type KnowledgeGap struct {
	Question   string
	AskedAt    time.Time
	HasRelated bool
}

// AgentHeartbeatSummary contains heartbeat counts attributed to a specific agent.
type AgentHeartbeatSummary struct {
	AgentID     string
	PendingWork int
	Deadlines   int
	Scheduled   int
	Decaying    int
	Conflicts   int
}

// ConflictPair re-exported from storage for public API.
type ConflictPair = storage.ConflictPair

// HeartbeatOption configures a HeartbeatCheck call.
type HeartbeatOption func(*heartbeatConfig)

type heartbeatConfig struct {
	deadlineWindow  time.Duration
	decayThreshold  float64
	importanceFloor float64
	maxResults      int
	agentID         string
	checks          []HeartbeatCheckType

	// Team heartbeat
	teamID        string // When set, runs team-wide heartbeat
	teamHeartbeat bool   // Enables team heartbeat mode

	// LLM prioritization (opt-in)
	llmProvider    llm.Provider
	agentContext   string
	entityContext  string

	// Intelligent heartbeat
	autonomy        string           // "observe", "suggest", "act"
	heartbeatParams *HeartbeatParams // optional overrides
}

// HeartbeatCheckType represents a specific check to run.
type HeartbeatCheckType string

const (
	CheckPendingWork  HeartbeatCheckType = "pending_work"
	CheckDeadlines    HeartbeatCheckType = "deadlines"
	CheckScheduled    HeartbeatCheckType = "scheduled"
	CheckDecaying     HeartbeatCheckType = "decaying"
	CheckConflicts    HeartbeatCheckType = "conflicts"
	CheckStale        HeartbeatCheckType = "stale_monitors"
	CheckGoalProgress HeartbeatCheckType = "goal_progress"
	CheckContinuity   HeartbeatCheckType = "continuity"
	CheckSentiment    HeartbeatCheckType = "sentiment"
	CheckRelationship HeartbeatCheckType = "relationship"
	CheckKnowledge    HeartbeatCheckType = "knowledge_gaps"
	CheckPatterns     HeartbeatCheckType = "patterns"
)

var allChecks = []HeartbeatCheckType{
	CheckPendingWork, CheckDeadlines, CheckScheduled,
	CheckDecaying, CheckConflicts, CheckStale,
	CheckGoalProgress, CheckContinuity, CheckSentiment,
	CheckRelationship, CheckKnowledge, CheckPatterns,
}

// WithDeadlineWindow sets how far ahead to look for deadlines (default: 24h).
func WithDeadlineWindow(d time.Duration) HeartbeatOption {
	return func(c *heartbeatConfig) { c.deadlineWindow = d }
}

// WithDecayThreshold sets the decay factor below which memories are flagged (default: 0.4).
func WithDecayThreshold(f float64) HeartbeatOption {
	return func(c *heartbeatConfig) { c.decayThreshold = f }
}

// WithImportanceFloor sets the minimum importance for flagging (default: 0.7).
func WithImportanceFloor(f float64) HeartbeatOption {
	return func(c *heartbeatConfig) { c.importanceFloor = f }
}

// WithMaxResults sets the maximum results per check category (default: 20).
func WithMaxResults(n int) HeartbeatOption {
	return func(c *heartbeatConfig) { c.maxResults = n }
}

// WithHeartbeatAgentID scopes the check to a specific agent.
func WithHeartbeatAgentID(id string) HeartbeatOption {
	return func(c *heartbeatConfig) { c.agentID = id }
}

// WithChecks enables only specific checks.
func WithChecks(checks ...HeartbeatCheckType) HeartbeatOption {
	return func(c *heartbeatConfig) { c.checks = checks }
}

// WithTeamHeartbeat enables team-wide heartbeat mode.
// Queries team-visible and global memories across all agents in the team.
// Results include ByAgent attribution showing which agent owns each signal.
func WithTeamHeartbeat(teamID string) HeartbeatOption {
	return func(c *heartbeatConfig) {
		c.teamID = teamID
		c.teamHeartbeat = true
	}
}

// WithAutonomy sets the autonomy level for heartbeat evaluation.
func WithAutonomy(autonomy string) HeartbeatOption {
	return func(c *heartbeatConfig) { c.autonomy = autonomy }
}

// WithHeartbeatParams sets optional parameter overrides for heartbeat evaluation.
func WithHeartbeatParams(params *HeartbeatParams) HeartbeatOption {
	return func(c *heartbeatConfig) { c.heartbeatParams = params }
}

// WithLLMPrioritization enables LLM-powered action prioritization on heartbeat results.
// Only fires when ShouldAct is true. The provider should be the same one used for memory extraction.
func WithLLMPrioritization(provider llm.Provider, agentContext, entityContext string) HeartbeatOption {
	return func(c *heartbeatConfig) {
		c.llmProvider = provider
		c.agentContext = agentContext
		c.entityContext = entityContext
	}
}

// HeartbeatCheck performs a zero-token local query against SQLite.
// Returns whether the agent should act and what needs attention.
func (k *Keyoku) HeartbeatCheck(ctx context.Context, entityID string, opts ...HeartbeatOption) (*HeartbeatResult, error) {
	cfg := &heartbeatConfig{
		deadlineWindow:  24 * time.Hour,
		decayThreshold:  0.4,
		importanceFloor: 0.7,
		maxResults:      20,
		checks:          allChecks,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build visibility context for team-aware queries
	var visibilityFor *storage.VisibilityContext
	if cfg.teamHeartbeat && cfg.teamID != "" {
		// Team heartbeat: see team + global memories (no private — agentID left empty)
		visibilityFor = &storage.VisibilityContext{TeamID: cfg.teamID}
	} else if cfg.agentID != "" {
		// Agent-level: auto-resolve team if agent belongs to one
		if teamID, err := k.store.GetTeamForAgent(ctx, cfg.agentID); err == nil && teamID != "" {
			visibilityFor = &storage.VisibilityContext{AgentID: cfg.agentID, TeamID: teamID}
		}
	}

	result := &HeartbeatResult{}
	checksToRun := make(map[HeartbeatCheckType]bool)
	for _, c := range cfg.checks {
		checksToRun[c] = true
	}

	// Helper to build a query with visibility applied
	buildQuery := func(q storage.MemoryQuery) storage.MemoryQuery {
		if visibilityFor != nil {
			q.VisibilityFor = visibilityFor
			// When using visibility filtering, don't also filter by agent_id
			// (VisibilityFor handles the scoping)
			q.AgentID = ""
		}
		return q
	}

	// 1. PENDING WORK — PLAN/ACTIVITY memories, active state, importance > threshold
	if checksToRun[CheckPendingWork] {
		pending, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			Types:      []storage.MemoryType{storage.TypePlan, storage.TypeActivity},
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      cfg.maxResults,
			OrderBy:    "importance",
			Descending: true,
		}))
		if err == nil {
			for _, m := range pending {
				if m.Importance >= cfg.importanceFloor {
					result.PendingWork = append(result.PendingWork, m)
				}
			}
		}
	}

	// 2. DEADLINES — Memories with expires_at approaching
	if checksToRun[CheckDeadlines] {
		allActive, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      cfg.maxResults * 5,
			OrderBy:    "importance",
			Descending: true,
		}))
		if err == nil {
			deadlineHorizon := time.Now().Add(cfg.deadlineWindow)
			for _, m := range allActive {
				if m.ExpiresAt != nil && m.ExpiresAt.Before(deadlineHorizon) {
					result.Deadlines = append(result.Deadlines, m)
					if len(result.Deadlines) >= cfg.maxResults {
						break
					}
				}
			}
		}
	}

	// 3. SCHEDULED (CRON) — Memories tagged "cron:*" where due
	if checksToRun[CheckScheduled] {
		allActive, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 5,
		}))
		if err == nil {
			now := time.Now()
			for _, m := range allActive {
				sched, err := ParseScheduleFromTags(m.Tags)
				if err != nil || sched == nil {
					continue
				}
				lastRun := m.CreatedAt
				if m.LastAccessedAt != nil {
					lastRun = *m.LastAccessedAt
				}
				if sched.IsDue(lastRun, now) {
					result.Scheduled = append(result.Scheduled, m)

					// Auto-acknowledge: advance last_accessed_at so the task
					// doesn't re-fire until the next scheduled interval.
					_ = k.store.UpdateAccessStats(ctx, []string{m.ID})

					// One-time cleanup: archive cron:once:* memories after they fire.
					if sched.Type == ScheduleOnce {
						archivedState := storage.StateArchived
						_, _ = k.store.UpdateMemory(ctx, m.ID, storage.MemoryUpdate{
							State: &archivedState,
						})
					}

					if len(result.Scheduled) >= cfg.maxResults {
						break
					}
				}
			}
		}
	}

	// 4. DECAYING ALERTS — Important memories with high decay
	if checksToRun[CheckDecaying] {
		stale, err := k.store.GetStaleMemories(ctx, entityID, cfg.decayThreshold)
		if err == nil {
			for _, m := range stale {
				if m.Importance >= 0.8 {
					// Apply visibility filter for team-aware heartbeat
					if visibilityFor != nil && !storage.IsVisibleTo(m.Visibility, m.AgentID, m.TeamID, visibilityFor) {
						continue
					}
					result.Decaying = append(result.Decaying, m)
					if len(result.Decaying) >= cfg.maxResults {
						break
					}
				}
			}
		}
	}

	// 5. CONFLICTS — Unresolved conflicts (flagged in confidence_factors)
	if checksToRun[CheckConflicts] {
		allActive, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 5,
		}))
		if err == nil {
			for _, m := range allActive {
				for _, factor := range m.ConfidenceFactors {
					if strings.HasPrefix(factor, "conflict_flagged:") {
						result.Conflicts = append(result.Conflicts, ConflictPair{
							MemoryA: m,
							Reason:  strings.TrimPrefix(factor, "conflict_flagged: "),
						})
						break
					}
				}
				if len(result.Conflicts) >= cfg.maxResults {
					break
				}
			}
		}
	}

	// 6. STALE MONITORS — PLAN memories tagged "monitor" not accessed within expected window
	if checksToRun[CheckStale] {
		plans, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			Types:    []storage.MemoryType{storage.TypePlan},
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 3,
		}))
		if err == nil {
			now := time.Now()
			for _, m := range plans {
				if hasTag(m.Tags, "monitor") {
					lastAccess := m.CreatedAt
					if m.LastAccessedAt != nil {
						lastAccess = *m.LastAccessedAt
					}
					// Default check interval: 24 hours
					if now.Sub(lastAccess) > 24*time.Hour {
						result.StaleMonitors = append(result.StaleMonitors, m)
						if len(result.StaleMonitors) >= cfg.maxResults {
							break
						}
					}
				}
			}
		}
	}

	// 7. GOAL PROGRESS — Cross-reference PLAN memories against ACTIVITY memories
	if checksToRun[CheckGoalProgress] {
		plans, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			Types:      []storage.MemoryType{storage.TypePlan},
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      cfg.maxResults,
			OrderBy:    "importance",
			Descending: true,
		}))
		if err == nil && len(plans) > 0 {
			now := time.Now()
			for _, plan := range plans {
				item := GoalProgressItem{Plan: plan, DaysLeft: -1}

				if plan.ExpiresAt != nil {
					item.DaysLeft = plan.ExpiresAt.Sub(now).Hours() / 24
				}

				// Find related activities by embedding similarity
				if k.emb != nil {
					emb, embErr := k.emb.Embed(ctx, plan.Content)
					if embErr == nil {
						similar, _ := k.store.FindSimilar(ctx, emb, entityID, 20, 0.4)
						for _, s := range similar {
							if s.Memory.Type == storage.TypeActivity && s.Memory.State == storage.StateActive {
								item.Activities = append(item.Activities, s.Memory)
							}
						}
					}
				}

				// Compute progress heuristic
				activityCount := len(item.Activities)
				if activityCount == 0 {
					item.Status = "no_activity"
					item.Progress = 0
				} else {
					// Weight by recency: activities in last 7 days count more
					recentCount := 0
					for _, a := range item.Activities {
						if now.Sub(a.CreatedAt) < 7*24*time.Hour {
							recentCount++
						}
					}
					item.Progress = float64(activityCount) / 10.0
					if item.Progress > 1.0 {
						item.Progress = 1.0
					}

					if recentCount > 0 {
						item.Status = "on_track"
					} else if item.DaysLeft >= 0 && item.DaysLeft < 14 {
						item.Status = "at_risk"
					} else {
						item.Status = "stalled"
					}
				}

				result.GoalProgress = append(result.GoalProgress, item)
			}
		}
	}

	// 8. CONTEXT CONTINUITY — Detect interrupted sessions
	if checksToRun[CheckContinuity] {
		recent, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			Types:      []storage.MemoryType{storage.TypeContext, storage.TypeActivity},
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      5,
			OrderBy:    "created_at",
			Descending: true,
		}))
		if err == nil && len(recent) > 0 {
			newest := recent[0]
			sessionAge := time.Since(newest.CreatedAt)

			if sessionAge < 48*time.Hour {
				// Check if there are unresolved plans/activities
				hasUnresolved := false
				var lastTopics []string
				for _, m := range recent {
					if m.Type == storage.TypeActivity || m.Type == storage.TypePlan {
						hasUnresolved = true
						if len(lastTopics) < 3 {
							content := m.Content
							if len(content) > 80 {
								content = content[:80] + "..."
							}
							lastTopics = append(lastTopics, content)
						}
					}
				}

				if hasUnresolved {
					suggestion := "You were working on: " + strings.Join(lastTopics, "; ")
					result.Continuity = &ContinuityItem{
						LastSessionMemories: recent,
						SessionAge:          sessionAge,
						WasInterrupted:      true,
						ResumeSuggestion:    suggestion,
					}
				}
			}
		}
	}

	// 9. SENTIMENT TRENDS — Pure SQL analysis, zero LLM cost
	if checksToRun[CheckSentiment] {
		recent, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      20,
			OrderBy:    "created_at",
			Descending: true,
		}))
		if err == nil && len(recent) >= 6 {
			mid := len(recent) / 2
			recentHalf := recent[:mid]
			previousHalf := recent[mid:]

			var recentSum, prevSum float64
			var notable []*Memory
			for _, m := range recentHalf {
				recentSum += m.Sentiment
				if m.Sentiment > 0.7 || m.Sentiment < -0.7 {
					notable = append(notable, m)
				}
			}
			for _, m := range previousHalf {
				prevSum += m.Sentiment
				if m.Sentiment > 0.7 || m.Sentiment < -0.7 {
					notable = append(notable, m)
				}
			}

			recentAvg := recentSum / float64(len(recentHalf))
			prevAvg := prevSum / float64(len(previousHalf))
			delta := recentAvg - prevAvg

			direction := "stable"
			if delta > 0.3 {
				direction = "improving"
			} else if delta < -0.3 {
				direction = "declining"
			}

			// Only report if there's a notable trend or extreme sentiment
			if direction != "stable" || len(notable) > 0 {
				absDelta := delta
				if absDelta < 0 {
					absDelta = -absDelta
				}
				result.Sentiment = &SentimentTrend{
					RecentAvg:   recentAvg,
					PreviousAvg: prevAvg,
					Direction:   direction,
					Delta:       absDelta,
					Notable:     notable,
				}
			}
		}
	}

	// 10. RELATIONSHIP CONTINUITY — Silent entities near deadlines
	if checksToRun[CheckRelationship] {
		entities, err := k.store.QueryEntities(ctx, storage.EntityQuery{
			OwnerEntityID: entityID,
			Limit:         50,
		})
		if err == nil {
			now := time.Now()
			for _, ent := range entities {
				if ent.LastMentionedAt == nil {
					continue
				}
				daysSilent := int(now.Sub(*ent.LastMentionedAt).Hours() / 24)
				if daysSilent < 7 {
					continue // recently active
				}

				// Find related active plans for this entity
				var relatedPlans []*Memory
				if k.emb != nil {
					emb, embErr := k.emb.Embed(ctx, ent.CanonicalName)
					if embErr == nil {
						similar, _ := k.store.FindSimilar(ctx, emb, entityID, 10, 0.4)
						for _, s := range similar {
							if s.Memory.Type == storage.TypePlan && s.Memory.State == storage.StateActive {
								relatedPlans = append(relatedPlans, s.Memory)
							}
						}
					}
				}

				urgency := "info"
				if len(relatedPlans) > 0 {
					urgency = "attention"
					// Check if any related plan has an approaching deadline
					for _, p := range relatedPlans {
						if p.ExpiresAt != nil && p.ExpiresAt.Sub(now).Hours()/24 < 14 {
							urgency = "urgent"
							break
						}
					}
				}

				if urgency != "info" || daysSilent > 14 {
					result.Relationships = append(result.Relationships, RelationshipAlert{
						Entity:        ent,
						LastMentioned: *ent.LastMentionedAt,
						DaysSilent:    daysSilent,
						RelatedPlans:  relatedPlans,
						Urgency:       urgency,
					})
				}
			}
		}
	}

	// 11. KNOWLEDGE GAPS — Unanswered questions in recent memories
	if checksToRun[CheckKnowledge] {
		recent, err := k.store.QueryMemories(ctx, buildQuery(storage.MemoryQuery{
			EntityID:   entityID,
			AgentID:    cfg.agentID,
			States:     []storage.MemoryState{storage.StateActive},
			Limit:      50,
			OrderBy:    "created_at",
			Descending: true,
		}))
		if err == nil && k.emb != nil {
			for _, m := range recent {
				if !looksLikeQuestion(m.Content) {
					continue
				}
				// Search for related memories that might answer the question
				emb, embErr := k.emb.Embed(ctx, m.Content)
				if embErr != nil {
					continue
				}
				similar, _ := k.store.FindSimilar(ctx, emb, entityID, 3, 0.6)
				hasAnswer := false
				for _, s := range similar {
					if s.Memory.ID != m.ID {
						hasAnswer = true
						break
					}
				}
				if !hasAnswer {
					result.KnowledgeGaps = append(result.KnowledgeGaps, KnowledgeGap{
						Question:   m.Content,
						AskedAt:    m.CreatedAt,
						HasRelated: false,
					})
					if len(result.KnowledgeGaps) >= 5 {
						break
					}
				}
			}
		}
	}

	// 12. BEHAVIORAL PATTERNS — Day-of-week topic frequency analysis
	if checksToRun[CheckPatterns] {
		result.Patterns = k.detectBehavioralPatterns(ctx, entityID)
	}

	// Build ByAgent attribution for team heartbeat mode
	if cfg.teamHeartbeat {
		result.ByAgent = buildByAgentAttribution(result)
	}

	// Build summary regardless of decision (used by LLM analysis if enabled)
	result.Summary = buildSummary(result)

	// Intelligent ShouldAct evaluation — replaces naive OR
	k.evaluateShouldAct(ctx, entityID, cfg, result)

	return result, nil
}

// evaluateShouldAct determines whether the heartbeat should trigger action.
// Implements cooldown, novelty, active hours, and nudge protocol.
func (k *Keyoku) evaluateShouldAct(ctx context.Context, entityID string, cfg *heartbeatConfig, result *HeartbeatResult) {
	agentID := cfg.agentID
	if agentID == "" {
		agentID = "default"
	}

	// Determine autonomy from config (will be overridden by handler if set in request)
	autonomy := "suggest"
	if cfg.autonomy != "" {
		autonomy = cfg.autonomy
	}
	params := DefaultHeartbeatParams(autonomy)
	// Apply overrides if set
	if cfg.heartbeatParams != nil {
		p := cfg.heartbeatParams
		if p.SignalCooldownNormal > 0 {
			params.SignalCooldownNormal = p.SignalCooldownNormal
		}
		if p.SignalCooldownLow > 0 {
			params.SignalCooldownLow = p.SignalCooldownLow
		}
		if p.NudgeAfterSilence > 0 {
			params.NudgeAfterSilence = p.NudgeAfterSilence
		}
		if p.MaxNudgesPerDay > 0 {
			params.MaxNudgesPerDay = p.MaxNudgesPerDay
		}
		if p.NudgeMaxInterval > 0 {
			params.NudgeMaxInterval = p.NudgeMaxInterval
		}
	}

	// Cleanup old records (fire-and-forget, 7 days)
	_ = k.store.CleanupOldHeartbeatActions(ctx, 7*24*time.Hour)

	// 1. Check active hours — suppress if in quiet window
	ah := getActiveHours(ctx, k.store, entityID)
	if ah.isQuietHour() {
		// Immediate tier bypasses quiet hours
		hasImmediate := len(result.Scheduled) > 0 || len(result.Deadlines) > 0
		if !hasImmediate {
			result.ShouldAct = false
			result.DecisionReason = "suppress_quiet"
			k.recordDecision(ctx, entityID, agentID, "signal", "", "suppress_quiet", TierLow, 0)
			return
		}
	}

	// 2. Classify active signals and find highest urgency tier
	activeSignals := k.classifyActiveSignals(result)
	if len(activeSignals) == 0 {
		// No signals — try nudge protocol
		k.evaluateNudge(ctx, entityID, agentID, autonomy, params, result)
		return
	}

	highestTier := TierLow
	for _, tier := range activeSignals {
		if tierPriority[tier] < tierPriority[highestTier] {
			highestTier = tier
		}
	}
	result.HighestUrgencyTier = highestTier

	// 3. Compute signal fingerprint
	fingerprint := k.computeSignalFingerprint(result)
	result.SignalFingerprint = fingerprint

	totalSignals := len(activeSignals)

	// 4. Get last "act" decision
	lastAct, err := k.store.GetLastHeartbeatAction(ctx, entityID, agentID, "act")

	// 5. Immediate tier always passes (cron, deadlines)
	if highestTier == TierImmediate {
		result.ShouldAct = true
		result.DecisionReason = "act"
		k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "act", highestTier, totalSignals)

		// Still run LLM prioritization if available
		k.runLLMPrioritization(ctx, cfg, result)
		return
	}

	// 6. Cooldown check
	if err == nil && lastAct != nil {
		cooldown := params.SignalCooldownNormal
		if highestTier == TierLow {
			cooldown = params.SignalCooldownLow
		} else if highestTier == TierElevated {
			cooldown = time.Hour // elevated always 1h
		}

		if time.Since(lastAct.ActedAt) < cooldown {
			result.ShouldAct = false
			result.DecisionReason = "suppress_cooldown"
			k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_cooldown", highestTier, totalSignals)
			return
		}
	}

	// 7. Novelty check — same fingerprint means nothing changed
	if err == nil && lastAct != nil && lastAct.SignalFingerprint == fingerprint {
		result.ShouldAct = false
		result.DecisionReason = "suppress_stale"
		k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_stale", highestTier, totalSignals)
		return
	}

	// 8. Passed all checks — act
	result.ShouldAct = true
	result.DecisionReason = "act"
	k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "act", highestTier, totalSignals)

	// LLM prioritization
	k.runLLMPrioritization(ctx, cfg, result)
}

// evaluateNudge implements the nudge protocol for silence periods.
func (k *Keyoku) evaluateNudge(ctx context.Context, entityID, agentID, autonomy string, params HeartbeatParams, result *HeartbeatResult) {
	// Observe mode never nudges
	if autonomy == "observe" || params.NudgeAfterSilence == 0 {
		result.ShouldAct = false
		result.DecisionReason = "no_signals"
		return
	}

	// Get time since last user message
	recentMsgs, err := k.store.GetRecentSessionMessages(ctx, entityID, 1)
	if err != nil || len(recentMsgs) == 0 {
		result.ShouldAct = false
		result.DecisionReason = "no_signals"
		return
	}

	silence := time.Since(recentMsgs[0].CreatedAt)

	// Not enough silence for a nudge yet
	if silence < params.NudgeAfterSilence {
		result.ShouldAct = false
		result.DecisionReason = "no_signals"
		return
	}

	// Exponential backoff: each consecutive nudge doubles the required interval.
	// 1st nudge: NudgeAfterSilence (e.g. 2h)
	// 2nd: 4h after last nudge, 3rd: 8h, 4th: 16h, ... capped at NudgeMaxInterval
	nudgeCount, err := k.store.GetNudgeCountToday(ctx, entityID, agentID)
	if err != nil {
		result.ShouldAct = false
		result.DecisionReason = "no_signals"
		return
	}

	// Safety cap on daily nudges
	if params.MaxNudgesPerDay > 0 && nudgeCount >= params.MaxNudgesPerDay {
		result.ShouldAct = false
		result.DecisionReason = "suppress_nudge_cap"
		return
	}

	// Compute backoff interval: base * 2^(consecutive nudges)
	// Use nudgeCount as proxy for consecutive nudges (resets daily)
	requiredInterval := params.NudgeAfterSilence
	for i := 0; i < nudgeCount; i++ {
		requiredInterval *= 2
		if requiredInterval > params.NudgeMaxInterval {
			requiredInterval = params.NudgeMaxInterval
			break
		}
	}

	// Check if enough time since last nudge (not last user message)
	lastNudge, err := k.store.GetLastHeartbeatAction(ctx, entityID, agentID, "act")
	if err == nil && lastNudge != nil && lastNudge.TriggerCategory == "nudge" {
		sinceLastNudge := time.Since(lastNudge.ActedAt)
		if sinceLastNudge < requiredInterval {
			result.ShouldAct = false
			result.DecisionReason = "suppress_nudge_backoff"
			return
		}
	}

	// Check active hours for nudges too
	ah := getActiveHours(ctx, k.store, entityID)
	if ah.isQuietHour() {
		result.ShouldAct = false
		result.DecisionReason = "suppress_quiet"
		return
	}

	// Find novel memory content for the nudge
	nudgeContent := k.findNudgeContent(ctx, entityID, agentID)
	if nudgeContent == "" {
		result.ShouldAct = false
		result.DecisionReason = "no_signals"
		return
	}

	result.ShouldAct = true
	result.DecisionReason = "nudge"
	result.NudgeContext = nudgeContent
	k.recordDecision(ctx, entityID, agentID, "nudge", "", "act", TierNormal, 0)
}

// findNudgeContent selects a meaningful memory to reference in a nudge.
func (k *Keyoku) findNudgeContent(ctx context.Context, entityID, agentID string) string {
	// Look for high-importance memories with low access count (unsurfaced)
	memories, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		AgentID:    agentID,
		States:     []storage.MemoryState{storage.StateActive},
		Limit:      20,
		OrderBy:    "importance",
		Descending: true,
	})
	if err != nil || len(memories) == 0 {
		return ""
	}

	// Prefer memories that haven't been surfaced much
	for _, m := range memories {
		if m.AccessCount < 3 && m.Importance >= 0.6 {
			return m.Content
		}
	}

	// Fall back to most important
	return memories[0].Content
}

// classifyActiveSignals returns a map of check type → tier for signals that are present.
func (k *Keyoku) classifyActiveSignals(result *HeartbeatResult) map[HeartbeatCheckType]string {
	active := make(map[HeartbeatCheckType]string)

	if len(result.Scheduled) > 0 {
		active[CheckScheduled] = signalTierMap[CheckScheduled]
	}
	if len(result.Deadlines) > 0 {
		active[CheckDeadlines] = signalTierMap[CheckDeadlines]
	}
	if len(result.Conflicts) > 0 {
		active[CheckConflicts] = signalTierMap[CheckConflicts]
	}
	if result.Continuity != nil && result.Continuity.WasInterrupted {
		active[CheckContinuity] = signalTierMap[CheckContinuity]
	}
	if len(result.StaleMonitors) > 0 {
		active[CheckStale] = signalTierMap[CheckStale]
	}
	if len(result.PendingWork) > 0 {
		active[CheckPendingWork] = signalTierMap[CheckPendingWork]
	}
	if len(result.GoalProgress) > 0 {
		active[CheckGoalProgress] = signalTierMap[CheckGoalProgress]
	}
	if len(result.KnowledgeGaps) > 0 {
		active[CheckKnowledge] = signalTierMap[CheckKnowledge]
	}
	if len(result.Decaying) > 0 {
		active[CheckDecaying] = signalTierMap[CheckDecaying]
	}
	if result.Sentiment != nil {
		active[CheckSentiment] = signalTierMap[CheckSentiment]
	}
	if len(result.Relationships) > 0 {
		active[CheckRelationship] = signalTierMap[CheckRelationship]
	}
	if len(result.Patterns) > 0 {
		active[CheckPatterns] = signalTierMap[CheckPatterns]
	}

	return active
}

// computeSignalFingerprint creates a SHA256 hash of the current signal state.
func (k *Keyoku) computeSignalFingerprint(result *HeartbeatResult) string {
	var parts []string

	// Collect memory IDs from each signal category
	for _, m := range result.Scheduled {
		parts = append(parts, "sched:"+m.ID)
	}
	for _, m := range result.Deadlines {
		parts = append(parts, "dead:"+m.ID)
	}
	for _, m := range result.PendingWork {
		parts = append(parts, "work:"+m.ID)
	}
	for _, c := range result.Conflicts {
		parts = append(parts, "conf:"+c.MemoryA.ID)
	}
	for _, m := range result.StaleMonitors {
		parts = append(parts, "stale:"+m.ID)
	}
	for _, m := range result.Decaying {
		parts = append(parts, "decay:"+m.ID)
	}
	for _, g := range result.GoalProgress {
		parts = append(parts, fmt.Sprintf("goal:%s:%s", g.Plan.ID, g.Status))
	}
	if result.Continuity != nil && result.Continuity.WasInterrupted {
		parts = append(parts, "continuity:interrupted")
	}
	if result.Sentiment != nil {
		parts = append(parts, fmt.Sprintf("sentiment:%s", result.Sentiment.Direction))
	}
	for _, r := range result.Relationships {
		parts = append(parts, fmt.Sprintf("rel:%s:%d", r.Entity.ID, r.DaysSilent))
	}
	for _, g := range result.KnowledgeGaps {
		parts = append(parts, "gap:"+g.Question[:min(len(g.Question), 50)])
	}

	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:8]) // first 8 bytes = 16 hex chars, enough for uniqueness
}

// recordDecision persists a heartbeat decision for tracking.
func (k *Keyoku) recordDecision(ctx context.Context, entityID, agentID, triggerCategory, fingerprint, decision, tier string, totalSignals int) {
	_ = k.store.RecordHeartbeatAction(ctx, &storage.HeartbeatAction{
		EntityID:          entityID,
		AgentID:           agentID,
		TriggerCategory:   triggerCategory,
		SignalFingerprint: fingerprint,
		Decision:          decision,
		UrgencyTier:       tier,
		TotalSignals:      totalSignals,
	})
}

// runLLMPrioritization runs the opt-in LLM prioritization on heartbeat results.
func (k *Keyoku) runLLMPrioritization(ctx context.Context, cfg *heartbeatConfig, result *HeartbeatResult) {
	if cfg.llmProvider == nil || result.Summary == "" {
		return
	}
	priorityResp, err := cfg.llmProvider.PrioritizeActions(ctx, llm.ActionPriorityRequest{
		Summary:       result.Summary,
		AgentContext:  cfg.agentContext,
		EntityContext: cfg.entityContext,
	})
	if err == nil && priorityResp != nil {
		result.PriorityAction = priorityResp.PriorityAction
		result.ActionItems = priorityResp.ActionItems
		result.Urgency = priorityResp.Urgency
	}
}

// --- helpers ---

// Deprecated: parseCronTag is superseded by ParseScheduleFromTags in schedule.go.
// Kept temporarily for any external callers; will be removed in a future release.
func parseCronTag(tags []string) (time.Duration, bool) {
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "cron:") {
			continue
		}

		rest := strings.TrimPrefix(tag, "cron:")

		switch {
		case rest == "hourly":
			return 1 * time.Hour, true
		case rest == "daily" || strings.HasPrefix(rest, "daily:"):
			return 24 * time.Hour, true
		case rest == "weekly" || strings.HasPrefix(rest, "weekly:"):
			return 7 * 24 * time.Hour, true
		case rest == "monthly":
			return 30 * 24 * time.Hour, true
		case strings.HasPrefix(rest, "every:"):
			durStr := strings.TrimPrefix(rest, "every:")
			d, err := time.ParseDuration(durStr)
			if err == nil {
				return d, true
			}
		}
	}
	return 0, false
}

func looksLikeQuestion(content string) bool {
	if strings.Contains(content, "?") {
		return true
	}
	lower := strings.ToLower(content)
	questionStarters := []string{"how ", "what ", "why ", "when ", "where ", "who ", "which ", "can ", "does ", "is there ", "should "}
	for _, q := range questionStarters {
		if strings.HasPrefix(lower, q) || strings.Contains(lower, " "+q) {
			return true
		}
	}
	return false
}

func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

func buildByAgentAttribution(result *HeartbeatResult) map[string]*AgentHeartbeatSummary {
	byAgent := make(map[string]*AgentHeartbeatSummary)

	getOrCreate := func(agentID string) *AgentHeartbeatSummary {
		if s, ok := byAgent[agentID]; ok {
			return s
		}
		s := &AgentHeartbeatSummary{AgentID: agentID}
		byAgent[agentID] = s
		return s
	}

	for _, m := range result.PendingWork {
		getOrCreate(m.AgentID).PendingWork++
	}
	for _, m := range result.Deadlines {
		getOrCreate(m.AgentID).Deadlines++
	}
	for _, m := range result.Scheduled {
		getOrCreate(m.AgentID).Scheduled++
	}
	for _, m := range result.Decaying {
		getOrCreate(m.AgentID).Decaying++
	}
	for _, c := range result.Conflicts {
		getOrCreate(c.MemoryA.AgentID).Conflicts++
	}

	return byAgent
}

func buildSummary(result *HeartbeatResult) string {
	var parts []string

	isTeam := result.ByAgent != nil

	if len(result.PendingWork) > 0 {
		parts = append(parts, fmt.Sprintf("PENDING WORK (%d):", len(result.PendingWork)))
		for _, m := range result.PendingWork {
			if isTeam {
				parts = append(parts, fmt.Sprintf("  - [%s] %s (importance: %.2f, agent: %s)", m.Type, m.Content, m.Importance, m.AgentID))
			} else {
				parts = append(parts, fmt.Sprintf("  - [%s] %s (importance: %.2f)", m.Type, m.Content, m.Importance))
			}
		}
	}

	if len(result.Deadlines) > 0 {
		parts = append(parts, fmt.Sprintf("APPROACHING DEADLINES (%d):", len(result.Deadlines)))
		for _, m := range result.Deadlines {
			remaining := time.Until(*m.ExpiresAt).Round(time.Minute)
			parts = append(parts, fmt.Sprintf("  - %s (expires in %s)", m.Content, remaining))
		}
	}

	if len(result.Scheduled) > 0 {
		parts = append(parts, fmt.Sprintf("SCHEDULED TASKS DUE (%d):", len(result.Scheduled)))
		for _, m := range result.Scheduled {
			schedTag := ""
			for _, t := range m.Tags {
				if strings.HasPrefix(t, "cron:") {
					schedTag = t
					break
				}
			}
			if schedTag != "" {
				parts = append(parts, fmt.Sprintf("  - %s [schedule: %s]", m.Content, schedTag))
			} else {
				parts = append(parts, fmt.Sprintf("  - %s", m.Content))
			}
		}
	}

	if len(result.Decaying) > 0 {
		parts = append(parts, fmt.Sprintf("IMPORTANT MEMORIES DECAYING (%d):", len(result.Decaying)))
		for _, m := range result.Decaying {
			parts = append(parts, fmt.Sprintf("  - %s (importance: %.2f)", m.Content, m.Importance))
		}
	}

	if len(result.Conflicts) > 0 {
		parts = append(parts, fmt.Sprintf("UNRESOLVED CONFLICTS (%d):", len(result.Conflicts)))
		for _, c := range result.Conflicts {
			parts = append(parts, fmt.Sprintf("  - %s: %s", c.MemoryA.Content, c.Reason))
		}
	}

	if len(result.StaleMonitors) > 0 {
		parts = append(parts, fmt.Sprintf("STALE MONITORS (%d):", len(result.StaleMonitors)))
		for _, m := range result.StaleMonitors {
			parts = append(parts, fmt.Sprintf("  - %s", m.Content))
		}
	}

	if len(result.GoalProgress) > 0 {
		parts = append(parts, fmt.Sprintf("GOAL PROGRESS (%d):", len(result.GoalProgress)))
		for _, g := range result.GoalProgress {
			daysStr := "no deadline"
			if g.DaysLeft >= 0 {
				daysStr = fmt.Sprintf("%.0f days left", g.DaysLeft)
			}
			parts = append(parts, fmt.Sprintf("  - %s (%.0f%% done, %s, status: %s)",
				g.Plan.Content, g.Progress*100, daysStr, g.Status))
		}
	}

	if result.Continuity != nil && result.Continuity.WasInterrupted {
		parts = append(parts, "SESSION CONTINUITY:")
		parts = append(parts, fmt.Sprintf("  - %s (last active %s ago)",
			result.Continuity.ResumeSuggestion,
			result.Continuity.SessionAge.Round(time.Minute)))
	}

	if result.Sentiment != nil {
		parts = append(parts, fmt.Sprintf("SENTIMENT TREND: %s (recent: %.2f, previous: %.2f, delta: %.2f)",
			result.Sentiment.Direction, result.Sentiment.RecentAvg, result.Sentiment.PreviousAvg, result.Sentiment.Delta))
		for _, m := range result.Sentiment.Notable {
			parts = append(parts, fmt.Sprintf("  - [sentiment: %.2f] %s", m.Sentiment, m.Content))
		}
	}

	if len(result.Relationships) > 0 {
		parts = append(parts, fmt.Sprintf("RELATIONSHIP ALERTS (%d):", len(result.Relationships)))
		for _, r := range result.Relationships {
			parts = append(parts, fmt.Sprintf("  - %s: silent for %d days [%s]",
				r.Entity.CanonicalName, r.DaysSilent, r.Urgency))
		}
	}

	if len(result.KnowledgeGaps) > 0 {
		parts = append(parts, fmt.Sprintf("KNOWLEDGE GAPS (%d):", len(result.KnowledgeGaps)))
		for _, g := range result.KnowledgeGaps {
			parts = append(parts, fmt.Sprintf("  - %s", g.Question))
		}
	}

	if len(result.Patterns) > 0 {
		parts = append(parts, fmt.Sprintf("BEHAVIORAL PATTERNS (%d):", len(result.Patterns)))
		for _, p := range result.Patterns {
			parts = append(parts, fmt.Sprintf("  - %s (confidence: %.0f%%)", p.Description, p.Confidence*100))
		}
	}

	return strings.Join(parts, "\n")
}
