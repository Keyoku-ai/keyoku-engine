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

func TestCalculateDecayFactorWithAccess(t *testing.T) {
	t.Run("zero access count matches base decay", func(t *testing.T) {
		accessed := time.Now().Add(-60 * 24 * time.Hour)
		base := CalculateDecayFactor(&accessed, 60)
		withAccess := CalculateDecayFactorWithAccess(&accessed, 60, 0)
		if math.Abs(base-withAccess) > 0.001 {
			t.Errorf("zero access should match base: got %v vs %v", withAccess, base)
		}
	})

	t.Run("high access count slows decay significantly", func(t *testing.T) {
		accessed := time.Now().Add(-60 * 24 * time.Hour) // 60 days ago
		base := CalculateDecayFactorWithAccess(&accessed, 60, 0)
		with10 := CalculateDecayFactorWithAccess(&accessed, 60, 10)
		with50 := CalculateDecayFactorWithAccess(&accessed, 60, 50)

		// 10 accesses should meaningfully slow decay
		if with10 <= base {
			t.Errorf("10 accesses should slow decay: base=%v, with10=%v", base, with10)
		}
		// 50 accesses should slow it even more
		if with50 <= with10 {
			t.Errorf("50 accesses should slow more than 10: with10=%v, with50=%v", with10, with50)
		}
		// With 50 accesses, 60-day memory should still be active (>0.3)
		if with50 < DecayThresholdStale {
			t.Errorf("heavily accessed memory at 60 days should still be active: %v", with50)
		}
	})

	t.Run("access frequency extends effective stability", func(t *testing.T) {
		// A CONTEXT memory (21 day stability) accessed 20 times should survive
		// much longer than 21 days.
		accessed := time.Now().Add(-30 * 24 * time.Hour) // 30 days ago
		factor := CalculateDecayFactorWithAccess(&accessed, 21, 20)
		// effective_stability = 21 × (1 + ln(21) × 0.5) ≈ 21 × 2.52 ≈ 53 days
		// decay = e^(-30/53) ≈ 0.57 → still active
		if factor < DecayThresholdStale {
			t.Errorf("CONTEXT with 20 accesses at 30 days should be active: %v", factor)
		}
	})
}

func TestGetStabilityForType(t *testing.T) {
	tests := []struct {
		memType storage.MemoryType
		want    float64
	}{
		{storage.TypeIdentity, 365},
		{storage.TypePreference, 270},
		{storage.TypeRelationship, 270},
		{storage.TypeEvent, 120},
		{storage.TypeActivity, 90},
		{storage.TypePlan, 60},
		{storage.TypeContext, 21},
		{storage.TypeEphemeral, 3},
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
		name string
		days float64
		want float64
	}{
		{"less than 1 day", 0.5, 1.10},
		{"exactly 0", 0, 1.10},
		{"3 days", 3, 1.20},
		{"6 days", 6, 1.20},
		{"15 days", 15, 1.35},
		{"29 days", 29, 1.35},
		{"30 days", 30, 1.60},
		{"100 days", 100, 1.60},
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

func TestStabilityGrowthFactorWithAccess(t *testing.T) {
	t.Run("zero access equals base growth", func(t *testing.T) {
		base := StabilityGrowthFactor(5)
		withAccess := StabilityGrowthFactorWithAccess(5, 0)
		// ln(1+0) = 0, so compound bonus = 1.0 (no change)
		if math.Abs(base-withAccess) > 0.001 {
			t.Errorf("zero access should match base: %v vs %v", withAccess, base)
		}
	})

	t.Run("high access adds compound bonus", func(t *testing.T) {
		base := StabilityGrowthFactor(5)
		with100 := StabilityGrowthFactorWithAccess(5, 100)
		if with100 <= base {
			t.Errorf("100 accesses should add bonus: base=%v, with100=%v", base, with100)
		}
		// Bonus should be moderate, not runaway
		if with100 > base*1.20 {
			t.Errorf("bonus should be moderate (max 15%%): base=%v, with100=%v", base, with100)
		}
	})
}

func TestCalculateNewStability(t *testing.T) {
	t.Run("nil lastAccessedAt", func(t *testing.T) {
		got := CalculateNewStability(60, nil)
		// daysSince = 0, growth = 1.10 (enhanced)
		want := 60 * 1.10
		if math.Abs(got-want) > 0.001 {
			t.Errorf("CalculateNewStability(60, nil) = %v, want %v", got, want)
		}
	})

	t.Run("accessed recently", func(t *testing.T) {
		now := time.Now()
		got := CalculateNewStability(60, &now)
		want := 60 * 1.10
		if math.Abs(got-want) > 0.1 {
			t.Errorf("CalculateNewStability(60, now) = %v, want ~%v", got, want)
		}
	})

	t.Run("accessed long ago", func(t *testing.T) {
		old := time.Now().Add(-60 * 24 * time.Hour)
		got := CalculateNewStability(60, &old)
		want := 60 * 1.60
		if math.Abs(got-want) > 0.1 {
			t.Errorf("CalculateNewStability(60, 60d ago) = %v, want ~%v", got, want)
		}
	})
}

func TestCalculateAccessBurstImportanceBoost(t *testing.T) {
	t.Run("no boost for low access count", func(t *testing.T) {
		now := time.Now()
		got := CalculateAccessBurstImportanceBoost(2, &now)
		if got != 0 {
			t.Errorf("2 accesses should not boost: %v", got)
		}
	})

	t.Run("no boost for nil last accessed", func(t *testing.T) {
		got := CalculateAccessBurstImportanceBoost(10, nil)
		if got != 0 {
			t.Errorf("nil access should not boost: %v", got)
		}
	})

	t.Run("no boost if last access was days ago", func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour)
		got := CalculateAccessBurstImportanceBoost(10, &old)
		if got != 0 {
			t.Errorf("old access should not boost: %v", got)
		}
	})

	t.Run("boost for frequent recent access", func(t *testing.T) {
		now := time.Now()
		got := CalculateAccessBurstImportanceBoost(10, &now)
		if got <= 0 {
			t.Errorf("10 recent accesses should boost: %v", got)
		}
		if got > 0.3 {
			t.Errorf("boost should be capped at 0.3: %v", got)
		}
	})

	t.Run("boost scales with access count", func(t *testing.T) {
		now := time.Now()
		boost5 := CalculateAccessBurstImportanceBoost(5, &now)
		boost20 := CalculateAccessBurstImportanceBoost(20, &now)
		if boost20 <= boost5 {
			t.Errorf("more accesses should mean more boost: 5=%v, 20=%v", boost5, boost20)
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

// --- Scenario tests: Real-world AI agent memory lifecycle ---

func TestScenario_ContextMemoryWithFrequentAccess(t *testing.T) {
	// CONTEXT memory (21d stability) accessed 30 times over 2 weeks.
	// Should still be active at day 30 due to access-frequency modifier.
	accessed := time.Now().Add(-30 * 24 * time.Hour)
	factor := CalculateDecayFactorWithAccess(&accessed, 21, 30)

	if factor < DecayThresholdStale {
		t.Errorf("heavily accessed CONTEXT at 30 days should be active: factor=%v", factor)
	}
}

func TestScenario_EphemeralDecaysWithoutAccess(t *testing.T) {
	// EPHEMERAL memory (3d stability) with no access for 5 days → should be stale.
	accessed := time.Now().Add(-5 * 24 * time.Hour)
	factor := CalculateDecayFactorWithAccess(&accessed, 3, 0)

	state := DetermineDecayState(factor)
	if state != DecayStateStale && state != DecayStateArchived {
		t.Errorf("unaccessed EPHEMERAL at 5 days should be stale/archived: factor=%v, state=%v", factor, state)
	}
}

func TestScenario_IdentityNeverDecays(t *testing.T) {
	// IDENTITY memory (365d stability) accessed 5 times, 180 days ago.
	accessed := time.Now().Add(-180 * 24 * time.Hour)
	factor := CalculateDecayFactorWithAccess(&accessed, 365, 5)

	if factor < DecayThresholdStale {
		t.Errorf("IDENTITY at 180 days with 5 accesses should still be active: factor=%v", factor)
	}
}
