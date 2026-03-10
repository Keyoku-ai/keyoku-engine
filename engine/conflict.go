// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// ConflictDetector identifies and resolves conflicts between memories.
type ConflictDetector struct {
	store    storage.Store
	provider llm.Provider
	config   ConflictConfig
}

type ConflictConfig struct {
	SimilarityThreshold    float64
	MaxCandidates          int
	EnableLLMConflictCheck bool
}

func DefaultConflictConfig() ConflictConfig {
	return ConflictConfig{
		SimilarityThreshold:    0.6,
		MaxCandidates:          10,
		EnableLLMConflictCheck: true,
	}
}

func NewConflictDetector(store storage.Store, provider llm.Provider, config ConflictConfig) *ConflictDetector {
	if config.SimilarityThreshold <= 0 {
		config.SimilarityThreshold = 0.6
	}
	if config.MaxCandidates <= 0 {
		config.MaxCandidates = 10
	}
	return &ConflictDetector{store: store, provider: provider, config: config}
}

type Conflict struct {
	NewContent      string
	ExistingMemory  *storage.Memory
	ConflictType    ConflictType
	Confidence      float64
	Resolution      ConflictResolution
	ResolvedContent string
	Explanation     string
}

type ConflictType string

const (
	ConflictTypeContradiction ConflictType = "contradiction"
	ConflictTypeUpdate        ConflictType = "update"
	ConflictTypeTemporal      ConflictType = "temporal"
	ConflictTypePartial       ConflictType = "partial"
)

type ConflictResolution string

const (
	ResolutionKeepExisting ConflictResolution = "keep_existing"
	ResolutionUseNew       ConflictResolution = "use_new"
	ResolutionMerge        ConflictResolution = "merge"
	ResolutionKeepBoth     ConflictResolution = "keep_both"
	ResolutionAskUser      ConflictResolution = "ask_user"
)

type ConflictResult struct {
	HasConflict       bool
	Conflicts         []Conflict
	RecommendedAction ConflictResolution
}

func (d *ConflictDetector) DetectConflicts(ctx context.Context, entityID string, newContent string, embedding []float32, memoryType storage.MemoryType) (*ConflictResult, error) {
	result := &ConflictResult{Conflicts: []Conflict{}}

	similar, err := d.store.FindSimilar(ctx, embedding, entityID, d.config.MaxCandidates, d.config.SimilarityThreshold)
	if err != nil {
		return nil, fmt.Errorf("failed to find similar memories: %w", err)
	}

	if len(similar) == 0 {
		return result, nil
	}

	for _, sim := range similar {
		conflict := d.checkConflict(ctx, newContent, sim.Memory, sim.Similarity, memoryType)
		if conflict != nil {
			result.Conflicts = append(result.Conflicts, *conflict)
			result.HasConflict = true
		}
	}

	if result.HasConflict {
		result.RecommendedAction = d.determineOverallResolution(result.Conflicts)
	}

	return result, nil
}

func (d *ConflictDetector) checkConflict(ctx context.Context, newContent string, existing *storage.Memory, similarity float64, newType storage.MemoryType) *Conflict {
	// Fast path: pattern matching (free, instant)
	if hasNegationPattern(newContent, existing.Content) {
		return &Conflict{
			NewContent: newContent, ExistingMemory: existing,
			ConflictType: ConflictTypeContradiction, Confidence: 0.8,
			Resolution: ResolutionUseNew, Explanation: "direct negation detected",
		}
	}

	if isTemporalConflict(newContent, existing) {
		return &Conflict{
			NewContent: newContent, ExistingMemory: existing,
			ConflictType: ConflictTypeTemporal, Confidence: 0.7,
			Resolution: ResolutionUseNew, Explanation: "newer information about same subject",
		}
	}

	if existing.Type == storage.TypePreference && newType == storage.TypePreference {
		if isSameSubject(newContent, existing.Content) {
			return &Conflict{
				NewContent: newContent, ExistingMemory: existing,
				ConflictType: ConflictTypeUpdate, Confidence: 0.75,
				Resolution: ResolutionUseNew, Explanation: "preference update for same subject",
			}
		}
	}

	// Heuristic check (numbers, booleans)
	heuristic := d.heuristicConflictCheck(newContent, existing)
	if heuristic != nil {
		return heuristic
	}

	// LLM escalation: only when pattern matching found nothing but similarity is suspicious
	// Similarity 0.6-0.85 with same subject = ambiguous zone worth checking with LLM
	if d.config.EnableLLMConflictCheck && d.provider != nil &&
		similarity >= 0.6 && similarity <= 0.85 && isSameSubject(newContent, existing.Content) {
		return d.llmConflictCheck(ctx, newContent, existing, newType)
	}

	return nil
}

// llmConflictCheck escalates to the LLM for semantic conflict detection.
func (d *ConflictDetector) llmConflictCheck(ctx context.Context, newContent string, existing *storage.Memory, newType storage.MemoryType) *Conflict {
	resp, err := d.provider.DetectConflict(ctx, llm.ConflictCheckRequest{
		NewContent:      newContent,
		ExistingContent: existing.Content,
		MemoryType:      string(newType),
	})
	if err != nil {
		// LLM failure is non-fatal - fall back to no conflict
		return nil
	}

	if !resp.Contradicts {
		return nil
	}

	conflictType := ConflictType(resp.ConflictType)
	switch conflictType {
	case ConflictTypeContradiction, ConflictTypeUpdate, ConflictTypeTemporal, ConflictTypePartial:
		// valid
	default:
		return nil
	}

	resolution := ConflictResolution(resp.Resolution)
	switch resolution {
	case ResolutionUseNew, ResolutionKeepExisting, ResolutionMerge, ResolutionKeepBoth:
		// valid
	default:
		resolution = ResolutionAskUser
	}

	return &Conflict{
		NewContent:   newContent,
		ExistingMemory: existing,
		ConflictType: conflictType,
		Confidence:   resp.Confidence,
		Resolution:   resolution,
		Explanation:  resp.Explanation,
	}
}

func hasNegationPattern(newContent, existingContent string) bool {
	newLower := strings.ToLower(newContent)
	existLower := strings.ToLower(existingContent)

	negationPatterns := []struct{ positive, negative string }{
		{"likes", "doesn't like"}, {"likes", "does not like"}, {"likes", "dislikes"},
		{"loves", "hates"}, {"loves", "doesn't love"},
		{"prefers", "doesn't prefer"}, {"prefers", "avoids"},
		{"wants", "doesn't want"}, {"enjoys", "doesn't enjoy"},
		{"is", "is not"}, {"is", "isn't"},
		{"can", "cannot"}, {"can", "can't"},
		{"will", "won't"}, {"will", "will not"},
		{"has", "doesn't have"}, {"has", "has no"},
	}

	for _, p := range negationPatterns {
		if (strings.Contains(existLower, p.positive) && strings.Contains(newLower, p.negative)) ||
			(strings.Contains(existLower, p.negative) && strings.Contains(newLower, p.positive)) {
			return true
		}
	}

	if strings.Contains(newLower, "no longer") || strings.Contains(newLower, "not anymore") ||
		strings.Contains(newLower, "stopped") || strings.Contains(newLower, "quit") {
		return true
	}

	return false
}

func isTemporalConflict(newContent string, existing *storage.Memory) bool {
	newLower := strings.ToLower(newContent)
	indicators := []string{
		"now", "currently", "recently", "just", "today",
		"this week", "this month", "this year",
		"changed to", "switched to", "moved to",
		"started", "began", "now using",
	}
	for _, indicator := range indicators {
		if strings.Contains(newLower, indicator) && isSameSubject(newContent, existing.Content) {
			return true
		}
	}
	return false
}

func isSameSubject(content1, content2 string) bool {
	words1 := strings.Fields(strings.ToLower(content1))
	words2 := strings.Fields(strings.ToLower(content2))

	skipWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true,
		"i": true, "me": true, "my": true, "mine": true, "we": true,
		"you": true, "your": true, "yours": true, "he": true, "she": true,
		"it": true, "they": true, "them": true, "their": true,
		"this": true, "that": true, "these": true, "those": true,
		"and": true, "or": true, "but": true, "if": true, "then": true,
		"to": true, "of": true, "in": true, "on": true, "at": true,
		"by": true, "for": true, "with": true, "about": true, "from": true,
	}

	meaningful1 := make(map[string]bool)
	meaningful2 := make(map[string]bool)
	for _, w := range words1 {
		if len(w) > 2 && !skipWords[w] {
			meaningful1[w] = true
		}
	}
	for _, w := range words2 {
		if len(w) > 2 && !skipWords[w] {
			meaningful2[w] = true
		}
	}

	overlap := 0
	for w := range meaningful1 {
		if meaningful2[w] {
			overlap++
		}
	}

	minLen := len(meaningful1)
	if len(meaningful2) < minLen {
		minLen = len(meaningful2)
	}
	if minLen == 0 {
		return false
	}
	return float64(overlap)/float64(minLen) >= 0.3
}

func (d *ConflictDetector) heuristicConflictCheck(newContent string, existing *storage.Memory) *Conflict {
	newLower := strings.ToLower(newContent)
	existingLower := strings.ToLower(existing.Content)

	newNumbers := extractNumbers(newLower)
	existingNumbers := extractNumbers(existingLower)
	if len(newNumbers) > 0 && len(existingNumbers) > 0 && isSameSubject(newContent, existing.Content) {
		for _, n1 := range newNumbers {
			for _, n2 := range existingNumbers {
				if n1 != n2 {
					return &Conflict{
						NewContent: newContent, ExistingMemory: existing,
						ConflictType: ConflictTypeUpdate, Confidence: 0.6,
						Resolution: ResolutionUseNew,
						Explanation: fmt.Sprintf("quantitative difference: %s vs %s", n1, n2),
					}
				}
			}
		}
	}

	boolPatterns := []struct{ positive, negative []string }{
		{[]string{"yes", "agree", "correct", "true", "will", "can", "do"}, []string{"no", "disagree", "wrong", "false", "won't", "can't", "don't"}},
		{[]string{"always", "every", "all"}, []string{"never", "none", "no"}},
		{[]string{"more", "increase", "higher"}, []string{"less", "decrease", "lower"}},
	}

	for _, bp := range boolPatterns {
		if isSameSubject(newContent, existing.Content) {
			if (containsAny(newLower, bp.positive) && containsAny(existingLower, bp.negative)) ||
				(containsAny(newLower, bp.negative) && containsAny(existingLower, bp.positive)) {
				return &Conflict{
					NewContent: newContent, ExistingMemory: existing,
					ConflictType: ConflictTypeContradiction, Confidence: 0.7,
					Resolution: ResolutionUseNew, Explanation: "boolean/quantitative contradiction detected",
				}
			}
		}
	}

	return nil
}

func extractNumbers(text string) []string {
	var numbers []string
	for _, word := range strings.Fields(text) {
		clean := strings.Trim(word, ".,!?;:'\"")
		if len(clean) > 0 {
			isNumeric := true
			for _, c := range clean {
				if c < '0' || c > '9' {
					isNumeric = false
					break
				}
			}
			if isNumeric {
				numbers = append(numbers, clean)
			}
		}
	}
	return numbers
}

func containsAny(text string, substrings []string) bool {
	for _, s := range substrings {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

func (d *ConflictDetector) determineOverallResolution(conflicts []Conflict) ConflictResolution {
	if len(conflicts) == 0 {
		return ResolutionKeepBoth
	}
	resolutionCounts := make(map[ConflictResolution]int)
	var highestConfidence float64
	var highestRes ConflictResolution
	for _, c := range conflicts {
		resolutionCounts[c.Resolution]++
		if c.Confidence > highestConfidence {
			highestConfidence = c.Confidence
			highestRes = c.Resolution
		}
	}
	if highestConfidence >= 0.8 {
		return highestRes
	}
	maxCount := 0
	var maxRes ConflictResolution
	for res, count := range resolutionCounts {
		if count > maxCount {
			maxCount = count
			maxRes = res
		}
	}
	if maxCount > len(conflicts)/2 {
		return maxRes
	}
	return ResolutionAskUser
}

func (d *ConflictDetector) ResolveConflict(ctx context.Context, conflict Conflict, resolution ConflictResolution) error {
	switch resolution {
	case ResolutionKeepExisting:
		return nil
	case ResolutionUseNew:
		updates := storage.MemoryUpdate{Content: &conflict.NewContent}
		_, err := d.store.UpdateMemory(ctx, conflict.ExistingMemory.ID, updates)
		if err != nil {
			return fmt.Errorf("failed to update memory: %w", err)
		}
		d.store.LogHistory(ctx, &storage.HistoryEntry{
			MemoryID: conflict.ExistingMemory.ID, Operation: "conflict_resolution",
			Changes: map[string]any{
				"conflict_type": conflict.ConflictType,
				"old_content":   conflict.ExistingMemory.Content,
				"new_content":   conflict.NewContent,
				"resolution":    resolution,
			},
			Reason: conflict.Explanation,
		})
		return nil
	case ResolutionMerge:
		mergedContent := conflict.ResolvedContent
		if mergedContent == "" {
			mergedContent = conflict.ExistingMemory.Content + " (Updated: " + conflict.NewContent + ")"
		}
		updates := storage.MemoryUpdate{Content: &mergedContent}
		_, err := d.store.UpdateMemory(ctx, conflict.ExistingMemory.ID, updates)
		if err != nil {
			return fmt.Errorf("failed to merge memories: %w", err)
		}
		d.store.LogHistory(ctx, &storage.HistoryEntry{
			MemoryID: conflict.ExistingMemory.ID, Operation: "conflict_merge",
			Changes: map[string]any{"conflict_type": conflict.ConflictType, "merged_content": mergedContent},
			Reason: "merged conflicting information",
		})
		return nil
	case ResolutionKeepBoth, ResolutionAskUser:
		return nil
	default:
		return fmt.Errorf("unknown resolution type: %s", resolution)
	}
}
