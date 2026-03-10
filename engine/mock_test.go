package engine

import (
	"context"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// --- mockStore implements storage.Store ---

type mockStore struct {
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

func (m *mockStore) CreateMemory(ctx context.Context, mem *storage.Memory) error {
	if m.createMemoryFn != nil {
		return m.createMemoryFn(ctx, mem)
	}
	if mem.ID == "" {
		mem.ID = "test-mem-id"
	}
	return nil
}
func (m *mockStore) GetMemory(ctx context.Context, id string) (*storage.Memory, error) {
	if m.getMemoryFn != nil {
		return m.getMemoryFn(ctx, id)
	}
	return nil, nil
}
func (m *mockStore) GetMemoriesByIDs(ctx context.Context, ids []string) ([]*storage.Memory, error) {
	if m.getMemoriesByIDsFn != nil {
		return m.getMemoriesByIDsFn(ctx, ids)
	}
	return nil, nil
}
func (m *mockStore) UpdateMemory(ctx context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
	if m.updateMemoryFn != nil {
		return m.updateMemoryFn(ctx, id, updates)
	}
	return &storage.Memory{ID: id}, nil
}
func (m *mockStore) DeleteMemory(ctx context.Context, id string, hard bool) error {
	if m.deleteMemoryFn != nil {
		return m.deleteMemoryFn(ctx, id, hard)
	}
	return nil
}
func (m *mockStore) FindSimilar(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*storage.SimilarityResult, error) {
	if m.findSimilarFn != nil {
		return m.findSimilarFn(ctx, embedding, entityID, limit, minScore)
	}
	return nil, nil
}
func (m *mockStore) FindSimilarWithOptions(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64, opts storage.SimilarityOptions) ([]*storage.SimilarityResult, error) {
	if m.findSimilarWithOptionsFn != nil {
		return m.findSimilarWithOptionsFn(ctx, embedding, entityID, limit, minScore, opts)
	}
	// Fall back to findSimilarFn for backward compatibility with existing tests
	if m.findSimilarFn != nil {
		return m.findSimilarFn(ctx, embedding, entityID, limit, minScore)
	}
	return nil, nil
}
func (m *mockStore) QueryMemories(ctx context.Context, query storage.MemoryQuery) ([]*storage.Memory, error) {
	if m.queryMemoriesFn != nil {
		return m.queryMemoriesFn(ctx, query)
	}
	return nil, nil
}
func (m *mockStore) GetRecentMemories(ctx context.Context, entityID string, hours int, limit int) ([]*storage.Memory, error) {
	if m.getRecentMemoriesFn != nil {
		return m.getRecentMemoriesFn(ctx, entityID, hours, limit)
	}
	return nil, nil
}
func (m *mockStore) FindByHash(ctx context.Context, entityID string, hash string) (*storage.Memory, error) {
	if m.findByHashFn != nil {
		return m.findByHashFn(ctx, entityID, hash)
	}
	return nil, nil
}
func (m *mockStore) FindByHashWithAgent(ctx context.Context, entityID, agentID, hash string) (*storage.Memory, error) {
	if m.findByHashWithAgentFn != nil {
		return m.findByHashWithAgentFn(ctx, entityID, agentID, hash)
	}
	return nil, nil
}
func (m *mockStore) LogHistory(ctx context.Context, entry *storage.HistoryEntry) error {
	if m.logHistoryFn != nil {
		return m.logHistoryFn(ctx, entry)
	}
	return nil
}
func (m *mockStore) GetHistory(ctx context.Context, memoryID string, limit int) ([]*storage.HistoryEntry, error) {
	if m.getHistoryFn != nil {
		return m.getHistoryFn(ctx, memoryID, limit)
	}
	return nil, nil
}
func (m *mockStore) AddSessionMessage(ctx context.Context, msg *storage.SessionMessage) error {
	if m.addSessionMessageFn != nil {
		return m.addSessionMessageFn(ctx, msg)
	}
	return nil
}
func (m *mockStore) GetRecentSessionMessages(ctx context.Context, entityID string, limit int) ([]*storage.SessionMessage, error) {
	if m.getRecentSessionMessagesFn != nil {
		return m.getRecentSessionMessagesFn(ctx, entityID, limit)
	}
	return nil, nil
}
func (m *mockStore) UpdateAccessStats(ctx context.Context, ids []string) error {
	if m.updateAccessStatsFn != nil {
		return m.updateAccessStatsFn(ctx, ids)
	}
	return nil
}
func (m *mockStore) UpdateStability(ctx context.Context, id string, newStability float64) error {
	if m.updateStabilityFn != nil {
		return m.updateStabilityFn(ctx, id, newStability)
	}
	return nil
}
func (m *mockStore) TransitionState(ctx context.Context, id string, newState storage.MemoryState, reason string) error {
	if m.transitionStateFn != nil {
		return m.transitionStateFn(ctx, id, newState, reason)
	}
	return nil
}
func (m *mockStore) GetStaleMemories(ctx context.Context, entityID string, decayThreshold float64) ([]*storage.Memory, error) {
	if m.getStaleMemoriesFn != nil {
		return m.getStaleMemoriesFn(ctx, entityID, decayThreshold)
	}
	return nil, nil
}
func (m *mockStore) GetAllEntities(ctx context.Context) ([]string, error) {
	if m.getAllEntitiesFn != nil {
		return m.getAllEntitiesFn(ctx)
	}
	return nil, nil
}
func (m *mockStore) GetActiveMemoriesForDecay(ctx context.Context, batchSize int, offset int) ([]*storage.Memory, error) {
	if m.getActiveMemoriesForDecayFn != nil {
		return m.getActiveMemoriesForDecayFn(ctx, batchSize, offset)
	}
	return nil, nil
}
func (m *mockStore) BatchTransitionStates(ctx context.Context, transitions []storage.StateTransition) (int, error) {
	if m.batchTransitionStatesFn != nil {
		return m.batchTransitionStatesFn(ctx, transitions)
	}
	return 0, nil
}
func (m *mockStore) CreateEntity(ctx context.Context, entity *storage.Entity) error {
	if m.createEntityFn != nil {
		return m.createEntityFn(ctx, entity)
	}
	if entity.ID == "" {
		entity.ID = "test-entity-id"
	}
	return nil
}
func (m *mockStore) GetEntity(ctx context.Context, id string) (*storage.Entity, error) {
	if m.getEntityFn != nil {
		return m.getEntityFn(ctx, id)
	}
	return nil, nil
}
func (m *mockStore) GetEntityByName(ctx context.Context, ownerEntityID, name string, entityType storage.EntityType) (*storage.Entity, error) {
	if m.getEntityByNameFn != nil {
		return m.getEntityByNameFn(ctx, ownerEntityID, name, entityType)
	}
	return nil, nil
}
func (m *mockStore) FindEntityByAlias(ctx context.Context, ownerEntityID, alias string) (*storage.Entity, error) {
	if m.findEntityByAliasFn != nil {
		return m.findEntityByAliasFn(ctx, ownerEntityID, alias)
	}
	return nil, nil
}
func (m *mockStore) FindSimilarEntities(ctx context.Context, embedding []float32, ownerEntityID string, limit int, minScore float64) ([]*storage.Entity, error) {
	if m.findSimilarEntitiesFn != nil {
		return m.findSimilarEntitiesFn(ctx, embedding, ownerEntityID, limit, minScore)
	}
	return nil, nil
}
func (m *mockStore) QueryEntities(ctx context.Context, query storage.EntityQuery) ([]*storage.Entity, error) {
	if m.queryEntitiesFn != nil {
		return m.queryEntitiesFn(ctx, query)
	}
	return nil, nil
}
func (m *mockStore) UpdateEntity(ctx context.Context, id string, updates map[string]any) (*storage.Entity, error) {
	if m.updateEntityFn != nil {
		return m.updateEntityFn(ctx, id, updates)
	}
	return &storage.Entity{ID: id}, nil
}
func (m *mockStore) UpdateEntityMentionCount(ctx context.Context, id string) error {
	if m.updateEntityMentionCountFn != nil {
		return m.updateEntityMentionCountFn(ctx, id)
	}
	return nil
}
func (m *mockStore) AddEntityAlias(ctx context.Context, id string, alias string) error {
	if m.addEntityAliasFn != nil {
		return m.addEntityAliasFn(ctx, id, alias)
	}
	return nil
}
func (m *mockStore) DeleteEntity(ctx context.Context, id string) error {
	if m.deleteEntityFn != nil {
		return m.deleteEntityFn(ctx, id)
	}
	return nil
}
func (m *mockStore) DeleteAllEntitiesForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	if m.deleteAllEntitiesForOwnerFn != nil {
		return m.deleteAllEntitiesForOwnerFn(ctx, ownerEntityID)
	}
	return 0, nil
}
func (m *mockStore) CreateEntityMention(ctx context.Context, mention *storage.EntityMention) error {
	if m.createEntityMentionFn != nil {
		return m.createEntityMentionFn(ctx, mention)
	}
	return nil
}
func (m *mockStore) GetEntityMentions(ctx context.Context, entityID string, limit int) ([]*storage.EntityMention, error) {
	if m.getEntityMentionsFn != nil {
		return m.getEntityMentionsFn(ctx, entityID, limit)
	}
	return nil, nil
}
func (m *mockStore) GetMemoryEntities(ctx context.Context, memoryID string) ([]*storage.Entity, error) {
	if m.getMemoryEntitiesFn != nil {
		return m.getMemoryEntitiesFn(ctx, memoryID)
	}
	return nil, nil
}
func (m *mockStore) CreateRelationship(ctx context.Context, rel *storage.Relationship) error {
	if m.createRelationshipFn != nil {
		return m.createRelationshipFn(ctx, rel)
	}
	if rel.ID == "" {
		rel.ID = "test-rel-id"
	}
	return nil
}
func (m *mockStore) GetRelationship(ctx context.Context, id string) (*storage.Relationship, error) {
	if m.getRelationshipFn != nil {
		return m.getRelationshipFn(ctx, id)
	}
	return nil, nil
}
func (m *mockStore) FindRelationship(ctx context.Context, ownerEntityID, sourceID, targetID, relType string) (*storage.Relationship, error) {
	if m.findRelationshipFn != nil {
		return m.findRelationshipFn(ctx, ownerEntityID, sourceID, targetID, relType)
	}
	return nil, nil
}
func (m *mockStore) GetEntityRelationships(ctx context.Context, ownerEntityID, entityID string, direction string) ([]*storage.Relationship, error) {
	if m.getEntityRelationshipsFn != nil {
		return m.getEntityRelationshipsFn(ctx, ownerEntityID, entityID, direction)
	}
	return nil, nil
}
func (m *mockStore) QueryRelationships(ctx context.Context, query storage.RelationshipQuery) ([]*storage.Relationship, error) {
	if m.queryRelationshipsFn != nil {
		return m.queryRelationshipsFn(ctx, query)
	}
	return nil, nil
}
func (m *mockStore) UpdateRelationship(ctx context.Context, id string, updates map[string]any) (*storage.Relationship, error) {
	if m.updateRelationshipFn != nil {
		return m.updateRelationshipFn(ctx, id, updates)
	}
	return &storage.Relationship{ID: id}, nil
}
func (m *mockStore) IncrementRelationshipEvidence(ctx context.Context, id string) error {
	if m.incrementRelationshipEvidenceFn != nil {
		return m.incrementRelationshipEvidenceFn(ctx, id)
	}
	return nil
}
func (m *mockStore) DeleteRelationship(ctx context.Context, id string) error {
	if m.deleteRelationshipFn != nil {
		return m.deleteRelationshipFn(ctx, id)
	}
	return nil
}
func (m *mockStore) CreateRelationshipEvidence(ctx context.Context, evidence *storage.RelationshipEvidence) error {
	if m.createRelationshipEvidenceFn != nil {
		return m.createRelationshipEvidenceFn(ctx, evidence)
	}
	return nil
}
func (m *mockStore) GetRelationshipEvidence(ctx context.Context, relationshipID string, limit int) ([]*storage.RelationshipEvidence, error) {
	if m.getRelationshipEvidenceFn != nil {
		return m.getRelationshipEvidenceFn(ctx, relationshipID, limit)
	}
	return nil, nil
}
func (m *mockStore) GetRelationshipPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string, maxDepth int) ([]string, error) {
	if m.getRelationshipPathFn != nil {
		return m.getRelationshipPathFn(ctx, ownerEntityID, fromEntityID, toEntityID, maxDepth)
	}
	return nil, nil
}
func (m *mockStore) DeleteAllRelationshipsForOwner(ctx context.Context, ownerEntityID string) (int, error) {
	if m.deleteAllRelationshipsForOwnerFn != nil {
		return m.deleteAllRelationshipsForOwnerFn(ctx, ownerEntityID)
	}
	return 0, nil
}
func (m *mockStore) CreateSchema(ctx context.Context, schema *storage.ExtractionSchema) error {
	if m.createSchemaFn != nil {
		return m.createSchemaFn(ctx, schema)
	}
	return nil
}
func (m *mockStore) GetSchema(ctx context.Context, id string) (*storage.ExtractionSchema, error) {
	if m.getSchemaFn != nil {
		return m.getSchemaFn(ctx, id)
	}
	return nil, nil
}
func (m *mockStore) GetSchemaByName(ctx context.Context, entityID, name string) (*storage.ExtractionSchema, error) {
	if m.getSchemaByNameFn != nil {
		return m.getSchemaByNameFn(ctx, entityID, name)
	}
	return nil, nil
}
func (m *mockStore) QuerySchemas(ctx context.Context, query storage.SchemaQuery) ([]*storage.ExtractionSchema, error) {
	if m.querySchemasFn != nil {
		return m.querySchemasFn(ctx, query)
	}
	return nil, nil
}
func (m *mockStore) UpdateSchema(ctx context.Context, id string, updates map[string]any) (*storage.ExtractionSchema, error) {
	if m.updateSchemaFn != nil {
		return m.updateSchemaFn(ctx, id, updates)
	}
	return &storage.ExtractionSchema{ID: id}, nil
}
func (m *mockStore) DeleteSchema(ctx context.Context, id string) error {
	if m.deleteSchemaFn != nil {
		return m.deleteSchemaFn(ctx, id)
	}
	return nil
}
func (m *mockStore) CreateCustomExtraction(ctx context.Context, extraction *storage.CustomExtraction) error {
	if m.createCustomExtractionFn != nil {
		return m.createCustomExtractionFn(ctx, extraction)
	}
	if extraction.ID == "" {
		extraction.ID = "test-extraction-id"
	}
	return nil
}
func (m *mockStore) GetCustomExtraction(ctx context.Context, id string) (*storage.CustomExtraction, error) {
	if m.getCustomExtractionFn != nil {
		return m.getCustomExtractionFn(ctx, id)
	}
	return nil, nil
}
func (m *mockStore) GetCustomExtractionsByMemory(ctx context.Context, memoryID string) ([]*storage.CustomExtraction, error) {
	if m.getCustomExtractionsByMemoryFn != nil {
		return m.getCustomExtractionsByMemoryFn(ctx, memoryID)
	}
	return nil, nil
}
func (m *mockStore) QueryCustomExtractions(ctx context.Context, query storage.CustomExtractionQuery) ([]*storage.CustomExtraction, error) {
	if m.queryCustomExtractionsFn != nil {
		return m.queryCustomExtractionsFn(ctx, query)
	}
	return nil, nil
}
func (m *mockStore) DeleteCustomExtraction(ctx context.Context, id string) error {
	if m.deleteCustomExtractionFn != nil {
		return m.deleteCustomExtractionFn(ctx, id)
	}
	return nil
}
func (m *mockStore) DeleteCustomExtractionsBySchema(ctx context.Context, schemaID string) error {
	if m.deleteCustomExtractionsBySchemaFn != nil {
		return m.deleteCustomExtractionsBySchemaFn(ctx, schemaID)
	}
	return nil
}
func (m *mockStore) CreateTeam(_ context.Context, _ *storage.Team) error { return nil }
func (m *mockStore) GetTeam(_ context.Context, _ string) (*storage.Team, error) { return nil, nil }
func (m *mockStore) DeleteTeam(_ context.Context, _ string) error { return nil }
func (m *mockStore) AddTeamMember(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) RemoveTeamMember(_ context.Context, _, _ string) error { return nil }
func (m *mockStore) GetTeamMembers(_ context.Context, _ string) ([]*storage.TeamMember, error) { return nil, nil }
func (m *mockStore) GetTeamForAgent(_ context.Context, _ string) (string, error) { return "", nil }
func (m *mockStore) CreateAgentState(_ context.Context, _ *storage.AgentState) error { return nil }
func (m *mockStore) GetAgentState(_ context.Context, _, _, _ string) (*storage.AgentState, error) {
	return nil, nil
}
func (m *mockStore) UpdateAgentState(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (m *mockStore) GetAgentStateHistory(_ context.Context, _ string, _ int) ([]*storage.AgentStateHistory, error) {
	return nil, nil
}
func (m *mockStore) LogAgentStateHistory(_ context.Context, _ *storage.AgentStateHistory) error {
	return nil
}
func (m *mockStore) AggregateStats(_ context.Context, _ string) (*storage.AggregatedStats, error) { return &storage.AggregatedStats{ByType: map[string]int{}, ByState: map[string]int{}}, nil }
func (m *mockStore) SampleMemories(_ context.Context, _ string, _ int) ([]*storage.Memory, error) { return nil, nil }
func (m *mockStore) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}
func (m *mockStore) Ping(ctx context.Context) error {
	if m.pingFn != nil {
		return m.pingFn(ctx)
	}
	return nil
}

// --- mockProvider implements llm.Provider ---

type mockProvider struct {
	extractMemoriesFn     func(context.Context, llm.ExtractionRequest) (*llm.ExtractionResponse, error)
	consolidateMemoriesFn func(context.Context, llm.ConsolidationRequest) (*llm.ConsolidationResponse, error)
	extractWithSchemaFn   func(context.Context, llm.CustomExtractionRequest) (*llm.CustomExtractionResponse, error)
	extractStateFn        func(context.Context, llm.StateExtractionRequest) (*llm.StateExtractionResponse, error)
	name                  string
	model                 string
}

func (m *mockProvider) ExtractMemories(ctx context.Context, req llm.ExtractionRequest) (*llm.ExtractionResponse, error) {
	if m.extractMemoriesFn != nil {
		return m.extractMemoriesFn(ctx, req)
	}
	return &llm.ExtractionResponse{}, nil
}
func (m *mockProvider) ConsolidateMemories(ctx context.Context, req llm.ConsolidationRequest) (*llm.ConsolidationResponse, error) {
	if m.consolidateMemoriesFn != nil {
		return m.consolidateMemoriesFn(ctx, req)
	}
	return &llm.ConsolidationResponse{}, nil
}
func (m *mockProvider) ExtractWithSchema(ctx context.Context, req llm.CustomExtractionRequest) (*llm.CustomExtractionResponse, error) {
	if m.extractWithSchemaFn != nil {
		return m.extractWithSchemaFn(ctx, req)
	}
	return &llm.CustomExtractionResponse{}, nil
}
func (m *mockProvider) ExtractState(ctx context.Context, req llm.StateExtractionRequest) (*llm.StateExtractionResponse, error) {
	if m.extractStateFn != nil {
		return m.extractStateFn(ctx, req)
	}
	return &llm.StateExtractionResponse{}, nil
}
func (m *mockProvider) DetectConflict(_ context.Context, _ llm.ConflictCheckRequest) (*llm.ConflictCheckResponse, error) {
	return &llm.ConflictCheckResponse{}, nil
}
func (m *mockProvider) ReEvaluateImportance(_ context.Context, _ llm.ImportanceReEvalRequest) (*llm.ImportanceReEvalResponse, error) {
	return &llm.ImportanceReEvalResponse{}, nil
}
func (m *mockProvider) PrioritizeActions(_ context.Context, _ llm.ActionPriorityRequest) (*llm.ActionPriorityResponse, error) {
	return &llm.ActionPriorityResponse{}, nil
}
func (m *mockProvider) SummarizeGraph(_ context.Context, _ llm.GraphSummaryRequest) (*llm.GraphSummaryResponse, error) {
	return &llm.GraphSummaryResponse{}, nil
}
func (m *mockProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "test-provider"
}
func (m *mockProvider) Model() string {
	if m.model != "" {
		return m.model
	}
	return "test-model"
}

// --- mockEmbedder implements embedder.Embedder ---

type mockEmbedder struct {
	embedFn      func(context.Context, string) ([]float32, error)
	embedBatchFn func(context.Context, []string) ([][]float32, error)
	dimensions   int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text)
	}
	d := m.dimensions
	if d <= 0 {
		d = 3
	}
	return make([]float32, d), nil
}
func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if m.embedBatchFn != nil {
		return m.embedBatchFn(ctx, texts)
	}
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i], _ = m.Embed(ctx, texts[i])
	}
	return result, nil
}
func (m *mockEmbedder) Dimensions() int {
	if m.dimensions > 0 {
		return m.dimensions
	}
	return 3
}

// --- test helpers ---

func testMemory(id, content string) *storage.Memory {
	now := time.Now()
	return &storage.Memory{
		ID:             id,
		EntityID:       "test-entity",
		AgentID:        "default",
		Content:        content,
		Type:           storage.TypeContext,
		State:          storage.StateActive,
		Importance:     0.5,
		Confidence:     0.8,
		Stability:      60,
		LastAccessedAt: &now,
		CreatedAt:      now,
	}
}

func testEntity(id, name string) *storage.Entity {
	return &storage.Entity{
		ID:            id,
		OwnerEntityID: "test-entity",
		CanonicalName: name,
		Type:          storage.EntityTypePerson,
		Aliases:       []string{},
		Attributes:    map[string]any{},
	}
}

func testRelationship(id, sourceID, targetID, relType string) *storage.Relationship {
	return &storage.Relationship{
		ID:               id,
		OwnerEntityID:    "test-entity",
		SourceEntityID:   sourceID,
		TargetEntityID:   targetID,
		RelationshipType: relType,
		Strength:         0.8,
		Confidence:       0.8,
		Attributes:       map[string]any{},
	}
}

func testEmbedding() []float32 {
	return []float32{0.5, 0.5, 0.5}
}
