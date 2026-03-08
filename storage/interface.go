package storage

import "context"

// Store defines the unified storage interface for Keyoku Embedded.
// Combines core memory, entity, and relationship operations in a single interface
// since there's only one backend (SQLite).
type Store interface {
	// Memory CRUD
	CreateMemory(ctx context.Context, mem *Memory) error
	GetMemory(ctx context.Context, id string) (*Memory, error)
	GetMemoriesByIDs(ctx context.Context, ids []string) ([]*Memory, error)
	UpdateMemory(ctx context.Context, id string, updates MemoryUpdate) (*Memory, error)
	DeleteMemory(ctx context.Context, id string, hard bool) error

	// Vector search (delegates to HNSW internally)
	FindSimilar(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64) ([]*SimilarityResult, error)
	FindSimilarWithOptions(ctx context.Context, embedding []float32, entityID string, limit int, minScore float64, opts SimilarityOptions) ([]*SimilarityResult, error)

	// Queries
	QueryMemories(ctx context.Context, query MemoryQuery) ([]*Memory, error)
	GetRecentMemories(ctx context.Context, entityID string, hours int, limit int) ([]*Memory, error)

	// Deduplication
	FindByHash(ctx context.Context, entityID string, hash string) (*Memory, error)
	FindByHashWithAgent(ctx context.Context, entityID, agentID, hash string) (*Memory, error)

	// History
	LogHistory(ctx context.Context, entry *HistoryEntry) error
	GetHistory(ctx context.Context, memoryID string, limit int) ([]*HistoryEntry, error)

	// Session context
	AddSessionMessage(ctx context.Context, msg *SessionMessage) error
	GetRecentSessionMessages(ctx context.Context, entityID string, limit int) ([]*SessionMessage, error)

	// Access tracking
	UpdateAccessStats(ctx context.Context, ids []string) error
	UpdateStability(ctx context.Context, id string, newStability float64) error

	// Lifecycle
	TransitionState(ctx context.Context, id string, newState MemoryState, reason string) error
	GetStaleMemories(ctx context.Context, entityID string, decayThreshold float64) ([]*Memory, error)

	// Batch operations for background jobs
	GetAllEntities(ctx context.Context) ([]string, error)
	GetActiveMemoriesForDecay(ctx context.Context, batchSize int, offset int) ([]*Memory, error)
	BatchTransitionStates(ctx context.Context, transitions []StateTransition) (int, error)

	// Entity CRUD
	CreateEntity(ctx context.Context, entity *Entity) error
	GetEntity(ctx context.Context, id string) (*Entity, error)
	GetEntityByName(ctx context.Context, ownerEntityID, name string, entityType EntityType) (*Entity, error)
	FindEntityByAlias(ctx context.Context, ownerEntityID, alias string) (*Entity, error)
	FindSimilarEntities(ctx context.Context, embedding []float32, ownerEntityID string, limit int, minScore float64) ([]*Entity, error)
	QueryEntities(ctx context.Context, query EntityQuery) ([]*Entity, error)
	UpdateEntity(ctx context.Context, id string, updates map[string]any) (*Entity, error)
	UpdateEntityMentionCount(ctx context.Context, id string) error
	AddEntityAlias(ctx context.Context, id string, alias string) error
	DeleteEntity(ctx context.Context, id string) error
	DeleteAllEntitiesForOwner(ctx context.Context, ownerEntityID string) (int, error)
	CreateEntityMention(ctx context.Context, mention *EntityMention) error
	GetEntityMentions(ctx context.Context, entityID string, limit int) ([]*EntityMention, error)
	GetMemoryEntities(ctx context.Context, memoryID string) ([]*Entity, error)

	// Relationship CRUD
	CreateRelationship(ctx context.Context, rel *Relationship) error
	GetRelationship(ctx context.Context, id string) (*Relationship, error)
	FindRelationship(ctx context.Context, ownerEntityID, sourceID, targetID, relType string) (*Relationship, error)
	GetEntityRelationships(ctx context.Context, ownerEntityID, entityID string, direction string) ([]*Relationship, error)
	QueryRelationships(ctx context.Context, query RelationshipQuery) ([]*Relationship, error)
	UpdateRelationship(ctx context.Context, id string, updates map[string]any) (*Relationship, error)
	IncrementRelationshipEvidence(ctx context.Context, id string) error
	DeleteRelationship(ctx context.Context, id string) error
	CreateRelationshipEvidence(ctx context.Context, evidence *RelationshipEvidence) error
	GetRelationshipEvidence(ctx context.Context, relationshipID string, limit int) ([]*RelationshipEvidence, error)
	GetRelationshipPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string, maxDepth int) ([]string, error)
	DeleteAllRelationshipsForOwner(ctx context.Context, ownerEntityID string) (int, error)

	// Schema CRUD
	CreateSchema(ctx context.Context, schema *ExtractionSchema) error
	GetSchema(ctx context.Context, id string) (*ExtractionSchema, error)
	GetSchemaByName(ctx context.Context, entityID, name string) (*ExtractionSchema, error)
	QuerySchemas(ctx context.Context, query SchemaQuery) ([]*ExtractionSchema, error)
	UpdateSchema(ctx context.Context, id string, updates map[string]any) (*ExtractionSchema, error)
	DeleteSchema(ctx context.Context, id string) error

	// Custom Extraction CRUD
	CreateCustomExtraction(ctx context.Context, extraction *CustomExtraction) error
	GetCustomExtraction(ctx context.Context, id string) (*CustomExtraction, error)
	GetCustomExtractionsByMemory(ctx context.Context, memoryID string) ([]*CustomExtraction, error)
	QueryCustomExtractions(ctx context.Context, query CustomExtractionQuery) ([]*CustomExtraction, error)
	DeleteCustomExtraction(ctx context.Context, id string) error
	DeleteCustomExtractionsBySchema(ctx context.Context, schemaID string) error

	// Agent State CRUD
	CreateAgentState(ctx context.Context, state *AgentState) error
	GetAgentState(ctx context.Context, entityID, agentID, schemaName string) (*AgentState, error)
	UpdateAgentState(ctx context.Context, id string, newState map[string]any) error
	GetAgentStateHistory(ctx context.Context, stateID string, limit int) ([]*AgentStateHistory, error)
	LogAgentStateHistory(ctx context.Context, entry *AgentStateHistory) error

	// Team CRUD
	CreateTeam(ctx context.Context, team *Team) error
	GetTeam(ctx context.Context, id string) (*Team, error)
	DeleteTeam(ctx context.Context, id string) error
	AddTeamMember(ctx context.Context, teamID, agentID string) error
	RemoveTeamMember(ctx context.Context, teamID, agentID string) error
	GetTeamMembers(ctx context.Context, teamID string) ([]*TeamMember, error)
	GetTeamForAgent(ctx context.Context, agentID string) (string, error) // returns team_id or ""

	// Aggregation & Sampling (for reporting at scale)
	AggregateStats(ctx context.Context, entityID string) (*AggregatedStats, error)
	SampleMemories(ctx context.Context, entityID string, limit int) ([]*Memory, error)

	// Maintenance
	Close() error
	Ping(ctx context.Context) error
}
