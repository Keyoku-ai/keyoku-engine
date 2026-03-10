package engine

import (
	"context"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// DuplicateDetector handles detection and resolution of duplicate memories.
type DuplicateDetector struct {
	store    storage.Store
	embedder embedder.Embedder
	config   DuplicateConfig
}

type DuplicateConfig struct {
	SemanticThreshold      float64
	NearDuplicateThreshold float64
	MaxCandidates          int
	EnableSemanticDedup    bool
}

func DefaultDuplicateConfig() DuplicateConfig {
	return DuplicateConfig{
		SemanticThreshold:      0.90, // tighter: catch more semantic duplicates
		NearDuplicateThreshold: 0.80, // tighter: merge more near-duplicates
		MaxCandidates:          10,
		EnableSemanticDedup:    true,
	}
}

func NewDuplicateDetector(store storage.Store, emb embedder.Embedder, config DuplicateConfig) *DuplicateDetector {
	if config.SemanticThreshold <= 0 {
		config.SemanticThreshold = 0.95
	}
	if config.NearDuplicateThreshold <= 0 {
		config.NearDuplicateThreshold = 0.85
	}
	if config.MaxCandidates <= 0 {
		config.MaxCandidates = 10
	}
	return &DuplicateDetector{store: store, embedder: emb, config: config}
}

type DuplicateResult struct {
	IsDuplicate     bool
	IsNearDuplicate bool
	ExistingMemory  *storage.Memory
	Similarity      float64
	Action          string // "skip", "merge", "create"
	Reason          string
}

func (d *DuplicateDetector) CheckDuplicate(ctx context.Context, entityID string, content string, embedding []float32, contentHash string) (*DuplicateResult, error) {
	existing, err := d.store.FindByHash(ctx, entityID, contentHash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &DuplicateResult{
			IsDuplicate: true, ExistingMemory: existing, Similarity: 1.0,
			Action: "skip", Reason: "exact content match (hash)",
		}, nil
	}

	if !d.config.EnableSemanticDedup || len(embedding) == 0 {
		return &DuplicateResult{Action: "create", Reason: "no duplicate found"}, nil
	}

	similar, err := d.store.FindSimilar(ctx, embedding, entityID, d.config.MaxCandidates, d.config.NearDuplicateThreshold)
	if err != nil {
		return nil, err
	}

	if len(similar) == 0 {
		return &DuplicateResult{Action: "create", Reason: "no similar memories found"}, nil
	}

	best := similar[0]

	if best.Similarity >= d.config.SemanticThreshold {
		return &DuplicateResult{
			IsDuplicate: true, ExistingMemory: best.Memory, Similarity: best.Similarity,
			Action: "skip", Reason: "semantic duplicate",
		}, nil
	}

	if best.Similarity >= d.config.NearDuplicateThreshold {
		if isSubsetOf(content, best.Memory.Content) {
			return &DuplicateResult{
				IsDuplicate: true, ExistingMemory: best.Memory, Similarity: best.Similarity,
				Action: "skip", Reason: "content is subset of existing memory",
			}, nil
		}
		return &DuplicateResult{
			IsNearDuplicate: true, ExistingMemory: best.Memory, Similarity: best.Similarity,
			Action: "merge", Reason: "near-duplicate that adds new information",
		}, nil
	}

	return &DuplicateResult{Action: "create", Reason: "no duplicate found"}, nil
}

func isSubsetOf(newContent, existingContent string) bool {
	newLower := strings.ToLower(strings.TrimSpace(newContent))
	existLower := strings.ToLower(strings.TrimSpace(existingContent))

	if len(newLower) < len(existLower) && strings.Contains(existLower, newLower) {
		return true
	}

	newWords := strings.Fields(newLower)
	existWords := strings.Fields(existLower)
	if len(newWords) == 0 {
		return true
	}

	existWordSet := make(map[string]bool)
	for _, w := range existWords {
		existWordSet[w] = true
	}

	matchCount := 0
	for _, w := range newWords {
		if existWordSet[w] {
			matchCount++
		}
	}

	return float64(matchCount)/float64(len(newWords)) >= 0.9
}

func (d *DuplicateDetector) MergeMemories(ctx context.Context, existing *storage.Memory, newContent string, newImportance float64) (*storage.Memory, error) {
	mergedContent := mergeContent(existing.Content, newContent)

	newImportanceVal := existing.Importance
	if newImportance > existing.Importance {
		newImportanceVal = (existing.Importance + newImportance) / 2
	}

	updates := storage.MemoryUpdate{
		Content:    &mergedContent,
		Importance: &newImportanceVal,
	}

	updated, err := d.store.UpdateMemory(ctx, existing.ID, updates)
	if err != nil {
		return nil, err
	}

	// Track provenance: record that this merge was derived from the existing memory
	if updated.DerivedFrom == nil {
		updated.DerivedFrom = storage.StringSlice{}
	}
	if existing.ID != "" {
		updated.DerivedFrom = append(updated.DerivedFrom, existing.ID)
	}

	d.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  existing.ID,
		Operation: "merge",
		Changes: map[string]any{
			"original_content": existing.Content,
			"merged_content":   mergedContent,
			"derived_from":     updated.DerivedFrom,
		},
		Reason: "merged with near-duplicate content",
	})

	return updated, nil
}

func mergeContent(existing, newContent string) string {
	existing = strings.TrimSpace(existing)
	newContent = strings.TrimSpace(newContent)

	if strings.Contains(strings.ToLower(existing), strings.ToLower(newContent)) {
		return existing
	}
	if strings.Contains(strings.ToLower(newContent), strings.ToLower(existing)) {
		return newContent
	}

	existingWords := strings.Fields(existing)
	newWords := strings.Fields(newContent)

	commonPrefix := 0
	for i := 0; i < len(existingWords) && i < len(newWords); i++ {
		if strings.ToLower(existingWords[i]) == strings.ToLower(newWords[i]) {
			commonPrefix = i + 1
		} else {
			break
		}
	}

	if commonPrefix > len(existingWords)/2 {
		return existing + ". Additionally: " + strings.Join(newWords[commonPrefix:], " ")
	}

	return existing + ". " + newContent
}

func (d *DuplicateDetector) FindDuplicatesForConsolidation(ctx context.Context, entityID string, threshold float64) ([][]*storage.Memory, error) {
	if threshold <= 0 {
		threshold = d.config.NearDuplicateThreshold
	}

	query := storage.MemoryQuery{
		EntityID: entityID,
		States:   []storage.MemoryState{storage.StateActive, storage.StateStale},
		Limit:    1000,
		OrderBy:  "created_at",
	}

	memories, err := d.store.QueryMemories(ctx, query)
	if err != nil {
		return nil, err
	}

	if len(memories) < 2 {
		return nil, nil
	}

	groups := make(map[string][]*storage.Memory)
	processed := make(map[string]bool)

	for _, mem := range memories {
		if processed[mem.ID] || len(mem.Embedding) == 0 {
			continue
		}

		// Need to decode embedding from blob for search
		// In embedded mode, FindSimilar delegates to HNSW, so we use query text embedding
		// For consolidation, we look up by embedding stored in HNSW
		// This is a simplification - the HNSW index handles the actual search

		processed[mem.ID] = true
	}

	result := make([][]*storage.Memory, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}

	return result, nil
}
