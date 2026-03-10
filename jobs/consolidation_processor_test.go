package jobs

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

func encodeTestEmbedding(vals []float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func TestConsolidationProcessor_Type(t *testing.T) {
	p := NewConsolidationProcessor(&mockStore{}, nil, nil, ConsolidationJobConfig{})
	if p.Type() != JobTypeConsolidation {
		t.Errorf("Type() = %q, want %q", p.Type(), JobTypeConsolidation)
	}
}

func TestDefaultConsolidationJobConfig(t *testing.T) {
	cfg := DefaultConsolidationJobConfig()
	if cfg.SimilarityThreshold != 0.85 {
		t.Errorf("SimilarityThreshold = %v, want 0.85", cfg.SimilarityThreshold)
	}
	if cfg.MinGroupSize != 2 {
		t.Errorf("MinGroupSize = %d, want 2", cfg.MinGroupSize)
	}
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", cfg.BatchSize)
	}
	if cfg.MaxMergeSize != 5 {
		t.Errorf("MaxMergeSize = %d, want 5", cfg.MaxMergeSize)
	}
}

func TestNewConsolidationProcessor_Defaults(t *testing.T) {
	p := NewConsolidationProcessor(&mockStore{}, nil, nil, ConsolidationJobConfig{})
	if p.config.SimilarityThreshold != 0.85 {
		t.Errorf("default SimilarityThreshold = %v", p.config.SimilarityThreshold)
	}
	if p.config.MinGroupSize != 2 {
		t.Errorf("default MinGroupSize = %d", p.config.MinGroupSize)
	}
	if p.useLLM {
		t.Error("useLLM should be false when provider is nil")
	}
}

func TestNewConsolidationProcessor_WithLLM(t *testing.T) {
	p := NewConsolidationProcessor(&mockStore{}, &mockProvider{}, nil, ConsolidationJobConfig{})
	if !p.useLLM {
		t.Error("useLLM should be true when provider is non-nil")
	}
}

func TestConsolidationProcessor_Process_NoEntities(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, nil
		},
	}
	p := NewConsolidationProcessor(store, nil, nil, DefaultConsolidationJobConfig())

	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestConsolidationProcessor_Process_NoGroups(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"entity-1"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			// Return only one memory — not enough for MinGroupSize=2
			return []*storage.Memory{
				testMemory("mem-1", "single memory", storage.StateStale),
			}, nil
		},
	}

	p := NewConsolidationProcessor(store, nil, nil, DefaultConsolidationJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsProcessed != 0 {
		t.Errorf("ItemsProcessed = %d, want 0", result.ItemsProcessed)
	}
}

func TestConsolidationProcessor_Process_WithLLMConsolidation(t *testing.T) {
	embedding := encodeTestEmbedding([]float32{0.5, 0.5, 0.5})

	mem1 := testMemory("mem-1", "User likes pizza", storage.StateStale)
	mem1.Embedding = embedding
	mem1.Importance = 0.8

	mem2 := testMemory("mem-2", "User enjoys Italian food", storage.StateStale)
	mem2.Embedding = embedding
	mem2.Importance = 0.6

	var updatedID string
	var updatedContent string
	var deletedIDs []string

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"test-entity"}, nil
		},
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem1, mem2}, nil
		},
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 1.0},
				{Memory: mem2, Similarity: 0.9},
			}, nil
		},
		updateMemoryFn: func(_ context.Context, id string, upd storage.MemoryUpdate) (*storage.Memory, error) {
			if upd.Content != nil {
				updatedID = id
				updatedContent = *upd.Content
			}
			return &storage.Memory{ID: id}, nil
		},
		deleteMemoryFn: func(_ context.Context, id string, _ bool) error {
			deletedIDs = append(deletedIDs, id)
			return nil
		},
	}

	provider := &mockProvider{
		consolidateMemoriesFn: func(_ context.Context, req llm.ConsolidationRequest) (*llm.ConsolidationResponse, error) {
			return &llm.ConsolidationResponse{
				Content:    "User enjoys pizza and Italian food",
				Confidence: 0.95,
			}, nil
		},
	}

	p := NewConsolidationProcessor(store, provider, nil, DefaultConsolidationJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}

	if result.ItemsProcessed != 2 {
		t.Errorf("ItemsProcessed = %d, want 2", result.ItemsProcessed)
	}
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1", result.ItemsAffected)
	}
	if updatedID != "mem-1" {
		t.Errorf("updated ID = %q, want %q (highest importance)", updatedID, "mem-1")
	}
	if updatedContent != "User enjoys pizza and Italian food" {
		t.Errorf("updated content = %q", updatedContent)
	}
	// mem-2 should be soft-deleted
	found := false
	for _, id := range deletedIDs {
		if id == "mem-2" {
			found = true
		}
	}
	if !found {
		t.Error("expected mem-2 to be soft-deleted")
	}
}

func TestConsolidationProcessor_Process_LLMFallback(t *testing.T) {
	embedding := encodeTestEmbedding([]float32{0.5, 0.5, 0.5})

	mem1 := testMemory("mem-1", "User likes pizza", storage.StateStale)
	mem1.Embedding = embedding
	mem1.Importance = 0.8

	mem2 := testMemory("mem-2", "User frequently orders calzones from restaurants", storage.StateStale)
	mem2.Embedding = embedding
	mem2.Importance = 0.6

	var updatedContent string

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"test-entity"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem1, mem2}, nil
		},
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 1.0},
				{Memory: mem2, Similarity: 0.9},
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, upd storage.MemoryUpdate) (*storage.Memory, error) {
			if upd.Content != nil {
				updatedContent = *upd.Content
			}
			return &storage.Memory{}, nil
		},
	}

	// LLM returns error — should fall back to text-based
	provider := &mockProvider{
		consolidateMemoriesFn: func(_ context.Context, _ llm.ConsolidationRequest) (*llm.ConsolidationResponse, error) {
			return nil, fmt.Errorf("LLM unavailable")
		},
	}

	p := NewConsolidationProcessor(store, provider, nil, DefaultConsolidationJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1", result.ItemsAffected)
	}
	// Text-based fallback should produce some content
	if updatedContent == "" {
		t.Error("expected non-empty updated content from text-based fallback")
	}
}

func TestConsolidationProcessor_Process_NoLLM(t *testing.T) {
	embedding := encodeTestEmbedding([]float32{0.5, 0.5, 0.5})

	mem1 := testMemory("mem-1", "User likes pizza", storage.StateStale)
	mem1.Embedding = embedding
	mem1.Importance = 0.8

	mem2 := testMemory("mem-2", "User frequently orders calzones from nearby restaurants", storage.StateStale)
	mem2.Embedding = embedding
	mem2.Importance = 0.6

	var updatedContent string

	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return []string{"test-entity"}, nil
		},
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{mem1, mem2}, nil
		},
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: mem1, Similarity: 1.0},
				{Memory: mem2, Similarity: 0.9},
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, upd storage.MemoryUpdate) (*storage.Memory, error) {
			if upd.Content != nil {
				updatedContent = *upd.Content
			}
			return &storage.Memory{}, nil
		},
	}

	// No LLM provider — uses text-based consolidation
	p := NewConsolidationProcessor(store, nil, nil, DefaultConsolidationJobConfig())
	result, err := p.Process(context.Background())
	if err != nil {
		t.Fatalf("Process error = %v", err)
	}
	if result.ItemsAffected != 1 {
		t.Errorf("ItemsAffected = %d, want 1", result.ItemsAffected)
	}
	if updatedContent == "" {
		t.Error("expected non-empty content from text-based consolidation")
	}
}

func TestConsolidationProcessor_Process_GetEntitiesError(t *testing.T) {
	store := &mockStore{
		getAllEntitiesFn: func(_ context.Context) ([]string, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	p := NewConsolidationProcessor(store, nil, nil, DefaultConsolidationJobConfig())
	_, err := p.Process(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestIsContentRedundant(t *testing.T) {
	tests := []struct {
		name     string
		content1 string
		content2 string
		want     bool
	}{
		{"exact same", "hello world", "hello world", true},
		{"substring", "user likes pizza and pasta", "likes pizza", true},
		{"high overlap", "the user really likes eating pizza", "the user really likes pizza", true},
		{"different content", "user likes pizza", "cat chases mouse", false},
		{"empty content2", "hello", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isContentRedundant(tt.content1, tt.content2)
			if got != tt.want {
				t.Errorf("isContentRedundant(%q, %q) = %v, want %v", tt.content1, tt.content2, got, tt.want)
			}
		})
	}
}

func TestExtractUniqueContent(t *testing.T) {
	tests := []struct {
		name     string
		content1 string
		content2 string
		wantLen  int // 0 means empty, >0 means has content
	}{
		{"all overlap", "user likes pizza", "user likes pizza", 0},
		{"some unique words", "user likes pizza", "User also enjoys calzones and pasta dishes", 1},
		{"too few unique", "hello world", "hello there", 0}, // only "there" is unique, < 3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractUniqueContent(tt.content1, tt.content2)
			if tt.wantLen == 0 && result != "" {
				t.Errorf("extractUniqueContent(%q, %q) = %q, want empty", tt.content1, tt.content2, result)
			}
			if tt.wantLen > 0 && result == "" {
				t.Errorf("extractUniqueContent(%q, %q) = empty, want non-empty", tt.content1, tt.content2)
			}
		})
	}
}

func TestDecodeEmbeddingBlob(t *testing.T) {
	t.Run("valid blob", func(t *testing.T) {
		original := []float32{1.0, 2.0, 3.0}
		blob := encodeTestEmbedding(original)
		result := decodeEmbeddingBlob(blob)
		if len(result) != 3 {
			t.Fatalf("len = %d, want 3", len(result))
		}
		for i, v := range result {
			if v != original[i] {
				t.Errorf("result[%d] = %v, want %v", i, v, original[i])
			}
		}
	})

	t.Run("empty blob", func(t *testing.T) {
		result := decodeEmbeddingBlob(nil)
		if result != nil {
			t.Errorf("expected nil for empty blob, got %v", result)
		}
	})

	t.Run("invalid length", func(t *testing.T) {
		result := decodeEmbeddingBlob([]byte{1, 2, 3})
		if result != nil {
			t.Errorf("expected nil for invalid blob length, got %v", result)
		}
	})
}

func TestGetMemoryIDs(t *testing.T) {
	memories := []*storage.Memory{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	ids := getMemoryIDs(memories)
	if len(ids) != 3 {
		t.Fatalf("len = %d, want 3", len(ids))
	}
	for i, expected := range []string{"a", "b", "c"} {
		if ids[i] != expected {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], expected)
		}
	}
}
