package engine

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestResolveEntity_ExactMatch(t *testing.T) {
	existing := testEntity("ent-1", "John Smith")
	store := &mockStore{
		getEntityByNameFn: func(_ context.Context, _, name string, _ storage.EntityType) (*storage.Entity, error) {
			if name == "John Smith" {
				return existing, nil
			}
			return nil, nil
		},
	}
	r := NewEntityResolver(store, &mockEmbedder{dimensions: 3}, DefaultEntityConfig())

	res, err := r.ResolveEntity(context.Background(), "owner-1", ExtractedEntity{
		Name: "John Smith", Type: storage.EntityTypePerson,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if res.IsNew {
		t.Error("expected existing entity")
	}
	if res.MatchType != "exact" {
		t.Errorf("MatchType = %q, want %q", res.MatchType, "exact")
	}
	if res.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", res.Confidence)
	}
}

func TestResolveEntity_AliasMatch(t *testing.T) {
	existing := testEntity("ent-1", "Robert Smith")
	store := &mockStore{
		findEntityByAliasFn: func(_ context.Context, _, alias string) (*storage.Entity, error) {
			if alias == "Bob Smith" {
				return existing, nil
			}
			return nil, nil
		},
	}
	r := NewEntityResolver(store, &mockEmbedder{dimensions: 3}, DefaultEntityConfig())

	res, err := r.ResolveEntity(context.Background(), "owner-1", ExtractedEntity{
		Name: "Bob Smith", Type: storage.EntityTypePerson,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if res.IsNew {
		t.Error("expected existing entity via alias")
	}
	if res.MatchType != "alias" {
		t.Errorf("MatchType = %q, want %q", res.MatchType, "alias")
	}
	if res.Confidence != 0.95 {
		t.Errorf("Confidence = %v, want 0.95", res.Confidence)
	}
}

func TestResolveEntity_SemanticMatch(t *testing.T) {
	existing := testEntity("ent-1", "Jonathan Smith")
	store := &mockStore{
		findSimilarEntitiesFn: func(_ context.Context, _ []float32, _ string, _ int, _ float64) ([]*storage.Entity, error) {
			return []*storage.Entity{existing}, nil
		},
	}
	emb := &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.5, 0.5, 0.5}, nil
		},
		dimensions: 3,
	}
	r := NewEntityResolver(store, emb, DefaultEntityConfig())

	res, err := r.ResolveEntity(context.Background(), "owner-1", ExtractedEntity{
		Name: "Jon Smith", Type: storage.EntityTypePerson,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if res.IsNew {
		t.Error("expected existing entity via semantic match")
	}
	if res.MatchType != "semantic" {
		t.Errorf("MatchType = %q, want %q", res.MatchType, "semantic")
	}
}

func TestResolveEntity_NewEntity(t *testing.T) {
	store := &mockStore{} // All lookups return nil
	r := NewEntityResolver(store, &mockEmbedder{dimensions: 3}, DefaultEntityConfig())

	res, err := r.ResolveEntity(context.Background(), "owner-1", ExtractedEntity{
		Name: "Brand New Person", Type: storage.EntityTypePerson, Confidence: 0.7,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !res.IsNew {
		t.Error("expected new entity")
	}
	if res.MatchType != "new" {
		t.Errorf("MatchType = %q, want %q", res.MatchType, "new")
	}
	if res.Confidence != 0.7 {
		t.Errorf("Confidence = %v, want 0.7", res.Confidence)
	}
}

func TestResolveEntity_FuzzyDisabled(t *testing.T) {
	cfg := DefaultEntityConfig()
	cfg.EnableFuzzyMatching = false
	store := &mockStore{} // Exact and alias return nil
	r := NewEntityResolver(store, &mockEmbedder{dimensions: 3}, cfg)

	res, err := r.ResolveEntity(context.Background(), "owner-1", ExtractedEntity{
		Name: "Someone", Type: storage.EntityTypePerson,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !res.IsNew {
		t.Error("expected new entity when fuzzy matching disabled")
	}
}

func TestMergeEntities(t *testing.T) {
	primary := testEntity("ent-1", "John Smith")
	primary.Aliases = []string{"Johnny"}
	duplicate := testEntity("ent-2", "Jon Smith")
	duplicate.Aliases = []string{"JS"}
	duplicate.MentionCount = 3

	var addedAliases []string
	var mentionCounts int
	var deletedID string
	store := &mockStore{
		addEntityAliasFn: func(_ context.Context, _ string, alias string) error {
			addedAliases = append(addedAliases, alias)
			return nil
		},
		getEntityMentionsFn: func(_ context.Context, _ string, _ int) ([]*storage.EntityMention, error) {
			return []*storage.EntityMention{
				{EntityID: "ent-2", MemoryID: "mem-1", MentionText: "Jon"},
			}, nil
		},
		updateEntityMentionCountFn: func(_ context.Context, _ string) error {
			mentionCounts++
			return nil
		},
		deleteEntityFn: func(_ context.Context, id string) error {
			deletedID = id
			return nil
		},
	}

	r := NewEntityResolver(store, &mockEmbedder{dimensions: 3}, DefaultEntityConfig())
	err := r.MergeEntities(context.Background(), primary, duplicate)
	if err != nil {
		t.Fatalf("MergeEntities error = %v", err)
	}
	if len(addedAliases) < 1 {
		t.Error("expected aliases to be added")
	}
	if mentionCounts != 3 {
		t.Errorf("mention counts updated = %d, want 3", mentionCounts)
	}
	if deletedID != "ent-2" {
		t.Errorf("deleted entity = %q, want %q", deletedID, "ent-2")
	}
}

func TestExtractProperNouns(t *testing.T) {
	tests := []struct {
		input string
		want  int // minimum expected proper nouns
	}{
		{"My friend John went to the store", 1},
		{"Then Alice met Bob at the park", 2},
		{"no proper nouns here", 0},
		{"My colleague Sarah Chen works at Google", 2}, // "Sarah Chen" and "Google"
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			nouns := extractProperNouns(tt.input)
			if len(nouns) < tt.want {
				t.Errorf("extractProperNouns(%q) found %d nouns, want >= %d: %v", tt.input, len(nouns), tt.want, nouns)
			}
		})
	}
}

func TestExtractQuotedStrings(t *testing.T) {
	tests := []struct {
		input string
		count int
	}{
		{`He said "hello world"`, 1},
		{`"first" and "second"`, 2},
		{"no quotes here", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			quoted := extractQuotedStrings(tt.input)
			if len(quoted) != tt.count {
				t.Errorf("extractQuotedStrings(%q) = %d, want %d: %v", tt.input, len(quoted), tt.count, quoted)
			}
		})
	}
}

func TestInferEntityType(t *testing.T) {
	tests := []struct {
		name    string
		context string
		want    storage.EntityType
	}{
		{"John", "my friend John went home", storage.EntityTypePerson},
		{"Acme", "works at Acme company", storage.EntityTypeOrganization},
		{"Seattle", "lives in Seattle city", storage.EntityTypeLocation},
		{"iPhone", "bought a new iPhone device", storage.EntityTypeProduct},
		{"Unknown", "something something", storage.EntityTypeOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferEntityType(tt.name, tt.context)
			if got != tt.want {
				t.Errorf("inferEntityType(%q, ...) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestDefaultEntityConfig(t *testing.T) {
	cfg := DefaultEntityConfig()
	if cfg.SemanticMatchThreshold != 0.85 {
		t.Errorf("SemanticMatchThreshold = %v, want 0.85", cfg.SemanticMatchThreshold)
	}
	if !cfg.EnableFuzzyMatching {
		t.Error("EnableFuzzyMatching should be true")
	}
	if cfg.MaxAliases != 10 {
		t.Errorf("MaxAliases = %d, want 10", cfg.MaxAliases)
	}
}

func TestNewEntityResolver_DefaultConfig(t *testing.T) {
	r := NewEntityResolver(&mockStore{}, &mockEmbedder{dimensions: 3}, EntityConfig{})
	if r.config.SemanticMatchThreshold != 0.85 {
		t.Errorf("default SemanticMatchThreshold = %v, want 0.85", r.config.SemanticMatchThreshold)
	}
	if r.config.MaxAliases != 10 {
		t.Errorf("default MaxAliases = %d, want 10", r.config.MaxAliases)
	}
}

func TestGetEntityContextHelper(t *testing.T) {
	content := "My friend John Smith works at Acme Corporation"
	ctx := getEntityContext(content, "John Smith")
	if ctx == "" {
		t.Error("expected non-empty context")
	}
	if len(ctx) > len(content) {
		t.Error("context should not exceed original content")
	}
}

func TestGetEntityContextHelper_NotFound(t *testing.T) {
	ctx := getEntityContext("Hello world", "NotFound")
	if ctx != "" {
		t.Errorf("expected empty context for missing entity, got %q", ctx)
	}
}

func TestDeduplicateEntities(t *testing.T) {
	entities := []ExtractedEntity{
		{Name: "John", Type: storage.EntityTypePerson},
		{Name: "john", Type: storage.EntityTypePerson},
		{Name: "John", Type: storage.EntityTypeOrganization},
	}
	result := deduplicateEntities(entities)
	if len(result) != 2 {
		t.Errorf("deduplicateEntities = %d, want 2 (same name+type deduped)", len(result))
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("expected true")
	}
	if contains([]string{"a", "b", "c"}, "d") {
		t.Error("expected false")
	}
	if contains(nil, "a") {
		t.Error("expected false for nil slice")
	}
}
