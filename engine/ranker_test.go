// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package engine

import (
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestRankCalculator_Rank(t *testing.T) {
	r := &RankCalculator{}

	now := time.Now()
	yesterday := now.Add(-12 * time.Hour)
	lastWeek := now.Add(-3 * 24 * time.Hour)
	lastMonth := now.Add(-15 * 24 * time.Hour)
	longAgo := now.Add(-60 * 24 * time.Hour)

	tests := []struct {
		name       string
		mem        *storage.Memory
		wantMin    float64
		wantMax    float64
	}{
		{
			name: "high importance, recent, many accesses",
			mem: &storage.Memory{
				Importance:     0.9,
				AccessCount:    50,
				LastAccessedAt: &yesterday,
			},
			wantMin: 5.0, // 0.9 * (1 + ln(51)) * 2.0
			wantMax: 10.0,
		},
		{
			name: "low importance, old, no accesses",
			mem: &storage.Memory{
				Importance:     0.1,
				AccessCount:    0,
				LastAccessedAt: &longAgo,
			},
			wantMin: 0.05,
			wantMax: 0.2,
		},
		{
			name: "medium importance, last week access",
			mem: &storage.Memory{
				Importance:     0.5,
				AccessCount:    10,
				LastAccessedAt: &lastWeek,
			},
			wantMin: 1.0,
			wantMax: 3.0,
		},
		{
			name: "nil last accessed",
			mem: &storage.Memory{
				Importance:  0.5,
				AccessCount: 5,
			},
			wantMin: 0.4,
			wantMax: 2.0,
		},
		{
			name: "high importance, last month",
			mem: &storage.Memory{
				Importance:     0.8,
				AccessCount:    20,
				LastAccessedAt: &lastMonth,
			},
			wantMin: 2.0,
			wantMax: 5.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Rank(tt.mem)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("Rank() = %f, want between %f and %f", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestRankCalculator_RelativeOrdering(t *testing.T) {
	r := &RankCalculator{}

	now := time.Now()
	recent := now.Add(-1 * time.Hour)
	old := now.Add(-60 * 24 * time.Hour)

	// High importance + recent + many accesses should rank higher
	highRank := &storage.Memory{
		Importance:     0.9,
		AccessCount:    100,
		LastAccessedAt: &recent,
	}
	// Low importance + old + no accesses should rank lower
	lowRank := &storage.Memory{
		Importance:     0.1,
		AccessCount:    0,
		LastAccessedAt: &old,
	}

	high := r.Rank(highRank)
	low := r.Rank(lowRank)

	if high <= low {
		t.Errorf("Expected high rank (%f) > low rank (%f)", high, low)
	}
}

func TestRankCalculator_RankMemories(t *testing.T) {
	r := &RankCalculator{}

	now := time.Now()
	recent := now.Add(-1 * time.Hour)
	old := now.Add(-60 * 24 * time.Hour)

	memories := []*storage.Memory{
		{ID: "low", Importance: 0.1, AccessCount: 0, LastAccessedAt: &old},
		{ID: "high", Importance: 0.9, AccessCount: 50, LastAccessedAt: &recent},
		{ID: "mid", Importance: 0.5, AccessCount: 10, LastAccessedAt: &recent},
	}

	ranked := r.RankMemories(memories)

	if len(ranked) != 3 {
		t.Fatalf("Expected 3 ranked memories, got %d", len(ranked))
	}
	if ranked[0].Memory.ID != "high" {
		t.Errorf("Expected first to be 'high', got '%s'", ranked[0].Memory.ID)
	}
	if ranked[2].Memory.ID != "low" {
		t.Errorf("Expected last to be 'low', got '%s'", ranked[2].Memory.ID)
	}
}

func TestRecencyBoost(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		accessed *time.Time
		want     float64
	}{
		{"nil", nil, 1.0},
		{"today", ptr(now.Add(-6 * time.Hour)), 2.0},
		{"this week", ptr(now.Add(-3 * 24 * time.Hour)), 1.5},
		{"this month", ptr(now.Add(-15 * 24 * time.Hour)), 1.2},
		{"long ago", ptr(now.Add(-60 * 24 * time.Hour)), 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recencyBoostFor(tt.accessed)
			if got != tt.want {
				t.Errorf("recencyBoostFor() = %f, want %f", got, tt.want)
			}
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }
