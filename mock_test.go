// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// testStore implements storage.Store for root package tests.
// Uses the same fn field delegation pattern as engine/jobs mocks.
type testStore struct {
	createMemoryFn                   func(context.Context, *storage.Memory) error
	getMemoryFn                      func(context.Context, string) (*storage.Memory, error)
	getMemoriesByIDsFn               func(context.Context, []string) ([]*storage.Memory, error)
	updateMemoryFn                   func(context.Context, string, storage.MemoryUpdate) (*storage.Memory, error)
	deleteMemoryFn                   func(context.Context, string, bool) error
	findSimilarFn                    func(context.Context, []float32, string, int, float64) ([]*storage.SimilarityResult, error)
	findSimilarWithOptionsFn         func(context.Context, []float32, string, int, float64, storage.SimilarityOptions) ([]*storage.SimilarityResult, error)
	queryMemoriesFn                  func(context.Context, storage.MemoryQuery) ([]*storage.Memory, error)
	getRecentMemoriesFn              func(context.Context, string, int, int) ([]*storage.Memory, error)
	findByHashFn                     func(context.Context, string, string) (*storage.Memory, error)
	findByHashWithAgentFn            func(context.Context, string, string, string) (*storage.Memory, error)
	logHistoryFn                     func(context.Context, *storage.HistoryEntry) error
	getHistoryFn                     func(context.Context, string, int) ([]*storage.HistoryEntry, error)
	addSessionMessageFn              func(context.Context, *storage.SessionMessage) error
	getRecentSessionMessagesFn       func(context.Context, string, int) ([]*storage.SessionMessage, error)
	updateAccessStatsFn              func(context.Context, []string) error
	updateStabilityFn                func(context.Context, string, float64) error
	transitionStateFn                func(context.Context, string, storage.MemoryState, string) error
	getStaleMemoriesFn               func(context.Context, string, float64) ([]*storage.Memory, error)
	getAllEntitiesFn                  func(context.Context) ([]string, error)
	getActiveMemoriesForDecayFn      func(context.Context, int, int) ([]*storage.Memory, error)
	batchTransitionStatesFn          func(context.Context, []storage.StateTransition) (int, error)
	createEntityFn                   func(context.Context, *storage.Entity) error
	getEntityFn                      func(context.Context, string) (*storage.Entity, error)
	getEntityByNameFn                func(context.Context, string, string, storage.EntityType) (*storage.Entity, error)
	findEntityByAliasFn              func(context.Context, string, string) (*storage.Entity, error)
	findSimilarEntitiesFn            func(context.Context, []float32, string, int, float64) ([]*storage.Entity, error)
	queryEntitiesFn                  func(context.Context, storage.EntityQuery) ([]*storage.Entity, error)
	updateEntityFn                   func(context.Context, string, map[string]any) (*storage.Entity, error)
	updateEntityMentionCountFn       func(context.Context, string) error
	addEntityAliasFn                 func(context.Context, string, string) error
	deleteEntityFn                   func(context.Context, string) error
	deleteAllEntitiesForOwnerFn      func(context.Context, string) (int, error)
	createEntityMentionFn            func(context.Context, *storage.EntityMention) error
	getEntityMentionsFn              func(context.Context, string, int) ([]*storage.EntityMention, error)
	getMemoryEntitiesFn              func(context.Context, string) ([]*storage.Entity, error)
	createRelationshipFn             func(context.Context, *storage.Relationship) error
	getRelationshipFn                func(context.Context, string) (*storage.Relationship, error)
	findRelationshipFn               func(context.Context, string, string, string, string) (*storage.Relationship, error)
	getEntityRelationshipsFn         func(context.Context, string, string, string) ([]*storage.Relationship, error)
	queryRelationshipsFn             func(context.Context, storage.RelationshipQuery) ([]*storage.Relationship, error)
	updateRelationshipFn             func(context.Context, string, map[string]any) (*storage.Relationship, error)
	incrementRelationshipEvidenceFn  func(context.Context, string) error
	deleteRelationshipFn             func(context.Context, string) error
	createRelationshipEvidenceFn     func(context.Context, *storage.RelationshipEvidence) error
	getRelationshipEvidenceFn        func(context.Context, string, int) ([]*storage.RelationshipEvidence, error)
	getRelationshipPathFn            func(context.Context, string, string, string, int) ([]string, error)
	deleteAllRelationshipsForOwnerFn func(context.Context, string) (int, error)
	createSchemaFn                   func(context.Context, *storage.ExtractionSchema) error
	getSchemaFn                      func(context.Context, string) (*storage.ExtractionSchema, error)
	getSchemaByNameFn                func(context.Context, string, string) (*storage.ExtractionSchema, error)
	querySchemasFn                   func(context.Context, storage.SchemaQuery) ([]*storage.ExtractionSchema, error)
	updateSchemaFn                   func(context.Context, string, map[string]any) (*storage.ExtractionSchema, error)
	deleteSchemaFn                   func(context.Context, string) error
	createCustomExtractionFn         func(context.Context, *storage.CustomExtraction) error
	getCustomExtractionFn            func(context.Context, string) (*storage.CustomExtraction, error)
	getCustomExtractionsByMemoryFn   func(context.Context, string) ([]*storage.CustomExtraction, error)
	queryCustomExtractionsFn         func(context.Context, storage.CustomExtractionQuery) ([]*storage.CustomExtraction, error)
	deleteCustomExtractionFn         func(context.Context, string) error
	deleteCustomExtractionsBySchemaFn func(context.Context, string) error
	closeFn                          func() error
	pingFn                           func(context.Context) error
}

func (m *testStore) CreateMemory(ctx context.Context, mem *storage.Memory) error {
	if m.createMemoryFn != nil { return m.createMemoryFn(ctx, mem) }; return nil
}
func (m *testStore) GetMemory(ctx context.Context, id string) (*storage.Memory, error) {
	if m.getMemoryFn != nil { return m.getMemoryFn(ctx, id) }; return nil, nil
}
func (m *testStore) GetMemoriesByIDs(ctx context.Context, ids []string) ([]*storage.Memory, error) {
	if m.getMemoriesByIDsFn != nil { return m.getMemoriesByIDsFn(ctx, ids) }; return nil, nil
}
func (m *testStore) UpdateMemory(ctx context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
	if m.updateMemoryFn != nil { return m.updateMemoryFn(ctx, id, updates) }; return &storage.Memory{ID: id}, nil
}
func (m *testStore) DeleteMemory(ctx context.Context, id string, hard bool) error {
	if m.deleteMemoryFn != nil { return m.deleteMemoryFn(ctx, id, hard) }; return nil
}
func (m *testStore) FindSimilar(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*storage.SimilarityResult, error) {
	if m.findSimilarFn != nil { return m.findSimilarFn(ctx, embedding, entityID, limit, minScore) }; return nil, nil
}
func (m *testStore) FindSimilarWithOptions(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
	if m.findSimilarWithOptionsFn != nil { return m.findSimilarWithOptionsFn(ctx, embedding, entityID, limit, minScore, opts) }; return nil, nil
}
func (m *testStore) QueryMemories(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
	if m.queryMemoriesFn != nil { return m.queryMemoriesFn(ctx, query) }; return nil, nil
}
func (m *testStore) GetRecentMemories(ctx context.Context, entityID string, hours int, limit int) ([]*storage.Memory, error) {
	if m.getRecentMemoriesFn != nil { return m.getRecentMemoriesFn(ctx, entityID, hours, limit) }; return nil, nil
}
func (m *testStore) FindByHash(ctx context.Context, entityID string, hash string) (*storage.Memory, error) {
	if m.findByHashFn != nil { return m.findByHashFn(ctx, entityID, hash) }; return nil, nil
}
func (m *testStore) FindByHashWithAgent(ctx context.Context, entityID, agentID, hash string) (*storage.Memory, error) {
	if m.findByHashWithAgentFn != nil { return m.findByHashWithAgentFn(ctx, entityID, agentID, hash) }; return nil, nil
}
func (m *testStore) LogHistory(ctx context.Context, entry *storage.HistoryEntry) error {
	if m.logHistoryFn != nil { return m.logHistoryFn(ctx, entry) }; return nil
}
func (m *testStore) GetHistory(ctx context.Context, memoryID string, limit int) ([]*storage.HistoryEntry, error) {
	if m.getHistoryFn != nil { return m.getHistoryFn(ctx, memoryID, limit) }; return nil, nil
}
func (m *testStore) AddSessionMessage(ctx context.Context, msg *storage.SessionMessage) error {
	if m.addSessionMessageFn != nil { return m.addSessionMessageFn(ctx, msg) }; return nil
}
func (m *testStore) GetRecentSessionMessages(ctx context.Context, entityID string, limit int) ([]*storage.SessionMessage, error) {
	if m.getRecentSessionMessagesFn != nil { return m.getRecentSessionMessagesFn(ctx, entityID, limit) }; return nil, nil
}
func (m *testStore) UpdateAccessStats(ctx context.Context, ids []string) error {
	if m.updateAccessStatsFn != nil { return m.updateAccessStatsFn(ctx, ids) }; return nil
}
func (m *testStore) UpdateStability(ctx context.Context, id string, newStability float64) error {
	if m.updateStabilityFn != nil { return m.updateStabilityFn(ctx, id, newStability) }; return nil
}
func (m *testStore) TransitionState(ctx context.Context, id string, newState storage.MemoryState, reason string) error {
	if m.transitionStateFn != nil { return m.transitionStateFn(ctx, id, newState, reason) }; return nil
}
func (m *testStore) GetStaleMemories(ctx context.Context, entityID string, decayThreshold float64) ([]*storage.Memory, error) {
	if m.getStaleMemoriesFn != nil { return m.getStaleMemoriesFn(ctx, entityID, decayThreshold) }; return nil, nil
}
func (m *testStore) GetAllEntities(ctx context.Context) ([]string, error) {
	if m.getAllEntitiesFn != nil { return m.getAllEntitiesFn(ctx) }; return nil, nil
}
func (m *testStore) GetActiveMemoriesForDecay(ctx context.Context, batchSize int, offset int) ([]*storage.Memory, error) {
	if m.getActiveMemoriesForDecayFn != nil { return m.getActiveMemoriesForDecayFn(ctx, batchSize, offset) }; return nil, nil
}
func (m *testStore) BatchTransitionStates(ctx context.Context, transitions []storage.StateTransition) (int, error) {
	if m.batchTransitionStatesFn != nil { return m.batchTransitionStatesFn(ctx, transitions) }; return len(transitions), nil
}
func (m *testStore) CreateEntity(ctx context.Context, entity *storage.Entity) error {
	if m.createEntityFn != nil { return m.createEntityFn(ctx, entity) }; return nil
}
func (m *testStore) GetEntity(ctx context.Context, id string) (*storage.Entity, error) {
	if m.getEntityFn != nil { return m.getEntityFn(ctx, id) }; return nil, nil
}
func (m *testStore) GetEntityByName(ctx context.Context, ownerEntityID, name string, entityType storage.EntityType) (*storage.Entity, error) {
	if m.getEntityByNameFn != nil { return m.getEntityByNameFn(ctx, ownerEntityID, name, entityType) }; return nil, nil
}
func (m *testStore) FindEntityByAlias(ctx context.Context, ownerEntityID, alias string) (*storage.Entity, error) {
	if m.findEntityByAliasFn != nil { return m.findEntityByAliasFn(ctx, ownerEntityID, alias) }; return nil, nil
}
func (m *testStore) FindSimilarEntities(ctx context.Context, embedding []float32, ownerEntityID string, limit int, minScore float64) ([]*storage.Entity, error) {
	if m.findSimilarEntitiesFn != nil { return m.findSimilarEntitiesFn(ctx, embedding, ownerEntityID, limit, minScore) }; return nil, nil
}
func (m *testStore) QueryEntities(ctx context.Context, query storage.EntityQuery) ([]*storage.Entity, error) {
	if m.queryEntitiesFn != nil { return m.queryEntitiesFn(ctx, query) }; return nil, nil
}
func (m *testStore) UpdateEntity(ctx context.Context, id string, updates map[string]any) (*storage.Entity, error) {
	if m.updateEntityFn != nil { return m.updateEntityFn(ctx, id, updates) }; return &storage.Entity{ID: id}, nil
}
func (m *testStore) UpdateEntityMentionCount(ctx context.Context, id string) error {
	if m.updateEntityMentionCountFn != nil { return m.updateEntityMentionCountFn(ctx, id) }; return nil
}
func (m *testStore) AddEntityAlias(ctx context.Context, id string, alias string) error {
	if m.addEntityAliasFn != nil { return m.addEntityAliasFn(ctx, id, alias) }; return nil
}
func (m *testStore) DeleteEntity(ctx context.Context, id string) error {
	if m.deleteEntityFn != nil { return m.deleteEntityFn(ctx, id) }; return nil
}
func (m *testStore) DeleteAllEntitiesForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	if m.deleteAllEntitiesForOwnerFn != nil { return m.deleteAllEntitiesForOwnerFn(ctx, ownerEntityID) }; return 0, nil
}
func (m *testStore) CreateEntityMention(ctx context.Context, mention *storage.EntityMention) error {
	if m.createEntityMentionFn != nil { return m.createEntityMentionFn(ctx, mention) }; return nil
}
func (m *testStore) GetEntityMentions(ctx context.Context, entityID string, limit int) ([]*storage.EntityMention, error) {
	if m.getEntityMentionsFn != nil { return m.getEntityMentionsFn(ctx, entityID, limit) }; return nil, nil
}
func (m *testStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*storage.Entity, error) {
	if m.getMemoryEntitiesFn != nil { return m.getMemoryEntitiesFn(ctx, memoryID) }; return nil, nil
}
func (m *testStore) CreateRelationship(ctx context.Context, rel *storage.Relationship) error {
	if m.createRelationshipFn != nil { return m.createRelationshipFn(ctx, rel) }; return nil
}
func (m *testStore) GetRelationship(ctx context.Context, id string) (*storage.Relationship, error) {
	if m.getRelationshipFn != nil { return m.getRelationshipFn(ctx, id) }; return nil, nil
}
func (m *testStore) FindRelationship(ctx context.Context, ownerEntityID, sourceID, targetID, relType string) (*storage.Relationship, error) {
	if m.findRelationshipFn != nil { return m.findRelationshipFn(ctx, ownerEntityID, sourceID, targetID, relType) }; return nil, nil
}
func (m *testStore) GetEntityRelationships(ctx context.Context, ownerEntityID, entityID string, direction string) ([]*storage.Relationship, error) {
	if m.getEntityRelationshipsFn != nil { return m.getEntityRelationshipsFn(ctx, ownerEntityID, entityID, direction) }; return nil, nil
}
func (m *testStore) QueryRelationships(ctx context.Context, query storage.RelationshipQuery) ([]*storage.Relationship, error) {
	if m.queryRelationshipsFn != nil { return m.queryRelationshipsFn(ctx, query) }; return nil, nil
}
func (m *testStore) UpdateRelationship(ctx context.Context, id string, updates map[string]any) (*storage.Relationship, error) {
	if m.updateRelationshipFn != nil { return m.updateRelationshipFn(ctx, id, updates) }; return &storage.Relationship{ID: id}, nil
}
func (m *testStore) IncrementRelationshipEvidence(ctx context.Context, id string) error {
	if m.incrementRelationshipEvidenceFn != nil { return m.incrementRelationshipEvidenceFn(ctx, id) }; return nil
}
func (m *testStore) DeleteRelationship(ctx context.Context, id string) error {
	if m.deleteRelationshipFn != nil { return m.deleteRelationshipFn(ctx, id) }; return nil
}
func (m *testStore) CreateRelationshipEvidence(ctx context.Context, evidence *storage.RelationshipEvidence) error {
	if m.createRelationshipEvidenceFn != nil { return m.createRelationshipEvidenceFn(ctx, evidence) }; return nil
}
func (m *testStore) GetRelationshipEvidence(ctx context.Context, relationshipID string, limit int) ([]*storage.RelationshipEvidence, error) {
	if m.getRelationshipEvidenceFn != nil { return m.getRelationshipEvidenceFn(ctx, relationshipID, limit) }; return nil, nil
}
func (m *testStore) GetRelationshipPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string, maxDepth int) ([]string, error) {
	if m.getRelationshipPathFn != nil { return m.getRelationshipPathFn(ctx, ownerEntityID, fromEntityID, toEntityID, maxDepth) }; return nil, nil
}
func (m *testStore) DeleteAllRelationshipsForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	if m.deleteAllRelationshipsForOwnerFn != nil { return m.deleteAllRelationshipsForOwnerFn(ctx, ownerEntityID) }; return 0, nil
}
func (m *testStore) CreateSchema(ctx context.Context, schema *storage.ExtractionSchema) error {
	if m.createSchemaFn != nil { return m.createSchemaFn(ctx, schema) }; return nil
}
func (m *testStore) GetSchema(ctx context.Context, id string) (*storage.ExtractionSchema, error) {
	if m.getSchemaFn != nil { return m.getSchemaFn(ctx, id) }; return nil, nil
}
func (m *testStore) GetSchemaByName(ctx context.Context, entityID, name string) (*storage.ExtractionSchema, error) {
	if m.getSchemaByNameFn != nil { return m.getSchemaByNameFn(ctx, entityID, name) }; return nil, nil
}
func (m *testStore) QuerySchemas(ctx context.Context, query storage.SchemaQuery) ([]*storage.ExtractionSchema, error) {
	if m.querySchemasFn != nil { return m.querySchemasFn(ctx, query) }; return nil, nil
}
func (m *testStore) UpdateSchema(ctx context.Context, id string, updates map[string]any) (*storage.ExtractionSchema, error) {
	if m.updateSchemaFn != nil { return m.updateSchemaFn(ctx, id, updates) }; return &storage.ExtractionSchema{ID: id}, nil
}
func (m *testStore) DeleteSchema(ctx context.Context, id string) error {
	if m.deleteSchemaFn != nil { return m.deleteSchemaFn(ctx, id) }; return nil
}
func (m *testStore) CreateCustomExtraction(ctx context.Context, extraction *storage.CustomExtraction) error {
	if m.createCustomExtractionFn != nil { return m.createCustomExtractionFn(ctx, extraction) }; return nil
}
func (m *testStore) GetCustomExtraction(ctx context.Context, id string) (*storage.CustomExtraction, error) {
	if m.getCustomExtractionFn != nil { return m.getCustomExtractionFn(ctx, id) }; return nil, nil
}
func (m *testStore) GetCustomExtractionsByMemory(ctx context.Context, memoryID string) ([]*storage.CustomExtraction, error) {
	if m.getCustomExtractionsByMemoryFn != nil { return m.getCustomExtractionsByMemoryFn(ctx, memoryID) }; return nil, nil
}
func (m *testStore) QueryCustomExtractions(ctx context.Context, query storage.CustomExtractionQuery) ([]*storage.CustomExtraction, error) {
	if m.queryCustomExtractionsFn != nil { return m.queryCustomExtractionsFn(ctx, query) }; return nil, nil
}
func (m *testStore) DeleteCustomExtraction(ctx context.Context, id string) error {
	if m.deleteCustomExtractionFn != nil { return m.deleteCustomExtractionFn(ctx, id) }; return nil
}
func (m *testStore) DeleteCustomExtractionsBySchema(ctx context.Context, schemaID string) error {
	if m.deleteCustomExtractionsBySchemaFn != nil { return m.deleteCustomExtractionsBySchemaFn(ctx, schemaID) }; return nil
}
func (m *testStore) CreateTeam(_ context.Context, _ *storage.Team) error { return nil }
func (m *testStore) GetTeam(_ context.Context, _ string) (*storage.Team, error) { return nil, nil }
func (m *testStore) DeleteTeam(_ context.Context, _ string) error { return nil }
func (m *testStore) AddTeamMember(_ context.Context, _, _ string) error { return nil }
func (m *testStore) RemoveTeamMember(_ context.Context, _, _ string) error { return nil }
func (m *testStore) GetTeamMembers(_ context.Context, _ string) ([]*storage.TeamMember, error) { return nil, nil }
func (m *testStore) GetTeamForAgent(_ context.Context, _ string) (string, error) { return "", nil }
func (m *testStore) CreateAgentState(_ context.Context, _ *storage.AgentState) error { return nil }
func (m *testStore) GetAgentState(_ context.Context, _, _, _ string) (*storage.AgentState, error) { return nil, nil }
func (m *testStore) UpdateAgentState(_ context.Context, _ string, _ map[string]any) error { return nil }
func (m *testStore) GetAgentStateHistory(_ context.Context, _ string, _ int) ([]*storage.AgentStateHistory, error) { return nil, nil }
func (m *testStore) LogAgentStateHistory(_ context.Context, _ *storage.AgentStateHistory) error { return nil }
func (m *testStore) AggregateStats(_ context.Context, _ string) (*storage.AggregatedStats, error) { return &storage.AggregatedStats{ByType: map[string]int{}, ByState: map[string]int{}}, nil }
func (m *testStore) SampleMemories(_ context.Context, _ string, _ int) ([]*storage.Memory, error) { return nil, nil }
func (m *testStore) SearchFTS(_ context.Context, _ string, _ string, _ int) ([]*storage.Memory, error) { return nil, nil }
func (m *testStore) SearchFTSWithOptions(_ context.Context, _ string, _ string, _ int, _ storage.SimilarityOptions) ([]*storage.Memory, error) { return nil, nil }
func (m *testStore) GetHNSWIndexSize() int { return 0 }
func (m *testStore) GetLowestRankedInHNSW(_ context.Context, _ int) ([]*storage.Memory, error) { return nil, nil }
func (m *testStore) RemoveFromHNSW(_ string) error { return nil }
func (m *testStore) RecordHeartbeatAction(_ context.Context, _ *storage.HeartbeatAction) error { return nil }
func (m *testStore) GetLastHeartbeatAction(_ context.Context, _, _, _ string) (*storage.HeartbeatAction, error) { return nil, nil }
func (m *testStore) GetNudgeCountToday(_ context.Context, _, _ string) (int, error) { return 0, nil }
func (m *testStore) CleanupOldHeartbeatActions(_ context.Context, _ time.Duration) error { return nil }
func (m *testStore) GetMessageHourDistribution(_ context.Context, _ string, _ int) (map[int]int, error) { return nil, nil }
func (m *testStore) GetStorageSizeBytes(_ context.Context) (int64, error) { return 0, nil }
func (m *testStore) GetMemoryCount(_ context.Context) (int, error) { return 0, nil }
func (m *testStore) Close() error {
	if m.closeFn != nil { return m.closeFn() }; return nil
}
func (m *testStore) Ping(ctx context.Context) error {
	if m.pingFn != nil { return m.pingFn(ctx) }; return nil
}
