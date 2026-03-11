// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package storage

import (
	"context"
	"math"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLite(":memory:", 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testEncodeEmbedding(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func testMemory(entityID string) *Memory {
	now := time.Now()
	return &Memory{
		EntityID:          entityID,
		AgentID:           "default",
		Content:           "test memory content",
		Hash:              "hash_" + entityID,
		Type:              TypeEvent,
		Importance:        0.5,
		Confidence:        0.8,
		Stability:         60,
		State:             StateActive,
		Tags:              StringSlice{"tag1"},
		LastAccessedAt:    &now,
		ImportanceFactors: StringSlice{},
		ConfidenceFactors: StringSlice{},
	}
}

// --- Memory CRUD ---

func TestSQLiteStore_CreateAndGetMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	if err := s.CreateMemory(ctx, mem); err != nil {
		t.Fatalf("CreateMemory error = %v", err)
	}
	if mem.ID == "" {
		t.Error("expected auto-generated ID")
	}

	got, err := s.GetMemory(ctx, mem.ID)
	if err != nil {
		t.Fatalf("GetMemory error = %v", err)
	}
	if got.Content != "test memory content" {
		t.Errorf("Content = %q, want %q", got.Content, "test memory content")
	}
	if got.EntityID != "user-1" {
		t.Errorf("EntityID = %q, want %q", got.EntityID, "user-1")
	}
	if got.State != StateActive {
		t.Errorf("State = %q, want %q", got.State, StateActive)
	}
}

func TestSQLiteStore_GetMemory_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetMemory(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent memory")
	}
}

func TestSQLiteStore_GetMemoriesByIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem1 := testMemory("user-1")
	mem1.Hash = "hash1"
	mem2 := testMemory("user-1")
	mem2.Hash = "hash2"
	mem2.Content = "second memory"
	_ = s.CreateMemory(ctx, mem1)
	_ = s.CreateMemory(ctx, mem2)

	mems, err := s.GetMemoriesByIDs(ctx, []string{mem1.ID, mem2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 2 {
		t.Errorf("got %d, want 2", len(mems))
	}

	// Empty list
	mems, err = s.GetMemoriesByIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mems != nil {
		t.Errorf("expected nil for empty IDs, got %v", mems)
	}
}

func TestSQLiteStore_UpdateMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	_ = s.CreateMemory(ctx, mem)

	newContent := "updated content"
	newImportance := 0.9
	updated, err := s.UpdateMemory(ctx, mem.ID, MemoryUpdate{
		Content:    &newContent,
		Importance: &newImportance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "updated content" {
		t.Errorf("Content = %q, want %q", updated.Content, "updated content")
	}
	if updated.Importance != 0.9 {
		t.Errorf("Importance = %v, want 0.9", updated.Importance)
	}
	if updated.Version != 2 {
		t.Errorf("Version = %d, want 2", updated.Version)
	}
}

func TestSQLiteStore_DeleteMemory_Soft(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	mem.Embedding = testEncodeEmbedding([]float32{1, 0, 0})
	_ = s.CreateMemory(ctx, mem)

	if err := s.DeleteMemory(ctx, mem.ID, false); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetMemory(ctx, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateDeleted {
		t.Errorf("State = %q, want deleted", got.State)
	}
	if got.DeletedAt == nil {
		t.Error("DeletedAt should be set")
	}
}

func TestSQLiteStore_DeleteMemory_Hard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	_ = s.CreateMemory(ctx, mem)

	if err := s.DeleteMemory(ctx, mem.ID, true); err != nil {
		t.Fatal(err)
	}

	_, err := s.GetMemory(ctx, mem.ID)
	if err == nil {
		t.Error("expected error after hard delete")
	}
}

// --- Vector Search ---

func TestSQLiteStore_FindSimilar(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem1 := testMemory("user-1")
	mem1.Hash = "h1"
	mem1.Embedding = testEncodeEmbedding([]float32{1, 0, 0})
	_ = s.CreateMemory(ctx, mem1)

	mem2 := testMemory("user-1")
	mem2.Hash = "h2"
	mem2.Content = "other content"
	mem2.Embedding = testEncodeEmbedding([]float32{0, 1, 0})
	_ = s.CreateMemory(ctx, mem2)

	mem3 := testMemory("user-2") // different entity
	mem3.Hash = "h3"
	mem3.Embedding = testEncodeEmbedding([]float32{0.9, 0.1, 0})
	_ = s.CreateMemory(ctx, mem3)

	results, err := s.FindSimilar(ctx, []float32{1, 0, 0}, "user-1", 10, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	// Should only return user-1 memories
	for _, r := range results {
		if r.Memory.EntityID != "user-1" {
			t.Errorf("got entity %q, want user-1", r.Memory.EntityID)
		}
	}
}

func TestSQLiteStore_FindSimilarWithOptions_AgentFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem1 := testMemory("user-1")
	mem1.Hash = "h1"
	mem1.AgentID = "agent-a"
	mem1.Embedding = testEncodeEmbedding([]float32{1, 0, 0})
	_ = s.CreateMemory(ctx, mem1)

	mem2 := testMemory("user-1")
	mem2.Hash = "h2"
	mem2.AgentID = "agent-b"
	mem2.Embedding = testEncodeEmbedding([]float32{0.9, 0.1, 0})
	_ = s.CreateMemory(ctx, mem2)

	results, err := s.FindSimilarWithOptions(ctx, []float32{1, 0, 0}, "user-1", 10, 0.0, SimilarityOptions{AgentID: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Memory.AgentID != "agent-a" {
			t.Errorf("got agent %q, want agent-a", r.Memory.AgentID)
		}
	}
}

// --- Queries ---

func TestSQLiteStore_QueryMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		mem := testMemory("user-1")
		mem.Hash = "qh" + string(rune('0'+i))
		if i < 3 {
			mem.Type = TypeEvent
		} else {
			mem.Type = TypePlan
		}
		_ = s.CreateMemory(ctx, mem)
	}

	// Filter by type
	results, err := s.QueryMemories(ctx, MemoryQuery{
		EntityID: "user-1",
		Types:    []MemoryType{TypeEvent},
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("type filter: got %d, want 3", len(results))
	}

	// Filter by state
	results, err = s.QueryMemories(ctx, MemoryQuery{
		EntityID: "user-1",
		States:   []MemoryState{StateActive},
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Errorf("state filter: got %d, want 5", len(results))
	}

	// Limit
	results, err = s.QueryMemories(ctx, MemoryQuery{
		EntityID: "user-1",
		Limit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("limit: got %d, want 2", len(results))
	}
}

func TestSQLiteStore_GetRecentMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	_ = s.CreateMemory(ctx, mem)

	results, err := s.GetRecentMemories(ctx, "user-1", 24, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d, want 1", len(results))
	}
}

// --- Deduplication ---

func TestSQLiteStore_FindByHash(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	mem.Hash = "unique_hash"
	_ = s.CreateMemory(ctx, mem)

	found, err := s.FindByHash(ctx, "user-1", "unique_hash")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find memory by hash")
	}
	if found.ID != mem.ID {
		t.Errorf("ID = %q, want %q", found.ID, mem.ID)
	}

	// Not found
	found, err = s.FindByHash(ctx, "user-1", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found != nil {
		t.Error("expected nil for non-existent hash")
	}
}

func TestSQLiteStore_FindByHashWithAgent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	mem.Hash = "agent_hash"
	mem.AgentID = "agent-x"
	_ = s.CreateMemory(ctx, mem)

	found, err := s.FindByHashWithAgent(ctx, "user-1", "agent-x", "agent_hash")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find memory")
	}

	// Wrong agent
	found, err = s.FindByHashWithAgent(ctx, "user-1", "agent-y", "agent_hash")
	if err != nil {
		t.Fatal(err)
	}
	if found != nil {
		t.Error("expected nil for wrong agent")
	}
}

// --- History ---

func TestSQLiteStore_LogAndGetHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	entry := &HistoryEntry{
		MemoryID:  mem.ID,
		Operation: "create",
		Changes:   JSONMap{"content": "test"},
		Reason:    "extracted",
	}
	if err := s.LogHistory(ctx, entry); err != nil {
		t.Fatal(err)
	}

	history, err := s.GetHistory(ctx, mem.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("got %d entries, want 1", len(history))
	}
	if history[0].Operation != "create" {
		t.Errorf("Operation = %q, want create", history[0].Operation)
	}
}

// --- Session Messages ---

func TestSQLiteStore_SessionMessages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	msg := &SessionMessage{
		EntityID:   "user-1",
		SessionID:  "s1",
		Role:       "user",
		Content:    "hello",
		TurnNumber: 1,
	}
	if err := s.AddSessionMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.GetRecentSessionMessages(ctx, "user-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d, want 1", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("Content = %q, want hello", msgs[0].Content)
	}
}

// --- Access Tracking ---

func TestSQLiteStore_UpdateAccessStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	if err := s.UpdateAccessStats(ctx, []string{mem.ID}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetMemory(ctx, mem.ID)
	if got.AccessCount != 1 {
		t.Errorf("AccessCount = %d, want 1", got.AccessCount)
	}

	// Empty IDs should not error
	if err := s.UpdateAccessStats(ctx, nil); err != nil {
		t.Errorf("empty IDs error = %v", err)
	}
}

func TestSQLiteStore_UpdateStability(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	if err := s.UpdateStability(ctx, mem.ID, 120.0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMemory(ctx, mem.ID)
	if got.Stability != 120.0 {
		t.Errorf("Stability = %v, want 120", got.Stability)
	}
}

// --- Lifecycle ---

func TestSQLiteStore_TransitionState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	if err := s.TransitionState(ctx, mem.ID, StateStale, "decayed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMemory(ctx, mem.ID)
	if got.State != StateStale {
		t.Errorf("State = %q, want stale", got.State)
	}
}

func TestSQLiteStore_GetStaleMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	mem.State = StateStale
	s.CreateMemory(ctx, mem)

	results, err := s.GetStaleMemories(ctx, "user-1", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 stale memory")
	}
}

func TestSQLiteStore_GetAllEntities(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem1 := testMemory("user-1")
	mem1.Hash = "e1"
	s.CreateMemory(ctx, mem1)
	mem2 := testMemory("user-2")
	mem2.Hash = "e2"
	s.CreateMemory(ctx, mem2)

	entities, err := s.GetAllEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 2 {
		t.Errorf("got %d entities, want 2", len(entities))
	}
}

func TestSQLiteStore_GetActiveMemoriesForDecay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mem := testMemory("user-1")
		mem.Hash = "decay" + string(rune('0'+i))
		s.CreateMemory(ctx, mem)
	}

	results, err := s.GetActiveMemoriesForDecay(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("got %d, want 3", len(results))
	}
}

func TestSQLiteStore_BatchTransitionStates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem1 := testMemory("user-1")
	mem1.Hash = "bt1"
	mem2 := testMemory("user-1")
	mem2.Hash = "bt2"
	s.CreateMemory(ctx, mem1)
	s.CreateMemory(ctx, mem2)

	count, err := s.BatchTransitionStates(ctx, []StateTransition{
		{MemoryID: mem1.ID, NewState: StateStale, Reason: "test"},
		{MemoryID: mem2.ID, NewState: StateArchived, Reason: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("affected = %d, want 2", count)
	}

	// Empty list
	count, err = s.BatchTransitionStates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("empty batch = %d, want 0", count)
	}
}

// --- Entity CRUD ---

func TestSQLiteStore_EntityCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entity := &Entity{
		OwnerEntityID: "user-1",
		CanonicalName: "John Smith",
		Type:          EntityTypePerson,
		Aliases:       StringSlice{"Johnny"},
		Attributes:    JSONMap{"age": float64(30)},
	}

	// Create
	if err := s.CreateEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if entity.ID == "" {
		t.Error("expected auto-generated ID")
	}

	// Get
	got, err := s.GetEntity(ctx, entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CanonicalName != "John Smith" {
		t.Errorf("CanonicalName = %q, want John Smith", got.CanonicalName)
	}

	// GetByName
	got, err = s.GetEntityByName(ctx, "user-1", "John Smith", EntityTypePerson)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected entity by name")
	}

	// FindByAlias
	got, err = s.FindEntityByAlias(ctx, "user-1", "Johnny")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected entity by alias")
	}

	// Update
	_, err = s.UpdateEntity(ctx, entity.ID, map[string]any{"description": "test person"})
	if err != nil {
		t.Fatal(err)
	}

	// AddAlias
	if err := s.AddEntityAlias(ctx, entity.ID, "J. Smith"); err != nil {
		t.Fatal(err)
	}

	// MentionCount
	if err := s.UpdateEntityMentionCount(ctx, entity.ID); err != nil {
		t.Fatal(err)
	}

	// Query
	entities, err := s.QueryEntities(ctx, EntityQuery{
		OwnerEntityID: "user-1",
		Limit:         10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Errorf("query got %d, want 1", len(entities))
	}

	// Delete
	if err := s.DeleteEntity(ctx, entity.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetEntity(ctx, entity.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestSQLiteStore_DeleteAllEntitiesForOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s.CreateEntity(ctx, &Entity{
			OwnerEntityID: "user-1",
			CanonicalName: "Entity" + string(rune('A'+i)),
			Type:          EntityTypePerson,
			Aliases:       StringSlice{},
			Attributes:    JSONMap{},
		})
	}

	count, err := s.DeleteAllEntitiesForOwner(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("deleted %d, want 3", count)
	}
}

func TestSQLiteStore_EntityMentions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	entity := &Entity{
		OwnerEntityID: "user-1",
		CanonicalName: "Alice",
		Type:          EntityTypePerson,
		Aliases:       StringSlice{},
		Attributes:    JSONMap{},
	}
	s.CreateEntity(ctx, entity)

	mention := &EntityMention{
		EntityID:       entity.ID,
		MemoryID:       mem.ID,
		MentionText:    "Alice",
		Confidence:     0.9,
		ContextSnippet: "met Alice",
	}
	if err := s.CreateEntityMention(ctx, mention); err != nil {
		t.Fatal(err)
	}

	mentions, err := s.GetEntityMentions(ctx, entity.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 1 {
		t.Errorf("got %d mentions, want 1", len(mentions))
	}

	// GetMemoryEntities
	entities, err := s.GetMemoryEntities(ctx, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Errorf("got %d entities for memory, want 1", len(entities))
	}
}

func TestSQLiteStore_FindSimilarEntities(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entity := &Entity{
		OwnerEntityID: "user-1",
		CanonicalName: "TestEntity",
		Type:          EntityTypePerson,
		Embedding:     testEncodeEmbedding([]float32{1, 0, 0}),
		Aliases:       StringSlice{},
		Attributes:    JSONMap{},
	}
	s.CreateEntity(ctx, entity)

	results, err := s.FindSimilarEntities(ctx, []float32{0.9, 0.1, 0}, "user-1", 10, 0.0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d, want 1", len(results))
	}
}

// --- Relationship CRUD ---

func TestSQLiteStore_RelationshipCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create entities first
	e1 := &Entity{OwnerEntityID: "user-1", CanonicalName: "Alice", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	e2 := &Entity{OwnerEntityID: "user-1", CanonicalName: "Bob", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	s.CreateEntity(ctx, e1)
	s.CreateEntity(ctx, e2)

	rel := &Relationship{
		OwnerEntityID:    "user-1",
		SourceEntityID:   e1.ID,
		TargetEntityID:   e2.ID,
		RelationshipType: "knows",
		Strength:         0.8,
		Confidence:       0.9,
		Attributes:       JSONMap{},
	}

	// Create
	if err := s.CreateRelationship(ctx, rel); err != nil {
		t.Fatal(err)
	}

	// Get
	got, err := s.GetRelationship(ctx, rel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RelationshipType != "knows" {
		t.Errorf("type = %q, want knows", got.RelationshipType)
	}

	// Find
	got, err = s.FindRelationship(ctx, "user-1", e1.ID, e2.ID, "knows")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected to find relationship")
	}

	// GetEntityRelationships
	rels, err := s.GetEntityRelationships(ctx, "user-1", e1.ID, "outgoing")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Errorf("outgoing got %d, want 1", len(rels))
	}

	rels, err = s.GetEntityRelationships(ctx, "user-1", e2.ID, "incoming")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Errorf("incoming got %d, want 1", len(rels))
	}

	rels, err = s.GetEntityRelationships(ctx, "user-1", e1.ID, "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Errorf("both got %d, want 1", len(rels))
	}

	// Update
	_, err = s.UpdateRelationship(ctx, rel.ID, map[string]any{"strength": 0.95})
	if err != nil {
		t.Fatal(err)
	}

	// IncrementEvidence
	if err := s.IncrementRelationshipEvidence(ctx, rel.ID); err != nil {
		t.Fatal(err)
	}

	// Query
	rels, err = s.QueryRelationships(ctx, RelationshipQuery{
		OwnerEntityID: "user-1",
		Limit:         10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Errorf("query got %d, want 1", len(rels))
	}

	// Delete
	if err := s.DeleteRelationship(ctx, rel.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetRelationship(ctx, rel.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestSQLiteStore_RelationshipEvidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)
	e1 := &Entity{OwnerEntityID: "user-1", CanonicalName: "A", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	e2 := &Entity{OwnerEntityID: "user-1", CanonicalName: "B", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	s.CreateEntity(ctx, e1)
	s.CreateEntity(ctx, e2)
	rel := &Relationship{OwnerEntityID: "user-1", SourceEntityID: e1.ID, TargetEntityID: e2.ID, RelationshipType: "knows", Strength: 0.5, Confidence: 0.5, Attributes: JSONMap{}}
	s.CreateRelationship(ctx, rel)

	evidence := &RelationshipEvidence{
		RelationshipID: rel.ID,
		MemoryID:       mem.ID,
		EvidenceText:   "they met",
		Confidence:     0.8,
	}
	if err := s.CreateRelationshipEvidence(ctx, evidence); err != nil {
		t.Fatal(err)
	}

	evs, err := s.GetRelationshipEvidence(ctx, rel.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Errorf("got %d evidence, want 1", len(evs))
	}
}

func TestSQLiteStore_GetRelationshipPath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1 := &Entity{OwnerEntityID: "user-1", CanonicalName: "A", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	e2 := &Entity{OwnerEntityID: "user-1", CanonicalName: "B", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	e3 := &Entity{OwnerEntityID: "user-1", CanonicalName: "C", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	s.CreateEntity(ctx, e1)
	s.CreateEntity(ctx, e2)
	s.CreateEntity(ctx, e3)

	// A -> B -> C
	_ = s.CreateRelationship(ctx, &Relationship{OwnerEntityID: "user-1", SourceEntityID: e1.ID, TargetEntityID: e2.ID, RelationshipType: "knows", Strength: 0.5, Confidence: 0.5, Attributes: JSONMap{}})
	_ = s.CreateRelationship(ctx, &Relationship{OwnerEntityID: "user-1", SourceEntityID: e2.ID, TargetEntityID: e3.ID, RelationshipType: "knows", Strength: 0.5, Confidence: 0.5, Attributes: JSONMap{}})

	// Direct
	path, err := s.GetRelationshipPath(ctx, "user-1", e1.ID, e2.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 2 {
		t.Errorf("direct path len = %d, want 2", len(path))
	}

	// 2-hop
	path, err = s.GetRelationshipPath(ctx, "user-1", e1.ID, e3.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 {
		t.Errorf("2-hop path len = %d, want 3", len(path))
	}

	// No path (create isolated entity)
	e4 := &Entity{OwnerEntityID: "user-1", CanonicalName: "D", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	s.CreateEntity(ctx, e4)
	_, err = s.GetRelationshipPath(ctx, "user-1", e1.ID, e4.ID, 5)
	if err == nil {
		t.Error("expected error for no path")
	}
}

func TestSQLiteStore_DeleteAllRelationshipsForOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e1 := &Entity{OwnerEntityID: "user-1", CanonicalName: "A", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	e2 := &Entity{OwnerEntityID: "user-1", CanonicalName: "B", Type: EntityTypePerson, Aliases: StringSlice{}, Attributes: JSONMap{}}
	s.CreateEntity(ctx, e1)
	s.CreateEntity(ctx, e2)
	s.CreateRelationship(ctx, &Relationship{OwnerEntityID: "user-1", SourceEntityID: e1.ID, TargetEntityID: e2.ID, RelationshipType: "knows", Strength: 0.5, Confidence: 0.5, Attributes: JSONMap{}})

	count, err := s.DeleteAllRelationshipsForOwner(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("deleted %d, want 1", count)
	}
}

// --- Schema CRUD ---

func TestSQLiteStore_SchemaCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	schema := &ExtractionSchema{
		EntityID:         "user-1",
		Name:             "customer_info",
		Description:      "Extract customer information",
		Version:          "1.0",
		SchemaDefinition: map[string]any{"type": "object", "properties": map[string]any{"name": "string"}},
		IsActive:         true,
	}

	// Create
	if err := s.CreateSchema(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if schema.ID == "" {
		t.Error("expected auto-generated ID")
	}

	// Get
	got, err := s.GetSchema(ctx, schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "customer_info" {
		t.Errorf("Name = %q, want customer_info", got.Name)
	}
	if !got.IsActive {
		t.Error("expected IsActive = true")
	}

	// GetByName
	got, err = s.GetSchemaByName(ctx, "user-1", "customer_info")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected schema by name")
	}

	// GetByName not found
	got, err = s.GetSchemaByName(ctx, "user-1", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for non-existent schema name")
	}

	// Query
	schemas, err := s.QuerySchemas(ctx, SchemaQuery{EntityID: "user-1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 {
		t.Errorf("query got %d, want 1", len(schemas))
	}

	// Query activeOnly
	schemas, err = s.QuerySchemas(ctx, SchemaQuery{EntityID: "user-1", ActiveOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 {
		t.Errorf("activeOnly got %d, want 1", len(schemas))
	}

	// Update
	_, err = s.UpdateSchema(ctx, schema.ID, map[string]any{"description": "updated desc"})
	if err != nil {
		t.Fatal(err)
	}

	// Update is_active
	_, err = s.UpdateSchema(ctx, schema.ID, map[string]any{"is_active": false})
	if err != nil {
		t.Fatal(err)
	}

	// Verify inactive
	schemas, err = s.QuerySchemas(ctx, SchemaQuery{EntityID: "user-1", ActiveOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 0 {
		t.Errorf("activeOnly after deactivate got %d, want 0", len(schemas))
	}

	// Delete
	if err := s.DeleteSchema(ctx, schema.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSchema(ctx, schema.ID)
	if err == nil && got != nil {
		t.Error("expected nil after delete")
	}
}

// --- Custom Extraction CRUD ---

func TestSQLiteStore_CustomExtractionCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create schema first
	schema := &ExtractionSchema{
		EntityID:         "user-1",
		Name:             "test_schema",
		Version:          "1.0",
		SchemaDefinition: map[string]any{"type": "object"},
		IsActive:         true,
	}
	_ = s.CreateSchema(ctx, schema)

	// Create memory
	mem := testMemory("user-1")
	s.CreateMemory(ctx, mem)

	extraction := &CustomExtraction{
		EntityID:           "user-1",
		MemoryID:           mem.ID,
		SchemaID:           schema.ID,
		ExtractedData:      map[string]any{"name": "Alice", "age": float64(30)},
		ExtractionProvider: "openai",
		ExtractionModel:    "gpt-4o-mini",
		Confidence:         0.85,
	}

	// Create
	if err := s.CreateCustomExtraction(ctx, extraction); err != nil {
		t.Fatal(err)
	}
	if extraction.ID == "" {
		t.Error("expected auto-generated ID")
	}

	// Get
	got, err := s.GetCustomExtraction(ctx, extraction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", got.Confidence)
	}

	// GetByMemory
	exts, err := s.GetCustomExtractionsByMemory(ctx, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 {
		t.Errorf("by memory got %d, want 1", len(exts))
	}

	// Query by schema
	exts, err = s.QueryCustomExtractions(ctx, CustomExtractionQuery{SchemaID: schema.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 {
		t.Errorf("query by schema got %d, want 1", len(exts))
	}

	// Delete
	if err := s.DeleteCustomExtraction(ctx, extraction.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetCustomExtraction(ctx, extraction.ID)
	if err == nil && got != nil {
		t.Error("expected nil after delete")
	}
}

func TestSQLiteStore_DeleteCustomExtractionsBySchema(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	schema := &ExtractionSchema{
		EntityID:         "user-1",
		Name:             "del_schema",
		Version:          "1.0",
		SchemaDefinition: map[string]any{},
		IsActive:         true,
	}
	_ = s.CreateSchema(ctx, schema)

	for i := 0; i < 3; i++ {
		_ = s.CreateCustomExtraction(ctx, &CustomExtraction{
			EntityID:      "user-1",
			SchemaID:      schema.ID,
			ExtractedData: map[string]any{},
		})
	}

	if err := s.DeleteCustomExtractionsBySchema(ctx, schema.ID); err != nil {
		t.Fatal(err)
	}

	exts, _ := s.QueryCustomExtractions(ctx, CustomExtractionQuery{SchemaID: schema.ID, Limit: 10})
	if len(exts) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(exts))
	}
}

// --- Maintenance ---

func TestSQLiteStore_Ping(t *testing.T) {
	s := newTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
