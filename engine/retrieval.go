// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"context"
	"sort"
	"time"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// EnhancedRetriever provides advanced memory retrieval with graph awareness.
type EnhancedRetriever struct {
	store    storage.Store
	embedder embedder.Embedder
	graph    *GraphEngine
	config   RetrievalConfig
}

// RetrievalConfig holds configuration for enhanced retrieval.
type RetrievalConfig struct {
	MaxResults         int
	MinSimilarity      float64
	RecencyBoostWindow int // hours
	RecencyBoostFactor float64
	ImportanceWeight   float64
	SimilarityWeight   float64
	RecencyWeight      float64
	EnableGraphContext bool
	GraphContextDepth  int
}

// DefaultRetrievalConfig returns default retrieval configuration.
func DefaultRetrievalConfig() RetrievalConfig {
	return RetrievalConfig{
		MaxResults:         20,
		MinSimilarity:      0.5,
		RecencyBoostWindow: 24,
		RecencyBoostFactor: 0.2,
		ImportanceWeight:   0.3,
		SimilarityWeight:   0.5,
		RecencyWeight:      0.2,
		EnableGraphContext: true,
		GraphContextDepth:  1,
	}
}

// NewEnhancedRetriever creates a new enhanced retriever.
func NewEnhancedRetriever(store storage.Store, emb embedder.Embedder, graph *GraphEngine, config RetrievalConfig) *EnhancedRetriever {
	if config.MaxResults <= 0 {
		config.MaxResults = 20
	}
	if config.MinSimilarity <= 0 {
		config.MinSimilarity = 0.5
	}
	if config.RecencyBoostWindow <= 0 {
		config.RecencyBoostWindow = 24
	}
	return &EnhancedRetriever{
		store:    store,
		embedder: emb,
		graph:    graph,
		config:   config,
	}
}

// RetrievalRequest represents a retrieval query.
type RetrievalRequest struct {
	EntityID  string
	Query     string
	Embedding []float32 // pre-computed (optional)

	// Filters
	Types  []string
	Tags   []string
	States []storage.MemoryState

	// Time filters
	Since *time.Time
	Until *time.Time

	// Options
	MaxResults     int
	IncludeDecayed bool

	// Graph options
	IncludeRelated  bool
	RelatedEntities []string

	// Team visibility
	VisibilityFor *storage.VisibilityContext
}

// RetrievalResult contains retrieved memories with scores.
type RetrievalResult struct {
	Memories    []*ScoredMemory
	TotalFound  int
	QueryVector []float32
	Timing      RetrievalTiming
}

// ScoredMemory wraps a memory with its retrieval score.
type ScoredMemory struct {
	Memory         *storage.Memory
	Similarity     float64
	Score          float64
	DecayFactor    float64
	Recency        float64
	GraphRelevance float64
	Source         string // "direct", "related", "graph"
}

// RetrievalTiming tracks timing for retrieval operations.
type RetrievalTiming struct {
	EmbeddingMs int64
	SearchMs    int64
	RankingMs   int64
	GraphMs     int64
	TotalMs     int64
}

// Retrieve performs enhanced memory retrieval.
func (r *EnhancedRetriever) Retrieve(ctx context.Context, req RetrievalRequest) (*RetrievalResult, error) {
	startTime := time.Now()
	result := &RetrievalResult{}

	// Step 1: Get or compute embedding
	embedStart := time.Now()
	var embedding []float32
	var err error

	if len(req.Embedding) > 0 {
		embedding = req.Embedding
	} else if r.embedder != nil {
		embedding, err = r.embedder.Embed(ctx, req.Query)
		if err != nil {
			return nil, err
		}
	}
	result.QueryVector = embedding
	result.Timing.EmbeddingMs = time.Since(embedStart).Milliseconds()

	// Step 2: Semantic search
	searchStart := time.Now()
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = r.config.MaxResults
	}

	searchLimit := maxResults * 3
	if searchLimit < 50 {
		searchLimit = 50
	}

	var allMemories []*ScoredMemory

	if len(embedding) > 0 {
		var similar []*storage.SimilarityResult
		if req.VisibilityFor != nil {
			similar, err = r.store.FindSimilarWithOptions(ctx, embedding, req.EntityID, searchLimit, r.config.MinSimilarity, storage.SimilarityOptions{
				VisibilityFor: req.VisibilityFor,
			})
		} else {
			similar, err = r.store.FindSimilar(ctx, embedding, req.EntityID, searchLimit, r.config.MinSimilarity)
		}
		if err != nil {
			return nil, err
		}
		for _, s := range similar {
			allMemories = append(allMemories, &ScoredMemory{
				Memory:     s.Memory,
				Similarity: s.Similarity,
				Source:     "direct",
			})
		}
	}

	// Include related entity memories if requested
	if req.IncludeRelated && len(req.RelatedEntities) > 0 && len(embedding) > 0 {
		for _, relatedID := range req.RelatedEntities {
			var similar []*storage.SimilarityResult
			if req.VisibilityFor != nil {
				similar, err = r.store.FindSimilarWithOptions(ctx, embedding, relatedID, searchLimit/2, r.config.MinSimilarity, storage.SimilarityOptions{
					VisibilityFor: req.VisibilityFor,
				})
			} else {
				similar, err = r.store.FindSimilar(ctx, embedding, relatedID, searchLimit/2, r.config.MinSimilarity)
			}
			if err != nil {
				continue
			}
			for _, s := range similar {
				allMemories = append(allMemories, &ScoredMemory{
					Memory:     s.Memory,
					Similarity: s.Similarity * 0.8,
					Source:     "related",
				})
			}
		}
	}

	result.Timing.SearchMs = time.Since(searchStart).Milliseconds()

	// Step 3: Apply filters
	filteredMemories := r.applyFilters(allMemories, req)

	// Step 4: Calculate scores and rank
	rankStart := time.Now()
	r.calculateScores(filteredMemories)
	sort.Slice(filteredMemories, func(i, j int) bool {
		return filteredMemories[i].Score > filteredMemories[j].Score
	})
	result.Timing.RankingMs = time.Since(rankStart).Milliseconds()

	// Step 5: Limit and return
	if len(filteredMemories) > maxResults {
		filteredMemories = filteredMemories[:maxResults]
	}

	result.Memories = filteredMemories
	result.TotalFound = len(allMemories)
	result.Timing.TotalMs = time.Since(startTime).Milliseconds()

	return result, nil
}

// RetrieveByType retrieves memories of specific types.
func (r *EnhancedRetriever) RetrieveByType(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	return r.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{storage.StateActive, storage.StateResolved},
		Limit:      limit,
		OrderBy:    "importance",
		Descending: true,
	})
}

// RetrieveRecent retrieves recent memories.
func (r *EnhancedRetriever) RetrieveRecent(ctx context.Context, entityID string, hours int, limit int) ([]*storage.Memory, error) {
	return r.store.GetRecentMemories(ctx, entityID, hours, limit)
}

// RetrieveImportant retrieves the most important memories.
func (r *EnhancedRetriever) RetrieveImportant(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	return r.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{storage.StateActive, storage.StateResolved},
		Limit:      limit,
		OrderBy:    "importance",
		Descending: true,
	})
}

// ContextualRetrieve retrieves memories relevant to conversation context.
func (r *EnhancedRetriever) ContextualRetrieve(ctx context.Context, entityID string, conversationHistory []string, limit int) (*RetrievalResult, error) {
	contextQuery := ""
	for _, msg := range conversationHistory {
		contextQuery += msg + " "
	}
	return r.Retrieve(ctx, RetrievalRequest{
		EntityID:       entityID,
		Query:          contextQuery,
		MaxResults:     limit,
		IncludeDecayed: false,
	})
}

// --- internal ---

func (r *EnhancedRetriever) applyFilters(memories []*ScoredMemory, req RetrievalRequest) []*ScoredMemory {
	filtered := make([]*ScoredMemory, 0, len(memories))

	for _, m := range memories {
		// State filter
		if len(req.States) > 0 {
			found := false
			for _, s := range req.States {
				if m.Memory.State == s {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		} else if !req.IncludeDecayed && m.Memory.State != storage.StateActive && m.Memory.State != storage.StateResolved {
			continue
		}

		// Time filters
		if req.Since != nil && m.Memory.CreatedAt.Before(*req.Since) {
			continue
		}
		if req.Until != nil && m.Memory.CreatedAt.After(*req.Until) {
			continue
		}

		// Tag filter
		if len(req.Tags) > 0 {
			hasTag := false
			for _, reqTag := range req.Tags {
				for _, memTag := range m.Memory.Tags {
					if reqTag == memTag {
						hasTag = true
						break
					}
				}
				if hasTag {
					break
				}
			}
			if !hasTag {
				continue
			}
		}

		filtered = append(filtered, m)
	}

	return filtered
}

func (r *EnhancedRetriever) calculateScores(memories []*ScoredMemory) {
	now := time.Now()
	recencyWindow := time.Duration(r.config.RecencyBoostWindow) * time.Hour

	for _, m := range memories {
		m.DecayFactor = CalculateDecayFactorWithAccess(m.Memory.LastAccessedAt, m.Memory.Stability, m.Memory.AccessCount)

		var recency float64
		if m.Memory.LastAccessedAt != nil {
			age := now.Sub(*m.Memory.LastAccessedAt)
			if age < recencyWindow {
				recency = 1.0 - (float64(age) / float64(recencyWindow))
			}
		} else {
			age := now.Sub(m.Memory.CreatedAt)
			if age < recencyWindow {
				recency = 1.0 - (float64(age) / float64(recencyWindow))
			}
		}
		m.Recency = recency

		m.Score = (m.Similarity * r.config.SimilarityWeight) +
			(m.Memory.Importance * r.config.ImportanceWeight) +
			(recency * r.config.RecencyWeight)

		// Apply decay penalty
		m.Score *= (0.5 + 0.5*m.DecayFactor)

		// Recency boost for very recent memories
		if recency > 0.5 {
			m.Score *= (1 + r.config.RecencyBoostFactor*(recency-0.5))
		}
	}
}
