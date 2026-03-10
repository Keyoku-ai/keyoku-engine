// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"net/http"
	"sort"
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
}

type rememberResponse struct {
	MemoriesCreated     int                    `json:"memories_created"`
	MemoriesUpdated     int                    `json:"memories_updated"`
	MemoriesDeleted     int                    `json:"memories_deleted"`
	Skipped             int                    `json:"skipped"`
	CustomExtractionID  string                 `json:"custom_extraction_id,omitempty"`
	CustomExtractedData map[string]any         `json:"custom_extracted_data,omitempty"`
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
	ID                string    `json:"id"`
	EntityID          string    `json:"entity_id"`
	AgentID           string    `json:"agent_id,omitempty"`
	TeamID            string    `json:"team_id,omitempty"`
	Visibility        string    `json:"visibility,omitempty"`
	Content           string    `json:"content"`
	Type              string    `json:"type"`
	State             string    `json:"state"`
	Importance        float64   `json:"importance"`
	Confidence        float64   `json:"confidence"`
	Sentiment         float64   `json:"sentiment"`
	Tags              []string  `json:"tags,omitempty"`
	AccessCount       int       `json:"access_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastAccessedAt    *time.Time `json:"last_accessed_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

type heartbeatCheckRequest struct {
	EntityID        string  `json:"entity_id"`
	DeadlineWindow  string  `json:"deadline_window,omitempty"`
	DecayThreshold  float64 `json:"decay_threshold,omitempty"`
	ImportanceFloor float64 `json:"importance_floor,omitempty"`
	MaxResults      int     `json:"max_results,omitempty"`
	AgentID         string  `json:"agent_id,omitempty"`
	TeamID          string  `json:"team_id,omitempty"`
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
	Query           string  `json:"query,omitempty"`             // Current conversation context for memory search
	TopK            int     `json:"top_k,omitempty"`             // Max relevant memories to return (default: 5)
	MinScore        float64 `json:"min_score,omitempty"`         // Min similarity for context search (default: 0.1)
	DeadlineWindow  string  `json:"deadline_window,omitempty"`   // How far ahead to look (default: 24h)
	MaxResults      int     `json:"max_results,omitempty"`       // Max signals per category (default: 10)
	AgentID         string  `json:"agent_id,omitempty"`
	TeamID          string  `json:"team_id,omitempty"`
	Analyze         bool    `json:"analyze,omitempty"`           // Request LLM analysis of context
	ActivitySummary string  `json:"activity_summary,omitempty"`  // Current conversation activity for LLM context
	Autonomy        string  `json:"autonomy,omitempty"`          // "observe", "suggest", "act" (default: "suggest")
}

type heartbeatAnalysisJSON struct {
	ShouldAct          bool     `json:"should_act"`
	ActionBrief        string   `json:"action_brief"`
	RecommendedActions []string `json:"recommended_actions"`
	Urgency            string   `json:"urgency"`
	Reasoning          string   `json:"reasoning"`
	Autonomy           string   `json:"autonomy"`
	UserFacing         string   `json:"user_facing"`
}

type heartbeatContextResponse struct {
	ShouldAct        bool                    `json:"should_act"`
	Scheduled        []memoryJSON            `json:"scheduled"`
	Deadlines        []memoryJSON            `json:"deadlines"`
	PendingWork      []memoryJSON            `json:"pending_work"`
	Conflicts        []conflictJSON          `json:"conflicts"`
	RelevantMemories []searchResultItem      `json:"relevant_memories"`
	Summary          string                  `json:"summary"`
	Analysis         *heartbeatAnalysisJSON  `json:"analysis,omitempty"`
}

type watcherStartRequest struct {
	IntervalMs int      `json:"interval_ms,omitempty"`
	EntityIDs  []string `json:"entity_ids"`
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

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// --- Handlers ---

// HandleHealth returns server health status.
func (h *Handlers) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
		"sse_clients": h.hub.ClientCount(),
	})
}

// HandleRemember extracts and stores memories from content.
func (h *Handlers) HandleRemember(w http.ResponseWriter, r *http.Request) {
	var req rememberRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EntityID == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "entity_id and content are required")
		return
	}

	var opts []keyoku.RememberOption
	if req.SessionID != "" {
		opts = append(opts, keyoku.WithSessionID(req.SessionID))
	}
	if req.AgentID != "" {
		opts = append(opts, keyoku.WithAgentID(req.AgentID))
	}
	if req.Source != "" {
		opts = append(opts, keyoku.WithSource(req.Source))
	}
	if req.SchemaID != "" {
		opts = append(opts, keyoku.WithSchemaID(req.SchemaID))
	}
	if req.TeamID != "" {
		opts = append(opts, keyoku.WithTeamID(req.TeamID))
	}
	if req.Visibility != "" {
		opts = append(opts, keyoku.WithVisibility(keyoku.MemoryVisibility(req.Visibility)))
	}

	result, err := h.k.Remember(r.Context(), req.EntityID, req.Content, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rememberResponse{
		MemoriesCreated:     result.MemoriesCreated,
		MemoriesUpdated:     result.MemoriesUpdated,
		MemoriesDeleted:     result.MemoriesDeleted,
		Skipped:             result.Skipped,
		CustomExtractionID:  result.CustomExtractionID,
		CustomExtractedData: result.CustomExtractedData,
	})
}

// HandleSearch performs semantic memory search.
func (h *Handlers) HandleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EntityID == "" || req.Query == "" {
		writeError(w, http.StatusBadRequest, "entity_id and query are required")
		return
	}

	var opts []keyoku.SearchOption
	if req.Limit > 0 {
		opts = append(opts, keyoku.WithLimit(req.Limit))
	}
	if req.MinScore > 0 {
		opts = append(opts, keyoku.WithMinScore(req.MinScore))
	}
	if req.TeamAware && req.AgentID != "" {
		opts = append(opts, keyoku.WithTeamAwareness(req.AgentID))
	} else if req.AgentID != "" {
		opts = append(opts, keyoku.WithSearchAgentID(req.AgentID))
	}

	results, err := h.k.Search(r.Context(), req.EntityID, req.Query, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]searchResultItem, 0, len(results))
	for _, r := range results {
		items = append(items, searchResultItem{
			Memory:     toMemoryJSON(r.Memory),
			Similarity: r.Score.SemanticScore,
			Score:      r.Score.TotalScore,
		})
	}

	writeJSON(w, http.StatusOK, items)
}

// HandleConsolidate triggers immediate memory consolidation for a given entity.
// Used for lifecycle-aware consolidation (e.g., after agent completion).
func (h *Handlers) HandleConsolidate(w http.ResponseWriter, r *http.Request) {
	if err := h.k.RunConsolidation(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "consolidation failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleHeartbeatCheck performs a zero-token heartbeat check.
func (h *Handlers) HandleHeartbeatCheck(w http.ResponseWriter, r *http.Request) {
	var req heartbeatCheckRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}

	var opts []keyoku.HeartbeatOption
	if req.DeadlineWindow != "" {
		if d, err := time.ParseDuration(req.DeadlineWindow); err == nil {
			opts = append(opts, keyoku.WithDeadlineWindow(d))
		}
	}
	if req.DecayThreshold > 0 {
		opts = append(opts, keyoku.WithDecayThreshold(req.DecayThreshold))
	}
	if req.ImportanceFloor > 0 {
		opts = append(opts, keyoku.WithImportanceFloor(req.ImportanceFloor))
	}
	if req.MaxResults > 0 {
		opts = append(opts, keyoku.WithMaxResults(req.MaxResults))
	}
	if req.AgentID != "" {
		opts = append(opts, keyoku.WithHeartbeatAgentID(req.AgentID))
	}
	if req.TeamID != "" {
		opts = append(opts, keyoku.WithTeamHeartbeat(req.TeamID))
	}

	result, err := h.k.HeartbeatCheck(r.Context(), req.EntityID, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	conflicts := make([]conflictJSON, 0, len(result.Conflicts))
	for _, c := range result.Conflicts {
		conflicts = append(conflicts, conflictJSON{
			Memory: toMemoryJSON(c.MemoryA),
			Reason: c.Reason,
		})
	}

	writeJSON(w, http.StatusOK, heartbeatCheckResponse{
		ShouldAct:      result.ShouldAct,
		PendingWork:    toMemoryJSONSlice(result.PendingWork),
		Deadlines:      toMemoryJSONSlice(result.Deadlines),
		Scheduled:      toMemoryJSONSlice(result.Scheduled),
		Decaying:       toMemoryJSONSlice(result.Decaying),
		Conflicts:      conflicts,
		StaleMonitors:  toMemoryJSONSlice(result.StaleMonitors),
		Summary:        result.Summary,
		PriorityAction: result.PriorityAction,
		ActionItems:    result.ActionItems,
		Urgency:        result.Urgency,
	})
}

// HandleHeartbeatContext performs a combined heartbeat check + context-relevant memory search.
// Returns heartbeat signals (scheduled, deadlines, pending work, conflicts) plus
// memories relevant to the current conversation — all in one call.
func (h *Handlers) HandleHeartbeatContext(w http.ResponseWriter, r *http.Request) {
	var req heartbeatContextRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}

	// Defaults
	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.1
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 10
	}

	// 1. Heartbeat check — skip decay, focus on actionable signals
	hbOpts := []keyoku.HeartbeatOption{
		keyoku.WithMaxResults(req.MaxResults),
		keyoku.WithChecks(
			keyoku.CheckScheduled,
			keyoku.CheckDeadlines,
			keyoku.CheckPendingWork,
			keyoku.CheckConflicts,
		),
	}
	if req.DeadlineWindow != "" {
		if d, err := time.ParseDuration(req.DeadlineWindow); err == nil {
			hbOpts = append(hbOpts, keyoku.WithDeadlineWindow(d))
		}
	}
	if req.AgentID != "" {
		hbOpts = append(hbOpts, keyoku.WithHeartbeatAgentID(req.AgentID))
	}
	if req.TeamID != "" {
		hbOpts = append(hbOpts, keyoku.WithTeamHeartbeat(req.TeamID))
	}

	hbResult, err := h.k.HeartbeatCheck(r.Context(), req.EntityID, hbOpts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "heartbeat check failed: "+err.Error())
		return
	}

	// 2. Context-relevant memory search (only if query provided)
	var relevantMemories []searchResultItem
	if req.Query != "" {
		searchOpts := []keyoku.SearchOption{
			keyoku.WithLimit(req.TopK),
			keyoku.WithMinScore(req.MinScore),
		}
		if req.AgentID != "" {
			searchOpts = append(searchOpts, keyoku.WithSearchAgentID(req.AgentID))
		}

		results, err := h.k.Search(r.Context(), req.EntityID, req.Query, searchOpts...)
		if err == nil {
			relevantMemories = make([]searchResultItem, 0, len(results))
			for _, sr := range results {
				relevantMemories = append(relevantMemories, searchResultItem{
					Memory:     toMemoryJSON(sr.Memory),
					Similarity: sr.Score.SemanticScore,
					Score:      sr.Score.TotalScore,
				})
			}
		}
		// Search failure is non-fatal — heartbeat data is still returned
	}

	// 3. Build combined response
	conflicts := make([]conflictJSON, 0, len(hbResult.Conflicts))
	for _, c := range hbResult.Conflicts {
		conflicts = append(conflicts, conflictJSON{
			Memory: toMemoryJSON(c.MemoryA),
			Reason: c.Reason,
		})
	}

	shouldAct := hbResult.ShouldAct || len(relevantMemories) > 0

	resp := heartbeatContextResponse{
		ShouldAct:        shouldAct,
		Scheduled:        toMemoryJSONSlice(hbResult.Scheduled),
		Deadlines:        toMemoryJSONSlice(hbResult.Deadlines),
		PendingWork:      toMemoryJSONSlice(hbResult.PendingWork),
		Conflicts:        conflicts,
		RelevantMemories: relevantMemories,
		Summary:          hbResult.Summary,
	}

	// 4. LLM analysis (if requested and provider available)
	if req.Analyze {
		provider := h.k.Provider()
		if provider != nil {
			autonomy := req.Autonomy
			if autonomy == "" {
				autonomy = "suggest"
			}

			// Build string slices from signals for the LLM
			scheduled := make([]string, 0, len(hbResult.Scheduled))
			for _, m := range hbResult.Scheduled {
				scheduled = append(scheduled, m.Content)
			}
			deadlines := make([]string, 0, len(hbResult.Deadlines))
			for _, m := range hbResult.Deadlines {
				deadlines = append(deadlines, m.Content)
			}
			pendingWork := make([]string, 0, len(hbResult.PendingWork))
			for _, m := range hbResult.PendingWork {
				pendingWork = append(pendingWork, m.Content)
			}
			conflictStrs := make([]string, 0, len(hbResult.Conflicts))
			for _, c := range hbResult.Conflicts {
				conflictStrs = append(conflictStrs, c.Reason)
			}
			memoryStrs := make([]string, 0, len(relevantMemories))
			for _, m := range relevantMemories {
				memoryStrs = append(memoryStrs, m.Memory.Content)
			}

			activitySummary := req.ActivitySummary
			if activitySummary == "" {
				activitySummary = req.Query // fall back to query
			}

			analysisResult, err := provider.AnalyzeHeartbeatContext(r.Context(), keyoku.HeartbeatAnalysisRequest{
				ActivitySummary:  activitySummary,
				Scheduled:        scheduled,
				Deadlines:        deadlines,
				PendingWork:      pendingWork,
				Conflicts:        conflictStrs,
				RelevantMemories: memoryStrs,
				Autonomy:         autonomy,
				AgentID:          req.AgentID,
				EntityID:         req.EntityID,
			})
			if err == nil {
				resp.Analysis = &heartbeatAnalysisJSON{
					ShouldAct:          analysisResult.ShouldAct,
					ActionBrief:        analysisResult.ActionBrief,
					RecommendedActions: analysisResult.RecommendedActions,
					Urgency:            analysisResult.Urgency,
					Reasoning:          analysisResult.Reasoning,
					Autonomy:           analysisResult.Autonomy,
					UserFacing:         analysisResult.UserFacing,
				}
				// Override should_act with LLM's determination
				resp.ShouldAct = analysisResult.ShouldAct
			}
			// LLM failure is non-fatal — raw signals still returned
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleWatcherStart starts the proactive watcher.
func (h *Handlers) HandleWatcherStart(w http.ResponseWriter, r *http.Request) {
	var req watcherStartRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.EntityIDs) == 0 {
		writeError(w, http.StatusBadRequest, "entity_ids is required")
		return
	}

	interval := 10 * time.Second
	if req.IntervalMs > 0 {
		interval = time.Duration(req.IntervalMs) * time.Millisecond
	}

	h.k.StartWatcher(keyoku.WatcherConfig{
		Interval:  interval,
		EntityIDs: req.EntityIDs,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "started",
		"interval": interval.String(),
		"entities": req.EntityIDs,
	})
}

// HandleWatcherStop stops the proactive watcher.
func (h *Handlers) HandleWatcherStop(w http.ResponseWriter, r *http.Request) {
	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Stop()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// HandleWatcherWatch adds an entity to the watch list.
func (h *Handlers) HandleWatcherWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}

	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Watch(req.EntityID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "watching", "entity_id": req.EntityID})
}

// HandleWatcherUnwatch removes an entity from the watch list.
func (h *Handlers) HandleWatcherUnwatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}

	watcher := h.k.Watcher()
	if watcher == nil {
		writeError(w, http.StatusNotFound, "no active watcher")
		return
	}
	watcher.Unwatch(req.EntityID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "unwatched", "entity_id": req.EntityID})
}

// HandleGetMemory retrieves a single memory by ID.
func (h *Handlers) HandleGetMemory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/memories/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "memory id is required")
		return
	}

	memory, err := h.k.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if memory == nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSON(memory))
}

// HandleListMemories lists memories for an entity (or all entities if entity_id is omitted).
func (h *Handlers) HandleListMemories(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	if entityID != "" {
		// Single entity listing
		memories, err := h.k.List(r.Context(), entityID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
		return
	}

	// No entity_id — list across all entities
	entities, err := h.k.ListEntities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var allMemories []memoryJSON
	for _, eid := range entities {
		memories, err := h.k.List(r.Context(), eid, limit)
		if err != nil {
			continue
		}
		allMemories = append(allMemories, toMemoryJSONSlice(memories)...)
	}
	// Sort by created_at descending, then cap to limit
	sort.Slice(allMemories, func(i, j int) bool {
		return allMemories[i].CreatedAt.After(allMemories[j].CreatedAt)
	})
	if len(allMemories) > limit {
		allMemories = allMemories[:limit]
	}
	writeJSON(w, http.StatusOK, allMemories)
}

// HandleListEntities returns all known entity IDs.
func (h *Handlers) HandleListEntities(w http.ResponseWriter, r *http.Request) {
	entities, err := h.k.ListEntities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entities)
}

// HandleDeleteMemory deletes a single memory by ID.
func (h *Handlers) HandleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/memories/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "memory id is required")
		return
	}

	if err := h.k.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleDeleteAllMemories deletes all memories for an entity.
func (h *Handlers) HandleDeleteAllMemories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}

	if err := h.k.DeleteAll(r.Context(), req.EntityID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted_all"})
}

// HandleStats returns memory statistics for an entity.
func (h *Handlers) HandleStats(w http.ResponseWriter, r *http.Request) {
	entityID := strings.TrimPrefix(r.URL.Path, "/api/v1/stats/")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required in path")
		return
	}

	stats, err := h.k.Stats(r.Context(), entityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	byType := make(map[string]int, len(stats.ByType))
	for k, v := range stats.ByType {
		byType[string(k)] = v
	}
	byState := make(map[string]int, len(stats.ByState))
	for k, v := range stats.ByState {
		byState[string(k)] = v
	}

	active := 0
	if v, ok := byState["active"]; ok {
		active = v
	}

	writeJSON(w, http.StatusOK, statsResponse{
		TotalMemories:  stats.TotalMemories,
		ActiveMemories: active,
		ByType:         byType,
		ByState:        byState,
	})
}

// HandleGlobalStats returns SQL-aggregated stats. No entity_id = global.
func (h *Handlers) HandleGlobalStats(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id") // optional

	stats, err := h.k.GlobalStats(r.Context(), entityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// HandleSampleMemories returns a representative sample of memories using server-side SQL.
func (h *Handlers) HandleSampleMemories(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id") // optional
	limit := 150
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	memories, err := h.k.SampleMemories(r.Context(), entityID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
}

// --- Schedule Handlers ---

// HandleScheduleAck acknowledges a scheduled memory run, advancing its last_accessed_at.
func (h *Handlers) HandleScheduleAck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MemoryID string `json:"memory_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.MemoryID == "" {
		writeError(w, http.StatusBadRequest, "memory_id is required")
		return
	}

	if err := h.k.AcknowledgeSchedule(r.Context(), req.MemoryID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged", "memory_id": req.MemoryID})
}

// HandleListScheduled returns all cron-tagged memories for an entity.
func (h *Handlers) HandleListScheduled(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id query parameter is required")
		return
	}
	agentID := r.URL.Query().Get("agent_id")

	memories, err := h.k.ListScheduled(r.Context(), entityID, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
}

// HandleCreateSchedule creates a new scheduled memory with a cron tag.
func (h *Handlers) HandleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
		AgentID  string `json:"agent_id"`
		Content  string `json:"content"`
		CronTag  string `json:"cron_tag"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EntityID == "" || req.Content == "" || req.CronTag == "" {
		writeError(w, http.StatusBadRequest, "entity_id, content, and cron_tag are required")
		return
	}

	mem, err := h.k.CreateSchedule(r.Context(), req.EntityID, req.AgentID, req.Content, req.CronTag)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toMemoryJSON(mem))
}

// HandleUpdateSchedule modifies an existing scheduled memory's cron tag and/or content.
func (h *Handlers) HandleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/schedule/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "schedule id is required in path")
		return
	}

	var req struct {
		CronTag    string  `json:"cron_tag"`
		NewContent *string `json:"new_content,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CronTag == "" {
		writeError(w, http.StatusBadRequest, "cron_tag is required")
		return
	}

	mem, err := h.k.UpdateSchedule(r.Context(), id, req.CronTag, req.NewContent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSON(mem))
}

// HandleCancelSchedule archives a scheduled memory, cancelling the schedule.
func (h *Handlers) HandleCancelSchedule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/schedule/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "schedule id is required in path")
		return
	}

	if err := h.k.CancelSchedule(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "memory_id": id})
}

// HandleUpdateTags updates the tags on a memory.
func (h *Handlers) HandleUpdateTags(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/memories/")
	id = strings.TrimSuffix(id, "/tags")
	if id == "" {
		writeError(w, http.StatusBadRequest, "memory id is required")
		return
	}

	var req struct {
		Tags []string `json:"tags"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.k.UpdateTags(r.Context(), id, req.Tags); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "memory_id": id, "tags": req.Tags})
}

// --- Team Handlers ---

type teamJSON struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	DefaultVisibility string    `json:"default_visibility"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type teamMemberJSON struct {
	TeamID   string    `json:"team_id"`
	AgentID  string    `json:"agent_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// HandleCreateTeam creates a new team.
func (h *Handlers) HandleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	team, err := h.k.Teams().Create(r.Context(), req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, teamJSON{
		ID:                team.ID,
		Name:              team.Name,
		Description:       team.Description,
		DefaultVisibility: string(team.DefaultVisibility),
		CreatedAt:         team.CreatedAt,
		UpdatedAt:         team.UpdatedAt,
	})
}

// HandleGetTeam retrieves a team by ID.
func (h *Handlers) HandleGetTeam(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	// Strip any trailing path segments (e.g., /members)
	if idx := strings.Index(id, "/"); idx != -1 {
		id = id[:idx]
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "team id is required")
		return
	}

	team, err := h.k.Teams().Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}

	writeJSON(w, http.StatusOK, teamJSON{
		ID:                team.ID,
		Name:              team.Name,
		Description:       team.Description,
		DefaultVisibility: string(team.DefaultVisibility),
		CreatedAt:         team.CreatedAt,
		UpdatedAt:         team.UpdatedAt,
	})
}

// HandleDeleteTeam deletes a team.
func (h *Handlers) HandleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "team id is required")
		return
	}

	if err := h.k.Teams().Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleAddTeamMember adds an agent to a team.
func (h *Handlers) HandleAddTeamMember(w http.ResponseWriter, r *http.Request) {
	// Extract team ID from path: /api/v1/teams/{id}/members
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "team id is required")
		return
	}
	teamID := parts[0]

	var req struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	if err := h.k.Teams().AddMember(r.Context(), teamID, req.AgentID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "added", "team_id": teamID, "agent_id": req.AgentID})
}

// HandleRemoveTeamMember removes an agent from a team.
func (h *Handlers) HandleRemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	// Extract from path: /api/v1/teams/{id}/members/{agent_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] == "" || parts[2] == "" {
		writeError(w, http.StatusBadRequest, "team id and agent_id are required in path")
		return
	}
	teamID := parts[0]
	agentID := parts[2]

	if err := h.k.Teams().RemoveMember(r.Context(), teamID, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// HandleListTeamMembers lists all members of a team.
func (h *Handlers) HandleListTeamMembers(w http.ResponseWriter, r *http.Request) {
	// Extract from path: /api/v1/teams/{id}/members
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "team id is required")
		return
	}
	teamID := parts[0]

	members, err := h.k.Teams().Members(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]teamMemberJSON, 0, len(members))
	for _, m := range members {
		result = append(result, teamMemberJSON{
			TeamID:   m.TeamID,
			AgentID:  m.AgentID,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		})
	}

	writeJSON(w, http.StatusOK, result)
}
