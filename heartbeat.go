package keyoku

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/keyoku-ai/keyoku-embedded/storage"
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
}

// HeartbeatCheckType represents a specific check to run.
type HeartbeatCheckType string

const (
	CheckPendingWork HeartbeatCheckType = "pending_work"
	CheckDeadlines   HeartbeatCheckType = "deadlines"
	CheckScheduled   HeartbeatCheckType = "scheduled"
	CheckDecaying    HeartbeatCheckType = "decaying"
	CheckConflicts   HeartbeatCheckType = "conflicts"
	CheckStale       HeartbeatCheckType = "stale_monitors"
)

var allChecks = []HeartbeatCheckType{
	CheckPendingWork, CheckDeadlines, CheckScheduled,
	CheckDecaying, CheckConflicts, CheckStale,
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

	result := &HeartbeatResult{}
	checksToRun := make(map[HeartbeatCheckType]bool)
	for _, c := range cfg.checks {
		checksToRun[c] = true
	}

	// 1. PENDING WORK — PLAN/ACTIVITY memories, active state, importance > threshold
	if checksToRun[CheckPendingWork] {
		pending, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			Types:    []storage.MemoryType{storage.TypePlan, storage.TypeActivity},
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults,
			OrderBy:  "importance",
			Descending: true,
		})
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
		allActive, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 5,
			OrderBy:  "importance",
			Descending: true,
		})
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
		allActive, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 5,
		})
		if err == nil {
			now := time.Now()
			for _, m := range allActive {
				if interval, ok := parseCronTag(m.Tags); ok {
					lastAccess := m.CreatedAt
					if m.LastAccessedAt != nil {
						lastAccess = *m.LastAccessedAt
					}
					if now.Sub(lastAccess) >= interval {
						result.Scheduled = append(result.Scheduled, m)
						if len(result.Scheduled) >= cfg.maxResults {
							break
						}
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
		allActive, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 5,
		})
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
		plans, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
			EntityID: entityID,
			AgentID:  cfg.agentID,
			Types:    []storage.MemoryType{storage.TypePlan},
			States:   []storage.MemoryState{storage.StateActive},
			Limit:    cfg.maxResults * 3,
		})
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

	// Determine if action is needed
	result.ShouldAct = len(result.PendingWork) > 0 ||
		len(result.Deadlines) > 0 ||
		len(result.Scheduled) > 0 ||
		len(result.Decaying) > 0 ||
		len(result.Conflicts) > 0 ||
		len(result.StaleMonitors) > 0

	// Build summary if action is needed
	if result.ShouldAct {
		result.Summary = buildSummary(result)
	}

	return result, nil
}

// --- helpers ---

// parseCronTag looks for cron:* tags and returns the interval.
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

func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

func buildSummary(result *HeartbeatResult) string {
	var parts []string

	if len(result.PendingWork) > 0 {
		parts = append(parts, fmt.Sprintf("PENDING WORK (%d):", len(result.PendingWork)))
		for _, m := range result.PendingWork {
			parts = append(parts, fmt.Sprintf("  - [%s] %s (importance: %.2f)", m.Type, m.Content, m.Importance))
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
			parts = append(parts, fmt.Sprintf("  - %s", m.Content))
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

	return strings.Join(parts, "\n")
}
