package engine

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestDetectConflicts_NegationPattern(t *testing.T) {
	existing := testMemory("mem-1", "User likes pizza")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{{Memory: existing, Similarity: 0.8}}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	result, err := d.DetectConflicts(context.Background(), "entity-1", "User doesn't like pizza", testEmbedding(), storage.TypePreference)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.HasConflict {
		t.Error("expected conflict for negation pattern")
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected at least one conflict")
	}
	if result.Conflicts[0].ConflictType != ConflictTypeContradiction {
		t.Errorf("ConflictType = %q, want %q", result.Conflicts[0].ConflictType, ConflictTypeContradiction)
	}
}

func TestDetectConflicts_TemporalConflict(t *testing.T) {
	existing := testMemory("mem-1", "User works at Acme Corp")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{{Memory: existing, Similarity: 0.8}}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	result, err := d.DetectConflicts(context.Background(), "entity-1", "User now works at BigTech Corp", testEmbedding(), storage.TypeIdentity)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.HasConflict {
		t.Error("expected temporal conflict")
	}
	if len(result.Conflicts) > 0 && result.Conflicts[0].ConflictType != ConflictTypeTemporal {
		t.Errorf("ConflictType = %q, want %q", result.Conflicts[0].ConflictType, ConflictTypeTemporal)
	}
}

func TestDetectConflicts_PreferenceUpdate(t *testing.T) {
	existing := testMemory("mem-1", "User prefers coffee in the morning")
	existing.Type = storage.TypePreference
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{{Memory: existing, Similarity: 0.8}}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	result, err := d.DetectConflicts(context.Background(), "entity-1", "User prefers tea in the morning", testEmbedding(), storage.TypePreference)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.HasConflict {
		t.Error("expected preference conflict")
	}
	if len(result.Conflicts) > 0 && result.Conflicts[0].ConflictType != ConflictTypeUpdate {
		t.Errorf("ConflictType = %q, want %q", result.Conflicts[0].ConflictType, ConflictTypeUpdate)
	}
}

func TestDetectConflicts_NoConflict(t *testing.T) {
	store := &mockStore{} // FindSimilar returns nil by default
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	result, err := d.DetectConflicts(context.Background(), "entity-1", "User likes pizza", testEmbedding(), storage.TypePreference)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.HasConflict {
		t.Error("expected no conflict when no similar memories")
	}
}

func TestDetectConflicts_QuantitativeConflict(t *testing.T) {
	existing := testMemory("mem-1", "User has 3 cats at home")
	store := &mockStore{
		findSimilarFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.SimilarityResult, error) {
			return []*storage.SimilarityResult{{Memory: existing, Similarity: 0.8}}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	result, err := d.DetectConflicts(context.Background(), "entity-1", "User has 5 cats at home", testEmbedding(), storage.TypeContext)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !result.HasConflict {
		t.Error("expected quantitative conflict")
	}
}

func TestResolveConflict_KeepExisting(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, DefaultConflictConfig())
	conflict := Conflict{
		ExistingMemory: testMemory("mem-1", "existing"),
		NewContent:     "new content",
	}
	err := d.ResolveConflict(context.Background(), conflict, ResolutionKeepExisting)
	if err != nil {
		t.Errorf("ResolveConflict(KeepExisting) error = %v", err)
	}
}

func TestResolveConflict_UseNew(t *testing.T) {
	var updatedContent string
	store := &mockStore{
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Content != nil {
				updatedContent = *updates.Content
			}
			return &storage.Memory{}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	conflict := Conflict{
		ExistingMemory: testMemory("mem-1", "old content"),
		NewContent:     "new content",
	}
	err := d.ResolveConflict(context.Background(), conflict, ResolutionUseNew)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if updatedContent != "new content" {
		t.Errorf("updated content = %q, want %q", updatedContent, "new content")
	}
}

func TestResolveConflict_Merge(t *testing.T) {
	var updatedContent string
	store := &mockStore{
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Content != nil {
				updatedContent = *updates.Content
			}
			return &storage.Memory{}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	conflict := Conflict{
		ExistingMemory:  testMemory("mem-1", "old"),
		NewContent:      "new",
		ResolvedContent: "merged old and new",
	}
	err := d.ResolveConflict(context.Background(), conflict, ResolutionMerge)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if updatedContent != "merged old and new" {
		t.Errorf("merged content = %q, want %q", updatedContent, "merged old and new")
	}
}

func TestResolveConflict_MergeWithoutResolvedContent(t *testing.T) {
	var updatedContent string
	store := &mockStore{
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Content != nil {
				updatedContent = *updates.Content
			}
			return &storage.Memory{}, nil
		},
	}
	d := NewConflictDetector(store, &mockProvider{}, DefaultConflictConfig())

	conflict := Conflict{
		ExistingMemory: testMemory("mem-1", "existing content"),
		NewContent:     "new content",
	}
	err := d.ResolveConflict(context.Background(), conflict, ResolutionMerge)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if updatedContent == "" {
		t.Error("expected content to be updated with fallback merge")
	}
}

func TestResolveConflict_KeepBoth(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, DefaultConflictConfig())
	err := d.ResolveConflict(context.Background(), Conflict{}, ResolutionKeepBoth)
	if err != nil {
		t.Errorf("error = %v", err)
	}
}

func TestResolveConflict_AskUser(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, DefaultConflictConfig())
	err := d.ResolveConflict(context.Background(), Conflict{}, ResolutionAskUser)
	if err != nil {
		t.Errorf("error = %v", err)
	}
}

func TestResolveConflict_Unknown(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, DefaultConflictConfig())
	err := d.ResolveConflict(context.Background(), Conflict{}, "unknown_resolution")
	if err == nil {
		t.Error("expected error for unknown resolution")
	}
}

func TestHasNegationPattern(t *testing.T) {
	tests := []struct {
		name    string
		newC    string
		existC  string
		want    bool
	}{
		{"likes vs dislikes", "user dislikes pizza", "user likes pizza", true},
		{"is vs isn't", "user isn't happy", "user is happy", true},
		{"no longer", "user no longer works here", "user works here", true},
		{"stopped", "user stopped running", "user likes running", true},
		{"no negation", "user likes pizza", "user likes pasta", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasNegationPattern(tt.newC, tt.existC); got != tt.want {
				t.Errorf("hasNegationPattern(%q, %q) = %v, want %v", tt.newC, tt.existC, got, tt.want)
			}
		})
	}
}

func TestIsSameSubject(t *testing.T) {
	tests := []struct {
		name string
		c1   string
		c2   string
		want bool
	}{
		{"same subject", "User likes pizza very much", "User likes pizza a lot", true},
		{"different subject", "Pizza restaurant downtown serving Italian food", "Tesla electric vehicle manufacturing company", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSameSubject(tt.c1, tt.c2); got != tt.want {
				t.Errorf("isSameSubject(%q, %q) = %v, want %v", tt.c1, tt.c2, got, tt.want)
			}
		})
	}
}

func TestExtractNumbers(t *testing.T) {
	tests := []struct {
		input string
		count int
	}{
		{"User has 3 cats", 1},
		{"Between 10 and 20 items", 2},
		{"No numbers here", 0},
		{"Price is $5.99", 0}, // 5.99 is not purely numeric with dot
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			numbers := extractNumbers(tt.input)
			if len(numbers) != tt.count {
				t.Errorf("extractNumbers(%q) returned %d numbers, want %d: %v", tt.input, len(numbers), tt.count, numbers)
			}
		})
	}
}

func TestDefaultConflictConfig(t *testing.T) {
	cfg := DefaultConflictConfig()
	if cfg.SimilarityThreshold != 0.6 {
		t.Errorf("SimilarityThreshold = %v, want 0.6", cfg.SimilarityThreshold)
	}
	if cfg.MaxCandidates != 10 {
		t.Errorf("MaxCandidates = %d, want 10", cfg.MaxCandidates)
	}
	if !cfg.EnableLLMConflictCheck {
		t.Error("EnableLLMConflictCheck should be true by default")
	}
}

func TestNewConflictDetector_DefaultConfig(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, ConflictConfig{})
	if d.config.SimilarityThreshold != 0.6 {
		t.Errorf("default SimilarityThreshold = %v, want 0.6", d.config.SimilarityThreshold)
	}
	if d.config.MaxCandidates != 10 {
		t.Errorf("default MaxCandidates = %d, want 10", d.config.MaxCandidates)
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("hello world", []string{"world", "foo"}) {
		t.Error("expected true")
	}
	if containsAny("hello world", []string{"foo", "bar"}) {
		t.Error("expected false")
	}
}

func TestDetermineOverallResolution(t *testing.T) {
	d := NewConflictDetector(&mockStore{}, &mockProvider{}, DefaultConflictConfig())

	t.Run("empty conflicts", func(t *testing.T) {
		got := d.determineOverallResolution(nil)
		if got != ResolutionKeepBoth {
			t.Errorf("got %q, want %q", got, ResolutionKeepBoth)
		}
	})

	t.Run("high confidence wins", func(t *testing.T) {
		conflicts := []Conflict{
			{Resolution: ResolutionUseNew, Confidence: 0.9},
			{Resolution: ResolutionKeepExisting, Confidence: 0.5},
		}
		got := d.determineOverallResolution(conflicts)
		if got != ResolutionUseNew {
			t.Errorf("got %q, want %q", got, ResolutionUseNew)
		}
	})

	t.Run("majority wins when no high confidence", func(t *testing.T) {
		conflicts := []Conflict{
			{Resolution: ResolutionMerge, Confidence: 0.6},
			{Resolution: ResolutionMerge, Confidence: 0.5},
			{Resolution: ResolutionUseNew, Confidence: 0.7},
		}
		got := d.determineOverallResolution(conflicts)
		if got != ResolutionMerge {
			t.Errorf("got %q, want %q", got, ResolutionMerge)
		}
	})
}
