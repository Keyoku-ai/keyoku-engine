// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

// Package keyoku provides an embedded memory engine for AI agents.
//
// Keyoku Embedded is a standalone Go library with SQLite + in-process HNSW vector
// search. It provides intelligent memory (extract, store, deduplicate, detect
// conflicts, decay, consolidate) and HeartbeatCheck (zero-token local query that
// tells an agent whether it needs to act).
package keyoku

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/engine"
	"github.com/keyoku-ai/keyoku-engine/jobs"
	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// Re-export commonly used types so users import only the keyoku package.
type (
	Memory           = storage.Memory
	MemoryType       = storage.MemoryType
	MemoryState      = storage.MemoryState
	MemoryVisibility = storage.MemoryVisibility
	Team             = storage.Team
	TeamMember       = storage.TeamMember
	Entity           = storage.Entity
	EntityType       = storage.EntityType
	Relationship     = storage.Relationship
	ExtractionSchema = storage.ExtractionSchema
	CustomExtraction = storage.CustomExtraction
	SearchResult     = engine.QueryResult
	Stats            = engine.Stats
)

// Re-export visibility constants.
const (
	VisibilityPrivate = storage.VisibilityPrivate
	VisibilityTeam    = storage.VisibilityTeam
	VisibilityGlobal  = storage.VisibilityGlobal
)

// Re-export memory type constants.
const (
	TypeIdentity     = storage.TypeIdentity
	TypePreference   = storage.TypePreference
	TypeRelationship = storage.TypeRelationship
	TypeEvent        = storage.TypeEvent
	TypeActivity     = storage.TypeActivity
	TypePlan         = storage.TypePlan
	TypeContext      = storage.TypeContext
	TypeEphemeral    = storage.TypeEphemeral
)

// Re-export memory state constants.
const (
	StateActive   = storage.StateActive
	StateStale    = storage.StateStale
	StateArchived = storage.StateArchived
	StateDeleted  = storage.StateDeleted
)

// Re-export scorer modes.
const (
	ModeBalanced      = engine.ModeBalanced
	ModeRecent        = engine.ModeRecent
	ModeImportant     = engine.ModeImportant
	ModeHistorical    = engine.ModeHistorical
	ModeComprehensive = engine.ModeComprehensive
)

// Keyoku is the main public API for the embedded memory engine.
type Keyoku struct {
	engine       *engine.Engine
	store        storage.Store
	scheduler    *jobs.Scheduler
	emb          embedder.Embedder
	provider     llm.Provider
	logger       *slog.Logger
	stateManager *engine.StateManager
	eventBus     *EventBus
	watcher      *Watcher
}

// New creates a new Keyoku instance with the given configuration.
func New(cfg Config) (*Keyoku, error) {
	logger := slog.Default()

	// Create LLM provider
	apiKey := cfg.OpenAIAPIKey
	baseURL := cfg.OpenAIBaseURL
	switch cfg.ExtractionProvider {
	case "google", "gemini":
		apiKey = cfg.GeminiAPIKey
		baseURL = "" // Gemini SDK doesn't support custom base URLs
	case "anthropic":
		apiKey = cfg.AnthropicAPIKey
		baseURL = cfg.AnthropicBaseURL
	}

	provider, err := llm.NewProvider(llm.ProviderConfig{
		Provider: cfg.ExtractionProvider,
		APIKey:   apiKey,
		Model:    cfg.ExtractionModel,
		BaseURL:  baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Create embedder (supports custom base URL for OpenRouter/LiteLLM)
	embBaseURL := cfg.EmbeddingBaseURL
	if embBaseURL == "" {
		embBaseURL = cfg.OpenAIBaseURL // Fall back to OpenAI base URL
	}
	emb := embedder.NewOpenAIWithBaseURL(cfg.OpenAIAPIKey, cfg.EmbeddingModel, embBaseURL)

	// Create storage (SQLite + HNSW vector index)
	store, err := storage.NewSQLite(cfg.DBPath, emb.Dimensions())
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Create event bus (async so handlers never block the hot path)
	eventBus := NewEventBus(true)

	k := &Keyoku{
		store:        store,
		emb:          emb,
		provider:     provider,
		logger:       logger,
		stateManager: engine.NewStateManager(store, provider),
		eventBus:     eventBus,
	}

	// Create engine
	k.engine = engine.NewEngine(provider, emb, store, engine.EngineConfig{
		ContextTurns: cfg.ContextTurns,
	})

	// Wire event emitter into all components
	emitterFn := eventBus.emitterFunc()
	k.engine.SetEmitter(emitterFn)
	k.stateManager.SetEmitter(emitterFn)

	// Create and start scheduler if enabled
	if cfg.SchedulerEnabled {
		k.scheduler = jobs.NewScheduler(logger, jobs.DefaultSchedules())
		k.scheduler.SetEmitter(jobs.EventEmitter(emitterFn))
		k.scheduler.RegisterProcessor(jobs.NewDecayProcessor(store, logger, jobs.DefaultDecayJobConfig()))
		k.scheduler.RegisterProcessor(jobs.NewConsolidationProcessor(store, provider, logger, jobs.DefaultConsolidationJobConfig()))
		k.scheduler.RegisterProcessor(jobs.NewArchivalProcessor(store, logger, jobs.DefaultArchivalJobConfig()))
		k.scheduler.RegisterProcessor(jobs.NewPurgeProcessor(store, logger, jobs.DefaultPurgeJobConfig()))
		k.scheduler.Start()
	}

	return k, nil
}

// SetStore sets the storage backend. Used for testing or custom storage.
func (k *Keyoku) SetStore(store storage.Store) {
	k.store = store
	k.engine = engine.NewEngine(k.provider, k.emb, store, engine.DefaultEngineConfig())
	k.stateManager = engine.NewStateManager(store, k.provider)
	// Re-wire emitter
	if k.eventBus != nil {
		emitterFn := k.eventBus.emitterFunc()
		k.engine.SetEmitter(emitterFn)
		k.stateManager.SetEmitter(emitterFn)
	}
}

// --- Event API ---

// OnEvent registers a handler for a specific event type.
// Handlers are called asynchronously and never block the engine pipeline.
func (k *Keyoku) OnEvent(eventType EventType, handler EventHandler) {
	k.eventBus.On(eventType, handler)
}

// OnAnyEvent registers a handler that fires for all events.
func (k *Keyoku) OnAnyEvent(handler EventHandler) {
	k.eventBus.OnAny(handler)
}

// Events returns the event bus for advanced usage.
func (k *Keyoku) Events() *EventBus {
	return k.eventBus
}

// StartWatcher starts the proactive heartbeat watcher.
// The watcher runs HeartbeatCheck on a tight interval and emits events
// when action is needed — no polling required by the consumer.
func (k *Keyoku) StartWatcher(config WatcherConfig) *Watcher {
	if k.watcher != nil {
		k.watcher.Stop()
	}
	k.watcher = newWatcher(k, config)
	k.watcher.Start()
	return k.watcher
}

// Watcher returns the active watcher, or nil if not started.
func (k *Keyoku) Watcher() *Watcher {
	return k.watcher
}

// --- RememberOption ---

// RememberOption configures a Remember call.
type RememberOption func(*rememberConfig)

type rememberConfig struct {
	sessionID  string
	agentID    string
	source     string
	schemaID   string
	teamID     string
	visibility storage.MemoryVisibility
}

// WithSessionID sets the session ID for a Remember call.
func WithSessionID(id string) RememberOption {
	return func(c *rememberConfig) { c.sessionID = id }
}

// WithAgentID scopes the memory to a specific agent.
func WithAgentID(id string) RememberOption {
	return func(c *rememberConfig) { c.agentID = id }
}

// WithSource records where the content came from.
func WithSource(source string) RememberOption {
	return func(c *rememberConfig) { c.source = source }
}

// WithSchemaID runs custom extraction alongside default memory extraction.
func WithSchemaID(id string) RememberOption {
	return func(c *rememberConfig) { c.schemaID = id }
}

// WithTeamID associates the memory with a team.
// When set and no explicit visibility is given, defaults to VisibilityTeam (share-by-default).
func WithTeamID(id string) RememberOption {
	return func(c *rememberConfig) { c.teamID = id }
}

// WithVisibility sets the visibility level of the memory.
func WithVisibility(v storage.MemoryVisibility) RememberOption {
	return func(c *rememberConfig) { c.visibility = v }
}

// RememberResult contains the result of a Remember call.
type RememberResult struct {
	MemoriesCreated     int
	MemoriesUpdated     int
	MemoriesDeleted     int
	Skipped             int
	Details             []engine.MemoryDetail
	CustomExtractionID  string
	CustomExtractedData map[string]any
}

// Remember extracts and stores memories from content.
func (k *Keyoku) Remember(ctx context.Context, entityID, content string, opts ...RememberOption) (*RememberResult, error) {
	cfg := &rememberConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// If agent has a team but caller didn't provide one, auto-resolve
	teamID := cfg.teamID
	if teamID == "" && cfg.agentID != "" {
		if resolved, err := k.store.GetTeamForAgent(ctx, cfg.agentID); err == nil && resolved != "" {
			teamID = resolved
		}
	}

	result, err := k.engine.Add(ctx, entityID, engine.AddRequest{
		Content:    content,
		SessionID:  cfg.sessionID,
		AgentID:    cfg.agentID,
		Source:     cfg.source,
		SchemaID:   cfg.schemaID,
		TeamID:     teamID,
		Visibility: cfg.visibility,
	})
	if err != nil {
		return nil, err
	}

	return &RememberResult{
		MemoriesCreated:     result.MemoriesCreated,
		MemoriesUpdated:     result.MemoriesUpdated,
		MemoriesDeleted:     result.MemoriesDeleted,
		Skipped:             result.Skipped,
		Details:             result.Details,
		CustomExtractionID:  result.CustomExtractionID,
		CustomExtractedData: result.CustomExtractedData,
	}, nil
}

// --- SearchOption ---

// SearchOption configures a Search call.
type SearchOption func(*searchConfig)

type searchConfig struct {
	limit     int
	mode      engine.ScorerMode
	agentID   string
	teamAware bool
}

// WithLimit sets the maximum number of results.
func WithLimit(n int) SearchOption {
	return func(c *searchConfig) { c.limit = n }
}

// WithMode sets the scoring mode (e.g., ModeRecent, ModeImportant).
func WithMode(mode engine.ScorerMode) SearchOption {
	return func(c *searchConfig) { c.mode = mode }
}

// WithSearchAgentID filters results by agent.
func WithSearchAgentID(id string) SearchOption {
	return func(c *searchConfig) { c.agentID = id }
}

// WithTeamAwareness enables team-aware visibility filtering for search.
// The agent's team is auto-resolved, and results include the agent's private
// memories, team-visible memories, and global memories.
func WithTeamAwareness(agentID string) SearchOption {
	return func(c *searchConfig) {
		c.agentID = agentID
		c.teamAware = true
	}
}

// Search retrieves memories relevant to a query.
func (k *Keyoku) Search(ctx context.Context, entityID, query string, opts ...SearchOption) ([]*SearchResult, error) {
	cfg := &searchConfig{limit: 10}
	for _, opt := range opts {
		opt(cfg)
	}

	qr := engine.QueryRequest{
		Query:     query,
		Limit:     cfg.limit,
		Mode:      cfg.mode,
		AgentID:   cfg.agentID,
		TeamAware: cfg.teamAware,
	}

	// Resolve team for visibility filtering
	if cfg.teamAware && cfg.agentID != "" {
		if teamID, err := k.store.GetTeamForAgent(ctx, cfg.agentID); err == nil && teamID != "" {
			qr.TeamID = teamID
		}
	}

	return k.engine.Query(ctx, entityID, qr)
}

// List returns all memories for an entity.
func (k *Keyoku) List(ctx context.Context, entityID string, limit int) ([]*Memory, error) {
	return k.engine.GetAll(ctx, entityID, limit)
}

// Get retrieves a specific memory by ID.
func (k *Keyoku) Get(ctx context.Context, id string) (*Memory, error) {
	return k.engine.GetByID(ctx, id)
}

// Delete removes a memory by ID.
func (k *Keyoku) Delete(ctx context.Context, id string) error {
	return k.engine.Delete(ctx, id)
}

// DeleteAll removes all memories, entities, and relationships for an entity.
func (k *Keyoku) DeleteAll(ctx context.Context, entityID string) error {
	return k.engine.DeleteAll(ctx, entityID)
}

// ListEntities returns all known entity IDs with stored memories.
func (k *Keyoku) ListEntities(ctx context.Context) ([]string, error) {
	return k.store.GetAllEntities(ctx)
}

// Stats returns statistics about stored memories.
func (k *Keyoku) Stats(ctx context.Context, entityID string) (*Stats, error) {
	return k.engine.GetStats(ctx, entityID)
}

// GlobalStats returns SQL-aggregated stats. Empty entityID = global across all entities.
func (k *Keyoku) GlobalStats(ctx context.Context, entityID string) (*storage.AggregatedStats, error) {
	return k.engine.GetGlobalStats(ctx, entityID)
}

// SampleMemories returns a representative sample of memories using server-side SQL.
func (k *Keyoku) SampleMemories(ctx context.Context, entityID string, limit int) ([]*Memory, error) {
	return k.engine.GetSampleMemories(ctx, entityID, limit)
}

// TokenUsage returns token usage statistics for an entity.
func (k *Keyoku) TokenUsage(entityID string) engine.TokenUsageStats {
	return k.engine.TokenBudget().GetUsage(entityID)
}

// --- Knowledge Graph ---

// EntityService provides entity operations.
type EntityService struct {
	store storage.Store
}

// Entities returns the entity service.
func (k *Keyoku) Entities() *EntityService {
	return &EntityService{store: k.store}
}

// List returns all entities for an owner.
func (es *EntityService) List(ctx context.Context, ownerEntityID string, limit int) ([]*Entity, error) {
	return es.store.QueryEntities(ctx, storage.EntityQuery{
		OwnerEntityID: ownerEntityID,
		Limit:         limit,
	})
}

// Get retrieves an entity by ID.
func (es *EntityService) Get(ctx context.Context, id string) (*Entity, error) {
	return es.store.GetEntity(ctx, id)
}

// Search finds entities by name.
func (es *EntityService) Search(ctx context.Context, ownerEntityID, name string) (*Entity, error) {
	return es.store.FindEntityByAlias(ctx, ownerEntityID, name)
}

// RelationshipService provides relationship operations.
type RelationshipService struct {
	store storage.Store
}

// Relationships returns the relationship service.
func (k *Keyoku) Relationships() *RelationshipService {
	return &RelationshipService{store: k.store}
}

// List returns relationships for an entity.
func (rs *RelationshipService) List(ctx context.Context, ownerEntityID, entityID string) ([]*Relationship, error) {
	return rs.store.GetEntityRelationships(ctx, ownerEntityID, entityID, "both")
}

// Get retrieves a relationship by ID.
func (rs *RelationshipService) Get(ctx context.Context, id string) (*Relationship, error) {
	return rs.store.GetRelationship(ctx, id)
}

// GraphService provides graph traversal operations.
type GraphService struct {
	graph    *engine.GraphEngine
	provider llm.Provider
}

// Graph returns the graph service.
func (k *Keyoku) Graph() *GraphService {
	return &GraphService{graph: k.engine.Graph(), provider: k.provider}
}

// FindPath finds the shortest path between two entities.
func (gs *GraphService) FindPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string) ([]string, error) {
	return gs.graph.FindPath(ctx, ownerEntityID, fromEntityID, toEntityID)
}

// ExplainConnection uses LLM to explain how two entities are connected.
func (gs *GraphService) ExplainConnection(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string) (string, error) {
	return gs.graph.ExplainConnection(ctx, ownerEntityID, fromEntityID, toEntityID, gs.provider)
}

// SummarizeEntity uses LLM to summarize all relationships for an entity.
func (gs *GraphService) SummarizeEntity(ctx context.Context, ownerEntityID, entityID string) (string, error) {
	return gs.graph.SummarizeEntityContext(ctx, ownerEntityID, entityID, gs.provider)
}

// --- Custom Extraction Schemas ---

// SchemaService provides extraction schema operations.
type SchemaService struct {
	store storage.Store
}

// Schemas returns the schema service.
func (k *Keyoku) Schemas() *SchemaService {
	return &SchemaService{store: k.store}
}

// Create creates a new extraction schema.
func (ss *SchemaService) Create(ctx context.Context, schema *ExtractionSchema) error {
	return ss.store.CreateSchema(ctx, schema)
}

// Get retrieves a schema by ID.
func (ss *SchemaService) Get(ctx context.Context, id string) (*ExtractionSchema, error) {
	return ss.store.GetSchema(ctx, id)
}

// GetByName retrieves a schema by name for an entity.
func (ss *SchemaService) GetByName(ctx context.Context, entityID, name string) (*ExtractionSchema, error) {
	return ss.store.GetSchemaByName(ctx, entityID, name)
}

// List returns schemas for an entity.
func (ss *SchemaService) List(ctx context.Context, entityID string, activeOnly bool, limit int) ([]*ExtractionSchema, error) {
	return ss.store.QuerySchemas(ctx, storage.SchemaQuery{
		EntityID:   entityID,
		ActiveOnly: activeOnly,
		Limit:      limit,
	})
}

// Update updates a schema.
func (ss *SchemaService) Update(ctx context.Context, id string, updates map[string]any) (*ExtractionSchema, error) {
	return ss.store.UpdateSchema(ctx, id, updates)
}

// Delete deletes a schema and its extractions.
func (ss *SchemaService) Delete(ctx context.Context, id string) error {
	return ss.store.DeleteSchema(ctx, id)
}

// --- Custom Extractions ---

// ExtractionService provides custom extraction operations.
type ExtractionService struct {
	store storage.Store
}

// Extractions returns the extraction service.
func (k *Keyoku) Extractions() *ExtractionService {
	return &ExtractionService{store: k.store}
}

// Get retrieves a custom extraction by ID.
func (xs *ExtractionService) Get(ctx context.Context, id string) (*CustomExtraction, error) {
	return xs.store.GetCustomExtraction(ctx, id)
}

// GetByMemory retrieves all extractions for a memory.
func (xs *ExtractionService) GetByMemory(ctx context.Context, memoryID string) ([]*CustomExtraction, error) {
	return xs.store.GetCustomExtractionsByMemory(ctx, memoryID)
}

// List returns extractions matching query filters.
func (xs *ExtractionService) List(ctx context.Context, query storage.CustomExtractionQuery) ([]*CustomExtraction, error) {
	return xs.store.QueryCustomExtractions(ctx, query)
}

// Delete removes a custom extraction.
func (xs *ExtractionService) Delete(ctx context.Context, id string) error {
	return xs.store.DeleteCustomExtraction(ctx, id)
}

// --- Agent State Machine ---

// AgentStateService provides agent state operations.
type AgentStateService struct {
	sm *engine.StateManager
}

// AgentState returns the agent state service.
func (k *Keyoku) AgentState() *AgentStateService {
	return &AgentStateService{sm: k.stateManager}
}

// Register creates a new agent state schema registration.
func (as *AgentStateService) Register(ctx context.Context, entityID, agentID, schemaName string, schema map[string]any, transitionRules map[string]any) error {
	return as.sm.Register(ctx, entityID, agentID, schemaName, schema, transitionRules)
}

// Update processes content through LLM to extract and persist state changes.
func (as *AgentStateService) Update(ctx context.Context, entityID, agentID, schemaName, content string, conversationCtx ...string) (*engine.StateUpdateResult, error) {
	return as.sm.Update(ctx, entityID, agentID, schemaName, content, conversationCtx)
}

// Get retrieves the current state for an agent.
func (as *AgentStateService) Get(ctx context.Context, entityID, agentID, schemaName string) (map[string]any, error) {
	return as.sm.Get(ctx, entityID, agentID, schemaName)
}

// StateHistoryEntry is the public type for agent state history.
type StateHistoryEntry = storage.AgentStateHistory

// History retrieves the state change history.
func (as *AgentStateService) History(ctx context.Context, entityID, agentID, schemaName string, limit int) ([]*StateHistoryEntry, error) {
	return as.sm.History(ctx, entityID, agentID, schemaName, limit)
}

// --- Schedule API ---

// AcknowledgeSchedule marks a scheduled memory as run by advancing its last_accessed_at.
// Call this after the agent has acted on a scheduled task to prevent re-firing.
func (k *Keyoku) AcknowledgeSchedule(ctx context.Context, memoryID string) error {
	return k.store.UpdateAccessStats(ctx, []string{memoryID})
}

// ListScheduled returns all cron-tagged memories for an entity/agent pair.
// Defense-in-depth: also recovers any cron memories that accidentally decayed to stale.
func (k *Keyoku) ListScheduled(ctx context.Context, entityID string, agentID string) ([]*Memory, error) {
	// Primary: fetch active cron memories
	active, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:  entityID,
		AgentID:   agentID,
		TagPrefix: "cron:",
		States:    []storage.MemoryState{storage.StateActive},
		Limit:     100,
	})
	if err != nil {
		return nil, err
	}

	// Defense-in-depth: check for stale cron memories and auto-recover them
	stale, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:  entityID,
		AgentID:   agentID,
		TagPrefix: "cron:",
		States:    []storage.MemoryState{storage.StateStale},
		Limit:     100,
	})
	if err != nil {
		k.logger.Warn("failed to query stale cron memories for recovery", "error", err)
	}

	for _, mem := range stale {
		activeState := storage.StateActive
		_, recoverErr := k.store.UpdateMemory(ctx, mem.ID, storage.MemoryUpdate{State: &activeState})
		if recoverErr != nil {
			k.logger.Error("failed to recover stale cron memory", "id", mem.ID, "error", recoverErr)
			continue
		}
		k.logger.Info("recovered stale cron memory to active", "id", mem.ID, "content", mem.Content)
		mem.State = storage.StateActive
		active = append(active, mem)
	}

	return active, nil
}

// CreateSchedule creates a new scheduled memory with a cron tag.
// It performs duplicate detection: if an existing active schedule for the same entity/agent
// has content that matches, the old schedule is archived before creating the new one.
func (k *Keyoku) CreateSchedule(ctx context.Context, entityID, agentID, content, cronTag string) (*Memory, error) {
	// Validate the cron tag
	if _, err := ParseSchedule(cronTag); err != nil {
		return nil, fmt.Errorf("invalid cron tag: %w", err)
	}

	// Duplicate detection: archive any existing schedule with matching content
	existing, err := k.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:  entityID,
		AgentID:   agentID,
		TagPrefix: "cron:",
		States:    []storage.MemoryState{storage.StateActive},
		Limit:     100,
	})
	if err == nil {
		for _, mem := range existing {
			if isScheduleContentMatch(mem.Content, content) {
				archivedState := storage.StateArchived
				k.store.UpdateMemory(ctx, mem.ID, storage.MemoryUpdate{State: &archivedState})
				k.store.LogHistory(ctx, &storage.HistoryEntry{
					MemoryID:  mem.ID,
					Operation: "schedule_replaced",
					Changes: map[string]any{
						"replaced_by_content": content,
						"replaced_by_cron":    cronTag,
					},
					Reason: "duplicate schedule detected during CreateSchedule",
				})
				k.logger.Info("archived duplicate schedule", "old_id", mem.ID, "content", content)
			}
		}
	}

	// Generate embedding for vector search
	embedding, err := k.emb.Embed(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Resolve team membership
	teamID := ""
	if agentID != "" {
		if resolved, resolveErr := k.store.GetTeamForAgent(ctx, agentID); resolveErr == nil && resolved != "" {
			teamID = resolved
		}
	}

	// Determine visibility (team if agent has a team, otherwise private)
	visibility := storage.VisibilityPrivate
	if teamID != "" {
		visibility = storage.VisibilityTeam
	}

	tags := storage.StringSlice{cronTag}

	mem := &storage.Memory{
		EntityID:   entityID,
		AgentID:    agentID,
		TeamID:     teamID,
		Content:    content,
		Hash:       scheduleContentHash(content),
		Embedding:  encodeEmbeddingBytes(embedding),
		Type:       storage.TypePlan,
		Tags:       tags,
		Importance: 0.9, // Schedules are high importance
		Confidence: 1.0, // Explicitly created, full confidence
		Stability:  365, // Max stability — decay processor skips cron anyway
		State:      storage.StateActive,
		Visibility: visibility,
		Source:     "schedule_api",
	}

	if err := k.store.CreateMemory(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to create schedule memory: %w", err)
	}

	k.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  mem.ID,
		Operation: "schedule_created",
		Changes: map[string]any{
			"cron_tag": cronTag,
			"content":  content,
		},
		Reason: "schedule created via CreateSchedule API",
	})

	return mem, nil
}

// UpdateSchedule modifies an existing scheduled memory's cron tag and/or content.
// It replaces the cron tag while preserving any non-cron tags.
func (k *Keyoku) UpdateSchedule(ctx context.Context, memoryID, newCronTag string, newContent *string) (*Memory, error) {
	// Validate the new cron tag
	if _, err := ParseSchedule(newCronTag); err != nil {
		return nil, fmt.Errorf("invalid cron tag: %w", err)
	}

	// Get the existing memory
	mem, err := k.store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, fmt.Errorf("schedule not found: %w", err)
	}

	// Verify it's actually a cron-tagged memory
	hasCron := false
	for _, tag := range mem.Tags {
		if strings.HasPrefix(tag, "cron:") {
			hasCron = true
			break
		}
	}
	if !hasCron {
		return nil, fmt.Errorf("memory %s is not a scheduled memory (no cron tag)", memoryID)
	}

	// Build new tags: replace all cron:* tags with the new one, keep non-cron tags
	var newTags []string
	for _, tag := range mem.Tags {
		if !strings.HasPrefix(tag, "cron:") {
			newTags = append(newTags, tag)
		}
	}
	newTags = append(newTags, newCronTag)

	update := storage.MemoryUpdate{
		Tags: &newTags,
	}
	if newContent != nil {
		update.Content = newContent
	}

	// Ensure memory is active (recover if stale)
	activeState := storage.StateActive
	update.State = &activeState

	updated, err := k.store.UpdateMemory(ctx, memoryID, update)
	if err != nil {
		return nil, fmt.Errorf("failed to update schedule: %w", err)
	}

	changes := map[string]any{
		"old_cron_tag": mem.Tags,
		"new_cron_tag": newCronTag,
	}
	if newContent != nil {
		changes["old_content"] = mem.Content
		changes["new_content"] = *newContent
	}

	k.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  memoryID,
		Operation: "schedule_updated",
		Changes:   changes,
		Reason:    "schedule updated via UpdateSchedule API",
	})

	return updated, nil
}

// CancelSchedule archives a scheduled memory, effectively cancelling the schedule.
func (k *Keyoku) CancelSchedule(ctx context.Context, memoryID string) error {
	// Get the memory to verify it exists and is a schedule
	mem, err := k.store.GetMemory(ctx, memoryID)
	if err != nil {
		return fmt.Errorf("schedule not found: %w", err)
	}

	hasCron := false
	for _, tag := range mem.Tags {
		if strings.HasPrefix(tag, "cron:") {
			hasCron = true
			break
		}
	}
	if !hasCron {
		return fmt.Errorf("memory %s is not a scheduled memory (no cron tag)", memoryID)
	}

	archivedState := storage.StateArchived
	if _, err := k.store.UpdateMemory(ctx, memoryID, storage.MemoryUpdate{State: &archivedState}); err != nil {
		return fmt.Errorf("failed to cancel schedule: %w", err)
	}

	k.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  memoryID,
		Operation: "schedule_cancelled",
		Changes: map[string]any{
			"content":  mem.Content,
			"cron_tag": mem.Tags,
		},
		Reason: "schedule cancelled via CancelSchedule API",
	})

	return nil
}

// UpdateTags sets the tags on a memory, replacing any existing tags.
func (k *Keyoku) UpdateTags(ctx context.Context, memoryID string, tags []string) error {
	_, err := k.store.UpdateMemory(ctx, memoryID, storage.MemoryUpdate{
		Tags: &tags,
	})
	return err
}

// --- schedule helpers ---

// isScheduleContentMatch checks if two schedule content strings refer to the same task.
// Uses normalized comparison — case-insensitive and whitespace-trimmed.
func isScheduleContentMatch(existing, incoming string) bool {
	a := strings.ToLower(strings.TrimSpace(existing))
	b := strings.ToLower(strings.TrimSpace(incoming))
	// Exact match after normalization
	if a == b {
		return true
	}
	// Substring containment (one is a more detailed version of the other)
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// scheduleContentHash generates a SHA-256 hash for schedule content deduplication.
func scheduleContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// encodeEmbeddingBytes converts a float32 slice to bytes for SQLite BLOB storage.
func encodeEmbeddingBytes(embedding []float32) []byte {
	if len(embedding) == 0 {
		return nil
	}
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// RunConsolidation triggers immediate memory consolidation via the scheduler.
// Used for lifecycle-aware consolidation (e.g., after agent completion).
func (k *Keyoku) RunConsolidation(ctx context.Context) error {
	if k.scheduler == nil {
		return nil // scheduler disabled, no-op
	}
	return k.scheduler.RunNow(ctx, jobs.JobTypeConsolidation)
}

// Close closes the Keyoku instance and releases all resources.
func (k *Keyoku) Close() error {
	if k.watcher != nil {
		k.watcher.Stop()
	}
	if k.scheduler != nil {
		k.scheduler.Stop()
	}
	if k.engine != nil {
		k.engine.Close()
	}
	if k.store != nil {
		k.store.Close()
	}
	return nil
}

// --- Team Service ---

// TeamService provides team management operations.
type TeamService struct {
	store storage.Store
}

// Teams returns the team service.
func (k *Keyoku) Teams() *TeamService {
	return &TeamService{store: k.store}
}

// Create creates a new team and returns it.
func (ts *TeamService) Create(ctx context.Context, name, description string) (*Team, error) {
	team := &storage.Team{
		Name:              name,
		Description:       description,
		DefaultVisibility: storage.VisibilityTeam,
	}
	if err := ts.store.CreateTeam(ctx, team); err != nil {
		return nil, err
	}
	return team, nil
}

// Get retrieves a team by ID.
func (ts *TeamService) Get(ctx context.Context, id string) (*Team, error) {
	return ts.store.GetTeam(ctx, id)
}

// Delete removes a team and all its memberships.
func (ts *TeamService) Delete(ctx context.Context, id string) error {
	return ts.store.DeleteTeam(ctx, id)
}

// AddMember adds an agent to a team.
// An agent can only belong to one team (enforced by UNIQUE constraint).
func (ts *TeamService) AddMember(ctx context.Context, teamID, agentID string) error {
	return ts.store.AddTeamMember(ctx, teamID, agentID)
}

// RemoveMember removes an agent from a team.
func (ts *TeamService) RemoveMember(ctx context.Context, teamID, agentID string) error {
	return ts.store.RemoveTeamMember(ctx, teamID, agentID)
}

// Members lists all members of a team.
func (ts *TeamService) Members(ctx context.Context, teamID string) ([]*TeamMember, error) {
	return ts.store.GetTeamMembers(ctx, teamID)
}

// ForAgent returns the team ID for an agent, or empty string if not in a team.
func (ts *TeamService) ForAgent(ctx context.Context, agentID string) (string, error) {
	return ts.store.GetTeamForAgent(ctx, agentID)
}
