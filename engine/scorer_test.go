package engine

import (
	"math"
	"testing"
	"time"
)

func TestNewScorerWithMode_WeightsSumToOne(t *testing.T) {
	modes := []ScorerMode{ModeBalanced, ModeRecent, ModeImportant, ModeHistorical, ModeComprehensive}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			s := NewScorerWithMode(mode)
			sum := s.SemanticWeight + s.RecencyWeight + s.DecayWeight + s.ImportanceWeight + s.ConfidenceWeight
			if math.Abs(sum-1.0) > 1e-6 {
				t.Errorf("weights sum = %v, want 1.0", sum)
			}
		})
	}
}

func TestNewScorerWithMode_DefaultIsBalanced(t *testing.T) {
	s := NewScorerWithMode("unknown_mode")
	balanced := NewScorerWithMode(ModeBalanced)
	if s.SemanticWeight != balanced.SemanticWeight {
		t.Errorf("unknown mode SemanticWeight = %v, want %v", s.SemanticWeight, balanced.SemanticWeight)
	}
}

func TestNewScorer_IsBalanced(t *testing.T) {
	s := NewScorer()
	if s.SemanticWeight != 0.35 {
		t.Errorf("NewScorer() SemanticWeight = %v, want 0.35", s.SemanticWeight)
	}
}

func TestNewScorerWithMode_Weights(t *testing.T) {
	tests := []struct {
		mode       ScorerMode
		semantic   float64
		recency    float64
		importance float64
	}{
		{ModeRecent, 0.30, 0.50, 0.07},
		{ModeImportant, 0.40, 0.10, 0.35},
		{ModeHistorical, 0.50, 0.05, 0.15},
		{ModeComprehensive, 0.45, 0.15, 0.20},
		{ModeBalanced, 0.35, 0.25, 0.15},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			s := NewScorerWithMode(tt.mode)
			if s.SemanticWeight != tt.semantic {
				t.Errorf("SemanticWeight = %v, want %v", s.SemanticWeight, tt.semantic)
			}
			if s.RecencyWeight != tt.recency {
				t.Errorf("RecencyWeight = %v, want %v", s.RecencyWeight, tt.recency)
			}
			if s.ImportanceWeight != tt.importance {
				t.Errorf("ImportanceWeight = %v, want %v", s.ImportanceWeight, tt.importance)
			}
		})
	}
}

func TestScorer_Score(t *testing.T) {
	now := time.Now()
	s := NewScorer()

	t.Run("all ones", func(t *testing.T) {
		result := s.Score(ScoringInput{
			Similarity:     1.0,
			CreatedAt:      now,
			LastAccessedAt: &now,
			Stability:      60,
			Importance:     1.0,
			Confidence:     1.0,
		})
		// Total should be close to 1.0 (all components ~1.0)
		if result.TotalScore < 0.8 || result.TotalScore > 1.1 {
			t.Errorf("Score(all ones) = %v, want ~1.0", result.TotalScore)
		}
		if result.SemanticScore != 1.0 {
			t.Errorf("SemanticScore = %v, want 1.0", result.SemanticScore)
		}
		if result.ImportanceScore != 1.0 {
			t.Errorf("ImportanceScore = %v, want 1.0", result.ImportanceScore)
		}
	})

	t.Run("all zeros", func(t *testing.T) {
		old := time.Now().Add(-365 * 24 * time.Hour)
		result := s.Score(ScoringInput{
			Similarity:     0,
			CreatedAt:      old,
			LastAccessedAt: &old,
			Stability:      1,
			Importance:     0,
			Confidence:     0,
		})
		if result.TotalScore > 0.3 {
			t.Errorf("Score(all zeros) = %v, want near 0", result.TotalScore)
		}
	})

	t.Run("nil lastAccessedAt", func(t *testing.T) {
		result := s.Score(ScoringInput{
			Similarity: 0.8,
			CreatedAt:  now,
			Stability:  60,
			Importance: 0.5,
			Confidence: 0.5,
		})
		if result.TotalScore <= 0 {
			t.Error("Score with nil lastAccessedAt should still compute")
		}
	})

	t.Run("different modes produce different scores", func(t *testing.T) {
		input := ScoringInput{
			Similarity:     0.9,
			CreatedAt:      now.Add(-30 * 24 * time.Hour),
			LastAccessedAt: &now,
			Stability:      60,
			Importance:     0.3,
			Confidence:     0.5,
		}
		balanced := NewScorerWithMode(ModeBalanced).Score(input)
		important := NewScorerWithMode(ModeImportant).Score(input)
		if balanced.TotalScore == important.TotalScore {
			t.Error("different modes should produce different scores")
		}
	})
}

func TestScorer_ScoreBatch(t *testing.T) {
	s := NewScorer()
	now := time.Now()

	t.Run("empty input", func(t *testing.T) {
		results := s.ScoreBatch(nil)
		if len(results) != 0 {
			t.Errorf("ScoreBatch(nil) len = %d, want 0", len(results))
		}
	})

	t.Run("multiple inputs", func(t *testing.T) {
		inputs := []ScoringInput{
			{Similarity: 0.9, CreatedAt: now, Stability: 60, Importance: 0.8, Confidence: 0.7},
			{Similarity: 0.5, CreatedAt: now, Stability: 60, Importance: 0.3, Confidence: 0.4},
		}
		results := s.ScoreBatch(inputs)
		if len(results) != 2 {
			t.Fatalf("ScoreBatch len = %d, want 2", len(results))
		}
		if results[0].TotalScore <= results[1].TotalScore {
			t.Error("first result should have higher score")
		}
	})
}
