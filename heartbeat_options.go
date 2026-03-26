// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
)

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

// WithAutoAckScheduled controls whether due schedules are acknowledged
// during HeartbeatCheck itself (default: true). Set false when an
// integration wants to ack only after successful downstream delivery.
func WithAutoAckScheduled(enabled bool) HeartbeatOption {
	return func(c *heartbeatConfig) { c.autoAckScheduled = enabled }
}

// WithVirtualNow overrides time.Now() for all signal computation.
// Used by the demo recording script to simulate heartbeat at different points in time.
func WithVirtualNow(t time.Time) HeartbeatOption {
	return func(c *heartbeatConfig) { c.virtualNow = t }
}

// WithSignalsOnly skips the evaluateShouldAct decision pipeline and forces ShouldAct=true.
// Used when the watcher already decided to act and the delivery path just needs fresh signals
// without re-running cooldown/novelty/topic-dedup checks that would suppress.
func WithSignalsOnly(signalsOnly bool) HeartbeatOption {
	return func(c *heartbeatConfig) { c.signalsOnly = signalsOnly }
}

// WithMinConfidence sets the minimum confidence for memories to be included in signals (default: 0.5).
func WithMinConfidence(f float64) HeartbeatOption {
	return func(c *heartbeatConfig) { c.minConfidence = f }
}

// WithVerbosity sets the verbosity level for heartbeat LLM analysis.
func WithVerbosity(v string) HeartbeatOption {
	return func(c *heartbeatConfig) { c.verbosity = llm.ParseVerbosity(v) }
}

func WithLLMPrioritization(provider llm.Provider, agentContext, entityContext string) HeartbeatOption {
	return func(c *heartbeatConfig) {
		c.llmProvider = provider
		c.agentContext = agentContext
		c.entityContext = entityContext
	}
}
