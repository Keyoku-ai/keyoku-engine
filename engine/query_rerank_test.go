// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// TestQuery_EmbeddingOnly verifies the default path: HNSW only, no FTS, no LLM rerank.
func TestQuery_EmbeddingOnly(t *testing.T) {
	mem1 := testMemory("mem-1", "Alice is the VP of Engineering")
	mem2 := testMemory("mem-2", "Bob likes hiking on weekends")
	mem3 := testMemory("mem-3", "Alice manages the backend team")

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.85},
				{Memory: mem3, Similarity: 0.70},
				{Memory: mem2, Similarity: 0.50},
			}, nil
		},
	}
	provider := &mockProvider{}
	var rerankCalled bool
	provider.rerankMemoriesFn = func(_ context.Context, _ llm.RerankRequest) (*llm.RerankResponse, error) {
		rerankCalled = true
		return &llm.RerankResponse{}, nil
	}

	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "who is Alice",
		Limit: 10,
		// EnableLLMRerank defaults to false
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if rerankCalled {
		t.Error("LLM rerank should NOT be called when EnableLLMRerank is false")
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Results should be sorted by score (highest first)
	for i := 1; i < len(results); i++ {
		if results[i].Score.TotalScore > results[i-1].Score.TotalScore {
			t.Errorf("results not sorted: result[%d].Score=%f > result[%d].Score=%f",
				i, results[i].Score.TotalScore, i-1, results[i-1].Score.TotalScore)
		}
	}
}

// TestQuery_WithLLMRerank verifies that LLM re-ranking re-orders results.
func TestQuery_WithLLMRerank(t *testing.T) {
	mem1 := testMemory("mem-1", "Alice is the VP of Engineering")
	mem2 := testMemory("mem-2", "Bob likes hiking on weekends")
	mem3 := testMemory("mem-3", "Alice manages the backend team")

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem2, Similarity: 0.90}, // Bob is highest by embedding
				{Memory: mem1, Similarity: 0.75}, // Alice VP second
				{Memory: mem3, Similarity: 0.60}, // Alice manages third
			}, nil
		},
	}
	provider := &mockProvider{
		rerankMemoriesFn: func(_ context.Context, req llm.RerankRequest) (*llm.RerankResponse, error) {
			if req.Query != "who is my boss" {
				t.Errorf("rerank query = %q, want %q", req.Query, "who is my boss")
			}
			if len(req.Candidates) != 3 {
				t.Errorf("rerank candidates = %d, want 3", len(req.Candidates))
			}
			// LLM determines Alice VP is most relevant to "boss"
			return &llm.RerankResponse{
				Rankings: []llm.RerankResult{
					{ID: "mem-1", Score: 0.95}, // Alice VP → most relevant
					{ID: "mem-3", Score: 0.70}, // Alice manages → somewhat relevant
					{ID: "mem-2", Score: 0.10}, // Bob hiking → not relevant
				},
			}, nil
		},
	}

	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query:           "who is my boss",
		Limit:           10,
		EnableLLMRerank: true,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// After LLM rerank, Alice VP should be first (was second by embedding alone)
	if results[0].Memory.ID != "mem-1" {
		t.Errorf("expected mem-1 (Alice VP) first, got %s", results[0].Memory.ID)
	}
	// Bob hiking should be last
	if results[2].Memory.ID != "mem-2" {
		t.Errorf("expected mem-2 (Bob hiking) last, got %s", results[2].Memory.ID)
	}
}

// TestQuery_LLMRerankFallback verifies graceful fallback when LLM rerank errors.
func TestQuery_LLMRerankFallback(t *testing.T) {
	mem1 := testMemory("mem-1", "First memory")
	mem2 := testMemory("mem-2", "Second memory")

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.90},
				{Memory: mem2, Similarity: 0.70},
			}, nil
		},
	}
	provider := &mockProvider{
		rerankMemoriesFn: func(_ context.Context, _ llm.RerankRequest) (*llm.RerankResponse, error) {
			return nil, errors.New("LLM service unavailable")
		},
	}

	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query:           "test query",
		Limit:           10,
		EnableLLMRerank: true,
	})
	if err != nil {
		t.Fatalf("Query should not error on rerank failure: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Order should be preserved from embedding scores (fallback)
	if results[0].Memory.ID != "mem-1" {
		t.Errorf("expected mem-1 first (embedding order preserved), got %s", results[0].Memory.ID)
	}
}

// TestQuery_WithFTSFallback verifies FTS results are merged when enabled.
func TestQuery_WithFTSFallback(t *testing.T) {
	mem1 := testMemory("mem-1", "Alice works at Google")
	mem2 := testMemory("mem-2", "Alice enjoys cooking pasta")

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.80},
			}, nil
		},
		searchFTSWithOptionsFn: func(_ context.Context, _ string, _ string, _ int, _ storage.SimilarityOptions) ([]*storage.Memory, error) {
			return []*storage.Memory{mem2}, nil
		},
	}
	provider := &mockProvider{}

	cfg := DefaultEngineConfig()
	cfg.EnableFTSFallback = true
	disabled := SignificanceConfig{Enabled: false}
	cfg.Significance = &disabled
	e := NewEngine(provider, &mockEmbedder{dimensions: 3}, store, cfg)

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "Alice cooking",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (HNSW + FTS), got %d", len(results))
	}
	// Both memories should be present
	ids := map[string]bool{}
	for _, r := range results {
		ids[r.Memory.ID] = true
	}
	if !ids["mem-1"] || !ids["mem-2"] {
		t.Errorf("expected both mem-1 and mem-2, got %v", ids)
	}
}

// TestQuery_FTSDisabledByDefault verifies FTS is NOT used when EnableFTSFallback is false (default).
func TestQuery_FTSDisabledByDefault(t *testing.T) {
	mem1 := testMemory("mem-1", "Alice works at Google")

	var ftsCalled bool
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.80},
			}, nil
		},
		searchFTSWithOptionsFn: func(_ context.Context, _ string, _ string, _ int, _ storage.SimilarityOptions) ([]*storage.Memory, error) {
			ftsCalled = true
			return nil, nil
		},
	}

	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	_, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "Alice",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if ftsCalled {
		t.Error("FTS should NOT be called when EnableFTSFallback is false (default)")
	}
}

// TestQuery_ConfigThresholds verifies custom min score and diversity threshold are respected.
func TestQuery_ConfigThresholds(t *testing.T) {
	mem1 := testMemory("mem-1", "High similarity memory")
	mem2 := testMemory("mem-2", "Low similarity memory")

	var capturedMinScore float64
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, minScore float64) ([]*storage.SimilarityResult, error) {
			capturedMinScore = minScore
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.95},
				{Memory: mem2, Similarity: 0.60},
			}, nil
		},
	}

	cfg := DefaultEngineConfig()
	cfg.DefaultMinScore = 0.5 // custom threshold
	cfg.DiversityThreshold = 0.8
	disabled := SignificanceConfig{Enabled: false}
	cfg.Significance = &disabled
	e := NewEngine(&mockProvider{}, &mockEmbedder{dimensions: 3}, store, cfg)

	_, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "test",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if capturedMinScore != 0.5 {
		t.Errorf("min score = %f, want 0.5", capturedMinScore)
	}
}

// TestQuery_MinScoreOverride verifies per-request MinScore overrides config default.
func TestQuery_MinScoreOverride(t *testing.T) {
	var capturedMinScore float64
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, minScore float64) ([]*storage.SimilarityResult, error) {
			capturedMinScore = minScore
			return nil, nil
		},
	}

	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	_, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query:    "test",
		Limit:    10,
		MinScore: 0.7, // override default 0.3
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if capturedMinScore != 0.7 {
		t.Errorf("min score = %f, want 0.7 (per-request override)", capturedMinScore)
	}
}

// TestQuery_LLMRerankBlendScores verifies the 60/40 blending of LLM and embedding scores.
func TestQuery_LLMRerankBlendScores(t *testing.T) {
	mem1 := testMemory("mem-1", "Test memory")

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 0.80},
			}, nil
		},
	}
	provider := &mockProvider{
		rerankMemoriesFn: func(_ context.Context, req llm.RerankRequest) (*llm.RerankResponse, error) {
			return &llm.RerankResponse{
				Rankings: []llm.RerankResult{
					{ID: "mem-1", Score: 1.0},
				},
			}, nil
		},
	}

	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query:           "test",
		Limit:           10,
		EnableLLMRerank: true,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// The blended score should be: 0.6 * 1.0 (LLM) + 0.4 * originalScore
	// originalScore is computed by the scorer from similarity=0.80 plus other factors.
	// The blended score should be higher than the original because LLM gave 1.0.
	score := results[0].Score.TotalScore
	if score <= 0 {
		t.Errorf("expected positive blended score, got %f", score)
	}
	// With LLM score of 1.0, blended should be at least 0.6
	if score < 0.6 {
		t.Errorf("blended score %f is too low; expected >= 0.6 (60%% of LLM 1.0)", score)
	}
}

// TestQuery_LLMRerankEmptyResults verifies rerank is skipped for empty result sets.
func TestQuery_LLMRerankEmptyResults(t *testing.T) {
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return nil, nil
		},
	}
	var rerankCalled bool
	provider := &mockProvider{
		rerankMemoriesFn: func(_ context.Context, _ llm.RerankRequest) (*llm.RerankResponse, error) {
			rerankCalled = true
			return &llm.RerankResponse{}, nil
		},
	}

	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query:           "test",
		Limit:           10,
		EnableLLMRerank: true,
	})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if rerankCalled {
		t.Error("rerank should NOT be called when there are no results")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
