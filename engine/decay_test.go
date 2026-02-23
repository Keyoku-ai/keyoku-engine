package engine

import (
	"math"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-embedded/storage"
)

func TestCalculateDecayFactor(t *testing.T) {
	t.Run("nil lastAccessedAt", func(t *testing.T) {
		got := CalculateDecayFactor(nil, 60)
		if got != 1.0 {
			t.Errorf("CalculateDecayFactor(nil, 60) = %v, want 1.0", got)
		}
	})

	t.Run("just accessed", func(t *testing.T) {
		now := time.Now()
		got := CalculateDecayFactor(&now, 60)
		if got < 0.99 {
			t.Errorf("CalculateDecayFactor(now, 60) = %v, want ~1.0", got)
		}
	})

	t.Run("e^-1 after stability days", func(t *testing.T) {
		accessed := time.Now().Add(-60 * 24 * time.Hour) // 60 days ago
		got := CalculateDecayFactor(&accessed, 60)
		expected := math.Exp(-1) // ~0.368
		if math.Abs(got-expected) > 0.02 {
			t.Errorf("CalculateDecayFactor(60 days ago, 60) = %v, want ~%v", got, expected)
		}
	})

	t.Run("zero stability defaults to 60", func(t *testing.T) {
		accessed := time.Now().Add(-60 * 24 * time.Hour)
		got := CalculateDecayFactor(&accessed, 0)
		expected := math.Exp(-1)
		if math.Abs(got-expected) > 0.02 {
			t.Errorf("CalculateDecayFactor(60 days ago, 0) = %v, want ~%v", got, expected)
		}
	})

	t.Run("negative stability defaults to 60", func(t *testing.T) {
		accessed := time.Now().Add(-60 * 24 * time.Hour)
		got := CalculateDecayFactor(&accessed, -10)
		expected := math.Exp(-1)
		if math.Abs(got-expected) > 0.02 {
			t.Errorf("CalculateDecayFactor(60 days ago, -10) = %v, want ~%v", got, expected)
		}
	})

	t.Run("future date clamps to 0 days", func(t *testing.T) {
		future := time.Now().Add(24 * time.Hour)
		got := CalculateDecayFactor(&future, 60)
		if got != 1.0 {
			t.Errorf("CalculateDecayFactor(future, 60) = %v, want 1.0", got)
		}
	})
}

func TestGetStabilityForType(t *testing.T) {
	tests := []struct {
		memType storage.MemoryType
		want    float64
	}{
		{storage.TypeIdentity, 365},
		{storage.TypeEphemeral, 1},
		{storage.TypeEvent, 60},
	}

	for _, tt := range tests {
		t.Run(string(tt.memType), func(t *testing.T) {
			got := GetStabilityForType(tt.memType)
			if got != tt.want {
				t.Errorf("GetStabilityForType(%q) = %v, want %v", tt.memType, got, tt.want)
			}
		})
	}
}

func TestStabilityGrowthFactor(t *testing.T) {
	tests := []struct {
		name     string
		days     float64
		want     float64
	}{
		{"less than 1 day", 0.5, 1.05},
		{"exactly 0", 0, 1.05},
		{"3 days", 3, 1.10},
		{"6 days", 6, 1.10},
		{"15 days", 15, 1.20},
		{"29 days", 29, 1.20},
		{"30 days", 30, 1.40},
		{"100 days", 100, 1.40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StabilityGrowthFactor(tt.days)
			if got != tt.want {
				t.Errorf("StabilityGrowthFactor(%v) = %v, want %v", tt.days, got, tt.want)
			}
		})
	}
}

func TestCalculateNewStability(t *testing.T) {
	t.Run("nil lastAccessedAt", func(t *testing.T) {
		got := CalculateNewStability(60, nil)
		// daysSince = 0, growth = 1.05
		want := 60 * 1.05
		if math.Abs(got-want) > 0.001 {
			t.Errorf("CalculateNewStability(60, nil) = %v, want %v", got, want)
		}
	})

	t.Run("accessed recently", func(t *testing.T) {
		now := time.Now()
		got := CalculateNewStability(60, &now)
		want := 60 * 1.05
		if math.Abs(got-want) > 0.1 {
			t.Errorf("CalculateNewStability(60, now) = %v, want ~%v", got, want)
		}
	})

	t.Run("accessed long ago", func(t *testing.T) {
		old := time.Now().Add(-60 * 24 * time.Hour)
		got := CalculateNewStability(60, &old)
		want := 60 * 1.40
		if math.Abs(got-want) > 0.1 {
			t.Errorf("CalculateNewStability(60, 60d ago) = %v, want ~%v", got, want)
		}
	})
}

func TestDetermineDecayState(t *testing.T) {
	tests := []struct {
		name  string
		decay float64
		want  DecayState
	}{
		{"high decay = active", 0.5, DecayStateActive},
		{"at stale threshold = active", 0.3, DecayStateActive},
		{"just below stale = stale", 0.29, DecayStateStale},
		{"at archive threshold = stale", 0.1, DecayStateStale},
		{"below archive = archived", 0.05, DecayStateArchived},
		{"at delete threshold = archived", 0.01, DecayStateArchived},
		{"below delete = deleted", 0.005, DecayStateDeleted},
		{"zero = deleted", 0, DecayStateDeleted},
		{"full retention = active", 1.0, DecayStateActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineDecayState(tt.decay)
			if got != tt.want {
				t.Errorf("DetermineDecayState(%v) = %v, want %v", tt.decay, got, tt.want)
			}
		})
	}
}

func TestHalfLife(t *testing.T) {
	got := HalfLife(60)
	want := 60 * math.Ln2 // ~41.59
	if math.Abs(got-want) > 0.01 {
		t.Errorf("HalfLife(60) = %v, want %v", got, want)
	}
}

func TestTimeUntilDecay(t *testing.T) {
	t.Run("normal target", func(t *testing.T) {
		got := TimeUntilDecay(60, 0.5)
		want := -60 * math.Log(0.5) // ~41.59
		if math.Abs(got-want) > 0.01 {
			t.Errorf("TimeUntilDecay(60, 0.5) = %v, want %v", got, want)
		}
	})

	t.Run("target <= 0", func(t *testing.T) {
		got := TimeUntilDecay(60, 0)
		if got != 0 {
			t.Errorf("TimeUntilDecay(60, 0) = %v, want 0", got)
		}
	})

	t.Run("target >= 1", func(t *testing.T) {
		got := TimeUntilDecay(60, 1.0)
		if got != 0 {
			t.Errorf("TimeUntilDecay(60, 1.0) = %v, want 0", got)
		}
	})

	t.Run("negative target", func(t *testing.T) {
		got := TimeUntilDecay(60, -0.5)
		if got != 0 {
			t.Errorf("TimeUntilDecay(60, -0.5) = %v, want 0", got)
		}
	})
}
