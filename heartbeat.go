// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
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
	Urgency        string   // "immediate", "soon", "can_wait"

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

	// Team heartbeat fields (populated only in team heartbeat mode)
	ByAgent map[string]*AgentHeartbeatSummary // per-agent breakdown (team mode only)
}

// StateSnapshot captures key metrics at a point in time for delta detection.
type StateSnapshot struct {
	GoalStatuses         map[string]string `json:"goal_statuses"`          // planID -> status
	RelationshipSilences map[string]int    `json:"relationship_silences"`  // entityID -> days_silent
	SentimentDirection   string            `json:"sentiment_direction"`
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
	CheckPositiveDeltas: TierNormal,
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

// --- Quiet Hours ---

// pstLocation is the US/Pacific timezone used as default for quiet hours.
var pstLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.FixedZone("PST", -8*60*60)
	}
	return loc
}()

// TimePeriod constants for time-of-day awareness.
const (
	PeriodMorning   = "morning"    // 7-10: proactive window
	PeriodWorking   = "working"    // 10-17: normal operations
	PeriodEvening   = "evening"    // 17-21: wind-down, less proactive
	PeriodLateNight = "late_night" // 21-23: minimal interruption
	PeriodQuiet     = "quiet"      // 23-7: only immediate urgency
)

// currentTimePeriod returns the current time-of-day tier.
// Uses the configured quiet hours timezone, falling back to PST.
func (k *Keyoku) currentTimePeriod() string {
	loc := pstLocation
	if k.quietHours.Location != nil {
		loc = k.quietHours.Location
	}
	hour := time.Now().In(loc).Hour()
	switch {
	case hour >= 7 && hour < 10:
		return PeriodMorning
	case hour >= 10 && hour < 17:
		return PeriodWorking
	case hour >= 17 && hour < 21:
		return PeriodEvening
	case hour >= 21 && hour < 23:
		return PeriodLateNight
	default: // 23-7
		return PeriodQuiet
	}
}

// timePeriodMinTier returns the minimum urgency tier required for a time period.
func timePeriodMinTier(period string) string {
	switch period {
	case PeriodMorning, PeriodWorking:
		return TierLow // everything allowed
	case PeriodEvening:
		return TierNormal
	case PeriodLateNight:
		return TierElevated
	case PeriodQuiet:
		return TierImmediate
	default:
		return TierLow
	}
}

// timePeriodCooldownMultiplier returns the cooldown multiplier for a time period.
func timePeriodCooldownMultiplier(period string) float64 {
	switch period {
	case PeriodMorning:
		return 0.5
	case PeriodWorking:
		return 1.0
	case PeriodEvening:
		return 1.5
	case PeriodLateNight:
		return 3.0
	case PeriodQuiet:
		return 10.0
	default:
		return 1.0
	}
}

// tierRank returns a numeric rank for urgency tier comparison.
func tierRank(tier string) int {
	switch tier {
	case TierImmediate:
		return 4
	case TierElevated:
		return 3
	case TierNormal:
		return 2
	case TierLow:
		return 1
	default:
		return 0
	}
}

// isQuietHour checks if the current time falls within the quiet period.
// Kept for backward compatibility — now delegates to currentTimePeriod.
func (k *Keyoku) isQuietHour() bool {
	return k.currentTimePeriod() == PeriodQuiet
}

// isUserTypicallyActive checks if the user has historically been active at the current hour.
// Returns true if current hour accounts for >= 2% of total message volume over the last 14 days,
// or if there's insufficient data to determine a pattern.
func (k *Keyoku) isUserTypicallyActive(ctx context.Context, entityID string) bool {
	dist, err := k.store.GetMessageHourDistribution(ctx, entityID, 14)
	if err != nil || len(dist) == 0 {
		return true // no data = assume active
	}
	total := 0
	for _, count := range dist {
		total += count
	}
	if total < 20 {
		return true // too few messages to determine pattern
	}
	loc := pstLocation
	if k.quietHours.Location != nil {
		loc = k.quietHours.Location
	}
	currentHour := time.Now().In(loc).Hour()
	hourCount := dist[currentHour]
	return float64(hourCount)/float64(total) >= 0.02
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
	CheckPatterns        HeartbeatCheckType = "patterns"
	CheckPositiveDeltas  HeartbeatCheckType = "positive_deltas"
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
func WithInConversation(inConversation bool) HeartbeatOption {
	return func(c *heartbeatConfig) { c.inConversation = inConversation }
}

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
		importanceFloor: 0.4,
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
			sessionAge := time.Since(newest.CreatedAt)

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

	// Detect positive deltas before decision (so they can trigger action)
	snapshot := buildStateSnapshot(result)
	agentIDForDelta := cfg.agentID
	if agentIDForDelta == "" {
		agentIDForDelta = "default"
	}
	lastAct, deltaErr := k.store.GetLastHeartbeatAction(ctx, entityID, agentIDForDelta, "act")
	if deltaErr == nil && lastAct != nil && lastAct.StateSnapshot != "" {
		var prevSnapshot StateSnapshot
		if json.Unmarshal([]byte(lastAct.StateSnapshot), &prevSnapshot) == nil {
			result.PositiveDeltas = detectDeltas(snapshot, prevSnapshot)
		}
	}

	// Build summary regardless of decision (used by LLM analysis if enabled)
	result.Summary = buildSummary(result)

	// Intelligent ShouldAct evaluation — replaces naive OR
	k.evaluateShouldAct(ctx, entityID, cfg, result)

	return result, nil
}

// evaluateShouldAct determines whether the heartbeat should trigger action.
// v2: Integrates response rate, confluence, topic dedup, proximity, deltas, and graph enrichment.
func (k *Keyoku) evaluateShouldAct(ctx context.Context, entityID string, cfg *heartbeatConfig, result *HeartbeatResult) {
	agentID := cfg.agentID
	if agentID == "" {
		agentID = "default"
	}

	// Determine autonomy from config
	autonomy := "suggest"
	if cfg.autonomy != "" {
		autonomy = cfg.autonomy
	}
	params := DefaultHeartbeatParams(autonomy)
	if cfg.heartbeatParams != nil {
		p := cfg.heartbeatParams
		if p.SignalCooldownNormal > 0 {
			params.SignalCooldownNormal = p.SignalCooldownNormal
		}
		if p.SignalCooldownElevated > 0 {
			params.SignalCooldownElevated = p.SignalCooldownElevated
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

	// 0. First-contact mode — very few memories means this is a new user
	memCount, countErr := k.store.GetMemoryCount(ctx)
	if countErr == nil && memCount < 5 {
		result.ShouldAct = true
		result.DecisionReason = "first_contact"
		result.HighestUrgencyTier = TierNormal
		result.TimePeriod = k.currentTimePeriod()
		k.recordDecision(ctx, entityID, agentID, "first_contact", "", "first_contact", TierNormal, 0)
		return
	}

	// Background: check response tracking for previous actions + cleanup
	go k.checkResponseTracking(ctx, entityID)
	_ = k.store.CleanupOldHeartbeatActions(ctx, 7*24*time.Hour)

	// 1. Deadline proximity — critical deadlines (<1h) force immediate
	if len(result.Deadlines) > 0 {
		proximityScore := calculateDeadlineProximity(result.Deadlines)
		if proximityScore > 1.0 { // less than 1 hour remaining
			result.ShouldAct = true
			result.DecisionReason = "act_deadline_critical"
			result.HighestUrgencyTier = TierImmediate
			k.finalizeAct(ctx, entityID, agentID, cfg, result, TierImmediate)
			return
		}
	}

	// 2. Time-of-day awareness — filter signals by minimum urgency tier
	period := k.currentTimePeriod()
	result.TimePeriod = period
	minTier := timePeriodMinTier(period)
	if tierRank(minTier) > tierRank(TierLow) {
		// Check if any signal meets the minimum tier for this time period
		activeSignals := k.classifyActiveSignals(result)
		highestSignalTier := TierLow
		for _, tier := range activeSignals {
			if tierRank(tier) > tierRank(highestSignalTier) {
				highestSignalTier = tier
			}
		}
		if tierRank(highestSignalTier) < tierRank(minTier) {
			result.ShouldAct = false
			result.DecisionReason = "suppress_time_period"
			k.recordDecision(ctx, entityID, agentID, "signal", "", "suppress_time_period", highestSignalTier, len(activeSignals))
			return
		}
	}

	// 2b. Conversation rhythm — suppress if user is never active at this hour
	if !k.isUserTypicallyActive(ctx, entityID) {
		// Only suppress if signals aren't elevated+
		activeSignalsForRhythm := k.classifyActiveSignals(result)
		highestForRhythm := TierLow
		for _, t := range activeSignalsForRhythm {
			if tierRank(t) > tierRank(highestForRhythm) {
				highestForRhythm = t
			}
		}
		if tierRank(highestForRhythm) < tierRank(TierElevated) {
			result.ShouldAct = false
			result.DecisionReason = "suppress_rhythm"
			k.recordDecision(ctx, entityID, agentID, "signal", "", "suppress_rhythm", highestForRhythm, len(activeSignalsForRhythm))
			return
		}
	}

	// 3. Active conversation awareness — tier-aware, not binary
	inConversation := cfg.inConversation
	if !inConversation {
		// Fall back to session message heuristic when plugin doesn't signal
		recentMsgs, convErr := k.store.GetRecentSessionMessages(ctx, entityID, 1)
		if convErr == nil && len(recentMsgs) > 0 {
			inConversation = time.Since(recentMsgs[0].CreatedAt) < 15*time.Minute
		}
	}
	if inConversation {
		result.InConversation = true
	}

	// 4. Classify signals
	activeSignals := k.classifyActiveSignals(result)

	// During active conversation: filter to elevated+ only (no nudges, no low/normal)
	if inConversation && len(activeSignals) > 0 {
		conversationSignals := make(map[HeartbeatCheckType]string)
		for checkType, tier := range activeSignals {
			if tier == TierImmediate || tier == TierElevated {
				conversationSignals[checkType] = tier
			}
		}
		if len(conversationSignals) == 0 {
			result.ShouldAct = false
			result.DecisionReason = "suppress_conversation_low"
			k.recordDecision(ctx, entityID, agentID, "signal", "", "suppress_conversation_low", TierLow, 0)
			return
		}
		activeSignals = conversationSignals
	}

	if len(activeSignals) == 0 {
		// Never nudge during active conversation
		if inConversation {
			result.ShouldAct = false
			result.DecisionReason = "no_signals"
			return
		}
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

	// 5. Confluence scoring — multiple weak signals can combine
	confluenceWeight, meetsConfluence := calculateSignalConfluence(activeSignals, autonomy)
	result.ConfluenceScore = confluenceWeight

	// 6. Fingerprint
	fingerprint := k.computeSignalFingerprint(result)
	result.SignalFingerprint = fingerprint
	totalSignals := len(activeSignals)

	// 7. Get last "act" decision
	lastAct, err := k.store.GetLastHeartbeatAction(ctx, entityID, agentID, "act")

	// 8. Immediate tier always passes
	if highestTier == TierImmediate {
		result.ShouldAct = true
		result.DecisionReason = "act"
		k.finalizeAct(ctx, entityID, agentID, cfg, result, highestTier)
		return
	}

	// 9. Confluence promotion — weak signals combining to trigger
	if highestTier != TierElevated && meetsConfluence {
		result.ShouldAct = true
		result.DecisionReason = "act_confluence"
		result.HighestUrgencyTier = "confluence"
		k.finalizeAct(ctx, entityID, agentID, cfg, result, "confluence")
		return
	}

	// 10. Response rate → cooldown multiplier
	responseRate := k.calculateResponseRate(ctx, entityID, agentID)
	result.ResponseRate = responseRate
	multiplier := responseCooldownMultiplier(responseRate)

	// 11. Cooldown check (with response rate multiplier)
	if err == nil && lastAct != nil {
		cooldown := params.SignalCooldownNormal
		if highestTier == TierLow {
			cooldown = params.SignalCooldownLow
		} else if highestTier == TierElevated {
			cooldown = params.SignalCooldownElevated
		}
		cooldown = time.Duration(float64(cooldown) * multiplier * timePeriodCooldownMultiplier(period))

		if time.Since(lastAct.ActedAt) < cooldown {
			result.ShouldAct = false
			result.DecisionReason = "suppress_cooldown"
			k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_cooldown", highestTier, totalSignals)
			return
		}
	}

	// 12. Novelty check — with time-based escape hatch
	if err == nil && lastAct != nil && lastAct.SignalFingerprint == fingerprint {
		staleDuration := time.Since(lastAct.ActedAt)
		staleEscapeThreshold := params.SignalCooldownNormal * 2
		if staleDuration < staleEscapeThreshold {
			// Same signals, not enough time has passed — suppress
			result.ShouldAct = false
			result.DecisionReason = "suppress_stale"
			k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_stale", highestTier, totalSignals)
			return
		}
		// Stale escape: same signals, but enough time has passed.
		// Fall through to nudge path with rotated content instead of blocking forever.
		if !inConversation {
			k.evaluateNudge(ctx, entityID, agentID, autonomy, params, result)
			return
		}
		result.ShouldAct = false
		result.DecisionReason = "suppress_stale"
		return
	}

	// 13. Topic entity dedup
	topicEntities := k.extractTopicEntities(ctx, result)
	result.TopicEntities = topicEntities
	if k.shouldSuppressTopicRepeat(ctx, entityID, agentID, topicEntities) {
		result.ShouldAct = false
		result.DecisionReason = "suppress_topic_repeat"
		k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_topic_repeat", highestTier, totalSignals)
		return
	}

	// 14. Passed all checks — act
	result.ShouldAct = true
	result.DecisionReason = "act"
	k.finalizeAct(ctx, entityID, agentID, cfg, result, highestTier)
}

// finalizeAct handles the post-decision work for "act" decisions:
// state snapshot, delta detection, graph enrichment, topic entities, and recording.
func (k *Keyoku) finalizeAct(ctx context.Context, entityID, agentID string, cfg *heartbeatConfig, result *HeartbeatResult, tier string) {
	fingerprint := result.SignalFingerprint
	totalSignals := len(k.classifyActiveSignals(result))

	// Extract topic entities if not already done
	if len(result.TopicEntities) == 0 {
		result.TopicEntities = k.extractTopicEntities(ctx, result)
	}

	// Build state snapshot (deltas already detected before evaluateShouldAct)
	snapshot := buildStateSnapshot(result)
	snapshotJSON, _ := json.Marshal(snapshot)

	// Graph enrichment
	result.GraphContext = k.enrichSignalsWithGraph(ctx, entityID, result)

	// Response rate
	if result.ResponseRate == 0 {
		result.ResponseRate = k.calculateResponseRate(ctx, entityID, agentID)
	}

	// Record decision with full metadata
	k.recordDecisionFull(ctx, entityID, agentID, "signal", fingerprint, "act", tier, totalSignals, result.TopicEntities, string(snapshotJSON))

	// Record surfaced memory IDs for content rotation
	if memIDs := collectSignalMemoryIDs(result); len(memIDs) > 0 {
		_ = k.store.RecordSurfacedMemories(ctx, entityID, agentID, memIDs)
	}

	// Escalation tracking: bump topic surfacing count
	if fingerprint != "" {
		topicLabel := k.buildTopicLabel(result)
		_ = k.store.UpsertTopicSurfacing(ctx, &storage.TopicSurfacing{
			EntityID:   entityID,
			AgentID:    agentID,
			TopicHash:  fingerprint,
			TopicLabel: topicLabel,
		})
		// Read back escalation level
		if ts, err := k.store.GetTopicSurfacing(ctx, entityID, agentID, fingerprint); err == nil && ts != nil {
			result.EscalationLevel = ts.TimesSurfaced
			// Auto-drop after 4 surfacings with no response
			if ts.TimesSurfaced >= 4 && !ts.UserResponded {
				_ = k.store.MarkTopicDropped(ctx, entityID, agentID, fingerprint)
			}
		}
	}

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

	// Apply response rate multiplier — if user doesn't respond, back off harder
	responseRate := k.calculateResponseRate(ctx, entityID, agentID)
	result.ResponseRate = responseRate
	multiplier := responseCooldownMultiplier(responseRate)
	requiredInterval = time.Duration(float64(requiredInterval) * multiplier)

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

	// Time-of-day check for nudges — nudges are low-tier, suppress in evening+
	nudgePeriod := k.currentTimePeriod()
	result.TimePeriod = nudgePeriod
	if tierRank(timePeriodMinTier(nudgePeriod)) > tierRank(TierLow) {
		result.ShouldAct = false
		result.DecisionReason = "suppress_time_period"
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
// v2: Uses behavioral patterns for today + graph traversal, then falls back to continuity-first.
func (k *Keyoku) findNudgeContent(ctx context.Context, entityID, agentID string) string {
	// 0. Pattern-aware: check what the user typically does today
	patterns := k.detectBehavioralPatterns(ctx, entityID)
	if len(patterns) > 0 {
		// Collect pattern topic tags
		var patternTags []string
		for _, p := range patterns {
			patternTags = append(patternTags, p.Topics...)
		}

		// Query memories matching today's typical topics
		if len(patternTags) > 0 {
			memories, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
				EntityID:   entityID,
				AgentID:    agentID,
				Tags:       patternTags,
				States:     []storage.MemoryState{storage.StateActive},
				Limit:      10,
				OrderBy:    "updated_at",
				Descending: true,
			})
			if err == nil {
				for _, m := range memories {
					if m.AccessCount < 3 && m.Importance >= 0.5 {
						return m.Content
					}
				}
			}
		}

		// Graph-enhanced: traverse from pattern entities to related plans
		if k.engine != nil && k.engine.Graph() != nil {
			for _, topic := range patternTags {
				ent, err := k.store.FindEntityByAlias(ctx, entityID, topic)
				if err != nil || ent == nil {
					continue
				}
				edges, err := k.engine.Graph().GetEntityNeighbors(ctx, entityID, ent.ID)
				if err != nil {
					continue
				}
				for _, edge := range edges {
					mentions, err := k.store.GetEntityMentions(ctx, edge.TargetEntity.ID, 5)
					if err != nil {
						continue
					}
					for _, mention := range mentions {
						mem, err := k.store.GetMemory(ctx, mention.MemoryID)
						if err != nil || mem == nil {
							continue
						}
						if mem.State == storage.StateActive && mem.AccessCount < 3 {
							return mem.Content
						}
					}
				}
			}
		}
	}

	// Content rotation cooldown: don't re-surface memories shown in the last 4 hours
	surfaceCooldown := 4 * time.Hour

	// 1. Continuity-first: plans and activities
	continuityTypes := []storage.MemoryType{storage.TypePlan, storage.TypeActivity}
	plans, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		AgentID:    agentID,
		Types:      continuityTypes,
		States:     []storage.MemoryState{storage.StateActive},
		Limit:      10,
		OrderBy:    "updated_at",
		Descending: true,
	})
	if err == nil {
		plans = k.filterSurfacedMemories(ctx, entityID, agentID, plans, surfaceCooldown)
		for _, m := range plans {
			if m.AccessCount < 3 && m.Importance >= 0.5 {
				return m.Content
			}
		}
	}

	// 2. High-importance unsurfaced memories
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

	memories = k.filterSurfacedMemories(ctx, entityID, agentID, memories, surfaceCooldown)

	for _, m := range memories {
		if m.AccessCount < 3 && m.Importance >= 0.6 {
			return m.Content
		}
	}

	// 3. Fall back to most important
	return memories[0].Content
}

// filterSurfacedMemories removes recently-surfaced memories from a list.
// Falls back to the original list if everything would be filtered out.
func (k *Keyoku) filterSurfacedMemories(ctx context.Context, entityID, agentID string, memories []*Memory, cooldown time.Duration) []*Memory {
	if len(memories) == 0 {
		return memories
	}
	surfacedIDs, err := k.store.GetRecentlySurfacedMemoryIDs(ctx, entityID, agentID, cooldown)
	if err != nil || len(surfacedIDs) == 0 {
		return memories
	}
	surfacedSet := make(map[string]bool, len(surfacedIDs))
	for _, id := range surfacedIDs {
		surfacedSet[id] = true
	}
	var filtered []*Memory
	for _, m := range memories {
		if !surfacedSet[m.ID] {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return memories // fallback: don't go silent
	}
	return filtered
}

// buildTopicLabel creates a short human-readable label from signal content.
func (k *Keyoku) buildTopicLabel(result *HeartbeatResult) string {
	// Pick the most relevant memory content as a label
	var content string
	if len(result.PendingWork) > 0 {
		content = result.PendingWork[0].Content
	} else if len(result.Deadlines) > 0 {
		content = result.Deadlines[0].Content
	} else if len(result.GoalProgress) > 0 && result.GoalProgress[0].Plan != nil {
		content = result.GoalProgress[0].Plan.Content
	} else if result.NudgeContext != "" {
		content = result.NudgeContext
	}
	if len(content) > 80 {
		content = content[:80]
	}
	return content
}

// collectSignalMemoryIDs gathers all memory IDs from a heartbeat result's signals.
func collectSignalMemoryIDs(result *HeartbeatResult) []string {
	seen := make(map[string]bool)
	add := func(id string) {
		if id != "" {
			seen[id] = true
		}
	}
	for _, m := range result.PendingWork {
		add(m.ID)
	}
	for _, m := range result.Deadlines {
		add(m.ID)
	}
	for _, m := range result.Scheduled {
		add(m.ID)
	}
	for _, m := range result.StaleMonitors {
		add(m.ID)
	}
	for _, m := range result.Decaying {
		add(m.ID)
	}
	for _, g := range result.GoalProgress {
		if g.Plan != nil {
			add(g.Plan.ID)
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
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
	if len(result.PositiveDeltas) > 0 {
		active[CheckPositiveDeltas] = signalTierMap[CheckPositiveDeltas]
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
	k.recordDecisionFull(ctx, entityID, agentID, triggerCategory, fingerprint, decision, tier, totalSignals, nil, "")
}

func (k *Keyoku) recordDecisionFull(ctx context.Context, entityID, agentID, triggerCategory, fingerprint, decision, tier string, totalSignals int, topicEntities []string, stateSnapshot string) {
	action := &storage.HeartbeatAction{
		EntityID:          entityID,
		AgentID:           agentID,
		TriggerCategory:   triggerCategory,
		SignalFingerprint: fingerprint,
		Decision:          decision,
		UrgencyTier:       tier,
		TotalSignals:      totalSignals,
		TopicEntities:     topicEntities,
		StateSnapshot:     stateSnapshot,
	}
	_ = k.store.RecordHeartbeatAction(ctx, action)
}

// --- Heartbeat v2: Intelligence Functions ---

// calculateResponseRate returns the 7-day response rate (0.0-1.0).
// If insufficient data, assumes the user is responsive (returns 1.0).
func (k *Keyoku) calculateResponseRate(ctx context.Context, entityID, agentID string) float64 {
	rate, total, err := k.store.GetResponseRate(ctx, entityID, agentID, 7)
	if err != nil || total < 3 {
		return 1.0 // not enough data, assume responsive
	}
	return rate
}

// responseCooldownMultiplier returns a multiplier for cooldowns based on response rate.
// Low response rates dramatically increase cooldowns to avoid annoying users.
func responseCooldownMultiplier(rate float64) float64 {
	if rate < 0.1 {
		return 10.0
	}
	if rate < 0.3 {
		return 3.0
	}
	return 1.0
}

// checkResponseTracking checks unchecked heartbeat actions and marks whether the user responded.
// Should be called as a background goroutine.
func (k *Keyoku) checkResponseTracking(ctx context.Context, entityID string) {
	unchecked, err := k.store.GetHeartbeatActionsForResponseCheck(ctx, entityID, 2*time.Hour)
	if err != nil || len(unchecked) == 0 {
		return
	}

	// Get recent session messages (enough to cover the check window)
	msgs, err := k.store.GetRecentSessionMessages(ctx, entityID, 100)
	if err != nil {
		return
	}

	for _, action := range unchecked {
		responded := false
		windowEnd := action.ActedAt.Add(2 * time.Hour)
		for _, msg := range msgs {
			if msg.Role == "user" && msg.CreatedAt.After(action.ActedAt) && msg.CreatedAt.Before(windowEnd) {
				responded = true
				break
			}
		}
		_ = k.store.UpdateHeartbeatActionResponse(ctx, action.ID, responded)
	}
}

// calculateSignalConfluence computes the total signal weight and whether it meets the threshold.
func calculateSignalConfluence(activeSignals map[HeartbeatCheckType]string, autonomy string) (int, bool) {
	totalWeight := 0
	for _, tier := range activeSignals {
		totalWeight += tierWeight[tier]
	}
	threshold, ok := confluenceThreshold[autonomy]
	if !ok {
		threshold = 12
	}
	return totalWeight, totalWeight >= threshold
}

// collectAllMemoryIDs gathers memory IDs from all signal categories in a HeartbeatResult.
func collectAllMemoryIDs(result *HeartbeatResult) []string {
	var ids []string
	for _, m := range result.PendingWork {
		ids = append(ids, m.ID)
	}
	for _, m := range result.Deadlines {
		ids = append(ids, m.ID)
	}
	for _, m := range result.Scheduled {
		ids = append(ids, m.ID)
	}
	for _, m := range result.Decaying {
		ids = append(ids, m.ID)
	}
	for _, c := range result.Conflicts {
		ids = append(ids, c.MemoryA.ID)
	}
	for _, m := range result.StaleMonitors {
		ids = append(ids, m.ID)
	}
	for _, g := range result.GoalProgress {
		ids = append(ids, g.Plan.ID)
	}
	return ids
}

// extractTopicEntities collects entity IDs from all signal memories using entity_mentions.
func (k *Keyoku) extractTopicEntities(ctx context.Context, result *HeartbeatResult) []string {
	memoryIDs := collectAllMemoryIDs(result)
	if len(memoryIDs) == 0 {
		return nil
	}

	entitySet := make(map[string]bool)
	for _, memID := range memoryIDs {
		entities, err := k.store.GetMemoryEntities(ctx, memID)
		if err != nil {
			continue
		}
		for _, ent := range entities {
			entitySet[ent.ID] = true
		}
	}

	ids := make([]string, 0, len(entitySet))
	for id := range entitySet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// shouldSuppressTopicRepeat checks if the current signal's entities overlap >60%
// with entities from recent act decisions (within 6h window).
func (k *Keyoku) shouldSuppressTopicRepeat(ctx context.Context, entityID, agentID string, currentEntities []string) bool {
	if len(currentEntities) == 0 {
		return false
	}

	recentActs, err := k.store.GetRecentActDecisions(ctx, entityID, agentID, 6*time.Hour)
	if err != nil || len(recentActs) == 0 {
		return false
	}

	currentSet := make(map[string]bool)
	for _, id := range currentEntities {
		currentSet[id] = true
	}

	for _, act := range recentActs {
		if len(act.TopicEntities) == 0 {
			continue
		}
		overlap := 0
		for _, id := range act.TopicEntities {
			if currentSet[id] {
				overlap++
			}
		}
		if float64(overlap)/float64(len(currentEntities)) > 0.6 {
			return true
		}
	}
	return false
}

// calculateDeadlineProximity scores deadlines by how close they are.
// Returns the max proximity score (higher = more urgent).
func calculateDeadlineProximity(deadlines []*Memory) float64 {
	maxScore := 0.0
	now := time.Now()
	for _, m := range deadlines {
		if m.ExpiresAt == nil {
			continue
		}
		hours := m.ExpiresAt.Sub(now).Hours()
		if hours < 0 {
			hours = 0.1 // overdue
		}
		score := 1.0 / math.Max(hours, 0.5)
		if score > maxScore {
			maxScore = score
		}
	}
	return maxScore
}

// buildStateSnapshot captures current heartbeat state for delta detection.
func buildStateSnapshot(result *HeartbeatResult) StateSnapshot {
	snap := StateSnapshot{
		GoalStatuses:         make(map[string]string),
		RelationshipSilences: make(map[string]int),
	}
	for _, g := range result.GoalProgress {
		snap.GoalStatuses[g.Plan.ID] = g.Status
	}
	for _, r := range result.Relationships {
		snap.RelationshipSilences[r.Entity.ID] = r.DaysSilent
	}
	if result.Sentiment != nil {
		snap.SentimentDirection = result.Sentiment.Direction
	}
	return snap
}

// goalStatusRank returns a numeric rank for goal status (higher = better).
func goalStatusRank(status string) int {
	switch status {
	case "no_activity":
		return 0
	case "stalled":
		return 1
	case "at_risk":
		return 2
	case "on_track":
		return 3
	default:
		return -1
	}
}

// detectDeltas compares two state snapshots and returns positive changes.
func detectDeltas(current, previous StateSnapshot) []PositiveDelta {
	var deltas []PositiveDelta

	// Goal improvements
	for planID, currentStatus := range current.GoalStatuses {
		prevStatus, exists := previous.GoalStatuses[planID]
		if !exists {
			continue
		}
		if goalStatusRank(currentStatus) > goalStatusRank(prevStatus) {
			deltas = append(deltas, PositiveDelta{
				Type:        "goal_improved",
				Description: fmt.Sprintf("Goal moved from %s to %s", prevStatus, currentStatus),
			})
		}
	}

	// Entity re-engagement (was silent >7 days, now significantly less)
	for entityID, currentSilent := range current.RelationshipSilences {
		prevSilent, exists := previous.RelationshipSilences[entityID]
		if exists && prevSilent > 7 && currentSilent < prevSilent/2 {
			deltas = append(deltas, PositiveDelta{
				Type:        "entity_reengaged",
				Description: fmt.Sprintf("Re-engaged after %d days of silence", prevSilent),
				EntityID:    entityID,
			})
		}
	}

	// Sentiment improvement
	if previous.SentimentDirection == "declining" &&
		(current.SentimentDirection == "stable" || current.SentimentDirection == "improving") {
		deltas = append(deltas, PositiveDelta{
			Type:        "sentiment_improved",
			Description: fmt.Sprintf("Sentiment shifted from %s to %s", previous.SentimentDirection, current.SentimentDirection),
		})
	}

	return deltas
}

// enrichSignalsWithGraph adds knowledge graph context to heartbeat signals.
// For each signal memory, finds entity mentions and their 1-hop relationships,
// producing context lines like "Alice (person) → works_at → ClientCo".
func (k *Keyoku) enrichSignalsWithGraph(ctx context.Context, entityID string, result *HeartbeatResult) []string {
	if k.engine == nil {
		return nil
	}
	memoryIDs := collectAllMemoryIDs(result)
	if len(memoryIDs) == 0 {
		return nil
	}

	graphEngine := k.engine.Graph()
	if graphEngine == nil {
		return nil
	}

	// Collect entities from signal memories
	entityMap := make(map[string]*storage.Entity)
	for _, memID := range memoryIDs {
		entities, err := k.store.GetMemoryEntities(ctx, memID)
		if err != nil {
			continue
		}
		for _, ent := range entities {
			entityMap[ent.ID] = ent
		}
	}

	if len(entityMap) == 0 {
		return nil
	}

	// For each unique entity, get 1-hop relationships
	seen := make(map[string]bool)
	var contextLines []string

	for entID, ent := range entityMap {
		edges, err := graphEngine.GetEntityNeighbors(ctx, entityID, entID)
		if err != nil || len(edges) == 0 {
			continue
		}

		for _, edge := range edges {
			line := fmt.Sprintf("%s (%s) -[%s]-> %s (%s)",
				ent.CanonicalName, ent.Type,
				edge.Relationship.RelationshipType,
				edge.TargetEntity.CanonicalName, edge.TargetEntity.Type)
			if !seen[line] {
				seen[line] = true
				contextLines = append(contextLines, line)
			}
		}
	}

	return contextLines
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
