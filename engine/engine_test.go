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

func newTestEngine(store *mockStore, provider *mockProvider, emb *mockEmbedder) *Engine {
	cfg := DefaultEngineConfig()
	// Disable significance filter in tests so short test content isn't skipped
	disabled := SignificanceConfig{Enabled: false}
	cfg.Significance = &disabled
	return NewEngine(provider, emb, store, cfg)
}

func TestEngine_Add_HappyPath(t *testing.T) {
	var createdMem *storage.Memory
	store := &mockStore{
		createMemoryFn: func(_ context.Context, mem *storage.Memory) error {
			mem.ID = "mem-1"
			createdMem = mem
			return nil
		},
	}
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, req llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return &llm.ExtractionResponse{
				Memories: []llm.ExtractedMemory{
					{Content: "User likes Go programming", Type: "PREFERENCE", Importance: 0.7, Confidence: 0.9},
				},
			}, nil
		},
	}
	emb := &mockEmbedder{dimensions: 3}

	e := newTestEngine(store, provider, emb)
	result, err := e.Add(context.Background(), "entity-1", AddRequest{
		Content:   "I really like Go programming",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if result.MemoriesCreated != 1 {
		t.Errorf("MemoriesCreated = %d, want 1", result.MemoriesCreated)
	}
	if createdMem == nil {
		t.Fatal("memory was not created")
	}
	if createdMem.Content != "User likes Go programming" {
		t.Errorf("Content = %q, want %q", createdMem.Content, "User likes Go programming")
	}
	if createdMem.Type != storage.TypePreference {
		t.Errorf("Type = %q, want PREFERENCE", createdMem.Type)
	}
}

func TestEngine_Add_WithUpdates(t *testing.T) {
	existingMem := testMemory("existing-1", "User likes Python")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: existingMem, Similarity: 0.8},
			}, nil
		},
		updateMemoryFn: func(_ context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Content != nil {
				existingMem.Content = *updates.Content
			}
			return existingMem, nil
		},
	}
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return &llm.ExtractionResponse{
				Updates: []llm.MemoryUpdate{
					{Query: "User likes Python", NewContent: "User now prefers Go over Python", Reason: "changed preference"},
				},
			}, nil
		},
	}
	emb := &mockEmbedder{dimensions: 3}
	e := newTestEngine(store, provider, emb)

	result, err := e.Add(context.Background(), "entity-1", AddRequest{Content: "I now prefer Go over Python"})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if result.MemoriesUpdated != 1 {
		t.Errorf("MemoriesUpdated = %d, want 1", result.MemoriesUpdated)
	}
}

func TestEngine_Add_WithDeletes(t *testing.T) {
	existingMem := testMemory("existing-1", "User works at Acme Corp")
	var deletedID string
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: existingMem, Similarity: 0.8},
			}, nil
		},
		deleteMemoryFn: func(_ context.Context, id string, _ bool) error {
			deletedID = id
			return nil
		},
	}
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return &llm.ExtractionResponse{
				Deletes: []llm.MemoryDelete{
					{Query: "User works at Acme Corp", Reason: "user left company"},
				},
			}, nil
		},
	}
	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	result, err := e.Add(context.Background(), "entity-1", AddRequest{Content: "I left Acme Corp"})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if result.MemoriesDeleted != 1 {
		t.Errorf("MemoriesDeleted = %d, want 1", result.MemoriesDeleted)
	}
	if deletedID != "existing-1" {
		t.Errorf("deleted ID = %q, want %q", deletedID, "existing-1")
	}
}

func TestEngine_Add_Skipped(t *testing.T) {
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return &llm.ExtractionResponse{
				Skipped: []llm.SkippedContent{
					{Text: "Hello there", Reason: "greeting, not memory-worthy"},
				},
			}, nil
		},
	}
	e := newTestEngine(&mockStore{}, provider, &mockEmbedder{dimensions: 3})

	result, err := e.Add(context.Background(), "entity-1", AddRequest{Content: "Hello there"})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestEngine_Add_EmbedError(t *testing.T) {
	emb := &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return nil, errors.New("embed failed")
		},
	}
	e := newTestEngine(&mockStore{}, &mockProvider{}, emb)

	_, err := e.Add(context.Background(), "entity-1", AddRequest{Content: "test"})
	if err == nil {
		t.Error("expected error for embed failure")
	}
}

func TestEngine_Add_ExtractionError(t *testing.T) {
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return nil, errors.New("LLM failed")
		},
	}
	e := newTestEngine(&mockStore{}, provider, &mockEmbedder{dimensions: 3})

	_, err := e.Add(context.Background(), "entity-1", AddRequest{Content: "test"})
	if err == nil {
		t.Error("expected error for extraction failure")
	}
}

func TestEngine_Add_WithCustomExtraction(t *testing.T) {
	store := &mockStore{
		getSchemaFn: func(_ context.Context, id string) (*storage.ExtractionSchema, error) {
			return &storage.ExtractionSchema{
				ID: id, Name: "test-schema", IsActive: true,
				SchemaDefinition: map[string]any{"field": "string"},
			}, nil
		},
		createCustomExtractionFn: func(_ context.Context, ext *storage.CustomExtraction) error {
			ext.ID = "custom-ext-1"
			return nil
		},
	}
	provider := &mockProvider{
		extractMemoriesFn: func(_ context.Context, _ llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
			return &llm.ExtractionResponse{}, nil
		},
		extractWithSchemaFn: func(_ context.Context, _ llm.CustomExtractionRequest) (*llm.CustomExtractionResponse, error) {
			return &llm.CustomExtractionResponse{
				ExtractedData: map[string]any{"field": "value"},
				Confidence:    0.9,
			}, nil
		},
	}
	e := newTestEngine(store, provider, &mockEmbedder{dimensions: 3})

	result, err := e.Add(context.Background(), "entity-1", AddRequest{
		Content:  "test content",
		SchemaID: "schema-1",
	})
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if result.CustomExtractionID != "custom-ext-1" {
		t.Errorf("CustomExtractionID = %q, want %q", result.CustomExtractionID, "custom-ext-1")
	}
	if result.CustomExtractedData["field"] != "value" {
		t.Errorf("CustomExtractedData = %v, want field=value", result.CustomExtractedData)
	}
}

func TestEngine_Query_HappyPath(t *testing.T) {
	mem := testMemory("mem-1", "User likes Go")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem, Similarity: 0.9},
			}, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{Query: "Go programming", Limit: 10})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Query returned %d results, want 1", len(results))
	}
	if results[0].Memory.ID != "mem-1" {
		t.Errorf("result ID = %q, want %q", results[0].Memory.ID, "mem-1")
	}
	if results[0].Score.TotalScore <= 0 {
		t.Error("expected positive score")
	}
}

func TestEngine_Query_WithAgentFilter(t *testing.T) {
	mem := testMemory("mem-1", "Agent-specific memory")
	mem.AgentID = "agent-1"
	var calledWithAgent bool
	store := &mockStore{
		findSimilarWithOptionsFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
			calledWithAgent = opts.AgentID == "agent-1"
			return []*storage.SimilarityResult{{Memory: mem, Similarity: 0.9}}, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "test", Limit: 10, AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if !calledWithAgent {
		t.Error("expected FindSimilarWithOptions to be called with agent filter")
	}
	if len(results) != 1 {
		t.Errorf("Query returned %d results, want 1", len(results))
	}
}

func TestEngine_Query_WithMode(t *testing.T) {
	mem := testMemory("mem-1", "test")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{{Memory: mem, Similarity: 0.9}}, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	results, err := e.Query(context.Background(), "entity-1", QueryRequest{
		Query: "test", Mode: ModeImportant,
	})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result")
	}
}

func TestEngine_Query_DefaultLimit(t *testing.T) {
	e := newTestEngine(&mockStore{}, &mockProvider{}, &mockEmbedder{dimensions: 3})
	results, err := e.Query(context.Background(), "entity-1", QueryRequest{Query: "test"})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if results == nil {
		t.Error("expected non-nil results")
	}
}

func TestEngine_GetAll(t *testing.T) {
	store := &mockStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if q.OrderBy != "created_at" || !q.Descending {
				t.Error("expected ordered by created_at desc")
			}
			return []*storage.Memory{testMemory("m1", "test")}, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	mems, err := e.GetAll(context.Background(), "entity-1", 50)
	if err != nil {
		t.Fatalf("GetAll error = %v", err)
	}
	if len(mems) != 1 {
		t.Errorf("GetAll returned %d, want 1", len(mems))
	}
}

func TestEngine_GetAll_DefaultLimit(t *testing.T) {
	var capturedLimit int
	store := &mockStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			capturedLimit = q.Limit
			return nil, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})
	e.GetAll(context.Background(), "entity-1", 0)
	if capturedLimit != 100 {
		t.Errorf("default limit = %d, want 100", capturedLimit)
	}
}

func TestEngine_GetByID(t *testing.T) {
	mem := testMemory("mem-1", "test content")
	store := &mockStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			if id == "mem-1" {
				return mem, nil
			}
			return nil, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	got, err := e.GetByID(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("GetByID error = %v", err)
	}
	if got.ID != "mem-1" {
		t.Errorf("GetByID ID = %q, want %q", got.ID, "mem-1")
	}
}

func TestEngine_Delete(t *testing.T) {
	var deletedID string
	store := &mockStore{
		deleteMemoryFn: func(_ context.Context, id string, hard bool) error {
			deletedID = id
			if hard {
				t.Error("Delete should use soft delete")
			}
			return nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	if err := e.Delete(context.Background(), "mem-1"); err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	if deletedID != "mem-1" {
		t.Errorf("deleted = %q, want %q", deletedID, "mem-1")
	}
}

func TestEngine_DeleteAll(t *testing.T) {
	var relDeleted, entDeleted bool
	var memDeletedCount int
	store := &mockStore{
		deleteAllRelationshipsForOwnerFn: func(_ context.Context, _ string) (int, error) {
			relDeleted = true
			return 0, nil
		},
		deleteAllEntitiesForOwnerFn: func(_ context.Context, _ string) (int, error) {
			entDeleted = true
			return 0, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				testMemory("m1", "a"),
				testMemory("m2", "b"),
			}, nil
		},
		deleteMemoryFn: func(_ context.Context, _ string, hard bool) error {
			if !hard {
				t.Error("DeleteAll should use hard delete")
			}
			memDeletedCount++
			return nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	if err := e.DeleteAll(context.Background(), "entity-1"); err != nil {
		t.Fatalf("DeleteAll error = %v", err)
	}
	if !relDeleted {
		t.Error("relationships not deleted")
	}
	if !entDeleted {
		t.Error("entities not deleted")
	}
	if memDeletedCount != 2 {
		t.Errorf("memories deleted = %d, want 2", memDeletedCount)
	}
}

func TestEngine_GetStats(t *testing.T) {
	store := &mockStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "1", Type: storage.TypeIdentity, State: storage.StateActive},
				{ID: "2", Type: storage.TypePreference, State: storage.StateActive},
				{ID: "3", Type: storage.TypeIdentity, State: storage.StateStale},
			}, nil
		},
	}
	e := newTestEngine(store, &mockProvider{}, &mockEmbedder{dimensions: 3})

	stats, err := e.GetStats(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("GetStats error = %v", err)
	}
	if stats.TotalMemories != 3 {
		t.Errorf("TotalMemories = %d, want 3", stats.TotalMemories)
	}
	if stats.ByType[storage.TypeIdentity] != 2 {
		t.Errorf("ByType[IDENTITY] = %d, want 2", stats.ByType[storage.TypeIdentity])
	}
	if stats.ByState[storage.StateActive] != 2 {
		t.Errorf("ByState[active] = %d, want 2", stats.ByState[storage.StateActive])
	}
}

func TestEngine_Close(t *testing.T) {
	e := newTestEngine(&mockStore{}, &mockProvider{}, &mockEmbedder{dimensions: 3})
	if err := e.Close(); err != nil {
		t.Errorf("Close error = %v", err)
	}
}

func TestEngine_Provider(t *testing.T) {
	p := &mockProvider{name: "test"}
	e := newTestEngine(&mockStore{}, p, &mockEmbedder{dimensions: 3})
	if e.Provider().Name() != "test" {
		t.Errorf("Provider().Name() = %q, want %q", e.Provider().Name(), "test")
	}
}

func TestHashContent(t *testing.T) {
	h1 := hashContent("hello")
	h2 := hashContent("hello")
	h3 := hashContent("world")

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 (SHA-256 hex)", len(h1))
	}
}

func TestContainsSubstring(t *testing.T) {
	tests := []struct {
		content, query string
		want           bool
	}{
		{"User likes Go", "User likes Go", true},
		{"User likes Go programming", "User likes Go", true},
		{"I love Go programming", "programming", true},
		{"", "test", false},
		{"test", "", false},
		{"ab", "abc", false},
	}
	for _, tt := range tests {
		if got := containsSubstring(tt.content, tt.query); got != tt.want {
			t.Errorf("containsSubstring(%q, %q) = %v, want %v", tt.content, tt.query, got, tt.want)
		}
	}
}

func TestEncodeDecodeEmbedding(t *testing.T) {
	original := []float32{1.0, -0.5, 0.25, 3.14}
	encoded := encodeEmbedding(original)
	decoded := DecodeEmbedding(encoded)

	if len(decoded) != len(original) {
		t.Fatalf("decoded length = %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("decoded[%d] = %v, want %v", i, decoded[i], original[i])
		}
	}
}

func TestEncodeDecodeEmbedding_Empty(t *testing.T) {
	if got := encodeEmbedding(nil); got != nil {
		t.Errorf("encodeEmbedding(nil) = %v, want nil", got)
	}
	if got := DecodeEmbedding(nil); got != nil {
		t.Errorf("DecodeEmbedding(nil) = %v, want nil", got)
	}
	if got := DecodeEmbedding([]byte{1, 2, 3}); got != nil {
		t.Errorf("DecodeEmbedding(odd bytes) = %v, want nil", got)
	}
}

func TestSortResultsByScore(t *testing.T) {
	results := []*QueryResult{
		{Score: ScoringResult{TotalScore: 0.3}},
		{Score: ScoringResult{TotalScore: 0.9}},
		{Score: ScoringResult{TotalScore: 0.6}},
	}
	sortResultsByScore(results)
	if results[0].Score.TotalScore != 0.9 {
		t.Errorf("first score = %v, want 0.9", results[0].Score.TotalScore)
	}
	if results[2].Score.TotalScore != 0.3 {
		t.Errorf("last score = %v, want 0.3", results[2].Score.TotalScore)
	}
}

func TestEnforceDiversity(t *testing.T) {
	results := []*QueryResult{
		{Memory: &storage.Memory{Content: "A"}},
		{Memory: &storage.Memory{Content: "A"}},
		{Memory: &storage.Memory{Content: "B"}},
	}
	diverse := enforceDiversity(results, 0.9, &mockStore{})
	if len(diverse) != 2 {
		t.Errorf("enforceDiversity returned %d, want 2", len(diverse))
	}
}

func TestEnforceDiversity_SingleOrEmpty(t *testing.T) {
	if got := enforceDiversity(nil, 0.9, &mockStore{}); len(got) != 0 {
		t.Errorf("enforceDiversity(nil) len = %d, want 0", len(got))
	}
	single := []*QueryResult{{Memory: &storage.Memory{Content: "A"}}}
	if got := enforceDiversity(single, 0.9, &mockStore{}); len(got) != 1 {
		t.Errorf("enforceDiversity(single) len = %d, want 1", len(got))
	}
}

func TestMapLLMEntityType(t *testing.T) {
	tests := []struct {
		input string
		want  storage.EntityType
	}{
		{"PERSON", storage.EntityTypePerson},
		{"ORGANIZATION", storage.EntityTypeOrganization},
		{"LOCATION", storage.EntityTypeLocation},
		{"PRODUCT", storage.EntityTypeProduct},
		{"UNKNOWN", storage.EntityTypeOther},
		{"", storage.EntityTypeOther},
	}
	for _, tt := range tests {
		if got := mapLLMEntityType(tt.input); got != tt.want {
			t.Errorf("mapLLMEntityType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDefaultEngineConfig(t *testing.T) {
	cfg := DefaultEngineConfig()
	if cfg.ContextTurns != 5 {
		t.Errorf("ContextTurns = %d, want 5", cfg.ContextTurns)
	}
}

func TestNewEngine_DefaultContextTurns(t *testing.T) {
	e := NewEngine(&mockProvider{}, &mockEmbedder{dimensions: 3}, &mockStore{}, EngineConfig{ContextTurns: -1})
	if e.config.ContextTurns != 5 {
		t.Errorf("ContextTurns = %d, want 5 (default)", e.config.ContextTurns)
	}
}
