package engine

import (
	"sync"
	"time"
)

// TokenBudgetConfig configures the token budget system.
type TokenBudgetConfig struct {
	MaxTokensPerMinute int           // 0 = unlimited
	WindowSize         time.Duration // default: 1 minute
}

// DefaultTokenBudgetConfig returns a config with no limits (unlimited).
func DefaultTokenBudgetConfig() *TokenBudgetConfig {
	return nil // nil means unlimited
}

// TokenBudget tracks and limits LLM token usage per entity.
type TokenBudget struct {
	config     TokenBudgetConfig
	mu         sync.Mutex
	usage      map[string]*tokenWindow
	totalUsage map[string]*TokenUsageStats
}

type tokenEntry struct {
	timestamp time.Time
	tokens    int
}

type tokenWindow struct {
	entries []tokenEntry
}

// TokenUsageStats contains usage statistics for an entity.
type TokenUsageStats struct {
	TotalTokens     int
	TokensLastHour  int
	TokensToday     int
	CallCount       int
	LastCallAt      *time.Time
	BudgetExceeded  int // number of times budget was exceeded
}

// NewTokenBudget creates a new token budget tracker.
func NewTokenBudget(config *TokenBudgetConfig) *TokenBudget {
	if config == nil {
		config = &TokenBudgetConfig{MaxTokensPerMinute: 0} // unlimited
	}
	if config.WindowSize == 0 {
		config.WindowSize = time.Minute
	}
	return &TokenBudget{
		config:     *config,
		usage:      make(map[string]*tokenWindow),
		totalUsage: make(map[string]*TokenUsageStats),
	}
}

// CanSpend checks if the entity has budget remaining for the estimated tokens.
func (tb *TokenBudget) CanSpend(entityID string, estimatedTokens int) bool {
	if tb.config.MaxTokensPerMinute <= 0 {
		return true // unlimited
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(entityID)
	tb.pruneOldEntries(w)

	currentUsage := 0
	for _, e := range w.entries {
		currentUsage += e.tokens
	}

	return currentUsage+estimatedTokens <= tb.config.MaxTokensPerMinute
}

// Record records token usage for an entity.
func (tb *TokenBudget) Record(entityID string, tokens int) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(entityID)
	w.entries = append(w.entries, tokenEntry{
		timestamp: time.Now(),
		tokens:    tokens,
	})

	// Update total stats
	stats := tb.getOrCreateStats(entityID)
	stats.TotalTokens += tokens
	stats.CallCount++
	now := time.Now()
	stats.LastCallAt = &now
}

// RecordExceeded records that a budget check was exceeded.
func (tb *TokenBudget) RecordExceeded(entityID string) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	stats := tb.getOrCreateStats(entityID)
	stats.BudgetExceeded++
}

// GetUsage returns token usage stats for an entity.
func (tb *TokenBudget) GetUsage(entityID string) TokenUsageStats {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	stats := tb.getOrCreateStats(entityID)

	// Calculate windowed stats
	w := tb.getOrCreateWindow(entityID)
	now := time.Now()
	hourAgo := now.Add(-time.Hour)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var lastHour, today int
	for _, e := range w.entries {
		if e.timestamp.After(hourAgo) {
			lastHour += e.tokens
		}
		if e.timestamp.After(dayStart) {
			today += e.tokens
		}
	}

	return TokenUsageStats{
		TotalTokens:    stats.TotalTokens,
		TokensLastHour: lastHour,
		TokensToday:    today,
		CallCount:      stats.CallCount,
		LastCallAt:     stats.LastCallAt,
		BudgetExceeded: stats.BudgetExceeded,
	}
}

// CurrentWindowUsage returns how many tokens have been used in the current window.
func (tb *TokenBudget) CurrentWindowUsage(entityID string) int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	w := tb.getOrCreateWindow(entityID)
	tb.pruneOldEntries(w)

	total := 0
	for _, e := range w.entries {
		total += e.tokens
	}
	return total
}

func (tb *TokenBudget) getOrCreateWindow(entityID string) *tokenWindow {
	w, ok := tb.usage[entityID]
	if !ok {
		w = &tokenWindow{}
		tb.usage[entityID] = w
	}
	return w
}

func (tb *TokenBudget) getOrCreateStats(entityID string) *TokenUsageStats {
	s, ok := tb.totalUsage[entityID]
	if !ok {
		s = &TokenUsageStats{}
		tb.totalUsage[entityID] = s
	}
	return s
}

func (tb *TokenBudget) pruneOldEntries(w *tokenWindow) {
	cutoff := time.Now().Add(-tb.config.WindowSize)
	newEntries := w.entries[:0]
	for _, e := range w.entries {
		if e.timestamp.After(cutoff) {
			newEntries = append(newEntries, e)
		}
	}
	w.entries = newEntries
}
