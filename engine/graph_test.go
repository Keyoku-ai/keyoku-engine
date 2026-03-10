// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func newTestGraph(store *mockStore) *GraphEngine {
	return NewGraphEngine(store, DefaultGraphConfig())
}

func TestTraverseFrom_HappyPath(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	relAB := testRelationship("rel-1", "ent-a", "ent-b", "friend_of")

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			if entityID == "ent-a" {
				return []*storage.Relationship{relAB}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID: "ent-a",
		MaxDepth:      2,
	})
	if err != nil {
		t.Fatalf("TraverseFrom error = %v", err)
	}
	if result.RootEntity.ID != "ent-a" {
		t.Errorf("RootEntity = %q, want %q", result.RootEntity.ID, "ent-a")
	}
	if len(result.Nodes) < 2 {
		t.Errorf("Nodes = %d, want >= 2", len(result.Nodes))
	}
	if result.TotalEdges < 1 {
		t.Errorf("TotalEdges = %d, want >= 1", result.TotalEdges)
	}
}

func TestTraverseFrom_ByName(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	store := &mockStore{
		findEntityByAliasFn: func(_ context.Context, _, alias string) (*storage.Entity, error) {
			if alias == "Alice" {
				return entityA, nil
			}
			return nil, nil
		},
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			if id == "ent-a" {
				return entityA, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityName: "Alice",
		MaxDepth:        1,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if result.RootEntity.ID != "ent-a" {
		t.Errorf("RootEntity = %q, want %q", result.RootEntity.ID, "ent-a")
	}
}

func TestTraverseFrom_MaxDepth(t *testing.T) {
	entities := map[string]*storage.Entity{
		"ent-a": testEntity("ent-a", "A"),
		"ent-b": testEntity("ent-b", "B"),
		"ent-c": testEntity("ent-c", "C"),
	}
	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			if e, ok := entities[id]; ok {
				return e, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			switch entityID {
			case "ent-a":
				return []*storage.Relationship{testRelationship("r1", "ent-a", "ent-b", "knows")}, nil
			case "ent-b":
				return []*storage.Relationship{testRelationship("r2", "ent-b", "ent-c", "knows")}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID: "ent-a",
		MaxDepth:      1,
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	// At max depth 1, should reach B but not C
	if _, hasC := result.Nodes["ent-c"]; hasC {
		t.Error("ent-c should not be reachable at depth 1")
	}
}

func TestTraverseFrom_FilterByRelType(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	entityC := testEntity("ent-c", "Acme")

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			case "ent-c":
				return entityC, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			if entityID == "ent-a" {
				return []*storage.Relationship{
					testRelationship("r1", "ent-a", "ent-b", "friend_of"),
					testRelationship("r2", "ent-a", "ent-c", "works_at"),
				}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID:     "ent-a",
		RelationshipTypes: []string{"friend_of"},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if _, hasC := result.Nodes["ent-c"]; hasC {
		t.Error("ent-c should be filtered out (wrong relationship type)")
	}
}

func TestTraverseFrom_FilterByEntityType(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	entityC := testEntity("ent-c", "Acme")
	entityC.Type = storage.EntityTypeOrganization

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			case "ent-c":
				return entityC, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			if entityID == "ent-a" {
				return []*storage.Relationship{
					testRelationship("r1", "ent-a", "ent-b", "knows"),
					testRelationship("r2", "ent-a", "ent-c", "works_at"),
				}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID: "ent-a",
		EntityTypes:   []storage.EntityType{storage.EntityTypePerson},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if _, hasC := result.Nodes["ent-c"]; hasC {
		t.Error("ent-c (org) should be filtered when only persons requested")
	}
}

func TestTraverseFrom_MinStrength(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	weakRel := testRelationship("r1", "ent-a", "ent-b", "knows")
	weakRel.Strength = 0.1 // Below default min strength (0.3)

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, _ string) ([]*storage.Relationship, error) {
			return []*storage.Relationship{weakRel}, nil
		},
	}
	g := newTestGraph(store)

	result, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID: "ent-a",
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if _, hasB := result.Nodes["ent-b"]; hasB {
		t.Error("ent-b should be filtered (weak relationship below threshold)")
	}
}

func TestTraverseFrom_MissingEntity(t *testing.T) {
	store := &mockStore{} // GetEntity returns nil
	g := newTestGraph(store)

	_, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{
		StartEntityID: "nonexistent",
	})
	if err == nil {
		t.Error("expected error for missing start entity")
	}
}

func TestTraverseFrom_NoStartSpecified(t *testing.T) {
	g := newTestGraph(&mockStore{})
	_, err := g.TraverseFrom(context.Background(), "owner-1", GraphQuery{})
	if err == nil {
		t.Error("expected error when no start entity specified")
	}
}

func TestFindPath_Direct(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	relAB := testRelationship("r1", "ent-a", "ent-b", "knows")

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			if entityID == "ent-a" {
				return []*storage.Relationship{relAB}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	path, err := g.FindPath(context.Background(), "owner-1", "ent-a", "ent-b")
	if err != nil {
		t.Fatalf("FindPath error = %v", err)
	}
	if len(path) != 2 {
		t.Fatalf("path length = %d, want 2", len(path))
	}
	if path[0] != "ent-a" || path[1] != "ent-b" {
		t.Errorf("path = %v, want [ent-a, ent-b]", path)
	}
}

func TestFindPath_TwoHop(t *testing.T) {
	entities := map[string]*storage.Entity{
		"ent-a": testEntity("ent-a", "A"),
		"ent-b": testEntity("ent-b", "B"),
		"ent-c": testEntity("ent-c", "C"),
	}
	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			if e, ok := entities[id]; ok {
				return e, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, entityID string, _ string) ([]*storage.Relationship, error) {
			switch entityID {
			case "ent-a":
				return []*storage.Relationship{testRelationship("r1", "ent-a", "ent-b", "knows")}, nil
			case "ent-b":
				return []*storage.Relationship{
					testRelationship("r1", "ent-a", "ent-b", "knows"),
					testRelationship("r2", "ent-b", "ent-c", "knows"),
				}, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	path, err := g.FindPath(context.Background(), "owner-1", "ent-a", "ent-c")
	if err != nil {
		t.Fatalf("FindPath error = %v", err)
	}
	if len(path) != 3 {
		t.Fatalf("path length = %d, want 3", len(path))
	}
	if path[0] != "ent-a" || path[2] != "ent-c" {
		t.Errorf("path = %v, want [ent-a, ent-b, ent-c]", path)
	}
}

func TestFindPath_SameEntity(t *testing.T) {
	g := newTestGraph(&mockStore{})

	path, err := g.FindPath(context.Background(), "owner-1", "ent-a", "ent-a")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(path) != 1 || path[0] != "ent-a" {
		t.Errorf("path = %v, want [ent-a]", path)
	}
}

func TestFindPath_NoPath(t *testing.T) {
	entityA := testEntity("ent-a", "A")
	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			if id == "ent-a" {
				return entityA, nil
			}
			return nil, nil
		},
	}
	g := newTestGraph(store)

	_, err := g.FindPath(context.Background(), "owner-1", "ent-a", "ent-z")
	if err == nil {
		t.Error("expected error for no path")
	}
}

func TestGetEntityNeighbors(t *testing.T) {
	entityA := testEntity("ent-a", "Alice")
	entityB := testEntity("ent-b", "Bob")
	relAB := testRelationship("r1", "ent-a", "ent-b", "knows")

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entityA, nil
			case "ent-b":
				return entityB, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, _ string) ([]*storage.Relationship, error) {
			return []*storage.Relationship{relAB}, nil
		},
	}
	g := newTestGraph(store)

	edges, err := g.GetEntityNeighbors(context.Background(), "owner-1", "ent-a")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("neighbors = %d, want 1", len(edges))
	}
}

func TestGetEntityContext(t *testing.T) {
	entity := testEntity("ent-a", "Alice")
	relAB := testRelationship("r1", "ent-a", "ent-b", "knows")
	entityB := testEntity("ent-b", "Bob")

	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-a":
				return entity, nil
			case "ent-b":
				return entityB, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, _ string) ([]*storage.Relationship, error) {
			return []*storage.Relationship{relAB}, nil
		},
	}
	g := newTestGraph(store)

	ctx, err := g.GetEntityContext(context.Background(), "owner-1", "ent-a")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if ctx.Entity.ID != "ent-a" {
		t.Errorf("Entity = %q, want %q", ctx.Entity.ID, "ent-a")
	}
	if len(ctx.Relationships) != 1 {
		t.Errorf("Relationships = %d, want 1", len(ctx.Relationships))
	}
}

func TestGetEntityContext_NotFound(t *testing.T) {
	store := &mockStore{}
	g := newTestGraph(store)

	_, err := g.GetEntityContext(context.Background(), "owner-1", "nonexistent")
	if err == nil {
		t.Error("expected error for missing entity")
	}
}

func TestFindRelatedEntities(t *testing.T) {
	entityB := testEntity("ent-b", "Bob")
	entityC := testEntity("ent-c", "Charlie")
	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-b":
				return entityB, nil
			case "ent-c":
				return entityC, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, _ string) ([]*storage.Relationship, error) {
			return []*storage.Relationship{
				testRelationship("r1", "ent-a", "ent-b", "friend_of"),
				testRelationship("r2", "ent-a", "ent-c", "colleague_of"),
			}, nil
		},
	}
	g := newTestGraph(store)

	entities, err := g.FindRelatedEntities(context.Background(), "owner-1", "ent-a", []string{"friend_of"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(entities) != 1 {
		t.Errorf("related entities = %d, want 1 (friend_of only)", len(entities))
	}
	if len(entities) > 0 && entities[0].ID != "ent-b" {
		t.Errorf("related entity = %q, want %q", entities[0].ID, "ent-b")
	}
}

func TestFindRelatedEntities_AllTypes(t *testing.T) {
	entityB := testEntity("ent-b", "Bob")
	entityC := testEntity("ent-c", "Charlie")
	store := &mockStore{
		getEntityFn: func(_ context.Context, id string) (*storage.Entity, error) {
			switch id {
			case "ent-b":
				return entityB, nil
			case "ent-c":
				return entityC, nil
			}
			return nil, nil
		},
		getEntityRelationshipsFn: func(_ context.Context, _, _ string, _ string) ([]*storage.Relationship, error) {
			return []*storage.Relationship{
				testRelationship("r1", "ent-a", "ent-b", "friend_of"),
				testRelationship("r2", "ent-a", "ent-c", "colleague_of"),
			}, nil
		},
	}
	g := newTestGraph(store)

	entities, err := g.FindRelatedEntities(context.Background(), "owner-1", "ent-a", nil)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(entities) != 2 {
		t.Errorf("related entities = %d, want 2", len(entities))
	}
}

func TestDefaultGraphConfig(t *testing.T) {
	cfg := DefaultGraphConfig()
	if cfg.MaxTraversalDepth != 5 {
		t.Errorf("MaxTraversalDepth = %d, want 5", cfg.MaxTraversalDepth)
	}
	if cfg.MaxResults != 100 {
		t.Errorf("MaxResults = %d, want 100", cfg.MaxResults)
	}
	if cfg.MinRelationshipStrength != 0.3 {
		t.Errorf("MinRelationshipStrength = %v, want 0.3", cfg.MinRelationshipStrength)
	}
}

func TestNewGraphEngine_DefaultConfig(t *testing.T) {
	g := NewGraphEngine(&mockStore{}, GraphConfig{})
	if g.config.MaxTraversalDepth != 5 {
		t.Errorf("default MaxTraversalDepth = %d, want 5", g.config.MaxTraversalDepth)
	}
	if g.config.MaxResults != 100 {
		t.Errorf("default MaxResults = %d, want 100", g.config.MaxResults)
	}
	if g.config.MinRelationshipStrength != 0.3 {
		t.Errorf("default MinRelationshipStrength = %v, want 0.3", g.config.MinRelationshipStrength)
	}
}

// Ensure unused imports are used
var _ = fmt.Sprintf
