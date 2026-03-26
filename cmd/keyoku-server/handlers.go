// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-engine"
)

// Handlers wraps the Keyoku instance and provides HTTP handlers.
type Handlers struct {
	k   *keyoku.Keyoku
	hub *SSEHub
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(k *keyoku.Keyoku, hub *SSEHub) *Handlers {
	return &Handlers{k: k, hub: hub}
}

// --- Request/Response types ---

type rememberRequest struct {
	EntityID   string `json:"entity_id"`
	Content    string `json:"content"`
	SessionID  string `json:"session_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Source     string `json:"source,omitempty"`
	SchemaID   string `json:"schema_id,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type rememberResponse struct {
	MemoriesCreated     int            `json:"memories_created"`
	MemoriesUpdated     int            `json:"memories_updated"`
	MemoriesDeleted     int            `json:"memories_deleted"`
	Skipped             int            `json:"skipped"`
	CustomExtractionID  string         `json:"custom_extraction_id,omitempty"`
	CustomExtractedData map[string]any `json:"custom_extracted_data,omitempty"`
}

type searchRequest struct {
	EntityID  string  `json:"entity_id"`
	Query     string  `json:"query"`
	Limit     int     `json:"limit,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	AgentID   string  `json:"agent_id,omitempty"`
	TeamAware bool    `json:"team_aware,omitempty"`
	MinScore  float64 `json:"min_score,omitempty"`
}

type searchResultItem struct {
	Memory     memoryJSON `json:"memory"`
	Similarity float64    `json:"similarity"`
	Score      float64    `json:"score"`
}

type memoryJSON struct {
	ID             string     `json:"id"`
	EntityID       string     `json:"entity_id"`
	AgentID        string     `json:"agent_id,omitempty"`
	TeamID         string     `json:"team_id,omitempty"`
	Visibility     string     `json:"visibility,omitempty"`
	Content        string     `json:"content"`
	Type           string     `json:"type"`
	State          string     `json:"state"`
	Importance     float64    `json:"importance"`
	Confidence     float64    `json:"confidence"`
	Sentiment      float64    `json:"sentiment"`
	Tags           []string   `json:"tags,omitempty"`
	AccessCount    int        `json:"access_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

type heartbeatCheckRequest struct {
	EntityID         string  `json:"entity_id"`
	DeadlineWindow   string  `json:"deadline_window,omitempty"`
	DecayThreshold   float64 `json:"decay_threshold,omitempty"`
	ImportanceFloor  float64 `json:"importance_floor,omitempty"`
	MaxResults       int     `json:"max_results,omitempty"`
	AgentID          string  `json:"agent_id,omitempty"`
	TeamID           string  `json:"team_id,omitempty"`
	VirtualNow       string  `json:"virtual_now,omitempty"`        // ISO8601 timestamp to override time.Now() for signal computation
	AutoAckScheduled *bool   `json:"auto_ack_scheduled,omitempty"` // nil=default(true), false=defer ack to integration layer
}

type heartbeatCheckResponse struct {
	ShouldAct      bool           `json:"should_act"`
	PendingWork    []memoryJSON   `json:"pending_work"`
	Deadlines      []memoryJSON   `json:"deadlines"`
	Scheduled      []memoryJSON   `json:"scheduled"`
	Decaying       []memoryJSON   `json:"decaying"`
	Conflicts      []conflictJSON `json:"conflicts"`
	StaleMonitors  []memoryJSON   `json:"stale_monitors"`
	Summary        string         `json:"summary"`
	PriorityAction string         `json:"priority_action,omitempty"`
	ActionItems    []string       `json:"action_items,omitempty"`
	Urgency        string         `json:"urgency,omitempty"`
}

type conflictJSON struct {
	Memory memoryJSON `json:"memory"`
	Reason string     `json:"reason"`
}

// Combined heartbeat + context search in a single call.
type heartbeatContextRequest struct {
	EntityID        string  `json:"entity_id"`
	Query           string  `json:"query,omitempty"`           // Current conversation context for memory search
	TopK            int     `json:"top_k,omitempty"`           // Max relevant memories to return (default: 5)
	MinScore        float64 `json:"min_score,omitempty"`       // Min similarity for context search (default: 0.1)
	DeadlineWindow  string  `json:"deadline_window,omitempty"` // How far ahead to look (default: 24h)
	MaxResults      int     `json:"max_results,omitempty"`     // Max signals per category (default: 10)
	AgentID         string  `json:"agent_id,omitempty"`
	TeamID          string  `json:"team_id,omitempty"`
	Analyze         bool    `json:"analyze,omitempty"`          // Request LLM analysis of context
	ActivitySummary string  `json:"activity_summary,omitempty"` // Current conversation activity for LLM context
	Autonomy        string  `json:"autonomy,omitempty"`         // "observe", "suggest", "act" (default: "suggest")

	// Conversation awareness
	InConversation bool `json:"in_conversation,omitempty"` // Plugin signals user is actively talking

	// Signals-only mode: skip decision pipeline, return fresh signals with ShouldAct=true.
	// Used when watcher already decided to act and delivery path needs signals without re-evaluating.
	SignalsOnly bool `json:"signals_only,omitempty"`

	// Optional parameter overrides (defaults come from autonomy level)
	NudgeAfterSilence    string `json:"nudge_after_silence,omitempty"` // e.g. "4h"
	MaxNudgesPerDay      int    `json:"max_nudges_per_day,omitempty"`
	NudgeMaxInterval     string `json:"nudge_max_interval,omitempty"`     // e.g. "48h" — cap for backoff decay
	SignalCooldownNormal string `json:"signal_cooldown_normal,omitempty"` // e.g. "2h"
	SignalCooldownLow    string `json:"signal_cooldown_low,omitempty"`    // e.g. "4h"

	// Verbosity control
	Verbosity string `json:"verbosity,omitempty"` // "conversational", "standard", "detailed", "debug" (default: "conversational")

	// Virtual time override for demo recording
	VirtualNow string `json:"virtual_now,omitempty"` // ISO8601 timestamp to override time.Now() for signal computation

	// Schedule ack behavior override
	AutoAckScheduled *bool `json:"auto_ack_scheduled,omitempty"` // nil=default(true), false=defer ack to integration layer
}

type heartbeatAnalysisJSON struct {
	ShouldAct          bool     `json:"should_act"`
	ActionBrief        string   `json:"action_brief"`
	RecommendedActions []string `json:"recommended_actions"`
	Urgency            string   `json:"urgency"`
	Reasoning          string   `json:"reasoning"`
	Autonomy           string   `json:"autonomy"`
	UserFacing         string   `json:"user_facing"`
	Evidence           []string `json:"evidence,omitempty"`
	LinkedEntities     []string `json:"linked_entities,omitempty"`
}

type developerTraceJSON struct {
	SignalFingerprint    string            `json:"signal_fingerprint"`
	SignalClassification map[string]string `json:"signal_classification,omitempty"`
	DecisionReason       string            `json:"decision_reason"`
	CooldownState        string            `json:"cooldown_state,omitempty"`
	ConfluenceScore      int               `json:"confluence_score"`
	ConfluenceThreshold  int               `json:"confluence_threshold"`
	ResponseRate         float64           `json:"response_rate"`
	TimePeriod           string            `json:"time_period"`
	EscalationLevel      int               `json:"escalation_level"`
	MemoryVelocity       int               `json:"memory_velocity"`
	LLMLatencyMs         int64             `json:"llm_latency_ms"`
	RawPrompt            string            `json:"raw_prompt,omitempty"`
}

type goalProgressJSON struct {
	Plan       memoryJSON   `json:"plan"`
	Activities []memoryJSON `json:"activities,omitempty"`
	Progress   float64      `json:"progress"`
	DaysLeft   float64      `json:"days_left"`
	Status     string       `json:"status"`
}

type continuityJSON struct {
	LastMemories     []memoryJSON `json:"last_memories,omitempty"`
	SessionAgeHours  float64      `json:"session_age_hours"`
	WasInterrupted   bool         `json:"was_interrupted"`
	ResumeSuggestion string       `json:"resume_suggestion"`
}

type sentimentTrendJSON struct {
	RecentAvg   float64      `json:"recent_avg"`
	PreviousAvg float64      `json:"previous_avg"`
	Direction   string       `json:"direction"`
	Delta       float64      `json:"delta"`
	Notable     []memoryJSON `json:"notable,omitempty"`
}

type relationshipAlertJSON struct {
	EntityName   string       `json:"entity_name"`
	DaysSilent   int          `json:"days_silent"`
	RelatedPlans []memoryJSON `json:"related_plans,omitempty"`
	Urgency      string       `json:"urgency"`
}

type knowledgeGapJSON struct {
	Question string `json:"question"`
	AskedAt  string `json:"asked_at"`
}

type behavioralPatternJSON struct {
	Description string   `json:"description"`
	Confidence  float64  `json:"confidence"`
	DayOfWeek   *int     `json:"day_of_week,omitempty"`
	Topics      []string `json:"topics,omitempty"`
}

type positiveDeltaJSON struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	EntityID    string `json:"entity_id,omitempty"`
}

type heartbeatContextResponse struct {
	ShouldAct        bool                   `json:"should_act"`
	Scheduled        []memoryJSON           `json:"scheduled"`
	Deadlines        []memoryJSON           `json:"deadlines"`
	PendingWork      []memoryJSON           `json:"pending_work"`
	Conflicts        []conflictJSON         `json:"conflicts"`
	RelevantMemories []searchResultItem     `json:"relevant_memories"`
	Summary          string                 `json:"summary"`
	Analysis         *heartbeatAnalysisJSON `json:"analysis,omitempty"`

	// Decision metadata
	DecisionReason     string `json:"decision_reason,omitempty"`      // "act", "nudge", "suppress_cooldown", "suppress_stale", "suppress_quiet", "no_signals"
	HighestUrgencyTier string `json:"highest_urgency_tier,omitempty"` // "immediate", "elevated", "normal", "low"
	NudgeContext       string `json:"nudge_context,omitempty"`        // memory content for nudge

	// Extended signals
	GoalProgress       []goalProgressJSON      `json:"goal_progress,omitempty"`
	Continuity         *continuityJSON         `json:"continuity,omitempty"`
	SentimentTrend     *sentimentTrendJSON     `json:"sentiment_trend,omitempty"`
	RelationshipAlerts []relationshipAlertJSON `json:"relationship_alerts,omitempty"`
	KnowledgeGaps      []knowledgeGapJSON      `json:"knowledge_gaps,omitempty"`
	BehavioralPatterns []behavioralPatternJSON `json:"behavioral_patterns,omitempty"`

	// Conversation state
	InConversation bool `json:"in_conversation,omitempty"`

	// Time and escalation awareness
	TimePeriod      string `json:"time_period,omitempty"`      // "morning", "working", "evening", "late_night", "quiet"
	EscalationLevel int    `json:"escalation_level,omitempty"` // 1=casual, 2=direct, 3=offer help, 4+=dropped

	// v2: Intelligence metadata
	ResponseRate    float64             `json:"response_rate,omitempty"`
	ConfluenceScore int                 `json:"confluence_score,omitempty"`
	PositiveDeltas  []positiveDeltaJSON `json:"positive_deltas,omitempty"`
	GraphContext    []string            `json:"graph_context,omitempty"`
	RecentMessages  []string            `json:"recent_messages,omitempty"` // last N heartbeat messages (for dedup)

	// v3: Memory velocity
	MemoryVelocity     int  `json:"memory_velocity,omitempty"`
	MemoryVelocityHigh bool `json:"memory_velocity_high,omitempty"`

	// v5: Developer trace (debug/detailed verbosity only)
	DeveloperTrace *developerTraceJSON `json:"developer_trace,omitempty"`
}

type watcherStartRequest struct {
	IntervalMs int      `json:"interval_ms,omitempty"`
	EntityIDs  []string `json:"entity_ids"`

	// Adaptive heartbeat
	Adaptive       bool `json:"adaptive,omitempty"`
	BaseIntervalMs int  `json:"base_interval_ms,omitempty"`
	MinIntervalMs  int  `json:"min_interval_ms,omitempty"`
	MaxIntervalMs  int  `json:"max_interval_ms,omitempty"`

	// Delivery config
	Delivery *deliveryRequest `json:"delivery,omitempty"`
}

type deliveryRequest struct {
	Method    string `json:"method"`     // "cli" or ""
	Command   string `json:"command"`    // e.g. "openclaw" or "docker exec kumo openclaw"
	Channel   string `json:"channel"`    // e.g. "telegram"
	Recipient string `json:"recipient"`  // e.g. "-4970078838"
	SessionID string `json:"session_id"` // e.g. "telegram:group:-4970078838"
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

type statsResponse struct {
	TotalMemories  int            `json:"total_memories"`
	ActiveMemories int            `json:"active_memories"`
	ByType         map[string]int `json:"by_type,omitempty"`
	ByState        map[string]int `json:"by_state,omitempty"`
}

// --- Helpers ---

func toMemoryJSON(m *keyoku.Memory) memoryJSON {
	return memoryJSON{
		ID:             m.ID,
		EntityID:       m.EntityID,
		AgentID:        m.AgentID,
		TeamID:         m.TeamID,
		Visibility:     string(m.Visibility),
		Content:        m.Content,
		Type:           string(m.Type),
		State:          string(m.State),
		Importance:     m.Importance,
		Confidence:     m.Confidence,
		Sentiment:      m.Sentiment,
		Tags:           m.Tags,
		AccessCount:    m.AccessCount,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		LastAccessedAt: m.LastAccessedAt,
		ExpiresAt:      m.ExpiresAt,
	}
}

func toMemoryJSONSlice(memories []*keyoku.Memory) []memoryJSON {
	result := make([]memoryJSON, 0, len(memories))
	for _, m := range memories {
		result = append(result, toMemoryJSON(m))
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type apiErrorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiErrorResponse{Error: msg})
}

func writeErrorCode(w http.ResponseWriter, status int, msg, code string, retryable bool) {
	writeJSON(w, status, apiErrorResponse{Error: msg, Code: code, Retryable: retryable})
}

// writeInternalError logs the real error and returns a generic message to the client.
func writeInternalError(w http.ResponseWriter, err error) {
	writeInternalErrorWithContext(w, "", err)
}

func writeInternalErrorWithContext(w http.ResponseWriter, context string, err error) {
	if context == "" {
		log.Printf("ERROR: %v", err)
	} else {
		log.Printf("ERROR [%s]: %v", context, err)
	}

	errStr := err.Error()
	if strings.Contains(errStr, "HNSW") {
		writeErrorCode(w, http.StatusServiceUnavailable, "vector index unavailable", "vector_index_unavailable", true)
		return
	}

	writeError(w, http.StatusInternalServerError, "internal server error")
}

func decodeBody(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodySize)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func extractPathID(r *http.Request, prefix string) (string, bool) {
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if id == "" || !validID.MatchString(id) {
		return "", false
	}
	return id, true
}

func clampLimit(v string, defaultVal int) int {
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}
