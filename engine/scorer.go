package engine

import (
	"math"
	"time"
)

// Scorer handles composite scoring for memory retrieval.
type Scorer struct {
	SemanticWeight   float64
	RecencyWeight    float64
	DecayWeight      float64
	ImportanceWeight float64
	ConfidenceWeight float64
}

// ScorerMode represents different retrieval modes.
type ScorerMode string

const (
	ModeBalanced      ScorerMode = "balanced"
	ModeRecent        ScorerMode = "recent"
	ModeImportant     ScorerMode = "important"
	ModeHistorical    ScorerMode = "historical"
	ModeComprehensive ScorerMode = "comprehensive"
)

func NewScorer() *Scorer {
	return NewScorerWithMode(ModeBalanced)
}

func NewScorerWithMode(mode ScorerMode) *Scorer {
	switch mode {
	case ModeRecent:
		return &Scorer{0.30, 0.50, 0.10, 0.07, 0.03}
	case ModeImportant:
		return &Scorer{0.40, 0.10, 0.10, 0.35, 0.05}
	case ModeHistorical:
		return &Scorer{0.50, 0.05, 0.25, 0.15, 0.05}
	case ModeComprehensive:
		return &Scorer{0.45, 0.15, 0.15, 0.20, 0.05}
	default:
		return &Scorer{0.35, 0.25, 0.20, 0.15, 0.05}
	}
}

type ScoringInput struct {
	Similarity     float64
	CreatedAt      time.Time
	LastAccessedAt *time.Time
	Stability      float64
	Importance     float64
	Confidence     float64
	AccessCount    int
}

type ScoringResult struct {
	TotalScore      float64
	SemanticScore   float64
	RecencyScore    float64
	DecayScore      float64
	ImportanceScore float64
	ConfidenceScore float64
}

func (s *Scorer) Score(input ScoringInput) ScoringResult {
	semanticScore := input.Similarity
	recencyScore := s.calculateRecencyScore(input.CreatedAt, input.LastAccessedAt)
	decayScore := CalculateDecayFactorWithAccess(input.LastAccessedAt, input.Stability, input.AccessCount)
	importanceScore := input.Importance
	confidenceScore := input.Confidence

	totalScore := (semanticScore * s.SemanticWeight) +
		(recencyScore * s.RecencyWeight) +
		(decayScore * s.DecayWeight) +
		(importanceScore * s.ImportanceWeight) +
		(confidenceScore * s.ConfidenceWeight)

	return ScoringResult{
		TotalScore:      totalScore,
		SemanticScore:   semanticScore,
		RecencyScore:    recencyScore,
		DecayScore:      decayScore,
		ImportanceScore: importanceScore,
		ConfidenceScore: confidenceScore,
	}
}

func (s *Scorer) calculateRecencyScore(createdAt time.Time, lastAccessedAt *time.Time) float64 {
	lastActivity := createdAt
	if lastAccessedAt != nil && lastAccessedAt.After(createdAt) {
		lastActivity = *lastAccessedAt
	}
	daysSinceActivity := time.Since(lastActivity).Hours() / 24
	if daysSinceActivity < 0 {
		daysSinceActivity = 0
	}
	return 1.0 / (1.0 + math.Log(daysSinceActivity+1))
}

func (s *Scorer) ScoreBatch(inputs []ScoringInput) []ScoringResult {
	results := make([]ScoringResult, len(inputs))
	for i, input := range inputs {
		results[i] = s.Score(input)
	}
	return results
}
