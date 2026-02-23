// Package keyoku provides an embedded memory engine for AI agents.
//
// Keyoku Embedded is a standalone Go library with SQLite + in-process HNSW vector
// search. It provides intelligent memory (extract, store, deduplicate, detect
// conflicts, decay, consolidate) and HeartbeatCheck (zero-token local query that
// tells an agent whether it needs to act).
package keyoku

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/keyoku-ai/keyoku-embedded/embedder"
	"github.com/keyoku-ai/keyoku-embedded/engine"
	"github.com/keyoku-ai/keyoku-embedded/jobs"
	"github.com/keyoku-ai/keyoku-embedded/llm"
	"github.com/keyoku-ai/keyoku-embedded/storage"
)

// Re-export commonly used types so users import only the keyoku package.
type (
	Memory           = storage.Memory
	MemoryType       = storage.MemoryType
	MemoryState      = storage.MemoryState
	Entity           = storage.Entity
	EntityType       = storage.EntityType
	Relationship     = storage.Relationship
	ExtractionSchema = storage.ExtractionSchema
	CustomExtraction = storage.CustomExtraction
	SearchResult     = engine.QueryResult
	Stats            = engine.Stats
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
	switch cfg.ExtractionProvider {
	case "google":
		apiKey = cfg.GeminiAPIKey
	case "anthropic":
		apiKey = cfg.AnthropicAPIKey
	}

	provider, err := llm.NewProvider(llm.ProviderConfig{
		Provider: cfg.ExtractionProvider,
		APIKey:   apiKey,
		Model:    cfg.ExtractionModel,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Create embedder
	emb := embedder.NewOpenAI(cfg.OpenAIAPIKey, cfg.EmbeddingModel)

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
	sessionID string
	agentID   string
	source    string
	schemaID  string
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

	result, err := k.engine.Add(ctx, entityID, engine.AddRequest{
		Content:   content,
		SessionID: cfg.sessionID,
		AgentID:   cfg.agentID,
		Source:    cfg.source,
		SchemaID: cfg.schemaID,
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
	limit   int
	mode    engine.ScorerMode
	agentID string
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

// Search retrieves memories relevant to a query.
func (k *Keyoku) Search(ctx context.Context, entityID, query string, opts ...SearchOption) ([]*SearchResult, error) {
	cfg := &searchConfig{limit: 10}
	for _, opt := range opts {
		opt(cfg)
	}

	return k.engine.Query(ctx, entityID, engine.QueryRequest{
		Query:   query,
		Limit:   cfg.limit,
		Mode:    cfg.mode,
		AgentID: cfg.agentID,
	})
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

// Stats returns statistics about stored memories.
func (k *Keyoku) Stats(ctx context.Context, entityID string) (*Stats, error) {
	return k.engine.GetStats(ctx, entityID)
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
