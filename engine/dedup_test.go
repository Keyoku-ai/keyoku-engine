package engine

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-embedded/storage"
)

func TestCheckDuplicate_ExactHash(t *testing.T) {
	existing := testMemory("mem-1", "User likes pizza")
	store := &mockStore{
		findByHashFn: func(_ context.Context, _ string, _ string) (*storage.Memory, error) {
			return existing, nil
		},
	}
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "User likes pizza", testEmbedding(), "hash123")
	if err != nil {
		t.Fatalf("CheckDuplicate error = %v", err)
	}
	if !result.IsDuplicate {
		t.Error("expected IsDuplicate = true")
	}
	if result.Action != "skip" {
		t.Errorf("Action = %q, want %q", result.Action, "skip")
	}
	if result.Similarity != 1.0 {
		t.Errorf("Similarity = %v, want 1.0", result.Similarity)
	}
}

func TestCheckDuplicate_SemanticDuplicate(t *testing.T) {
	existing := testMemory("mem-1", "User enjoys pizza")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: existing, Similarity: 0.96},
			}, nil
		},
	}
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "User likes pizza", testEmbedding(), "hash-new")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.IsDuplicate {
		t.Error("expected IsDuplicate = true for similarity >= 0.95")
	}
	if result.Action != "skip" {
		t.Errorf("Action = %q, want %q", result.Action, "skip")
	}
}

func TestCheckDuplicate_NearDuplicateSubset(t *testing.T) {
	existing := testMemory("mem-1", "User likes pizza and pasta")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: existing, Similarity: 0.90},
			}, nil
		},
	}
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "User likes pizza", testEmbedding(), "hash-new")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.Action != "skip" {
		t.Errorf("Action = %q, want %q (subset should be skipped)", result.Action, "skip")
	}
}

func TestCheckDuplicate_NearDuplicateMerge(t *testing.T) {
	existing := testMemory("mem-1", "User likes pizza")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{
				{Memory: existing, Similarity: 0.90},
			}, nil
		},
	}
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "User also likes sushi and ramen for dinner", testEmbedding(), "hash-new")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.IsNearDuplicate {
		t.Error("expected IsNearDuplicate = true")
	}
	if result.Action != "merge" {
		t.Errorf("Action = %q, want %q", result.Action, "merge")
	}
}

func TestCheckDuplicate_NoMatch(t *testing.T) {
	store := &mockStore{} // FindByHash and FindSimilar return nil by default
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "Something new", testEmbedding(), "hash-new")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.Action != "create" {
		t.Errorf("Action = %q, want %q", result.Action, "create")
	}
}

func TestCheckDuplicate_SemanticDisabled(t *testing.T) {
	cfg := DefaultDuplicateConfig()
	cfg.EnableSemanticDedup = false
	d := NewDuplicateDetector(&mockStore{}, &mockEmbedder{dimensions: 3}, cfg)

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "test", testEmbedding(), "hash")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.Action != "create" {
		t.Errorf("Action = %q, want %q when semantic dedup disabled", result.Action, "create")
	}
}

func TestCheckDuplicate_EmptyEmbedding(t *testing.T) {
	d := NewDuplicateDetector(&mockStore{}, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.CheckDuplicate(context.Background(), "entity-1", "test", nil, "hash")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.Action != "create" {
		t.Errorf("Action = %q, want %q for empty embedding", result.Action, "create")
	}
}

func TestMergeMemories(t *testing.T) {
	existing := testMemory("mem-1", "User likes pizza")
	existing.Importance = 0.5
	var updatedContent string
	store := &mockStore{
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Content != nil {
				updatedContent = *updates.Content
				existing.Content = *updates.Content
			}
			return existing, nil
		},
	}
	d := NewDuplicateDetector(store, &mockEmbedder{dimensions: 3}, DefaultDuplicateConfig())

	result, err := d.MergeMemories(context.Background(), existing, "User also likes sushi", 0.7)
	if err != nil {
		t.Fatalf("MergeMemories error = %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if updatedContent == "" {
		t.Error("expected content to be updated")
	}
}

func TestIsSubsetOf(t *testing.T) {
	tests := []struct {
		name     string
		newC     string
		existC   string
		want     bool
	}{
		{"contained string", "likes pizza", "User likes pizza very much", true},
		{"word overlap >= 90%", "user likes pizza", "User likes pizza and pasta", true},
		{"different content", "user loves sushi", "User likes pizza", false},
		{"empty new", "", "anything", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSubsetOf(tt.newC, tt.existC); got != tt.want {
				t.Errorf("isSubsetOf(%q, %q) = %v, want %v", tt.newC, tt.existC, got, tt.want)
			}
		})
	}
}

func TestMergeContent(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		new      string
		wantContains string
	}{
		{"new is subset of existing", "User likes pizza and pasta", "likes pizza", "User likes pizza and pasta"},
		{"existing is subset of new", "likes pizza", "User likes pizza and pasta", "User likes pizza and pasta"},
		{"different content", "User likes pizza", "User also enjoys hiking", "User likes pizza"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeContent(tt.existing, tt.new)
			if len(got) == 0 {
				t.Error("mergeContent returned empty string")
			}
			if got != tt.wantContains && !containsStr(got, tt.wantContains) {
				t.Errorf("mergeContent = %q, want to contain %q", got, tt.wantContains)
			}
		})
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && s != "" && substr != "" && findStr(s, substr))
}

func findStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDefaultDuplicateConfig(t *testing.T) {
	cfg := DefaultDuplicateConfig()
	if cfg.SemanticThreshold != 0.95 {
		t.Errorf("SemanticThreshold = %v, want 0.95", cfg.SemanticThreshold)
	}
	if cfg.NearDuplicateThreshold != 0.85 {
		t.Errorf("NearDuplicateThreshold = %v, want 0.85", cfg.NearDuplicateThreshold)
	}
	if !cfg.EnableSemanticDedup {
		t.Error("EnableSemanticDedup should be true by default")
	}
}

func TestNewDuplicateDetector_DefaultConfig(t *testing.T) {
	d := NewDuplicateDetector(&mockStore{}, &mockEmbedder{dimensions: 3}, DuplicateConfig{})
	if d.config.SemanticThreshold != 0.95 {
		t.Errorf("default SemanticThreshold = %v, want 0.95", d.config.SemanticThreshold)
	}
	if d.config.NearDuplicateThreshold != 0.85 {
		t.Errorf("default NearDuplicateThreshold = %v, want 0.85", d.config.NearDuplicateThreshold)
	}
	if d.config.MaxCandidates != 10 {
		t.Errorf("default MaxCandidates = %d, want 10", d.config.MaxCandidates)
	}
}
