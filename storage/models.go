package storage

import "time"

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
func (t MemoryType) StabilityDays() float64 {
	switch t {
	case TypeIdentity:
		return 365
	case TypePreference:
		return 180
	case TypeRelationship:
		return 180
	case TypeEvent:
		return 60
	case TypeActivity:
		return 45
	case TypePlan:
		return 30
	case TypeContext:
		return 7
	case TypeEphemeral:
		return 1
	default:
		return 60
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
	Content   string     `db:"content"`
	Hash      string     `db:"content_hash"`
	Embedding []byte     `db:"embedding"` // BLOB backup for HNSW recovery

	Type       MemoryType  `db:"memory_type"`
	Tags       StringSlice `db:"tags"`

	Importance float64 `db:"importance"`
	Confidence float64 `db:"confidence"`
	Stability  float64 `db:"stability"`

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
	Types      []MemoryType
	Tags       []string
	States     []MemoryState
	MinScore   float64
	Limit      int
	Offset     int
	OrderBy    string
	Descending bool
}

// MemoryUpdate represents fields to update on a memory.
type MemoryUpdate struct {
	Content    *string
	Importance *float64
	Confidence *float64
	Tags       *[]string
	State      *MemoryState
	ExpiresAt  *time.Time
}

// SimilarityResult wraps a memory with its similarity score.
type SimilarityResult struct {
	Memory     *Memory
	Similarity float64
}

// SimilarityOptions defines optional filters for similarity search.
type SimilarityOptions struct {
	AgentID string
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
	Types         []EntityType
	NamePattern   string
	Limit         int
	Offset        int
}

// RelationshipQuery represents query parameters for searching relationships.
type RelationshipQuery struct {
	OwnerEntityID     string
	EntityID          string
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
