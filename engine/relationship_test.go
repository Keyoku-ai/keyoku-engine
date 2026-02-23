package engine

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-embedded/storage"
)

func TestDetectRelationships_PatternBased(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	content := "Alice works at Google"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rels) == 0 {
		t.Fatal("expected at least one relationship")
	}

	found := false
	for _, r := range rels {
		if r.RelationshipType == "works_at" && r.SourceEntity == "Alice" && r.TargetEntity == "Google" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Alice works_at Google', got: %+v", rels)
	}
}

func TestDetectRelationships_FamilyPattern(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	content := "Sarah is my wife"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	found := false
	for _, r := range rels {
		if r.RelationshipType == "married_to" {
			found = true
			if !r.IsBidirectional {
				t.Error("married_to should be bidirectional")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected married_to relationship, got: %+v", rels)
	}
}

func TestDetectRelationships_FriendPattern(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	content := "Bob is my friend"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	found := false
	for _, r := range rels {
		if r.RelationshipType == "friend_of" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected friend_of relationship, got: %+v", rels)
	}
}

func TestDetectRelationships_VerbBased(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	content := "Alice met Bob at the conference"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	found := false
	for _, r := range rels {
		if r.RelationshipType == "knows" && r.SourceEntity == "Alice" && r.TargetEntity == "Bob" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'Alice knows Bob', got: %+v", rels)
	}
}

func TestDetectRelationships_Cooccurrence(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	entities := []ExtractedEntity{
		{Name: "Alice", Type: storage.EntityTypePerson},
		{Name: "Acme", Type: storage.EntityTypeOrganization},
	}
	content := "Alice works at Acme Corp"
	rels, err := d.DetectRelationships(context.Background(), content, entities)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	// Should find at least one relationship (pattern-based or cooccurrence)
	if len(rels) == 0 {
		t.Error("expected at least one relationship from cooccurrence")
	}
}

func TestDetectRelationships_Deduplication(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	// Content that matches multiple patterns for the same relationship
	content := "My friend Alice met Alice at the park"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// Check no exact duplicates in source:target:type
	seen := make(map[string]bool)
	for _, r := range rels {
		key := r.SourceEntity + ":" + r.TargetEntity + ":" + r.RelationshipType
		if seen[key] {
			t.Errorf("duplicate relationship found: %s", key)
		}
		seen[key] = true
	}
}

func TestDetectRelationships_NoPatterns(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	content := "the weather is nice today"
	rels, err := d.DetectRelationships(context.Background(), content, nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d: %+v", len(rels), rels)
	}
}

func TestGetRelationshipStrength(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, DefaultRelationshipConfig())

	tests := []struct {
		name       string
		evidence   int
		recency    float64
		confidence float64
		wantMin    float64
	}{
		{"no evidence", 0, 0, 0, 0},
		{"single evidence, max recency/confidence", 1, 1.0, 1.0, 0.7},
		{"high evidence", 10, 0.5, 0.8, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.GetRelationshipStrength(tt.evidence, tt.recency, tt.confidence)
			if got < tt.wantMin {
				t.Errorf("GetRelationshipStrength(%d, %v, %v) = %v, want >= %v",
					tt.evidence, tt.recency, tt.confidence, got, tt.wantMin)
			}
		})
	}
}

func TestDefaultRelationshipConfig(t *testing.T) {
	cfg := DefaultRelationshipConfig()
	if cfg.MinConfidence != 0.6 {
		t.Errorf("MinConfidence = %v, want 0.6", cfg.MinConfidence)
	}
	if !cfg.EnableBidirectionalInference {
		t.Error("EnableBidirectionalInference should be true")
	}
}

func TestNewRelationshipDetector_DefaultConfig(t *testing.T) {
	d := NewRelationshipDetector(&mockStore{}, RelationshipConfig{})
	if d.config.MinConfidence != 0.6 {
		t.Errorf("default MinConfidence = %v, want 0.6", d.config.MinConfidence)
	}
}

func TestInferRelationshipFromContext(t *testing.T) {
	tests := []struct {
		between string
		want    string
	}{
		{" and work together ", "works_with"},
		{" and ", "associated_with"},
		{" with ", "associated_with"},
		{" at ", "located_at"},
		{" from ", "from"},
		{" xyz ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.between, func(t *testing.T) {
			got := inferRelationshipFromContext(tt.between)
			if got != tt.want {
				t.Errorf("inferRelationshipFromContext(%q) = %q, want %q", tt.between, got, tt.want)
			}
		})
	}
}

func TestDeduplicateRelationships(t *testing.T) {
	rels := []DetectedRelationship{
		{SourceEntity: "A", TargetEntity: "B", RelationshipType: "knows", Confidence: 0.7},
		{SourceEntity: "A", TargetEntity: "B", RelationshipType: "knows", Confidence: 0.9},
		{SourceEntity: "A", TargetEntity: "C", RelationshipType: "knows", Confidence: 0.5},
	}
	result := deduplicateRelationships(rels)
	if len(result) != 2 {
		t.Errorf("deduplicateRelationships = %d, want 2", len(result))
	}
	// Higher confidence should be kept
	for _, r := range result {
		if r.SourceEntity == "A" && r.TargetEntity == "B" && r.Confidence != 0.9 {
			t.Errorf("expected higher confidence 0.9, got %v", r.Confidence)
		}
	}
}

func TestDeduplicateRelationships_Bidirectional(t *testing.T) {
	rels := []DetectedRelationship{
		{SourceEntity: "A", TargetEntity: "B", RelationshipType: "friend_of", IsBidirectional: true, Confidence: 0.7},
		{SourceEntity: "B", TargetEntity: "A", RelationshipType: "friend_of", IsBidirectional: true, Confidence: 0.8},
	}
	result := deduplicateRelationships(rels)
	if len(result) != 1 {
		t.Errorf("bidirectional dedup = %d, want 1", len(result))
	}
}
