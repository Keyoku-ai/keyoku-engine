// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"testing"
)

func TestNewSignificanceScorer_DefaultMinSignificance(t *testing.T) {
	s := NewSignificanceScorer(SignificanceConfig{MinSignificance: 0, Enabled: true})
	// Should default to 0.3
	r := s.Score("not much")
	if !r.Skip {
		t.Error("expected skip for short content with default threshold")
	}

	s2 := NewSignificanceScorer(SignificanceConfig{MinSignificance: 0.5, Enabled: true})
	r2 := s2.Score("The team discussed architecture changes in the meeting room today")
	// base(0.4) + proper noun check + temporal + 10+ words
	if r2.Score < 0.5 && !r2.Skip {
		t.Error("expected custom threshold to be respected")
	}
}

func TestScore_FilterDisabled(t *testing.T) {
	s := NewSignificanceScorer(SignificanceConfig{Enabled: false})
	r := s.Score("ok")
	if r.Score != 1.0 {
		t.Errorf("expected score 1.0 when disabled, got %f", r.Score)
	}
	if r.Skip {
		t.Error("expected Skip=false when disabled")
	}
	if r.Reason != "filter disabled" {
		t.Errorf("expected reason 'filter disabled', got %q", r.Reason)
	}
}

func TestScore_EmptyContent(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	for _, content := range []string{"", "   ", "\t\n"} {
		r := s.Score(content)
		if r.Score != 0.0 {
			t.Errorf("expected score 0.0 for %q, got %f", content, r.Score)
		}
		if !r.Skip {
			t.Errorf("expected skip for empty content %q", content)
		}
	}
}

func TestScore_TrivialPhrases(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	phrases := []string{"ok", "thanks", "hello", "bye", "sure", "lol", "got it", "cool", "OK", "Thanks", "HELLO"}
	for _, p := range phrases {
		r := s.Score(p)
		if r.Score != 0.0 {
			t.Errorf("expected score 0.0 for trivial %q, got %f", p, r.Score)
		}
		if !r.Skip {
			t.Errorf("expected skip for trivial %q", p)
		}
		if r.Reason != "trivial phrase" {
			t.Errorf("expected reason 'trivial phrase' for %q, got %q", p, r.Reason)
		}
	}
}

func TestScore_VeryShortContent(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())

	// Short, no proper noun, no number → skip
	r := s.Score("not much")
	if r.Score != 0.1 {
		t.Errorf("expected score 0.1, got %f", r.Score)
	}
	if !r.Skip {
		t.Error("expected skip for very short content")
	}

	// Short but has proper noun → NOT filtered here
	r2 := s.Score("hi Bob")
	if r2.Score == 0.1 {
		t.Error("short content with proper noun should not be filtered as 'very short'")
	}

	// Short but has number → NOT filtered here
	r3 := s.Score("got 5 cats")
	if r3.Score == 0.1 {
		t.Error("short content with number should not be filtered as 'very short'")
	}
}

func TestScore_ShortQuestion(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())

	// Short question, no proper noun, >= 15 chars → score 0.2
	r := s.Score("what is going on here?")
	if r.Score != 0.2 {
		t.Errorf("expected score 0.2 for short question, got %f", r.Score)
	}
	if !r.Skip {
		t.Error("expected skip for short question")
	}

	// Short question WITH proper noun → not filtered as short question
	r2 := s.Score("Where does John live?")
	if r2.Score == 0.2 {
		t.Error("short question with proper noun should not be filtered")
	}
}

func TestScore_FirstPersonBoost(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	r := s.Score("I am a software engineer working on distributed systems")
	// base(0.4) + first-person(0.3) + 10+ words(0.1) = 0.8
	if r.Score < 0.7 {
		t.Errorf("expected first-person boost, score too low: %f", r.Score)
	}
	if r.Skip {
		t.Error("expected no skip for first-person statement")
	}
}

func TestScore_ProperNounBoost(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	r := s.Score("Talked with Sarah about the project deadline for next quarter")
	// base(0.4) + proper noun(0.2) + 10+ words(0.1) = 0.7
	if r.Score < 0.6 {
		t.Errorf("expected proper noun boost, score too low: %f", r.Score)
	}
}

func TestScore_NumberBoost(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	r := s.Score("The meeting has 5 attendees and lasts 2 hours at the office today")
	// base(0.4) + number(0.1) + 10+ words(0.1) + temporal("today", 0.15) = 0.75
	if r.Score < 0.5 {
		t.Errorf("expected number boost, score too low: %f", r.Score)
	}
}

func TestScore_TemporalIndicatorBoost(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	// base(0.4) + temporal("recently", 0.15) = 0.55 minimum
	r := s.Score("the team discussed architecture changes recently in the office")
	if r.Score < 0.55 {
		t.Errorf("expected temporal boost, score too low: %f", r.Score)
	}
	// Verify temporal indicator is actually boosting (without it would be just base + word count)
	r2 := s.Score("the team discussed architecture changes perfectly in the office")
	if r.Score <= r2.Score {
		t.Error("temporal indicator should boost score above non-temporal")
	}
}

func TestScore_WordCountBonus(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())

	// Exactly 9 words → no word count bonus
	r9 := s.Score("word one two three four five six seven eight")
	// base(0.4) only
	if r9.Score != 0.4 {
		t.Errorf("expected score 0.4 for 9 words, got %f", r9.Score)
	}

	// 10 words → +0.1
	r10 := s.Score("word one two three four five six seven eight nine")
	if r10.Score != 0.5 {
		t.Errorf("expected score 0.5 for 10 words, got %f", r10.Score)
	}

	// 20+ words → +0.2 (both bonuses stack)
	r20 := s.Score("word one two three four five six seven eight nine eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty")
	if r20.Score != 0.6 {
		t.Errorf("expected score 0.6 for 20 words, got %f", r20.Score)
	}
}

func TestScore_CapAt1(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	// Trigger all boosters: first person + proper noun + number + temporal + 20+ words
	content := "I am working with Sarah on 5 projects yesterday and the team discussed architecture changes in the main conference room for three hours straight"
	r := s.Score(content)
	if r.Score > 1.0 {
		t.Errorf("expected score capped at 1.0, got %f", r.Score)
	}
	if r.Score != 1.0 {
		t.Errorf("expected score exactly 1.0 with all boosters, got %f", r.Score)
	}
}

func TestScore_BelowThreshold(t *testing.T) {
	s := NewSignificanceScorer(SignificanceConfig{MinSignificance: 0.5, Enabled: true})
	// 9 words, no proper nouns, no numbers, no temporal, no first-person → base only (0.4)
	r := s.Score("word one two three four five six seven eight")
	if r.Score != 0.4 {
		t.Errorf("expected score 0.4, got %f", r.Score)
	}
	if !r.Skip {
		t.Error("expected skip when below custom threshold")
	}
	if r.Reason != "below significance threshold" {
		t.Errorf("expected reason 'below significance threshold', got %q", r.Reason)
	}
}

func TestShouldSkip_Convenience(t *testing.T) {
	s := NewSignificanceScorer(DefaultSignificanceConfig())
	if !s.ShouldSkip("ok") {
		t.Error("expected ShouldSkip=true for trivial phrase")
	}
	if s.ShouldSkip("I am a software engineer working on distributed systems") {
		t.Error("expected ShouldSkip=false for meaningful content")
	}
}

func TestHasProperNoun(t *testing.T) {
	tests := []struct {
		text   string
		expect bool
	}{
		{"hello World", true},   // World is capitalized, not first word
		{"Hello world", false},  // Hello is first word, world is lowercase
		{"a", false},            // single word, first word
		{"", false},             // empty
		{"a b c", false},        // all lowercase
		{"a Big word", true},    // Big is capitalized
	}
	for _, tt := range tests {
		got := hasProperNoun(tt.text)
		if got != tt.expect {
			t.Errorf("hasProperNoun(%q) = %v, want %v", tt.text, got, tt.expect)
		}
	}
}

func TestHasNumber(t *testing.T) {
	tests := []struct {
		text   string
		expect bool
	}{
		{"abc 123", true},
		{"no numbers", false},
		{"", false},
		{"has5inside", true},
	}
	for _, tt := range tests {
		got := hasNumber(tt.text)
		if got != tt.expect {
			t.Errorf("hasNumber(%q) = %v, want %v", tt.text, got, tt.expect)
		}
	}
}
