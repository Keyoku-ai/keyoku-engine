// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DeliveryConfig configures how heartbeat actions are delivered externally.
type DeliveryConfig struct {
	// Method controls delivery: "cli" shells out to an external command,
	// "" (empty) means event-only mode (backward compatible, no external delivery).
	Method string

	// Command is the base command for CLI delivery.
	// Examples: "openclaw", "docker exec kumo openclaw"
	Command string

	// Channel is the delivery channel (e.g. "telegram", "discord").
	Channel string

	// Recipient is the channel-specific target (e.g. Telegram chat ID "-4970078838").
	Recipient string

	// SessionID is the explicit session identifier (e.g. "telegram:group:-4970078838").
	// If empty, it is derived from Channel + Recipient.
	SessionID string

	// Timeout for delivery attempts (default: 30s).
	Timeout time.Duration
}

// Deliverer sends heartbeat results to an external agent system.
type Deliverer interface {
	Deliver(ctx context.Context, entityID string, result *HeartbeatResult) error
}

// NewDeliverer creates the appropriate Deliverer for the given config.
// Returns nil if no delivery method is configured (event-only mode).
func NewDeliverer(config DeliveryConfig) Deliverer {
	switch config.Method {
	case "cli":
		return NewCLIDeliverer(config)
	default:
		return nil
	}
}

// buildDeliveryMessage composes a message from a HeartbeatResult for delivery
// to an external agent. When EnhancedAnalysis is available (from
// runEnhancedLLMAnalysis), the message uses the LLM's structured output which
// is conversation-aware and suppresses already-discussed topics.
func buildDeliveryMessage(result *HeartbeatResult) string {
	// Use enhanced LLM analysis if available
	if ea := result.EnhancedAnalysis; ea != nil {
		if !ea.ShouldAct || ea.ActionBrief == "" {
			return "" // LLM decided nothing worth acting on after context analysis
		}

		var parts []string
		parts = append(parts, fmt.Sprintf("[%s] %s", ea.Urgency, ea.ActionBrief))

		if len(ea.RecommendedActions) > 0 {
			parts = append(parts, "\nActions:")
			for _, action := range ea.RecommendedActions {
				parts = append(parts, fmt.Sprintf("- %s", action))
			}
		}

		if ea.UserFacing != "" {
			parts = append(parts, fmt.Sprintf("\nTell the User:\n%s", ea.UserFacing))
		}

		parts = append(parts, fmt.Sprintf("\nUrgency: %s | Mode: %s", ea.Urgency, ea.Autonomy))

		return strings.Join(parts, "\n")
	}

	// Fallback: programmatic message assembly (no LLM analysis available)
	var parts []string

	// Decision context
	switch result.DecisionReason {
	case "nudge":
		parts = append(parts, "[Nudge] It's been quiet. Check in naturally.")
		if result.NudgeContext != "" {
			parts = append(parts, fmt.Sprintf("Context: %s", truncate(result.NudgeContext, 200)))
		}
	case "act":
		if result.PriorityAction != "" {
			parts = append(parts, fmt.Sprintf("[Priority] %s", result.PriorityAction))
		}
	}

	// Urgency
	if result.Urgency != "" && result.Urgency != "can_wait" {
		parts = append(parts, fmt.Sprintf("Urgency: %s", result.Urgency))
	}

	// Escalation tone guidance
	switch result.EscalationLevel {
	case 1:
		parts = append(parts, "Tone: casual mention")
	case 2:
		parts = append(parts, "Tone: direct, bring it up clearly")
	case 3:
		parts = append(parts, "Tone: offer help, this has come up before")
	}

	// Memory velocity
	if result.MemoryVelocityHigh {
		parts = append(parts, fmt.Sprintf("A lot has happened recently (%d new memories since last check-in).", result.MemoryVelocity))
	}

	// Positive deltas (celebrate wins)
	for _, d := range result.PositiveDeltas {
		parts = append(parts, fmt.Sprintf("[+] %s", d.Description))
	}

	// Summary (the main signal content)
	if result.Summary != "" {
		summary := truncate(result.Summary, 500)
		parts = append(parts, fmt.Sprintf("Signals:\n%s", summary))
	}

	// Time context
	if result.TimePeriod != "" {
		parts = append(parts, fmt.Sprintf("Time: %s", result.TimePeriod))
	}

	if len(parts) == 0 {
		return "" // No concrete signals to deliver — skip rather than send a generic prompt
	}

	return strings.Join(parts, "\n")
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
