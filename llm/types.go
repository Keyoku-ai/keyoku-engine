// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

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
	Sentiment         float64  `json:"sentiment"` // -1.0 to 1.0
	ImportanceFactors []string `json:"importance_factors,omitempty"`
	ConfidenceFactors []string `json:"confidence_factors,omitempty"`
	HedgingDetected   bool     `json:"hedging_detected"`
	Tags              []string `json:"tags,omitempty"` // e.g., ["cron:daily:08:00"] for schedule-tagged memories
}

// MemoryUpdate represents a suggested update to an existing memory.
type MemoryUpdate struct {
	Query      string   `json:"query"`
	NewContent string   `json:"new_content"`
	Reason     string   `json:"reason"`
	Tags       []string `json:"tags,omitempty"` // allows LLM to update cron/schedule tags
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
	Memories          []string
	EntityContext     []string  // "Alice (person)", "Google (organization)"
	RelationshipContext []string // "Alice works_at Google"
	ImportanceScores  []float64 // importance score per memory (parallel to Memories)
	ImportanceFactors []string  // deduplicated importance factors across all memories
	SentimentValues   []float64 // sentiment per memory (parallel to Memories)
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

// ConflictCheckRequest contains input for LLM-based conflict detection.
type ConflictCheckRequest struct {
	NewContent      string
	ExistingContent string
	MemoryType      string
	Context         string // brief context about the entity
}

// ConflictCheckResponse contains the LLM's conflict analysis.
type ConflictCheckResponse struct {
	Contradicts  bool    `json:"contradicts"`
	ConflictType string  `json:"conflict_type"` // contradiction, update, temporal, partial, none
	Confidence   float64 `json:"confidence"`
	Explanation  string  `json:"explanation"`
	Resolution   string  `json:"resolution"` // use_new, keep_existing, merge, keep_both
}

// ImportanceReEvalRequest contains input for adaptive importance re-evaluation.
type ImportanceReEvalRequest struct {
	NewContent        string
	ExistingContent   string
	CurrentImportance float64
	CurrentType       string
	RelatedMemories   []string
}

// ImportanceReEvalResponse contains the re-evaluated importance.
type ImportanceReEvalResponse struct {
	NewImportance float64 `json:"new_importance"`
	Reason        string  `json:"reason"`
	ShouldUpdate  bool    `json:"should_update"`
}

// ActionPriorityRequest contains input for LLM-powered heartbeat prioritization.
type ActionPriorityRequest struct {
	Summary       string // the raw heartbeat summary
	AgentContext  string // what the agent is currently doing
	EntityContext string // who the entity is
}

// ActionPriorityResponse contains the LLM's prioritized action plan.
type ActionPriorityResponse struct {
	PriorityAction string   `json:"priority_action"`
	ActionItems    []string `json:"action_items"`
	Reasoning      string   `json:"reasoning"`
	Urgency        string   `json:"urgency"` // immediate, soon, can_wait
}

// HeartbeatAnalysisRequest contains input for LLM-powered heartbeat context analysis.
type HeartbeatAnalysisRequest struct {
	ActivitySummary  string   // Recent conversation activity from the agent
	Scheduled        []string // Scheduled task descriptions
	Deadlines        []string // Approaching deadline descriptions
	PendingWork      []string // Pending work descriptions
	Conflicts        []string // Conflict descriptions
	RelevantMemories []string // Semantically relevant memory texts
	Autonomy         string   // "observe", "suggest", or "act"
	AgentID          string
	EntityID         string

	// Extended signals
	GoalProgress      []string `json:"goal_progress,omitempty"`      // Goal progress descriptions
	Continuity        string   `json:"continuity,omitempty"`         // Session continuity summary
	SentimentTrend    string   `json:"sentiment_trend,omitempty"`    // Sentiment direction summary
	RelationshipAlerts []string `json:"relationship_alerts,omitempty"` // Silent entity alerts
	KnowledgeGaps     []string `json:"knowledge_gaps,omitempty"`     // Unanswered questions
	BehavioralPatterns []string `json:"behavioral_patterns,omitempty"` // Day-of-week patterns

	// v2: Graph enrichment and delta detection
	GraphContext    []string `json:"graph_context,omitempty"`    // Entity relationship context from knowledge graph
	PositiveDeltas []string `json:"positive_deltas,omitempty"` // Detected positive changes since last heartbeat
}

// HeartbeatAnalysisResponse contains the LLM's analysis of heartbeat context.
type HeartbeatAnalysisResponse struct {
	ShouldAct          bool     `json:"should_act"`
	ActionBrief        string   `json:"action_brief"`
	RecommendedActions []string `json:"recommended_actions"`
	Urgency            string   `json:"urgency"`
	Reasoning          string   `json:"reasoning"`
	Autonomy           string   `json:"autonomy"`
	UserFacing         string   `json:"user_facing"`
}

// GraphSummaryRequest contains input for LLM-powered graph reasoning.
type GraphSummaryRequest struct {
	Entities      []string // entity names/descriptions in the path
	Relationships []string // "A works_at B" formatted strings
	Question      string   // optional: specific question about the graph
}

// GraphSummaryResponse contains the LLM's graph summary.
type GraphSummaryResponse struct {
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

// RerankRequest contains input for LLM-based re-ranking of search results.
type RerankRequest struct {
	Query      string            `json:"query"`
	Candidates []RerankCandidate `json:"candidates"`
}

// RerankCandidate represents a memory candidate for re-ranking.
type RerankCandidate struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Type    string  `json:"type"`
	Score   float64 `json:"score"`
}

// RerankResponse contains the LLM's re-ranked results.
type RerankResponse struct {
	Rankings []RerankResult `json:"rankings"`
}

// RerankResult represents a single re-ranked result.
type RerankResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
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
