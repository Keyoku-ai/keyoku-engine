// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func newTestRetriever(store *mockStore, emb *mockEmbedder) *EnhancedRetriever {
	graph := NewGraphEngine(store, DefaultGraphConfig())
	return NewEnhancedRetriever(store, emb, graph, DefaultRetrievalConfig())
}

func TestRetrieve_HappyPath(t *testing.T) {
	mem := testMemory("mem-1", "User likes Go")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.9},
			}, nil
		},
	}
	emb := &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.5, 0.5, 0.5}, nil
		},
		dimensions: 3,
	}
	r := newTestRetriever(store, emb)

	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID:   "entity-1",
		Query:      "Go programming",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Retrieve error = %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("Memories = %d, want 1", len(result.Memories))
	}
	if result.Memories[0].Memory.ID != "mem-1" {
		t.Errorf("Memory ID = %q, want %q", result.Memories[0].Memory.ID, "mem-1")
	}
	if result.Memories[0].Source != "direct" {
		t.Errorf("Source = %q, want %q", result.Memories[0].Source, "direct")
	}
	if result.TotalFound != 1 {
		t.Errorf("TotalFound = %d, want 1", result.TotalFound)
	}
}

func TestRetrieve_PreComputedEmbedding(t *testing.T) {
	var embedCalled bool
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: testMemory("mem-1", "test"), Similarity: 0.8},
			}, nil
		},
	}
	emb := &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			embedCalled = true
			return []float32{0.5, 0.5, 0.5}, nil
		},
		dimensions: 3,
	}
	r := newTestRetriever(store, emb)

	_, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID:  "entity-1",
		Embedding: []float32{0.1, 0.2, 0.3},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if embedCalled {
		t.Error("should not call Embed when pre-computed embedding is provided")
	}
}

func TestRetrieve_NoEmbedder(t *testing.T) {
	store := &mockStore{}
	r := NewEnhancedRetriever(store, nil, NewGraphEngine(store, DefaultGraphConfig()), DefaultRetrievalConfig())

	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID: "entity-1",
		Query:    "test",
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(result.Memories) != 0 {
		t.Errorf("expected 0 memories without embedder, got %d", len(result.Memories))
	}
}

func TestRetrieve_IncludeRelated(t *testing.T) {
	mem1 := testMemory("mem-1", "primary memory")
	mem2 := testMemory("mem-2", "related memory")
	mem2.EntityID = "related-entity"

	callCount := 0
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, entityID string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			callCount++
			if entityID == "entity-1" {
				return []*storage.SimilarityResult{{Memory: mem1, Similarity: 0.9}}, nil
			}
			return []*storage.SimilarityResult{{Memory: mem2, Similarity: 0.7}}, nil
		},
	}
	emb := &mockEmbedder{dimensions: 3}
	r := newTestRetriever(store, emb)

	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID:        "entity-1",
		Query:           "test",
		IncludeRelated:  true,
		RelatedEntities: []string{"related-entity"},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if callCount < 2 {
		t.Error("expected FindSimilar to be called for related entities too")
	}
	if result.TotalFound < 2 {
		t.Errorf("TotalFound = %d, want >= 2", result.TotalFound)
	}
}

func TestRetrieve_StateFilter(t *testing.T) {
	activeMem := testMemory("mem-1", "active")
	staleMem := testMemory("mem-2", "stale")
	staleMem.State = storage.StateStale

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: activeMem, Similarity: 0.9},
				{Memory: staleMem, Similarity: 0.8},
			}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID: "entity-1",
		Query:    "test",
		States:   []storage.MemoryState{storage.StateActive},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(result.Memories) != 1 {
		t.Errorf("expected 1 active memory, got %d", len(result.Memories))
	}
}

func TestRetrieve_MaxResults(t *testing.T) {
	var mems []*storage.SimilarityResult
	for i := 0; i < 20; i++ {
		mems = append(mems, &storage.SimilarityResult{
			Memory:     testMemory("mem-"+string(rune('a'+i)), "content"),
			Similarity: 0.9 - float64(i)*0.01,
		})
	}
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return mems, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID:   "entity-1",
		Query:      "test",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(result.Memories) != 5 {
		t.Errorf("Memories = %d, want 5 (max results)", len(result.Memories))
	}
}

func TestRetrieve_TimeFilter(t *testing.T) {
	now := time.Now()
	recentMem := testMemory("mem-1", "recent")
	recentMem.CreatedAt = now

	oldMem := testMemory("mem-2", "old")
	oldMem.CreatedAt = now.Add(-48 * time.Hour)

	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: recentMem, Similarity: 0.9},
				{Memory: oldMem, Similarity: 0.8},
			}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	since := now.Add(-24 * time.Hour)
	result, err := r.Retrieve(context.Background(), RetrievalRequest{
		EntityID: "entity-1",
		Query:    "test",
		Since:    &since,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(result.Memories) != 1 {
		t.Errorf("expected 1 recent memory, got %d", len(result.Memories))
	}
}

func TestRetrieveByType(t *testing.T) {
	store := &mockStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if q.OrderBy != "importance" || !q.Descending {
				t.Error("expected ordered by importance desc")
			}
			return []*storage.Memory{testMemory("m1", "test")}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	mems, err := r.RetrieveByType(context.Background(), "entity-1", 10)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(mems) != 1 {
		t.Errorf("got %d memories, want 1", len(mems))
	}
}

func TestRetrieveRecent(t *testing.T) {
	store := &mockStore{
		getRecentMemoriesFn: func(_ context.Context, _ string, hours int, limit int) ([]*storage.Memory, error) {
			if hours != 24 || limit != 5 {
				t.Errorf("hours=%d, limit=%d, want 24, 5", hours, limit)
			}
			return []*storage.Memory{testMemory("m1", "test")}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	mems, err := r.RetrieveRecent(context.Background(), "entity-1", 24, 5)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(mems) != 1 {
		t.Errorf("got %d, want 1", len(mems))
	}
}

func TestRetrieveImportant(t *testing.T) {
	store := &mockStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if q.OrderBy != "importance" {
				t.Errorf("OrderBy = %q, want %q", q.OrderBy, "importance")
			}
			return []*storage.Memory{testMemory("m1", "important")}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	mems, err := r.RetrieveImportant(context.Background(), "entity-1", 10)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(mems) != 1 {
		t.Errorf("got %d, want 1", len(mems))
	}
}

func TestContextualRetrieve(t *testing.T) {
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: testMemory("mem-1", "context memory"), Similarity: 0.8},
			}, nil
		},
	}
	r := newTestRetriever(store, &mockEmbedder{dimensions: 3})

	result, err := r.ContextualRetrieve(context.Background(), "entity-1", []string{"Hello", "How are you?"}, 5)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(result.Memories) != 1 {
		t.Errorf("got %d memories, want 1", len(result.Memories))
	}
}

func TestDefaultRetrievalConfig(t *testing.T) {
	cfg := DefaultRetrievalConfig()
	if cfg.MaxResults != 20 {
		t.Errorf("MaxResults = %d, want 20", cfg.MaxResults)
	}
	if cfg.MinSimilarity != 0.5 {
		t.Errorf("MinSimilarity = %v, want 0.5", cfg.MinSimilarity)
	}
	if !cfg.EnableGraphContext {
		t.Error("EnableGraphContext should be true")
	}
}

func TestNewEnhancedRetriever_DefaultConfig(t *testing.T) {
	r := NewEnhancedRetriever(&mockStore{}, &mockEmbedder{dimensions: 3}, nil, RetrievalConfig{})
	if r.config.MaxResults != 20 {
		t.Errorf("default MaxResults = %d, want 20", r.config.MaxResults)
	}
	if r.config.MinSimilarity != 0.5 {
		t.Errorf("default MinSimilarity = %v, want 0.5", r.config.MinSimilarity)
	}
	if r.config.RecencyBoostWindow != 24 {
		t.Errorf("default RecencyBoostWindow = %d, want 24", r.config.RecencyBoostWindow)
	}
}
