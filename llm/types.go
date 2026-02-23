package llm

// ExtractionResponse is the standardized response from any LLM provider.
type ExtractionResponse struct {
	Memories      []ExtractedMemory      `json:"memories"`
	Entities      []ExtractedEntity      `json:"entities,omitempty"`
	Relationships []ExtractedRelationship `json:"relationships,omitempty"`
	Updates       []MemoryUpdate         `json:"updates"`
	Deletes       []MemoryDelete         `json:"deletes"`
	Skipped       []SkippedContent       `json:"skipped"`
}

// ExtractedEntity represents an entity extracted by the LLM.
type ExtractedEntity struct {
	CanonicalName string   `json:"canonical_name"`
	Type          string   `json:"type"`
	Aliases       []string `json:"aliases,omitempty"`
	Context       string   `json:"context,omitempty"`
}

// ExtractedRelationship represents a relationship between entities.
type ExtractedRelationship struct {
	Source     string  `json:"source"`
	Relation   string  `json:"relation"`
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
}

// ExtractedMemory represents a single memory extracted by the LLM.
type ExtractedMemory struct {
	Content           string   `json:"content"`
	Type              string   `json:"type"`
	Importance        float64  `json:"importance"`
	Confidence        float64  `json:"confidence"`
	ImportanceFactors []string `json:"importance_factors,omitempty"`
	ConfidenceFactors []string `json:"confidence_factors,omitempty"`
	HedgingDetected   bool     `json:"hedging_detected"`
}

// MemoryUpdate represents a suggested update to an existing memory.
type MemoryUpdate struct {
	Query      string `json:"query"`
	NewContent string `json:"new_content"`
	Reason     string `json:"reason"`
}

// MemoryDelete represents a suggested deletion of an existing memory.
type MemoryDelete struct {
	Query  string `json:"query"`
	Reason string `json:"reason"`
}

// SkippedContent represents content the LLM decided not to extract.
type SkippedContent struct {
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// ExtractionRequest contains all the context needed for extraction.
type ExtractionRequest struct {
	Content          string
	ConversationCtx  []string
	ExistingMemories []string
}

// ConsolidationRequest contains memories to consolidate.
type ConsolidationRequest struct {
	Memories []string
}

// ConsolidationResponse contains the consolidated memory.
type ConsolidationResponse struct {
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// CustomExtractionRequest contains input for custom schema extraction.
type CustomExtractionRequest struct {
	Content         string
	Schema          map[string]any
	SchemaName      string
	ConversationCtx []string
}

// CustomExtractionResponse contains the extracted data from custom schema.
type CustomExtractionResponse struct {
	ExtractedData map[string]any `json:"extracted_data"`
	Confidence    float64        `json:"confidence"`
	Reasoning     string         `json:"reasoning"`
}

// StateExtractionRequest contains input for automatic state extraction.
type StateExtractionRequest struct {
	Content          string
	Schema           map[string]any
	SchemaName       string
	CurrentState     map[string]any
	TransitionRules  map[string]any
	ConversationCtx  []string
	AgentID          string
}

// StateExtractionResponse contains the extracted state.
type StateExtractionResponse struct {
	ExtractedState  map[string]any `json:"extracted_state"`
	ChangedFields   []string       `json:"changed_fields"`
	Confidence      float64        `json:"confidence"`
	Reasoning       string         `json:"reasoning"`
	SuggestedAction string         `json:"suggested_action"`
	ValidationError string         `json:"validation_error"`
}
