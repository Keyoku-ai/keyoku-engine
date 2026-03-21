// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// HandleRemember extracts and stores memories from content.
func (h *Handlers) HandleRemember(w http.ResponseWriter, r *http.Request) {
	var req rememberRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateMemoryContent(req.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOptionalID(req.AgentID, "agent_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOptionalID(req.TeamID, "team_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	if req.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, req.CreatedAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid created_at: must be RFC3339 format")
			return
		}
		opts = append(opts, keyoku.WithCreatedAt(t))
	}

	result, err := h.k.Remember(r.Context(), req.EntityID, req.Content, opts...)
	if err != nil {
		writeInternalErrorWithContext(w, "remember", err)
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
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOptionalID(req.AgentID, "agent_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeInternalErrorWithContext(w, "search", err)
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

// HandleSeedMemories inserts memories directly without LLM extraction.
// Useful for testing, migration, and bootstrapping.
func (h *Handlers) HandleSeedMemories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Memories []keyoku.SeedMemoryInput `json:"memories"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Memories) == 0 {
		writeError(w, http.StatusBadRequest, "memories array is required")
		return
	}

	ids, err := h.k.SeedMemories(r.Context(), req.Memories)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"created": len(ids),
		"ids":     ids,
	})
}

// HandleGetMemory retrieves a single memory by ID.
func (h *Handlers) HandleGetMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := extractPathID(r, "/api/v1/memories/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	memory, err := h.k.Get(r.Context(), id)
	if err != nil {
		writeInternalError(w, err)
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
	if err := validateOptionalID(entityID, "entity_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := clampLimit(r.URL.Query().Get("limit"), 100)

	if entityID != "" {
		// Single entity listing
		memories, err := h.k.List(r.Context(), entityID, limit)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
		return
	}

	// No entity_id — list across all entities
	entities, err := h.k.ListEntities(r.Context())
	if err != nil {
		writeInternalError(w, err)
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
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entities)
}

// HandleDeleteMemory deletes a single memory by ID.
func (h *Handlers) HandleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := extractPathID(r, "/api/v1/memories/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	if err := h.k.Delete(r.Context(), id); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleDeleteAllMemories deletes all memories for an entity.
func (h *Handlers) HandleDeleteAllMemories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.k.DeleteAll(r.Context(), req.EntityID); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted_all"})
}

// HandleConsolidate triggers immediate memory consolidation for a given entity.
// Used for lifecycle-aware consolidation (e.g., after agent completion).
func (h *Handlers) HandleConsolidate(w http.ResponseWriter, r *http.Request) {
	if err := h.k.RunConsolidation(r.Context()); err != nil {
		writeInternalError(w, fmt.Errorf("consolidation failed: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleUpdateMemory applies a partial update to a memory.
func (h *Handlers) HandleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !validID.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	var req struct {
		Content    *string  `json:"content,omitempty"`
		Importance *float64 `json:"importance,omitempty"`
		State      *string  `json:"state,omitempty"`
		Tags       *[]string `json:"tags,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var updates storage.MemoryUpdate
	if req.Content != nil {
		updates.Content = req.Content
	}
	if req.Importance != nil {
		updates.Importance = req.Importance
	}
	if req.State != nil {
		state := storage.MemoryState(*req.State)
		updates.State = &state
	}
	if req.Tags != nil {
		updates.Tags = req.Tags
	}

	mem, err := h.k.UpdateMemory(r.Context(), id, updates)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSON(mem))
}

// HandleResolveMemory marks a memory as resolved and drops its importance.
func (h *Handlers) HandleResolveMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || !validID.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid memory id")
		return
	}

	mem, err := h.k.ResolveMemory(r.Context(), id)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSON(mem))
}

// HandleUpdateTags updates the tags on a memory.
func (h *Handlers) HandleUpdateTags(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimPrefix(r.URL.Path, "/api/v1/memories/")
	id := strings.TrimSuffix(rawID, "/tags")
	if id == "" || !validID.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid memory id")
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
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "memory_id": id, "tags": req.Tags})
}
