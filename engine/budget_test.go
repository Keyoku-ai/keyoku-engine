// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"sync"
	"testing"
	"time"
)

func TestNewTokenBudget_NilConfig(t *testing.T) {
	tb := NewTokenBudget(nil)
	if !tb.CanSpend("any-entity", 999999) {
		t.Error("nil config should mean unlimited budget")
	}
}

func TestNewTokenBudget_DefaultWindowSize(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 100, WindowSize: 0})
	if tb.config.WindowSize != time.Minute {
		t.Errorf("expected default window size 1m, got %v", tb.config.WindowSize)
	}
}

func TestCanSpend_Unlimited(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 0})
	if !tb.CanSpend("entity-1", 999999) {
		t.Error("MaxTokensPerMinute=0 should be unlimited")
	}
}

func TestCanSpend_WithinBudget(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 1000, WindowSize: time.Minute})
	tb.Record("entity-1", 500)
	if !tb.CanSpend("entity-1", 400) {
		t.Error("500+400=900 is within 1000 budget")
	}
}

func TestCanSpend_ExceedsBudget(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 1000, WindowSize: time.Minute})
	tb.Record("entity-1", 800)
	if tb.CanSpend("entity-1", 300) {
		t.Error("800+300=1100 exceeds 1000 budget")
	}
}

func TestCanSpend_DifferentEntities(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 1000, WindowSize: time.Minute})
	tb.Record("entity-1", 900)
	if !tb.CanSpend("entity-2", 500) {
		t.Error("different entities should have separate budgets")
	}
}

func TestCanSpend_WindowPruning(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 100, WindowSize: 200 * time.Millisecond})
	tb.Record("e", 80)
	if tb.CanSpend("e", 80) {
		t.Error("80+80=160 should exceed 100 budget before pruning")
	}
	time.Sleep(250 * time.Millisecond)
	if !tb.CanSpend("e", 80) {
		t.Error("after window elapsed, old entries should be pruned")
	}
}

func TestRecord_UpdatesStats(t *testing.T) {
	tb := NewTokenBudget(nil)
	tb.Record("entity-1", 100)
	tb.Record("entity-1", 200)
	stats := tb.GetUsage("entity-1")
	if stats.TotalTokens != 300 {
		t.Errorf("expected TotalTokens=300, got %d", stats.TotalTokens)
	}
	if stats.CallCount != 2 {
		t.Errorf("expected CallCount=2, got %d", stats.CallCount)
	}
	if stats.LastCallAt == nil {
		t.Error("expected LastCallAt to be set")
	}
}

func TestRecordExceeded(t *testing.T) {
	tb := NewTokenBudget(nil)
	tb.RecordExceeded("entity-1")
	tb.RecordExceeded("entity-1")
	stats := tb.GetUsage("entity-1")
	if stats.BudgetExceeded != 2 {
		t.Errorf("expected BudgetExceeded=2, got %d", stats.BudgetExceeded)
	}
}

func TestGetUsage_EmptyEntity(t *testing.T) {
	tb := NewTokenBudget(nil)
	stats := tb.GetUsage("nonexistent")
	if stats.TotalTokens != 0 || stats.CallCount != 0 || stats.BudgetExceeded != 0 {
		t.Error("expected zeroed stats for nonexistent entity")
	}
	if stats.LastCallAt != nil {
		t.Error("expected nil LastCallAt for nonexistent entity")
	}
}

func TestGetUsage_TokensLastHour_TokensToday(t *testing.T) {
	tb := NewTokenBudget(nil)
	tb.Record("entity-1", 500)
	stats := tb.GetUsage("entity-1")
	if stats.TokensLastHour != 500 {
		t.Errorf("expected TokensLastHour=500, got %d", stats.TokensLastHour)
	}
	if stats.TokensToday != 500 {
		t.Errorf("expected TokensToday=500, got %d", stats.TokensToday)
	}
}

func TestCurrentWindowUsage(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 10000, WindowSize: time.Minute})
	tb.Record("entity-1", 100)
	tb.Record("entity-1", 200)
	usage := tb.CurrentWindowUsage("entity-1")
	if usage != 300 {
		t.Errorf("expected CurrentWindowUsage=300, got %d", usage)
	}
}

func TestCurrentWindowUsage_AfterPrune(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 10000, WindowSize: 200 * time.Millisecond})
	tb.Record("entity-1", 100)
	time.Sleep(250 * time.Millisecond)
	usage := tb.CurrentWindowUsage("entity-1")
	if usage != 0 {
		t.Errorf("expected CurrentWindowUsage=0 after prune, got %d", usage)
	}
}

func TestTokenBudget_ConcurrentAccess(t *testing.T) {
	tb := NewTokenBudget(&TokenBudgetConfig{MaxTokensPerMinute: 100000, WindowSize: time.Minute})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			entity := "entity-concurrent"
			for j := 0; j < 100; j++ {
				tb.Record(entity, 10)
				tb.CanSpend(entity, 10)
				tb.CurrentWindowUsage(entity)
				tb.GetUsage(entity)
			}
		}(i)
	}
	wg.Wait()
	// If we get here without panicking, the test passes
}
