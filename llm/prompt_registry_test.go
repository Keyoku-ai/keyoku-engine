// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package llm

import (
	"strings"
	"testing"
)

func TestRenderHeartbeatPrompt_SelectsTemplateByVerbosity(t *testing.T) {
	tests := []struct {
		name      string
		verbosity HeartbeatVerbosity
		want      string
	}{
		{name: "conversational", verbosity: VerbosityConversational, want: "produce a brief, casual one-sentence message"},
		{name: "standard", verbosity: VerbosityStandard, want: "produce an action brief"},
		{name: "detailed", verbosity: VerbosityDetailed, want: "produce a detailed, evidence-backed action brief"},
		{name: "debug", verbosity: VerbosityDebug, want: "DEBUG mode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, err := RenderHeartbeatPrompt(HeartbeatAnalysisRequest{
				Verbosity:        tt.verbosity,
				Autonomy:         "suggest",
				ActivitySummary:  "Ship release",
				PendingWork:      []string{"Ship release"},
				SignalUrgencyTier: "normal",
				SignalCount:      1,
			})
			if err != nil {
				t.Fatalf("RenderHeartbeatPrompt() error = %v", err)
			}
			if !strings.Contains(prompt, tt.want) {
				t.Errorf("prompt missing template marker %q", tt.want)
			}
		})
	}
}

func TestRenderHeartbeatPrompt_FallsBackToConversational(t *testing.T) {
	tests := []struct {
		name      string
		verbosity HeartbeatVerbosity
	}{
		{name: "empty", verbosity: ""},
		{name: "unknown", verbosity: HeartbeatVerbosity("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, err := RenderHeartbeatPrompt(HeartbeatAnalysisRequest{Verbosity: tt.verbosity})
			if err != nil {
				t.Fatalf("RenderHeartbeatPrompt() error = %v", err)
			}
			if !strings.Contains(prompt, "brief, casual one-sentence message") {
				t.Errorf("fallback prompt did not use conversational template: %q", prompt)
			}
		})
	}
}

func TestRenderHeartbeatPrompt_TemplateContract(t *testing.T) {
	prompt, err := RenderHeartbeatPrompt(HeartbeatAnalysisRequest{
		Verbosity:         VerbosityDebug,
		Autonomy:          "act",
		ActivitySummary:   "Prepare launch",
		PendingWork:       []string{"Finish launch checklist"},
		RecentMessages:    []string{"Need to finish launch checklist"},
		ConversationHistory: []string{"[Mar 21 09:00] user: I already handled the billing issue."},
		TimePeriod:        "working",
		EscalationLevel:   2,
		MemoryVelocity:    3,
		SignalUrgencyTier: "elevated",
		SignalCount:       2,
	})
	if err != nil {
		t.Fatalf("RenderHeartbeatPrompt() error = %v", err)
	}

	if !strings.Contains(prompt, "Recent Conversation History") {
		t.Error("prompt missing conversation history section")
	}
	if !strings.Contains(prompt, "elevated (2 active signals)") {
		t.Error("prompt missing urgency tier helper output")
	}
	if !strings.Contains(prompt, "- Finish launch checklist") {
		t.Error("prompt missing formatted list output")
	}
	if !strings.Contains(prompt, "2") {
		t.Error("prompt missing escalation formatting")
	}
}
