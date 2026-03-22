// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"sort"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

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

// filterSurfacedMemoriesStrict removes recently-surfaced memories from a list.
// Returns nil if everything is filtered out (caller should treat as "no signals").
func (k *Keyoku) filterSurfacedMemoriesStrict(ctx context.Context, entityID, agentID string, memories []*Memory, cooldown time.Duration) []*Memory {
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
	return filtered // may be nil — that's intentional
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
	sort.Strings(ids)
	return ids
}

// shouldSuppressTopicRepeat checks for topic repetition using two layers:
// Layer 1: Content hash — exact same summary text = suppress (precise, no false positives)
// Layer 2: Entity overlap 85% within dedup window (configurable, default 1h) — but ONLY if the
//          signal fingerprint also matches. This prevents "same project, different work"
//          from being suppressed. Same entities + different fingerprint = new work on same
//          project = allow through.
func (k *Keyoku) shouldSuppressTopicRepeat(ctx context.Context, entityID, agentID string, currentEntities []string, currentSummaryHash string, currentFingerprint string, dedupWindow ...time.Duration) bool {
	window := 1 * time.Hour
	if len(dedupWindow) > 0 && dedupWindow[0] > 0 {
		window = dedupWindow[0]
	}
	recentActs, err := k.store.GetRecentActDecisions(ctx, entityID, agentID, window)
	if err != nil || len(recentActs) == 0 {
		return false
	}

	// Layer 1: Content hash match — same summary = same topic, regardless of entities
	if currentSummaryHash != "" {
		for _, act := range recentActs {
			if act.SignalSummaryHash != "" && act.SignalSummaryHash == currentSummaryHash {
				return true
			}
		}
	}

	// Layer 2: Entity overlap (fallback) — only suppresses when fingerprint also matches.
	// If the fingerprint changed, the underlying work is different even if the project is the same.
	if len(currentEntities) == 0 {
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
		// Different fingerprint = different work, even if same entities. Allow through.
		if currentFingerprint != "" && act.SignalFingerprint != "" && act.SignalFingerprint != currentFingerprint {
			continue
		}
		overlap := 0
		for _, id := range act.TopicEntities {
			if currentSet[id] {
				overlap++
			}
		}
		// 85% overlap + same fingerprint = truly the same topic repeating
		if float64(overlap)/float64(len(currentEntities)) > 0.85 {
			return true
		}
	}
	return false
}
