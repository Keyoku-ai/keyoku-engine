package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-embedded"
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
	EntityID  string `json:"entity_id"`
	Content   string `json:"content"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Source    string `json:"source,omitempty"`
	SchemaID string `json:"schema_id,omitempty"`
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
	EntityID string `json:"entity_id"`
	Query    string `json:"query"`
	Limit    int    `json:"limit,omitempty"`
	Mode     string `json:"mode,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
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
	if req.AgentID != "" {
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

// HandleListMemories lists memories for an entity.
func (h *Handlers) HandleListMemories(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id query parameter is required")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	memories, err := h.k.List(r.Context(), entityID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
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
