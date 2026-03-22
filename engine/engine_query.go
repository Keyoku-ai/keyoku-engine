// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"context"
	"fmt"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// QueryRequest represents a memory query request.
type QueryRequest struct {
	Query           string
	Limit           int
	Mode            ScorerMode
	AgentID         string
	TeamAware       bool    // When true, resolve team and apply visibility filtering
	TeamID          string  // Team ID for visibility resolution
	MinScore        float64 // Minimum similarity threshold (0 = use default)
	EnableLLMRerank bool    // When true, use LLM to re-rank HNSW results for better accuracy
}

// QueryResult represents a single query result.
type QueryResult struct {
	Memory *storage.Memory
	Score  ScoringResult
}

// Query retrieves memories relevant to a query.
// Uses hybrid retrieval: HNSW embedding search + FTS keyword fallback,
// then fuses results with reciprocal rank fusion for better recall.
func (e *Engine) Query(ctx context.Context, entityID string, req QueryRequest) ([]*QueryResult, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}

	embedding, err := e.embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	scorer := e.scorer
	if req.Mode != "" {
		scorer = NewScorerWithMode(req.Mode)
	}

	candidateCount := req.Limit * 3
	if candidateCount < 30 {
		candidateCount = 30
	}

	var candidates []*storage.SimilarityResult
	opts := storage.SimilarityOptions{
		AgentID: req.AgentID,
	}
	// When team-aware, build visibility context for filtering
	if req.TeamAware && req.AgentID != "" {
		opts.VisibilityFor = &storage.VisibilityContext{
			AgentID: req.AgentID,
			TeamID:  req.TeamID,
		}
	}
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = e.config.DefaultMinScore
	}
	if req.AgentID != "" || req.TeamAware {
		candidates, err = e.store.FindSimilarWithOptions(ctx, embedding, entityID, candidateCount, minScore, opts)
	} else {
		candidates, err = e.store.FindSimilar(ctx, embedding, entityID, candidateCount, minScore)
	}
	if err != nil {
		return nil, fmt.Errorf("similarity search failed: %w", err)
	}

	// Build result set from HNSW candidates
	seenIDs := make(map[string]bool, len(candidates))
	results := make([]*QueryResult, 0, len(candidates))
	for _, c := range candidates {
		seenIDs[c.Memory.ID] = true
		score := scorer.Score(ScoringInput{
			Similarity:     c.Similarity,
			CreatedAt:      c.Memory.CreatedAt,
			LastAccessedAt: c.Memory.LastAccessedAt,
			Stability:      c.Memory.Stability,
			Importance:     c.Memory.Importance,
			Confidence:     c.Memory.Confidence,
			AccessCount:    c.Memory.AccessCount,
		})
		results = append(results, &QueryResult{
			Memory: c.Memory,
			Score:  score,
		})
	}

	// Optional FTS keyword fallback (disabled by default — embedding search handles semantics).
	if e.config.EnableFTSFallback {
		ftsQueries := expandQueryForFTS(req.Query)
		for _, ftsQuery := range ftsQueries {
			ftsResults, ftsErr := e.store.SearchFTSWithOptions(ctx, ftsQuery, entityID, candidateCount, opts)
			if ftsErr != nil || len(ftsResults) == 0 {
				continue
			}
			for _, mem := range ftsResults {
				if seenIDs[mem.ID] {
					continue
				}
				seenIDs[mem.ID] = true
				score := scorer.Score(ScoringInput{
					Similarity:     e.config.FTSBaselineSimilarity,
					CreatedAt:      mem.CreatedAt,
					LastAccessedAt: mem.LastAccessedAt,
					Stability:      mem.Stability,
					Importance:     mem.Importance,
					Confidence:     mem.Confidence,
					AccessCount:    mem.AccessCount,
				})
				results = append(results, &QueryResult{
					Memory: mem,
					Score:  score,
				})
			}
		}
	}

	sortResultsByScore(results)
	results = enforceDiversity(results, e.config.DiversityThreshold, e.store)

	// Optional LLM re-ranking: use the LLM to re-rank results for better accuracy.
	// This adds one LLM call per query but closes the semantic gap that embeddings miss
	// (e.g., "boss" → "VP of Engineering").
	if req.EnableLLMRerank && e.provider != nil && len(results) > 0 {
		reranked, rerankErr := e.llmRerank(ctx, req.Query, results)
		if rerankErr == nil {
			results = reranked
		}
		// On error, silently fall back to embedding-only ordering
	}

	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	// Update access stats
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Memory.ID
	}
	//nolint:errcheck // fire-and-forget stats update
	e.store.UpdateAccessStats(ctx, ids)

	// Spaced repetition: increase stability for accessed memories
	for _, r := range results {
		newStability := CalculateNewStabilityWithAccess(r.Memory.Stability, r.Memory.LastAccessedAt, r.Memory.AccessCount)
		if newStability > r.Memory.Stability {
			//nolint:errcheck // fire-and-forget stability update
			e.store.UpdateStability(ctx, r.Memory.ID, newStability)
		}
	}

	return results, nil
}

// llmRerank uses the LLM provider to re-rank query results by relevance.
func (e *Engine) llmRerank(ctx context.Context, query string, results []*QueryResult) ([]*QueryResult, error) {
	candidates := make([]llm.RerankCandidate, len(results))
	for i, r := range results {
		candidates[i] = llm.RerankCandidate{
			ID:      r.Memory.ID,
			Content: r.Memory.Content,
			Type:    string(r.Memory.Type),
			Score:   r.Score.TotalScore,
		}
	}

	resp, err := e.provider.RerankMemories(ctx, llm.RerankRequest{
		Query:      query,
		Candidates: candidates,
	})
	if err != nil {
		return nil, err
	}

	// Map LLM scores back and blend with original scores
	scoreMap := make(map[string]float64, len(resp.Rankings))
	for _, r := range resp.Rankings {
		scoreMap[r.ID] = r.Score
	}
	for _, r := range results {
		if llmScore, ok := scoreMap[r.Memory.ID]; ok {
			// Blend: 60% LLM relevance + 40% original embedding score
			r.Score.TotalScore = 0.6*llmScore + 0.4*r.Score.TotalScore
		}
	}

	sortResultsByScore(results)
	return results, nil
}

// GetAll returns all memories for the entity.
func (e *Engine) GetAll(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	if limit <= 0 {
		limit = 100
	}
	return e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{storage.StateActive, storage.StateStale, storage.StateResolved},
		Limit:      limit,
		OrderBy:    "created_at",
		Descending: true,
	})
}

// GetByID retrieves a specific memory by ID.
func (e *Engine) GetByID(ctx context.Context, id string) (*storage.Memory, error) {
	return e.store.GetMemory(ctx, id)
}
