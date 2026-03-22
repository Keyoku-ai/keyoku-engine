// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	Urgency        string   // Canonical: "none", "low", "medium", "high", "critical" (mapped from tier or LLM)

	// Intelligent heartbeat decision metadata
	DecisionReason     string // "act", "nudge", "suppress_cooldown", "suppress_stale", "suppress_quiet"
	HighestUrgencyTier string // "immediate", "elevated", "normal", "low"
	SignalFingerprint  string // for debugging
	NudgeContext       string // memory content selected for nudge

	// Time awareness
	TimePeriod      string // "morning", "working", "evening", "late_night", "quiet"
	EscalationLevel int    // 1=casual, 2=direct, 3=offer help, 4+=dropped
	InConversation  bool   // Whether user is actively in conversation

	// v2: Intelligence metadata
	PositiveDeltas  []PositiveDelta // Detected positive changes since last heartbeat
	GraphContext    []string        // Entity relationship context for LLM
	TopicEntities  []string        // Entity IDs from current signals
	ResponseRate   float64         // 7-day user response rate (0.0-1.0)
	ConfluenceScore int            // Total signal weight

	// v3: Memory velocity
	MemoryVelocity     int  // New memories since last act
	MemoryVelocityHigh bool // True when velocity >= 5 (elevated signal)

	// Team heartbeat fields (populated only in team heartbeat mode)
	ByAgent map[string]*AgentHeartbeatSummary // per-agent breakdown (team mode only)

	// v4: Enhanced LLM analysis (populated by runEnhancedLLMAnalysis)
	EnhancedAnalysis *llm.HeartbeatAnalysisResponse `json:"enhanced_analysis,omitempty"`

	// Internal: unfiltered goal progress for snapshot delta detection (includes no_activity)
	allGoalProgress []GoalProgressItem `json:"-"`
}

// StateSnapshot captures key metrics at a point in time for delta detection.
type StateSnapshot struct {
	GoalStatuses         map[string]string `json:"goal_statuses"`          // planID -> status
	RelationshipSilences map[string]int    `json:"relationship_silences"`  // entityID -> days_silent
	SentimentDirection   string            `json:"sentiment_direction"`
	MemoryCount          int               `json:"memory_count"`           // Total active memories at snapshot time
	MemoryCountAt        time.Time         `json:"memory_count_at"`        // When count was taken
}

// PositiveDelta represents a detected improvement between heartbeat cycles.
type PositiveDelta struct {
	Type        string // "goal_improved", "entity_reengaged", "sentiment_improved"
	Description string
	EntityID    string // optional, for entity-specific deltas
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
	CheckPatterns:        TierLow,
	CheckPositiveDeltas:  TierNormal,
	CheckMemoryVelocity: TierElevated,
}

// tierPriority for comparison (lower = more urgent).
var tierPriority = map[string]int{
	TierImmediate: 0,
	TierElevated:  1,
	TierNormal:    2,
	TierLow:       3,
}

// tierWeight assigns signal importance for confluence scoring.
var tierWeight = map[string]int{
	TierImmediate: 10,
	TierElevated:  5,
	TierNormal:    3,
	TierLow:       1,
}

// confluenceThreshold is the minimum combined signal weight to trigger action.
var confluenceThreshold = map[string]int{
	"act":     8,
	"suggest": 12,
	"observe": 20,
}

// HeartbeatParams holds configurable parameters for heartbeat evaluation.
// Defaults are set per autonomy level; individual fields can be overridden.
type HeartbeatParams struct {
	SignalCooldownNormal   time.Duration
	SignalCooldownElevated time.Duration // cooldown for elevated-tier signals
	SignalCooldownLow      time.Duration
	NudgeAfterSilence      time.Duration // 0 = disabled
	MaxNudgesPerDay        int           // safety cap per 24h
	NudgeMaxInterval       time.Duration // cap for backoff decay (e.g. 48h)
	TopicDedupWindow       time.Duration // lookback for topic dedup (0 = use SignalCooldownNormal)
}

// DefaultHeartbeatParams returns defaults based on autonomy level.
func DefaultHeartbeatParams(autonomy string) HeartbeatParams {
	switch autonomy {
	case "observe":
		return HeartbeatParams{
			SignalCooldownNormal:   4 * time.Hour,
			SignalCooldownElevated: 2 * time.Hour,
			SignalCooldownLow:      8 * time.Hour,
			NudgeAfterSilence:     0, // disabled
			MaxNudgesPerDay:       0,
			NudgeMaxInterval:      12 * time.Hour,
		}
	case "act":
		return HeartbeatParams{
			SignalCooldownNormal:   10 * time.Minute,
			SignalCooldownElevated: 5 * time.Minute,
			SignalCooldownLow:      30 * time.Minute,
			NudgeAfterSilence:     30 * time.Minute,
			MaxNudgesPerDay:       24,
			NudgeMaxInterval:      48 * time.Hour,
		}
	default: // "suggest"
		return HeartbeatParams{
			SignalCooldownNormal:   2 * time.Hour,
			SignalCooldownElevated: 30 * time.Minute,
			SignalCooldownLow:      4 * time.Hour,
			NudgeAfterSilence:     4 * time.Hour,
			MaxNudgesPerDay:       3,
			NudgeMaxInterval:      24 * time.Hour,
		}
	}
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

	// Conversation awareness
	inConversation bool // Plugin signals that user is actively talking

	// Signals-only mode: skip evaluateShouldAct, return signals without decision.
	// Used when watcher already decided to act and delivery path just needs fresh signals.
	signalsOnly bool

	// Confidence gating: memories below this threshold are excluded from signals.
	minConfidence float64 // default: 0.5

	// Verbosity control for LLM analysis
	verbosity llm.HeartbeatVerbosity

	// Virtual time override (for demo recording)
	virtualNow time.Time // When non-zero, replaces time.Now() for all signal computation
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
	CheckPatterns         HeartbeatCheckType = "patterns"
	CheckPositiveDeltas   HeartbeatCheckType = "positive_deltas"
	CheckMemoryVelocity   HeartbeatCheckType = "memory_velocity"
)

var allChecks = []HeartbeatCheckType{
	CheckPendingWork, CheckDeadlines, CheckScheduled,
	CheckDecaying, CheckConflicts, CheckStale,
	CheckGoalProgress, CheckContinuity, CheckSentiment,
	CheckRelationship, CheckKnowledge, CheckPatterns,
	CheckPositiveDeltas, CheckMemoryVelocity,
}

// HeartbeatCheck performs a zero-token local query against SQLite.
// Returns whether the agent should act and what needs attention.
func (k *Keyoku) HeartbeatCheck(ctx context.Context, entityID string, opts ...HeartbeatOption) (*HeartbeatResult, error) {
	cfg := &heartbeatConfig{
		deadlineWindow:  24 * time.Hour,
		decayThreshold:  0.4,
		importanceFloor: 0.4,
		maxResults:      20,
		minConfidence:   0.5,
		checks:          allChecks,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Resolve "now" — use virtualNow if set, otherwise real clock
	now := time.Now()
	if !cfg.virtualNow.IsZero() {
		now = cfg.virtualNow
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

	// Helper to build a query with visibility and virtual time applied
	buildQuery := func(q storage.MemoryQuery) storage.MemoryQuery {
		if visibilityFor != nil {
			q.VisibilityFor = visibilityFor
			// When using visibility filtering, don't also filter by agent_id
			// (VisibilityFor handles the scoping)
			q.AgentID = ""
		}
		// Virtual time: only show memories that existed at this point in time
		if !cfg.virtualNow.IsZero() {
			q.CreatedBefore = cfg.virtualNow
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
			stalePlanCutoff := now.Add(-7 * 24 * time.Hour)   // 7 days
			deadlineHorizon := now.Add(14 * 24 * time.Hour)   // 14 days
			for _, m := range pending {
				if m.Importance < cfg.importanceFloor {
					continue
				}
				// Recency gate: only include if updated within 7 days OR has deadline within 14 days
				// Zero UpdatedAt = not set, treat as recent
				recentEnough := m.UpdatedAt.IsZero() || m.UpdatedAt.After(stalePlanCutoff)
				hasUpcomingDeadline := m.ExpiresAt != nil && m.ExpiresAt.After(now) && m.ExpiresAt.Before(deadlineHorizon)
				if recentEnough || hasUpcomingDeadline {
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
			deadlineHorizon := now.Add(cfg.deadlineWindow)
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

		// Preserve all goals for delta detection snapshot (includes no_activity)
		result.allGoalProgress = append([]GoalProgressItem{}, result.GoalProgress...)

		// Filter out no_activity goals — they're noise, not signals
		var filteredGoals []GoalProgressItem
		for _, g := range result.GoalProgress {
			if g.Status != "no_activity" {
				filteredGoals = append(filteredGoals, g)
			}
		}
		result.GoalProgress = filteredGoals
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
			sessionAge := now.Sub(newest.CreatedAt)

			if sessionAge < 12*time.Hour {
				// Session-window filter: only count memories within 2h of the newest as "same session"
				sessionWindow := 2 * time.Hour
				hasUnresolved := false
				var lastTopics []string
				for _, m := range recent {
					if newest.CreatedAt.Sub(m.CreatedAt) > sessionWindow {
						continue // not part of the same session
					}
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
			Limit:      40, // fetch more to compensate for filtering
			OrderBy:    "created_at",
			Descending: true,
		}))
		if err == nil {
			// Filter to only memories with non-zero sentiment — unscored memories
			// (sentiment=0.0) dilute the signal and produce false "stable" readings.
			var scored []*Memory
			for _, m := range recent {
				if m.Sentiment != 0 {
					scored = append(scored, m)
				}
			}

			if len(scored) >= 6 {
				mid := len(scored) / 2
				recentHalf := scored[:mid]
				previousHalf := scored[mid:]

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
	}

	// 10. RELATIONSHIP CONTINUITY — Silent entities near deadlines
	if checksToRun[CheckRelationship] {
		entities, err := k.store.QueryEntities(ctx, storage.EntityQuery{
			OwnerEntityID: entityID,
			Limit:         50,
		})
		if err == nil {
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

	// 12b. CONFIDENCE GATING — filter out low-confidence memories from signal categories
	if cfg.minConfidence > 0 {
		result.PendingWork = filterByConfidence(result.PendingWork, cfg.minConfidence)
		result.Deadlines = filterByConfidence(result.Deadlines, cfg.minConfidence)
		result.Decaying = filterByConfidence(result.Decaying, cfg.minConfidence)
		result.StaleMonitors = filterByConfidence(result.StaleMonitors, cfg.minConfidence)
	}

	// 13. SURFACED MEMORY FILTER — remove signals that were already surfaced recently.
	// This prevents the AI from covering the same topics with different wording.
	// Scheduled signals are exempt (cron tasks that are due must fire regardless).
	// Deadlines use soft filter (always keep at least one reminder).
	// PendingWork/StaleMonitors/Decaying use strict filter (go quiet if already covered).
	if !cfg.signalsOnly {
		agentIDForFilter := cfg.agentID
		if agentIDForFilter == "" {
			agentIDForFilter = "default"
		}
		surfaceCooldown := 1 * time.Hour // same memory won't resurface for 1h
		result.PendingWork = k.filterSurfacedMemoriesStrict(ctx, entityID, agentIDForFilter, result.PendingWork, surfaceCooldown)
		result.Deadlines = k.filterSurfacedMemories(ctx, entityID, agentIDForFilter, result.Deadlines, surfaceCooldown)
		result.StaleMonitors = k.filterSurfacedMemoriesStrict(ctx, entityID, agentIDForFilter, result.StaleMonitors, surfaceCooldown)
		result.Decaying = k.filterSurfacedMemoriesStrict(ctx, entityID, agentIDForFilter, result.Decaying, surfaceCooldown)
	}

	// Build ByAgent attribution for team heartbeat mode
	if cfg.teamHeartbeat {
		result.ByAgent = buildByAgentAttribution(result)
	}

	// Capture memory count for velocity detection
	memCount, memCountErr := k.store.GetMemoryCountForEntity(ctx, entityID)
	if memCountErr != nil {
		memCount = 0
	}

	// Detect positive deltas and memory velocity (gated by check types)
	if checksToRun[CheckPositiveDeltas] || checksToRun[CheckMemoryVelocity] {
		snapshot := buildStateSnapshot(result)
		snapshot.MemoryCount = memCount
		snapshot.MemoryCountAt = now
		agentIDForDelta := cfg.agentID
		if agentIDForDelta == "" {
			agentIDForDelta = "default"
		}
		lastAct, deltaErr := k.store.GetLastHeartbeatAction(ctx, entityID, agentIDForDelta, "act")
		if deltaErr == nil && lastAct != nil && lastAct.StateSnapshot != "" {
			var prevSnapshot StateSnapshot
			if json.Unmarshal([]byte(lastAct.StateSnapshot), &prevSnapshot) == nil {
				if checksToRun[CheckPositiveDeltas] {
					result.PositiveDeltas = detectDeltas(snapshot, prevSnapshot)
				}

				// Memory velocity: how many new memories since last act
				if checksToRun[CheckMemoryVelocity] && prevSnapshot.MemoryCount > 0 {
					result.MemoryVelocity = memCount - prevSnapshot.MemoryCount
					if result.MemoryVelocity >= 5 {
						result.MemoryVelocityHigh = true
					}
				}
			}
		}
	}

	// Build summary regardless of decision (used by LLM analysis if enabled)
	result.Summary = buildSummary(result)

	if cfg.signalsOnly {
		// Signals-only mode: watcher already decided to act.
		// Return fresh signals without re-running the decision pipeline.
		result.ShouldAct = true
		result.InConversation = cfg.inConversation
		result.DecisionReason = "signals_only"
		activeSignals := k.classifyActiveSignals(result)
		for _, tier := range activeSignals {
			if result.HighestUrgencyTier == "" || tierPriority[tier] < tierPriority[result.HighestUrgencyTier] {
				result.HighestUrgencyTier = tier
			}
		}
		result.TimePeriod = k.currentTimePeriodAt(now)
		result.Urgency = tierToUrgency(result.HighestUrgencyTier)
	} else {
		// Intelligent ShouldAct evaluation — replaces naive OR
		k.evaluateShouldAct(ctx, entityID, cfg, result)
	}

	return result, nil
}

// filterByConfidence removes memories with confidence below the threshold.
// Memories with confidence == 0 are treated as "not set" and pass through.
func filterByConfidence(memories []*Memory, minConfidence float64) []*Memory {
	if minConfidence <= 0 {
		return memories
	}
	var filtered []*Memory
	for _, m := range memories {
		if m.Confidence == 0 || m.Confidence >= minConfidence {
			filtered = append(filtered, m)
		}
	}
	return filtered
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
