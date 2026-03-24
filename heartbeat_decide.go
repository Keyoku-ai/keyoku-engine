// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

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

// ctxKeyVirtualNow is a context key for passing virtual time to recordDecision.
type ctxKeyVirtualNow struct{}

// virtualNowFromContext returns the virtual time from context, or zero time if not set.
func virtualNowFromContext(ctx context.Context) time.Time {
	if v, ok := ctx.Value(ctxKeyVirtualNow{}).(time.Time); ok {
		return v
	}
	return time.Time{}
}

// evaluateShouldAct determines whether the heartbeat should trigger action.
// v2: Integrates response rate, confluence, topic dedup, proximity, deltas, and graph enrichment.
func (k *Keyoku) evaluateShouldAct(ctx context.Context, entityID string, cfg *heartbeatConfig, result *HeartbeatResult) {
	now := time.Now()
	if !cfg.virtualNow.IsZero() {
		now = cfg.virtualNow
		ctx = context.WithValue(ctx, ctxKeyVirtualNow{}, now)
	}

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
		if p.TopicDedupWindow > 0 {
			params.TopicDedupWindow = p.TopicDedupWindow
		}
	}

	// 0. First-contact mode — very few memories means this is a new user
	memCount, countErr := k.store.GetMemoryCount(ctx)
	if countErr == nil && memCount < 5 {
		result.ShouldAct = true
		result.DecisionReason = "first_contact"
		result.HighestUrgencyTier = TierNormal
		result.TimePeriod = k.currentTimePeriodAt(now)
		k.recordDecision(ctx, entityID, agentID, "first_contact", "", "first_contact", TierNormal, 0)
		return
	}

	// Background: check response tracking for previous actions + cleanup
	go k.checkResponseTracking(ctx, entityID)
	_ = k.store.CleanupOldHeartbeatActions(ctx, 7*24*time.Hour)

	// 1. Deadline proximity — critical deadlines (<1h) force immediate
	if len(result.Deadlines) > 0 {
		proximityScore := calculateDeadlineProximity(result.Deadlines, now)
		if proximityScore > 1.0 { // less than 1 hour remaining
			result.ShouldAct = true
			result.DecisionReason = "act_deadline_critical"
			result.HighestUrgencyTier = TierImmediate
			k.finalizeAct(ctx, entityID, agentID, cfg, result, TierImmediate)
			return
		}
	}

	// 2. Time-of-day awareness — filter signals by minimum urgency tier
	period := k.currentTimePeriodAt(now)
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
	if !k.isUserTypicallyActiveAt(ctx, entityID, now) {
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
			inConversation = now.Sub(recentMsgs[0].CreatedAt) < 15*time.Minute
		}
	}
	if inConversation {
		result.InConversation = true
	}

	// 4. Classify signals
	activeSignals := k.classifyActiveSignals(result)

	// 4a. Signal freshness boost — recent memories get a one-step tier upgrade
	boostSignalFreshness(activeSignals, result, now)

	// 4b. Conversation awareness gradient
	// Default: filter to elevated+ only during active conversation.
	// But if memory velocity is high (lots of new context), lower threshold to Normal
	// so the agent can chime in about what's happening.
	if inConversation && len(activeSignals) > 0 {
		minTier := TierElevated
		if result.MemoryVelocityHigh {
			minTier = TierNormal // user is actively generating context, let agent participate
		}
		conversationSignals := make(map[HeartbeatCheckType]string)
		for checkType, tier := range activeSignals {
			if tierPriority[tier] <= tierPriority[minTier] {
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
	result.ConfluenceThreshold = confluenceThreshold[autonomy]
	confluenceWeight, meetsConfluence := calculateSignalConfluence(activeSignals, autonomy, result)
	result.ConfluenceScore = confluenceWeight

	// 6. Fingerprint
	fingerprint := k.computeSignalFingerprint(result)
	result.SignalFingerprint = fingerprint
	totalSignals := len(activeSignals)

	// 7. Get last "act" decision
	lastAct, err := k.store.GetLastHeartbeatAction(ctx, entityID, agentID, "act")

	// 8. Immediate tier bypasses cooldown but still checks novelty.
	// If the exact same signals were acted on recently (within 30 min), suppress to prevent spam.
	if highestTier == TierImmediate {
		result.CooldownState = "bypassed"
		if err == nil && lastAct != nil && lastAct.SignalFingerprint == fingerprint {
			immediateStaleCooldown := 30 * time.Minute
			if now.Sub(lastAct.ActedAt) < immediateStaleCooldown {
				result.ShouldAct = false
				result.DecisionReason = "suppress_stale"
				k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_stale", highestTier, totalSignals)
				return
			}
		}
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

	// 9b. Low-tier-only signals require confluence to trigger.
	// Without enough combined weight, low signals (patterns, sentiment,
	// relationships, decaying) are informational — not worth acting on.
	if highestTier == TierLow && !meetsConfluence {
		result.ShouldAct = false
		result.DecisionReason = "suppress_low_no_confluence"
		k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_low_no_confluence", highestTier, totalSignals)
		return
	}

	// 10. Response rate → cooldown multiplier
	responseRate := k.calculateResponseRate(ctx, entityID, agentID)
	result.ResponseRate = responseRate
	multiplier := responseCooldownMultiplier(responseRate)

	// 11. Cooldown check (with response rate multiplier)
	// Immediate tier bypasses cooldown entirely — these are time-sensitive (deadlines, scheduled).
	result.CooldownState = "expired"
	if err == nil && lastAct != nil && highestTier != TierImmediate {
		cooldown := params.SignalCooldownNormal
		if highestTier == TierLow {
			cooldown = params.SignalCooldownLow
		} else if highestTier == TierElevated {
			cooldown = params.SignalCooldownElevated
		}
		cooldown = time.Duration(float64(cooldown) * multiplier * timePeriodCooldownMultiplier(period))

		if now.Sub(lastAct.ActedAt) < cooldown {
			result.CooldownState = "active"
			result.ShouldAct = false
			result.DecisionReason = "suppress_cooldown"
			k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_cooldown", highestTier, totalSignals)
			return
		}
	}

	// 12. Novelty check — with time-based escape hatch
	if err == nil && lastAct != nil && lastAct.SignalFingerprint == fingerprint {
		staleDuration := now.Sub(lastAct.ActedAt)
		staleEscapeThreshold := params.SignalCooldownNormal * 2
		if staleDuration < staleEscapeThreshold {
			// Same signals, not enough time has passed — suppress
			result.ShouldAct = false
			result.DecisionReason = "suppress_stale"
			k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_stale", highestTier, totalSignals)
			return
		}
		// Stale escape: same signals, but enough time has passed.
		// Outside a conversation, real signals re-enter the normal act finalization path.
		// No real signals still use nudge. In conversation we continue to suppress.
		if inConversation {
			result.ShouldAct = false
			result.DecisionReason = "suppress_stale"
			return
		}
		realSignals := k.classifyActiveSignals(result)
		if len(realSignals) > 0 {
			result.ShouldAct = true
			result.DecisionReason = "act_stale_escalated"
			result.EscalationLevel++
		} else {
			k.evaluateNudge(ctx, entityID, agentID, autonomy, params, result)
			return
		}
	}

	// 13. Topic entity dedup — compares entities AND fingerprint.
	// Same project + different work (fingerprint changed) = allowed through.
	topicEntities := k.extractTopicEntities(ctx, result)
	result.TopicEntities = topicEntities
	currentSummaryHash := hashSignalSummary(result.Summary)
	// Use TopicDedupWindow if set, otherwise fall back to SignalCooldownNormal
	dedupWindow := params.TopicDedupWindow
	if dedupWindow == 0 {
		dedupWindow = params.SignalCooldownNormal
	}
	if k.shouldSuppressTopicRepeat(ctx, entityID, agentID, topicEntities, currentSummaryHash, fingerprint, dedupWindow) {
		result.ShouldAct = false
		result.DecisionReason = "suppress_topic_repeat"
		k.recordDecision(ctx, entityID, agentID, "signal", fingerprint, "suppress_topic_repeat", highestTier, totalSignals)
		return
	}

	// 14. Passed all checks — act
	result.ShouldAct = true
	if result.DecisionReason == "" {
		result.DecisionReason = "act"
	}
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

	// Record decision with full metadata (include summary hash for content-based dedup)
	summaryHash := hashSignalSummary(result.Summary)
	k.recordDecisionFull(ctx, entityID, agentID, "signal", fingerprint, "act", tier, totalSignals, result.TopicEntities, string(snapshotJSON), summaryHash)

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
			// Auto-drop after 8 surfacings with no response (was 4, but that was
			// too aggressive — users may simply be busy for a day and topics
			// shouldn't be permanently silenced that quickly).
			if ts.TimesSurfaced >= 8 && !ts.UserResponded {
				_ = k.store.MarkTopicDropped(ctx, entityID, agentID, fingerprint)
			}
		}
	}

	// Enhanced LLM analysis with conversation context (falls back to basic prioritization)
	k.runEnhancedLLMAnalysis(ctx, entityID, cfg, result)
}

// classifyActiveSignals returns a map of check type -> tier for signals that are present.
func (k *Keyoku) classifyActiveSignals(result *HeartbeatResult) map[HeartbeatCheckType]string {
	active := make(map[HeartbeatCheckType]string)

	if len(result.Scheduled) > 0 {
		active[CheckScheduled] = adjustTierByTags(signalTierMap[CheckScheduled], result.Scheduled)
	}
	if len(result.Deadlines) > 0 {
		active[CheckDeadlines] = adjustTierByTags(signalTierMap[CheckDeadlines], result.Deadlines)
	}
	if len(result.Conflicts) > 0 {
		active[CheckConflicts] = signalTierMap[CheckConflicts]
	}
	if result.Continuity != nil && result.Continuity.WasInterrupted {
		active[CheckContinuity] = signalTierMap[CheckContinuity]
	}
	if len(result.StaleMonitors) > 0 {
		active[CheckStale] = adjustTierByTags(signalTierMap[CheckStale], result.StaleMonitors)
	}
	if len(result.PendingWork) > 0 {
		active[CheckPendingWork] = adjustTierByTags(signalTierMap[CheckPendingWork], result.PendingWork)
	}
	if len(result.GoalProgress) > 0 {
		active[CheckGoalProgress] = signalTierMap[CheckGoalProgress]
	}
	if len(result.KnowledgeGaps) > 0 {
		active[CheckKnowledge] = signalTierMap[CheckKnowledge]
	}
	if len(result.Decaying) > 0 {
		active[CheckDecaying] = adjustTierByTags(signalTierMap[CheckDecaying], result.Decaying)
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
	if result.MemoryVelocityHigh {
		active[CheckMemoryVelocity] = signalTierMap[CheckMemoryVelocity]
	}

	return active
}

// adjustTierByTags scans memories for tag-based tier modifiers.
// "critical"/"urgent" → boost one tier, "low-priority"/"backlog" → demote one tier.
// The strongest modifier across all memories wins.
func adjustTierByTags(baseTier string, memories []*Memory) string {
	delta := 0
	for _, m := range memories {
		for _, tag := range m.Tags {
			switch tag {
			case "critical", "urgent":
				if delta < 1 {
					delta = 1 // boost
				}
			case "low-priority", "backlog":
				if delta > -1 {
					delta = -1 // demote
				}
			}
		}
		if delta > 0 {
			break // boost wins, no need to check more
		}
	}
	if delta == 0 {
		return baseTier
	}
	return shiftTier(baseTier, delta)
}

// shiftTier moves a tier up (positive delta) or down (negative delta), clamped to valid tiers.
func shiftTier(tier string, delta int) string {
	tiers := []string{TierLow, TierNormal, TierElevated, TierImmediate}
	idx := 0
	for i, t := range tiers {
		if t == tier {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(tiers) {
		idx = len(tiers) - 1
	}
	return tiers[idx]
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
	k.recordDecisionFull(ctx, entityID, agentID, triggerCategory, fingerprint, decision, tier, totalSignals, nil, "", "")
}

func (k *Keyoku) recordDecisionFull(ctx context.Context, entityID, agentID, triggerCategory, fingerprint, decision, tier string, totalSignals int, topicEntities []string, stateSnapshot string, summaryHash string) {
	action := &storage.HeartbeatAction{
		EntityID:           entityID,
		AgentID:            agentID,
		TriggerCategory:    triggerCategory,
		SignalFingerprint:  fingerprint,
		Decision:           decision,
		UrgencyTier:        tier,
		TotalSignals:       totalSignals,
		TopicEntities:      topicEntities,
		StateSnapshot:      stateSnapshot,
		SignalSummaryHash:  summaryHash,
		ActedAt:            virtualNowFromContext(ctx),
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
// Uses linear interpolation: rate 0 → 2x, rate 0.5 → 1x, rate 0.5+ → 1x.
// Capped at 2x to prevent the watcher from going completely silent when combined
// with other multipliers (quiet hours, escalation). The old 10x ceiling caused
// compound penalties (10x response × 10x quiet = 100x cooldown = permanent silence).
func responseCooldownMultiplier(rate float64) float64 {
	const maxMult = 2.0
	if rate >= 0.5 {
		return 1.0
	}
	if rate <= 0 {
		return maxMult
	}
	// Linear interpolation: 2x at 0, 1x at 0.5
	return maxMult - (rate/0.5)*(maxMult-1.0)
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
// When a HeartbeatResult is provided, stability of the underlying memories modulates weights:
// each signal's weight is scaled by (0.5 + avgStability), so high-stability signals carry more weight.
func calculateSignalConfluence(activeSignals map[HeartbeatCheckType]string, autonomy string, result ...*HeartbeatResult) (int, bool) {
	var r *HeartbeatResult
	if len(result) > 0 {
		r = result[0]
	}

	totalWeight := 0
	for checkType, tier := range activeSignals {
		base := tierWeight[tier]
		if r != nil {
			avgStab := avgStabilityForCheck(r, checkType)
			scaled := float64(base) * (0.5 + avgStab)
			totalWeight += int(scaled + 0.5) // round
		} else {
			totalWeight += base
		}
	}
	threshold, ok := confluenceThreshold[autonomy]
	if !ok {
		threshold = 12
	}
	return totalWeight, totalWeight >= threshold
}

// avgStabilityForCheck returns the average stability of memories in a given signal category.
// Returns 0.5 (neutral) if no memories are available.
func avgStabilityForCheck(r *HeartbeatResult, checkType HeartbeatCheckType) float64 {
	var memories []*Memory
	switch checkType {
	case CheckPendingWork:
		memories = r.PendingWork
	case CheckDeadlines:
		memories = r.Deadlines
	case CheckScheduled:
		memories = r.Scheduled
	case CheckDecaying:
		memories = r.Decaying
	case CheckStale:
		memories = r.StaleMonitors
	default:
		return 0.5 // neutral for categories without direct memory lists
	}
	if len(memories) == 0 {
		return 0.5
	}
	sum := 0.0
	for _, m := range memories {
		sum += m.Stability
	}
	return sum / float64(len(memories))
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

// calculateDeadlineProximity scores deadlines by how close they are.
// Returns the max proximity score (higher = more urgent).
func calculateDeadlineProximity(deadlines []*Memory, now time.Time) float64 {
	maxScore := 0.0
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

// hasFreshMemory returns true if any memory was created or updated within the given window.
func hasFreshMemory(memories []*Memory, window time.Duration, now time.Time) bool {
	cutoff := now.Add(-window)
	for _, m := range memories {
		if m.CreatedAt.After(cutoff) || m.UpdatedAt.After(cutoff) {
			return true
		}
	}
	return false
}

// boostSignalFreshness upgrades signal tiers by one level when fresh memories are present.
// Only boosts specific signal types where recency meaningfully changes urgency.
func boostSignalFreshness(activeSignals map[HeartbeatCheckType]string, result *HeartbeatResult, now time.Time) {
	freshWindow := 30 * time.Minute

	boostOne := func(tier string) string {
		switch tier {
		case TierLow:
			return TierNormal
		case TierNormal:
			return TierElevated
		default:
			return tier // already elevated or immediate, no boost
		}
	}

	// PendingWork, GoalProgress, KnowledgeGaps: Normal -> Elevated if fresh
	if _, ok := activeSignals[CheckPendingWork]; ok && hasFreshMemory(result.PendingWork, freshWindow, now) {
		activeSignals[CheckPendingWork] = boostOne(activeSignals[CheckPendingWork])
	}
	if _, ok := activeSignals[CheckGoalProgress]; ok {
		var goalMemories []*Memory
		for _, g := range result.GoalProgress {
			goalMemories = append(goalMemories, g.Plan)
		}
		if hasFreshMemory(goalMemories, freshWindow, now) {
			activeSignals[CheckGoalProgress] = boostOne(activeSignals[CheckGoalProgress])
		}
	}
	if _, ok := activeSignals[CheckKnowledge]; ok {
		cutoff := now.Add(-freshWindow)
		for _, g := range result.KnowledgeGaps {
			if g.AskedAt.After(cutoff) {
				activeSignals[CheckKnowledge] = boostOne(activeSignals[CheckKnowledge])
				break
			}
		}
	}

	// Decaying, Sentiment: Low -> Normal if fresh
	if _, ok := activeSignals[CheckDecaying]; ok && hasFreshMemory(result.Decaying, freshWindow, now) {
		activeSignals[CheckDecaying] = boostOne(activeSignals[CheckDecaying])
	}
}

// hashSignalSummary creates a short hash of the signal summary text for content-based dedup.
func hashSignalSummary(summary string) string {
	if summary == "" {
		return ""
	}
	h := sha256.Sum256([]byte(summary))
	return hex.EncodeToString(h[:8])
}

// buildStateSnapshot captures current heartbeat state for delta detection.
func buildStateSnapshot(result *HeartbeatResult) StateSnapshot {
	snap := StateSnapshot{
		GoalStatuses:         make(map[string]string),
		RelationshipSilences: make(map[string]int),
	}
	// Use unfiltered goals (includes no_activity) so delta detection works across transitions
	goals := result.allGoalProgress
	if len(goals) == 0 {
		goals = result.GoalProgress
	}
	for _, g := range goals {
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
// producing context lines like "Alice (person) -> works_at -> ClientCo".
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

// mapPriorityUrgency converts PrioritizeActions urgency ("immediate"/"soon"/"can_wait")
// to the canonical heartbeat urgency enum ("none"/"low"/"medium"/"high"/"critical").
func mapPriorityUrgency(u string) string {
	switch u {
	case "immediate":
		return "critical"
	case "soon":
		return "high"
	case "can_wait":
		return "low"
	default:
		return ""
	}
}

// tierToUrgency maps HighestUrgencyTier to the canonical heartbeat urgency enum.
// Used as a fallback when LLM prioritization doesn't run or fails.
func tierToUrgency(tier string) string {
	switch tier {
	case TierImmediate:
		return "critical"
	case TierElevated:
		return "high"
	case TierNormal:
		return "medium"
	case TierLow:
		return "low"
	case "confluence":
		return "medium"
	default:
		return "none"
	}
}

// runEnhancedLLMAnalysis gathers conversation context and runs the full
// AnalyzeHeartbeatContext LLM call, which is context-aware and suppresses
// topics already discussed in conversation. Falls back to basic prioritization
// if the LLM call fails.
func (k *Keyoku) runEnhancedLLMAnalysis(ctx context.Context, entityID string, cfg *heartbeatConfig, result *HeartbeatResult) *llm.HeartbeatAnalysisResponse {
	// Ensure a baseline urgency is always set even when LLM analysis is skipped.
	if result != nil && result.Urgency == "" {
		result.Urgency = tierToUrgency(result.HighestUrgencyTier)
	}
	if cfg.llmProvider == nil {
		return nil
	}

	hctx := k.gatherHeartbeatContext(ctx, entityID, result)

	// Build conversation history strings
	var conversationHistory []string
	for _, msg := range hctx.RecentMessages {
		conversationHistory = append(conversationHistory, fmt.Sprintf("[%s] %s: %s",
			msg.CreatedAt.Format("Jan 2 15:04"), msg.Role, truncate(msg.Content, 200)))
	}

	// Build relevant memories strings
	var relevantMemories []string
	for _, mem := range hctx.RelevantMemories {
		relevantMemories = append(relevantMemories, fmt.Sprintf("[importance:%.1f] %s",
			mem.Importance, truncate(mem.Content, 150)))
	}

	// Build the analysis request from the heartbeat result signals
	verbosity := cfg.verbosity
	if verbosity == "" {
		verbosity = llm.VerbosityConversational
	}

	req := llm.HeartbeatAnalysisRequest{
		ActivitySummary:     hctx.SignalSummary,
		RelevantMemories:    relevantMemories,
		ConversationHistory: conversationHistory,
		Autonomy:            cfg.autonomy,
		AgentID:             cfg.agentID,
		EntityID:            entityID,
		TimePeriod:          result.TimePeriod,
		EscalationLevel:     result.EscalationLevel,
		MemoryVelocity:      result.MemoryVelocity,
		SignalUrgencyTier:   result.HighestUrgencyTier,
		SignalCount:         countSignals(result),
		Verbosity:           verbosity,
	}

	// Populate signal lists
	for _, m := range result.Scheduled {
		req.Scheduled = append(req.Scheduled, m.Content)
	}
	for _, m := range result.Deadlines {
		req.Deadlines = append(req.Deadlines, m.Content)
	}
	for _, m := range result.PendingWork {
		req.PendingWork = append(req.PendingWork, m.Content)
	}
	for _, c := range result.Conflicts {
		req.Conflicts = append(req.Conflicts, fmt.Sprintf("%s vs %s", c.MemoryA.Content, c.MemoryB.Content))
	}
	for _, g := range result.GoalProgress {
		req.GoalProgress = append(req.GoalProgress, fmt.Sprintf("[%s] %s", g.Status, g.Plan.Content))
	}
	for _, gap := range result.KnowledgeGaps {
		req.KnowledgeGaps = append(req.KnowledgeGaps, gap.Question)
	}
	req.GraphContext = result.GraphContext
	for _, d := range result.PositiveDeltas {
		req.PositiveDeltas = append(req.PositiveDeltas, d.Description)
	}

	// Bound the LLM call to prevent hanging the entire heartbeat pipeline.
	llmCtx, llmCancel := context.WithTimeout(ctx, 15*time.Second)
	defer llmCancel()

	llmStart := time.Now()
	resp, err := cfg.llmProvider.AnalyzeHeartbeatContext(llmCtx, req)
	llmLatency := time.Since(llmStart)
	if err != nil {
		// Fall back to basic prioritization (uses programmatic urgency, no LLM)
		result.Urgency = tierToUrgency(result.HighestUrgencyTier)
		return nil
	}

	// Apply LLM analysis results to the heartbeat result
	result.PriorityAction = resp.ActionBrief
	result.ActionItems = resp.RecommendedActions
	result.EnhancedAnalysis = resp

	// Populate DeveloperTrace for detailed/debug verbosity
	if verbosity == llm.VerbosityDetailed || verbosity == llm.VerbosityDebug {
		trace := &llm.DeveloperTrace{
			SignalFingerprint:   result.SignalFingerprint,
			DecisionReason:      result.DecisionReason,
			CooldownState:       result.CooldownState,
			ConfluenceScore:     result.ConfluenceScore,
			ConfluenceThreshold: result.ConfluenceThreshold,
			ResponseRate:        result.ResponseRate,
			TimePeriod:          result.TimePeriod,
			EscalationLevel:     result.EscalationLevel,
			MemoryVelocity:      result.MemoryVelocity,
			LLMLatencyMs:        llmLatency.Milliseconds(),
		}
		// Build signal classification map
		activeSignals := k.classifyActiveSignals(result)
		if len(activeSignals) > 0 {
			trace.SignalClassification = make(map[string]string, len(activeSignals))
			for checkType, tier := range activeSignals {
				trace.SignalClassification[string(checkType)] = tier
			}
		}
		// Include raw prompt only at debug verbosity
		if verbosity == llm.VerbosityDebug {
			if rendered, renderErr := llm.RenderHeartbeatPrompt(req); renderErr == nil {
				trace.RawPrompt = rendered
			}
		}
		resp.DeveloperTrace = trace
	}

	// Normalize LLM urgency (canonical: none/low/medium/high/critical)
	// to the legacy values expected by mapPriorityUrgency (e.g., immediate/soon/can_wait).
	normalizedUrgency := strings.TrimSpace(strings.ToLower(resp.Urgency))
	switch normalizedUrgency {
	case "none", "low":
		normalizedUrgency = "can_wait"
	case "medium":
		normalizedUrgency = "soon"
	case "high", "critical":
		normalizedUrgency = "immediate"
	}

	if mapped := mapPriorityUrgency(normalizedUrgency); mapped != "" {
		result.Urgency = mapped
	}

	return resp
}

// HeartbeatContext gathers conversation history and relevant memories
// to provide the LLM analysis step with full context for smarter decisions.
type HeartbeatContext struct {
	RecentMessages   []*storage.SessionMessage // Last N conversation messages
	RelevantMemories []*storage.Memory         // Top memories by importance
	SignalSummary    string                    // Pre-built signal summary from HeartbeatResult
}

// gatherHeartbeatContext collects conversation history and high-importance
// memories for an entity, to be used by LLM analysis during heartbeat delivery.
func (k *Keyoku) gatherHeartbeatContext(ctx context.Context, entityID string, result *HeartbeatResult) *HeartbeatContext {
	hctx := &HeartbeatContext{
		SignalSummary: result.Summary,
	}

	// Fetch recent conversation messages (last 20)
	messages, err := k.store.GetRecentSessionMessages(ctx, entityID, 20)
	if err == nil {
		hctx.RecentMessages = messages
	}

	// Fetch top-importance active memories for this entity
	memories, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{storage.StateActive},
		Limit:      10,
		OrderBy:    "importance",
		Descending: true,
	})
	if err == nil {
		hctx.RelevantMemories = memories
	}

	return hctx
}
