// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"time"
)

// Store defines the unified storage interface for Keyoku Embedded.
// Combines core memory, entity, and relationship operations in a single interface
// since there's only one backend (SQLite).
type Store interface {
	// Memory CRUD
	CreateMemory(ctx context.Context, mem *Memory) error
	GetMemory(ctx context.Context, id string) (*Memory, error)
	GetMemoriesByIDs(ctx context.Context, ids []string) ([]*Memory, error)
	UpdateMemory(ctx context.Context, id string, updates MemoryUpdate) (*Memory, error)
	ResolveMemory(ctx context.Context, id string) error
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

	// Heartbeat action tracking
	RecordHeartbeatAction(ctx context.Context, action *HeartbeatAction) error
	GetLastHeartbeatAction(ctx context.Context, entityID, agentID, decision string) (*HeartbeatAction, error)
	GetNudgeCountToday(ctx context.Context, entityID, agentID string) (int, error)
	CleanupOldHeartbeatActions(ctx context.Context, olderThan time.Duration) error
	GetMessageHourDistribution(ctx context.Context, entityID string, days int) (map[int]int, error)

	// Heartbeat v2: intelligence
	GetHeartbeatActionsForResponseCheck(ctx context.Context, entityID string, minAge time.Duration) ([]*HeartbeatAction, error)
	UpdateHeartbeatActionResponse(ctx context.Context, actionID string, responded bool) error
	GetRecentActDecisions(ctx context.Context, entityID, agentID string, since time.Duration) ([]*HeartbeatAction, error)
	GetRecentDecisions(ctx context.Context, entityID, agentID string, since time.Duration) ([]*HeartbeatAction, error)
	GetResponseRate(ctx context.Context, entityID, agentID string, days int) (float64, int, error) // rate, total, error

	// Content rotation tracking
	RecordSurfacedMemories(ctx context.Context, entityID, agentID string, memoryIDs []string) error
	GetRecentlySurfacedMemoryIDs(ctx context.Context, entityID, agentID string, since time.Duration) ([]string, error)
	CleanupOldSurfacedMemories(ctx context.Context, olderThan time.Duration) error

	// Heartbeat message history (what the AI actually said)
	RecordHeartbeatMessage(ctx context.Context, msg *HeartbeatMessage) error
	GetRecentHeartbeatMessages(ctx context.Context, entityID, agentID string, limit int) ([]*HeartbeatMessage, error)

	// Topic escalation tracking
	UpsertTopicSurfacing(ctx context.Context, surfacing *TopicSurfacing) error
	GetTopicSurfacing(ctx context.Context, entityID, agentID, topicHash string) (*TopicSurfacing, error)
	GetActiveTopicSurfacings(ctx context.Context, entityID, agentID string, limit int) ([]*TopicSurfacing, error)
	MarkTopicDropped(ctx context.Context, entityID, agentID, topicHash string) error

	// Aggregation & Sampling (for reporting at scale)
	AggregateStats(ctx context.Context, entityID string) (*AggregatedStats, error)
	SampleMemories(ctx context.Context, entityID string, limit int) ([]*Memory, error)

	// Full-text search (Tier 3 fallback)
	SearchFTS(ctx context.Context, query string, entityID string, limit int) ([]*Memory, error)
	SearchFTSWithOptions(ctx context.Context, query string, entityID string, limit int, opts SimilarityOptions) ([]*Memory, error)

	// HNSW index management (for eviction)
	GetHNSWIndexSize() int
	GetLowestRankedInHNSW(ctx context.Context, limit int) ([]*Memory, error)
	RemoveFromHNSW(id string) error

	// Storage metrics
	GetStorageSizeBytes(ctx context.Context) (int64, error)
	GetMemoryCount(ctx context.Context) (int, error)
	GetMemoryCountForEntity(ctx context.Context, entityID string) (int, error)

	// Maintenance
	Close() error
	Ping(ctx context.Context) error
}
