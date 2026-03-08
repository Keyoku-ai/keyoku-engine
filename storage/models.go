package storage

import "time"

// MemoryVisibility represents the access level of a memory.
type MemoryVisibility string

const (
	VisibilityPrivate MemoryVisibility = "private" // Only the owning agent sees this
	VisibilityTeam    MemoryVisibility = "team"    // All agents in the same team see this
	VisibilityGlobal  MemoryVisibility = "global"  // All agents across all teams see this
)

// IsValid checks if the visibility is valid.
func (v MemoryVisibility) IsValid() bool {
	switch v {
	case VisibilityPrivate, VisibilityTeam, VisibilityGlobal:
		return true
	default:
		return false
	}
}

// Team represents a group of agents that share a collective consciousness.
type Team struct {
	ID                string           `db:"id"`
	Name              string           `db:"name"`
	Description       string           `db:"description"`
	DefaultVisibility MemoryVisibility `db:"default_visibility"`
	CreatedAt         time.Time        `db:"created_at"`
	UpdatedAt         time.Time        `db:"updated_at"`
}

// TeamMember represents an agent's membership in a team.
type TeamMember struct {
	TeamID   string    `db:"team_id"`
	AgentID  string    `db:"agent_id"`
	Role     string    `db:"role"`
	JoinedAt time.Time `db:"joined_at"`
}

// VisibilityContext provides the information needed to resolve what an agent can see.
// Used by queries to build the visibility WHERE clause.
type VisibilityContext struct {
	AgentID string // The querying agent
	TeamID  string // Pre-resolved team_id for the agent (empty if no team)
}

// IsVisibleTo checks if a memory is visible to an agent given a visibility context.
func IsVisibleTo(visibility MemoryVisibility, memAgentID, memTeamID string, vc *VisibilityContext) bool {
	if vc == nil {
		return true // No visibility filtering
	}
	switch visibility {
	case VisibilityGlobal:
		return true
	case VisibilityTeam:
		return vc.TeamID != "" && memTeamID == vc.TeamID
	case VisibilityPrivate:
		return memAgentID == vc.AgentID
	default:
		return memAgentID == vc.AgentID // safe default
	}
}

// MemoryState represents the lifecycle state of a memory.
type MemoryState string

const (
	StateActive   MemoryState = "active"
	StateStale    MemoryState = "stale"
	StateArchived MemoryState = "archived"
	StateDeleted  MemoryState = "deleted"
)

// MemoryType represents the type of memory with associated stability.
type MemoryType string

const (
	TypeIdentity     MemoryType = "IDENTITY"
	TypePreference   MemoryType = "PREFERENCE"
	TypeRelationship MemoryType = "RELATIONSHIP"
	TypeEvent        MemoryType = "EVENT"
	TypeActivity     MemoryType = "ACTIVITY"
	TypePlan         MemoryType = "PLAN"
	TypeContext      MemoryType = "CONTEXT"
	TypeEphemeral    MemoryType = "EPHEMERAL"
)

// StabilityDays returns the default stability (in days) for a memory type.
//
// Tuned for AI agents that access memory far more frequently than humans.
// AI agents perform semantic search on nearly every interaction, so memories
// need higher base stability to avoid premature decay. The access-frequency
// modifier in the decay engine further extends effective stability for
// frequently retrieved memories.
//
// Design rationale:
//   - IDENTITY (365d): Who am I? Core identity rarely changes.
//   - PREFERENCE (270d): Preferences evolve slowly, ~9 months is right.
//   - RELATIONSHIP (270d): Relationships are long-term context.
//   - EVENT (120d): Important events stay relevant for months.
//   - ACTIVITY (90d): What was I doing? 3 months of behavioral history.
//   - PLAN (60d): Plans span weeks to months in agent workflows.
//   - CONTEXT (21d): Conversation context stays relevant across sessions.
//   - EPHEMERAL (3d): Truly transient, but 1 day was too aggressive.
func (t MemoryType) StabilityDays() float64 {
	switch t {
	case TypeIdentity:
		return 365
	case TypePreference:
		return 270
	case TypeRelationship:
		return 270
	case TypeEvent:
		return 120
	case TypeActivity:
		return 90
	case TypePlan:
		return 60
	case TypeContext:
		return 21
	case TypeEphemeral:
		return 3
	default:
		return 90
	}
}

// IsValid checks if the memory type is valid.
func (t MemoryType) IsValid() bool {
	switch t {
	case TypeIdentity, TypePreference, TypeRelationship, TypeEvent,
		TypeActivity, TypePlan, TypeContext, TypeEphemeral:
		return true
	default:
		return false
	}
}

// Memory represents a stored memory.
type Memory struct {
	ID        string     `db:"id"`
	EntityID  string     `db:"entity_id"`
	AgentID   string     `db:"agent_id"`
	TeamID    string     `db:"team_id"`
	Content   string     `db:"content"`
	Hash      string     `db:"content_hash"`
	Embedding []byte     `db:"embedding"` // BLOB backup for HNSW recovery

	Visibility MemoryVisibility `db:"visibility"`

	Type       MemoryType  `db:"memory_type"`
	Tags       StringSlice `db:"tags"`

	Importance float64 `db:"importance"`
	Confidence float64 `db:"confidence"`
	Stability  float64 `db:"stability"`
	Sentiment  float64 `db:"sentiment"` // -1.0 (very negative) to 1.0 (very positive), 0.0 = neutral

	AccessCount    int        `db:"access_count"`
	LastAccessedAt *time.Time `db:"last_accessed_at"`

	State     MemoryState `db:"state"`
	CreatedAt time.Time   `db:"created_at"`
	UpdatedAt time.Time   `db:"updated_at"`
	ExpiresAt *time.Time  `db:"expires_at"`
	DeletedAt *time.Time  `db:"deleted_at"`
	Version   int         `db:"version"`

	Source    string `db:"source"`
	SessionID string `db:"session_id"`

	DerivedFrom StringSlice `db:"derived_from"` // IDs of memories this was derived from (merges, consolidation)

	ExtractionProvider string      `db:"extraction_provider"`
	ExtractionModel    string      `db:"extraction_model"`
	ImportanceFactors  StringSlice `db:"importance_factors"`
	ConfidenceFactors  StringSlice `db:"confidence_factors"`
}

// HistoryEntry represents an entry in the audit trail.
type HistoryEntry struct {
	ID        string  `db:"id"`
	MemoryID  string  `db:"memory_id"`
	Operation string  `db:"operation"`
	Changes   JSONMap `db:"changes"`
	Reason    string  `db:"reason"`
	CreatedAt time.Time `db:"created_at"`
}

// SessionMessage represents a conversation turn.
type SessionMessage struct {
	ID         string    `db:"id"`
	EntityID   string    `db:"entity_id"`
	AgentID    string    `db:"agent_id"`
	SessionID  string    `db:"session_id"`
	Role       string    `db:"role"`
	Content    string    `db:"content"`
	TurnNumber int       `db:"turn_number"`
	CreatedAt  time.Time `db:"created_at"`
}

// MemoryQuery represents query parameters for searching memories.
type MemoryQuery struct {
	EntityID   string
	AgentID    string
	TeamID     string             // Filter to specific team
	Visibility []MemoryVisibility // Filter by specific visibility levels
	VisibilityFor *VisibilityContext // Build visibility clause for an agent (private+team+global resolution)
	Types      []MemoryType
	Tags       []string // Exact tag match (all must be present)
	TagPrefix  string   // Filter tags by prefix (e.g., "cron:" matches any cron-tagged memory)
	States     []MemoryState
	MinScore   float64
	Limit      int
	Offset     int
	OrderBy    string
	Descending bool
}

// MemoryUpdate represents fields to update on a memory.
type MemoryUpdate struct {
	Content     *string
	Importance  *float64
	Confidence  *float64
	Sentiment   *float64
	Tags        *[]string
	State       *MemoryState
	ExpiresAt   *time.Time
	DerivedFrom *StringSlice
	Visibility  *string
}

// SimilarityResult wraps a memory with its similarity score.
type SimilarityResult struct {
	Memory     *Memory
	Similarity float64
}

// SimilarityOptions defines optional filters for similarity search.
type SimilarityOptions struct {
	AgentID       string
	VisibilityFor *VisibilityContext // Apply visibility filtering
}

// StateTransition represents a memory state change for batch processing.
type StateTransition struct {
	MemoryID string
	NewState MemoryState
	Reason   string
}

// EntityType represents the type of entity in the knowledge graph.
type EntityType string

const (
	EntityTypePerson       EntityType = "person"
	EntityTypeOrganization EntityType = "organization"
	EntityTypeLocation     EntityType = "location"
	EntityTypeProduct      EntityType = "product"
	EntityTypeConcept      EntityType = "concept"
	EntityTypeEvent        EntityType = "event"
	EntityTypeOther        EntityType = "other"
)

// Entity represents a resolved entity in the knowledge graph.
type Entity struct {
	ID              string      `db:"id"`
	OwnerEntityID   string      `db:"owner_entity_id"`
	AgentID         string      `db:"agent_id"`
	TeamID          string      `db:"team_id"`
	CanonicalName   string      `db:"canonical_name"`
	Type            EntityType  `db:"type"`
	Description     string      `db:"description"`
	Aliases         StringSlice `db:"aliases"`
	Embedding       []byte      `db:"embedding"` // BLOB backup
	Attributes      JSONMap     `db:"attributes"`
	MentionCount    int         `db:"mention_count"`
	LastMentionedAt *time.Time  `db:"last_mentioned_at"`
	CreatedAt       time.Time   `db:"created_at"`
	UpdatedAt       time.Time   `db:"updated_at"`
}

// EntityMention links an entity to a memory.
type EntityMention struct {
	ID             string    `db:"id"`
	EntityID       string    `db:"entity_id"`
	MemoryID       string    `db:"memory_id"`
	MentionText    string    `db:"mention_text"`
	Confidence     float64   `db:"confidence"`
	ContextSnippet string    `db:"context_snippet"`
	CreatedAt      time.Time `db:"created_at"`
}

// Relationship represents a relationship between two entities.
type Relationship struct {
	ID               string     `db:"id"`
	OwnerEntityID    string     `db:"owner_entity_id"`
	AgentID          string     `db:"agent_id"`
	TeamID           string     `db:"team_id"`
	SourceEntityID   string     `db:"source_entity_id"`
	TargetEntityID   string     `db:"target_entity_id"`
	RelationshipType string     `db:"relationship_type"`
	Description      string     `db:"description"`
	Strength         float64    `db:"strength"`
	Confidence       float64    `db:"confidence"`
	IsBidirectional  bool       `db:"is_bidirectional"`
	EvidenceCount    int        `db:"evidence_count"`
	Attributes       JSONMap    `db:"attributes"`
	FirstSeenAt      time.Time  `db:"first_seen_at"`
	LastSeenAt       time.Time  `db:"last_seen_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// RelationshipEvidence links a relationship to a memory.
type RelationshipEvidence struct {
	ID             string    `db:"id"`
	RelationshipID string    `db:"relationship_id"`
	MemoryID       string    `db:"memory_id"`
	EvidenceText   string    `db:"evidence_text"`
	Confidence     float64   `db:"confidence"`
	CreatedAt      time.Time `db:"created_at"`
}

// EntityQuery represents query parameters for searching entities.
type EntityQuery struct {
	OwnerEntityID string
	AgentID       string
	TeamID        string
	Types         []EntityType
	NamePattern   string
	Limit         int
	Offset        int
}

// RelationshipQuery represents query parameters for searching relationships.
type RelationshipQuery struct {
	OwnerEntityID     string
	EntityID          string
	TeamID            string
	RelationshipTypes []string
	MinStrength       float64
	Limit             int
	Offset            int
}

// ConflictPair represents two memories that conflict with each other.
type ConflictPair struct {
	MemoryA *Memory
	MemoryB *Memory
	Reason  string
}

// ExtractionSchema defines a custom extraction schema.
type ExtractionSchema struct {
	ID               string         `db:"id"`
	EntityID         string         `db:"entity_id"`
	Name             string         `db:"name"`
	Description      string         `db:"description"`
	Version          string         `db:"version"`
	SchemaDefinition map[string]any `db:"schema_definition"`
	IsActive         bool           `db:"is_active"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`
}

// SchemaQuery represents query parameters for searching schemas.
type SchemaQuery struct {
	EntityID   string
	ActiveOnly bool
	Limit      int
	Offset     int
}

// CustomExtraction represents the result of a custom schema extraction.
type CustomExtraction struct {
	ID                 string         `db:"id"`
	EntityID           string         `db:"entity_id"`
	MemoryID           string         `db:"memory_id"`
	SchemaID           string         `db:"schema_id"`
	ExtractedData      map[string]any `db:"extracted_data"`
	ExtractionProvider string         `db:"extraction_provider"`
	ExtractionModel    string         `db:"extraction_model"`
	Confidence         float64        `db:"confidence"`
	CreatedAt          time.Time      `db:"created_at"`
}

// CustomExtractionQuery represents query parameters for searching extractions.
type CustomExtractionQuery struct {
	EntityID string
	MemoryID string
	SchemaID string
	Limit    int
	Offset   int
}

// AgentState represents a persistent state for an agent workflow.
type AgentState struct {
	ID              string         `db:"id"`
	EntityID        string         `db:"entity_id"`
	AgentID         string         `db:"agent_id"`
	SchemaName      string         `db:"schema_name"`
	CurrentState    map[string]any `db:"current_state"`
	SchemaDefinition map[string]any `db:"schema_definition"`
	TransitionRules map[string]any `db:"transition_rules"`
	LastUpdatedAt   *time.Time     `db:"last_updated_at"`
	CreatedAt       time.Time      `db:"created_at"`
}

// AgentStateHistory represents a state change event.
type AgentStateHistory struct {
	ID             string         `db:"id"`
	StateID        string         `db:"state_id"`
	PreviousState  map[string]any `db:"previous_state"`
	NewState       map[string]any `db:"new_state"`
	ChangedFields  StringSlice    `db:"changed_fields"`
	TriggerContent string         `db:"trigger_content"`
	Confidence     float64        `db:"confidence"`
	Reasoning      string         `db:"reasoning"`
	CreatedAt      time.Time      `db:"created_at"`
}
