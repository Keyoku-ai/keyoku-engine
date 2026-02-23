package engine

import (
	"strings"
	"unicode"
)

// SignificanceConfig configures the significance filter.
type SignificanceConfig struct {
	MinSignificance float64 // minimum score to proceed with extraction (default: 0.3)
	Enabled         bool
}

// DefaultSignificanceConfig returns default significance configuration.
func DefaultSignificanceConfig() SignificanceConfig {
	return SignificanceConfig{
		MinSignificance: 0.3,
		Enabled:         true,
	}
}

// SignificanceResult contains the result of a significance check.
type SignificanceResult struct {
	Score  float64
	Reason string
	Skip   bool
}

// SignificanceScorer evaluates whether content is worth extracting memories from.
type SignificanceScorer struct {
	config SignificanceConfig
}

// NewSignificanceScorer creates a new significance scorer.
func NewSignificanceScorer(config SignificanceConfig) *SignificanceScorer {
	if config.MinSignificance <= 0 {
		config.MinSignificance = 0.3
	}
	return &SignificanceScorer{config: config}
}

// trivialPhrases are common messages with zero memory value.
var trivialPhrases = map[string]bool{
	"ok": true, "okay": true, "k": true,
	"thanks": true, "thank you": true, "thx": true, "ty": true,
	"hello": true, "hi": true, "hey": true, "howdy": true,
	"bye": true, "goodbye": true, "see you": true, "later": true,
	"sure": true, "yeah": true, "yes": true, "no": true, "nah": true,
	"lol": true, "haha": true, "hehe": true, "lmao": true,
	"np": true, "no problem": true, "no worries": true,
	"got it": true, "understood": true, "i see": true,
	"cool": true, "nice": true, "great": true, "awesome": true,
	"hmm": true, "hm": true, "uh": true, "um": true,
	"yep": true, "yup": true, "nope": true,
	"right": true, "exactly": true, "correct": true,
	"sounds good": true, "makes sense": true,
	"good morning": true, "good night": true, "good evening": true,
	"what": true, "why": true, "how": true, "when": true, "where": true,
}

// firstPersonPatterns indicate the user is stating something about themselves.
var firstPersonPatterns = []string{
	"i am", "i'm", "i work", "i live", "i like", "i love", "i hate",
	"i prefer", "i want", "i need", "i have", "i've", "i was",
	"my name", "my job", "my wife", "my husband", "my friend",
	"my favorite", "my hobby", "i enjoy", "i started", "i moved",
	"i changed", "i quit", "i joined", "i bought", "i'm learning",
	"i just", "i recently", "i used to",
}

// temporalIndicators suggest time-sensitive information.
var temporalIndicators = []string{
	"yesterday", "today", "tomorrow", "last week", "next week",
	"last month", "next month", "this morning", "tonight",
	"just now", "recently", "currently", "right now",
	"planning to", "going to", "will be", "about to",
}

// Score evaluates the significance of content for memory extraction.
func (s *SignificanceScorer) Score(content string) SignificanceResult {
	if !s.config.Enabled {
		return SignificanceResult{Score: 1.0, Reason: "filter disabled"}
	}

	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)

	// Empty or near-empty content
	if len(trimmed) == 0 {
		return SignificanceResult{Score: 0.0, Reason: "empty content", Skip: true}
	}

	// Check trivial phrases (exact match on normalized form)
	if trivialPhrases[lower] {
		return SignificanceResult{Score: 0.0, Reason: "trivial phrase", Skip: true}
	}

	// Very short content with no substance
	if len(trimmed) < 15 && !hasProperNoun(trimmed) && !hasNumber(trimmed) {
		return SignificanceResult{Score: 0.1, Reason: "very short, no entities or facts", Skip: true}
	}

	// Short questions without substance
	if strings.HasSuffix(trimmed, "?") && len(trimmed) < 30 && !hasProperNoun(trimmed) {
		return SignificanceResult{Score: 0.2, Reason: "short question without entities", Skip: true}
	}

	// Score boosters
	score := 0.4 // base score for non-trivial content

	// First-person statements (strong signal for personal memories)
	for _, pattern := range firstPersonPatterns {
		if strings.Contains(lower, pattern) {
			score += 0.3
			break
		}
	}

	// Proper nouns (entities to extract)
	if hasProperNoun(trimmed) {
		score += 0.2
	}

	// Numbers (quantitative info)
	if hasNumber(trimmed) {
		score += 0.1
	}

	// Temporal indicators (events, plans)
	for _, indicator := range temporalIndicators {
		if strings.Contains(lower, indicator) {
			score += 0.15
			break
		}
	}

	// Longer content is more likely to contain useful information
	wordCount := len(strings.Fields(trimmed))
	if wordCount >= 10 {
		score += 0.1
	}
	if wordCount >= 20 {
		score += 0.1
	}

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	skip := score < s.config.MinSignificance
	reason := "scored normally"
	if skip {
		reason = "below significance threshold"
	}

	return SignificanceResult{Score: score, Reason: reason, Skip: skip}
}

// ShouldSkip is a convenience method that returns true if content should be skipped.
func (s *SignificanceScorer) ShouldSkip(content string) bool {
	return s.Score(content).Skip
}

func hasProperNoun(text string) bool {
	words := strings.Fields(text)
	for i, word := range words {
		if i == 0 {
			continue // skip first word (sentence start)
		}
		if len(word) > 1 && unicode.IsUpper(rune(word[0])) {
			return true
		}
	}
	return false
}

func hasNumber(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
