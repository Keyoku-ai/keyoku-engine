package keyoku

import (
	"context"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// newTestKeyoku creates a Keyoku with mock store and noop embedder for testing.
func newTestKeyoku(store storage.Store) *Keyoku {
	emb := embedder.NewNoop(3)
	k := &Keyoku{
		store: store,
		emb:   emb,
	}
	k.engine = engine.NewEngine(nil, emb, store, engine.DefaultEngineConfig())
	return k
}

func TestKeyoku_SetStore(t *testing.T) {
	store := &testStore{}
	k := &Keyoku{emb: embedder.NewNoop(3)}
	k.SetStore(store)
	if k.store != store {
		t.Error("SetStore did not set the store")
	}
	if k.engine == nil {
		t.Error("SetStore did not create engine")
	}
}

func TestKeyoku_Close_NilScheduler(t *testing.T) {
	store := &testStore{}
	k := newTestKeyoku(store)
	// scheduler is nil — Close should not panic
	err := k.Close()
	if err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestKeyoku_Close_NilEngine(t *testing.T) {
	k := &Keyoku{store: &testStore{}}
	// engine is nil — Close should not panic
	err := k.Close()
	if err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestKeyoku_Close_NilStore(t *testing.T) {
	k := &Keyoku{}
	err := k.Close()
	if err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestKeyoku_List(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", Content: "hello"},
				{ID: "mem-2", Content: "world"},
			}, nil
		},
	}

	k := newTestKeyoku(store)
	memories, err := k.List(context.Background(), "entity-1", 10)
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(memories) != 2 {
		t.Errorf("List returned %d, want 2", len(memories))
	}
}

func TestKeyoku_Get(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{ID: id, Content: "found"}, nil
		},
	}

	k := newTestKeyoku(store)
	mem, err := k.Get(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if mem.ID != "mem-1" {
		t.Errorf("Get ID = %q, want %q", mem.ID, "mem-1")
	}
}

func TestKeyoku_Delete(t *testing.T) {
	var deletedID string
	store := &testStore{
		deleteMemoryFn: func(_ context.Context, id string, _ bool) error {
			deletedID = id
			return nil
		},
	}

	k := newTestKeyoku(store)
	err := k.Delete(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	if deletedID != "mem-1" {
		t.Errorf("deleted ID = %q, want %q", deletedID, "mem-1")
	}
}

func TestKeyoku_DeleteAll(t *testing.T) {
	var deletedEntityID string
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", EntityID: q.EntityID},
			}, nil
		},
		deleteMemoryFn: func(_ context.Context, _ string, _ bool) error {
			return nil
		},
		deleteAllEntitiesForOwnerFn: func(_ context.Context, ownerEntityID string) (int, error) {
			deletedEntityID = ownerEntityID
			return 1, nil
		},
	}

	k := newTestKeyoku(store)
	err := k.DeleteAll(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("DeleteAll error = %v", err)
	}
	if deletedEntityID != "entity-1" {
		t.Errorf("deleted entity = %q, want %q", deletedEntityID, "entity-1")
	}
}

func TestKeyoku_Stats(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{
				{ID: "mem-1", Type: storage.TypeIdentity, State: storage.StateActive},
				{ID: "mem-2", Type: storage.TypePreference, State: storage.StateActive},
				{ID: "mem-3", Type: storage.TypeIdentity, State: storage.StateStale},
			}, nil
		},
	}

	k := newTestKeyoku(store)
	stats, err := k.Stats(context.Background(), "entity-1")
	if err != nil {
		t.Fatalf("Stats error = %v", err)
	}
	if stats.TotalMemories != 3 {
		t.Errorf("TotalMemories = %d, want 3", stats.TotalMemories)
	}
}

// --- Service tests ---

func TestEntityService_List(t *testing.T) {
	store := &testStore{
		queryEntitiesFn: func(_ context.Context, q storage.EntityQuery) ([]*storage.Entity, error) {
			return []*storage.Entity{
				{ID: "ent-1", CanonicalName: "Alice"},
			}, nil
		},
	}

	k := newTestKeyoku(store)
	entities, err := k.Entities().List(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatalf("Entities.List error = %v", err)
	}
	if len(entities) != 1 {
		t.Errorf("entities = %d, want 1", len(entities))
	}
}

func TestEntityService_Get(t *testing.T) {
	store := &testStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			return &storage.Entity{ID: id, CanonicalName: "Alice"}, nil
		},
	}

	k := newTestKeyoku(store)
	entity, err := k.Entities().Get(context.Background(), "ent-1")
	if err != nil {
		t.Fatalf("Entities.Get error = %v", err)
	}
	if entity.CanonicalName != "Alice" {
		t.Errorf("CanonicalName = %q, want %q", entity.CanonicalName, "Alice")
	}
}

func TestEntityService_Search(t *testing.T) {
	store := &testStore{
		findEntityByAliasFn: func(_ context.Context, _, alias string) (*storage.Entity, error) {
			if alias == "Bob" {
				return &storage.Entity{ID: "ent-2", CanonicalName: "Robert"}, nil
			}
			return nil, nil
		},
	}

	k := newTestKeyoku(store)
	entity, err := k.Entities().Search(context.Background(), "owner-1", "Bob")
	if err != nil {
		t.Fatalf("Entities.Search error = %v", err)
	}
	if entity == nil || entity.CanonicalName != "Robert" {
		t.Error("expected entity named Robert")
	}
}

func TestRelationshipService_List(t *testing.T) {
	store := &testStore{
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, direction string) ([]*storage.Relationship, error) {
			if direction != "both" {
				t.Errorf("direction = %q, want %q", direction, "both")
			}
			return []*storage.Relationship{
				{ID: "rel-1", RelationshipType: "knows"},
			}, nil
		},
	}

	k := newTestKeyoku(store)
	rels, err := k.Relationships().List(context.Background(), "owner-1", "ent-1")
	if err != nil {
		t.Fatalf("Relationships.List error = %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("relationships = %d, want 1", len(rels))
	}
}

func TestRelationshipService_Get(t *testing.T) {
	store := &testStore{
		getRelationshipFn: func(_ context.Context, id string) (*storage.Relationship, error) {
			return &storage.Relationship{ID: id, RelationshipType: "works_with"}, nil
		},
	}

	k := newTestKeyoku(store)
	rel, err := k.Relationships().Get(context.Background(), "rel-1")
	if err != nil {
		t.Fatalf("Relationships.Get error = %v", err)
	}
	if rel.RelationshipType != "works_with" {
		t.Errorf("RelationshipType = %q, want %q", rel.RelationshipType, "works_with")
	}
}

func TestSchemaService_CRUD(t *testing.T) {
	var createdSchema *storage.ExtractionSchema
	store := &testStore{
		createSchemaFn: func(_ context.Context, s *storage.ExtractionSchema) error {
			createdSchema = s
			return nil
		},
		getSchemaFn: func(_ context.Context, id string) (*storage.ExtractionSchema, error) {
			return &storage.ExtractionSchema{ID: id, Name: "test"}, nil
		},
		getSchemaByNameFn: func(_ context.Context, _, name string) (*storage.ExtractionSchema, error) {
			return &storage.ExtractionSchema{ID: "s-1", Name: name}, nil
		},
		querySchemasFn: func(_ context.Context, q storage.SchemaQuery) ([]*storage.ExtractionSchema, error) {
			return []*storage.ExtractionSchema{{ID: "s-1"}}, nil
		},
		updateSchemaFn: func(_ context.Context, id string, _ map[string]any) (*storage.ExtractionSchema, error) {
			return &storage.ExtractionSchema{ID: id}, nil
		},
		deleteSchemaFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	k := newTestKeyoku(store)
	ss := k.Schemas()

	// Create
	err := ss.Create(context.Background(), &storage.ExtractionSchema{Name: "test-schema"})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if createdSchema == nil || createdSchema.Name != "test-schema" {
		t.Error("Create did not pass schema correctly")
	}

	// Get
	schema, err := ss.Get(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if schema.Name != "test" {
		t.Errorf("Get Name = %q", schema.Name)
	}

	// GetByName
	schema, err = ss.GetByName(context.Background(), "entity-1", "my-schema")
	if err != nil {
		t.Fatalf("GetByName error = %v", err)
	}
	if schema.Name != "my-schema" {
		t.Errorf("GetByName Name = %q", schema.Name)
	}

	// List
	schemas, err := ss.List(context.Background(), "entity-1", true, 10)
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(schemas) != 1 {
		t.Errorf("List = %d, want 1", len(schemas))
	}

	// Update
	updated, err := ss.Update(context.Background(), "s-1", map[string]any{"name": "new"})
	if err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if updated.ID != "s-1" {
		t.Errorf("Update ID = %q", updated.ID)
	}

	// Delete
	err = ss.Delete(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("Delete error = %v", err)
	}
}

func TestExtractionService(t *testing.T) {
	store := &testStore{
		getCustomExtractionFn: func(_ context.Context, id string) (*storage.CustomExtraction, error) {
			return &storage.CustomExtraction{ID: id}, nil
		},
		getCustomExtractionsByMemoryFn: func(_ context.Context, memID string) ([]*storage.CustomExtraction, error) {
			return []*storage.CustomExtraction{{ID: "ext-1", MemoryID: memID}}, nil
		},
		queryCustomExtractionsFn: func(_ context.Context, _ storage.CustomExtractionQuery) ([]*storage.CustomExtraction, error) {
			return []*storage.CustomExtraction{{ID: "ext-1"}}, nil
		},
		deleteCustomExtractionFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	k := newTestKeyoku(store)
	xs := k.Extractions()

	// Get
	ext, err := xs.Get(context.Background(), "ext-1")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if ext.ID != "ext-1" {
		t.Errorf("Get ID = %q", ext.ID)
	}

	// GetByMemory
	exts, err := xs.GetByMemory(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("GetByMemory error = %v", err)
	}
	if len(exts) != 1 {
		t.Errorf("GetByMemory = %d, want 1", len(exts))
	}

	// List
	exts, err = xs.List(context.Background(), storage.CustomExtractionQuery{EntityID: "entity-1"})
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(exts) != 1 {
		t.Errorf("List = %d, want 1", len(exts))
	}

	// Delete
	err = xs.Delete(context.Background(), "ext-1")
	if err != nil {
		t.Fatalf("Delete error = %v", err)
	}
}

func TestRememberOptions(t *testing.T) {
	cfg := &rememberConfig{}

	WithSessionID("session-1")(cfg)
	if cfg.sessionID != "session-1" {
		t.Errorf("sessionID = %q", cfg.sessionID)
	}

	WithAgentID("agent-1")(cfg)
	if cfg.agentID != "agent-1" {
		t.Errorf("agentID = %q", cfg.agentID)
	}

	WithSource("api")(cfg)
	if cfg.source != "api" {
		t.Errorf("source = %q", cfg.source)
	}

	WithSchemaID("schema-1")(cfg)
	if cfg.schemaID != "schema-1" {
		t.Errorf("schemaID = %q", cfg.schemaID)
	}
}

func TestSearchOptions(t *testing.T) {
	cfg := &searchConfig{}

	WithLimit(25)(cfg)
	if cfg.limit != 25 {
		t.Errorf("limit = %d", cfg.limit)
	}

	WithMode(engine.ModeRecent)(cfg)
	if cfg.mode != engine.ModeRecent {
		t.Errorf("mode = %q", cfg.mode)
	}

	WithSearchAgentID("agent-1")(cfg)
	if cfg.agentID != "agent-1" {
		t.Errorf("agentID = %q", cfg.agentID)
	}
}

func TestReExportedConstants(t *testing.T) {
	// Memory types
	if TypeIdentity != storage.TypeIdentity {
		t.Error("TypeIdentity mismatch")
	}
	if TypeEphemeral != storage.TypeEphemeral {
		t.Error("TypeEphemeral mismatch")
	}

	// Memory states
	if StateActive != storage.StateActive {
		t.Error("StateActive mismatch")
	}
	if StateDeleted != storage.StateDeleted {
		t.Error("StateDeleted mismatch")
	}

	// Scorer modes
	if ModeBalanced != engine.ModeBalanced {
		t.Error("ModeBalanced mismatch")
	}
	if ModeRecent != engine.ModeRecent {
		t.Error("ModeRecent mismatch")
	}
}
