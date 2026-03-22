// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"net/http"
	"strings"
	"time"
)

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
		writeInternalError(w, err)
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
	if err := validateEntityID(id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid team id")
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
	id, ok := extractPathID(r, "/api/v1/teams/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid team id")
		return
	}

	if err := h.k.Teams().Delete(r.Context(), id); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleAddTeamMember adds an agent to a team.
func (h *Handlers) HandleAddTeamMember(w http.ResponseWriter, r *http.Request) {
	// Extract team ID from path: /api/v1/teams/{id}/members
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || validateEntityID(parts[0]) != nil {
		writeError(w, http.StatusBadRequest, "invalid team id")
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
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "added", "team_id": teamID, "agent_id": req.AgentID})
}

// HandleRemoveTeamMember removes an agent from a team.
func (h *Handlers) HandleRemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	// Extract from path: /api/v1/teams/{id}/members/{agent_id}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || validateEntityID(parts[0]) != nil || validateAgentID(parts[2]) != nil {
		writeError(w, http.StatusBadRequest, "invalid team id or agent_id")
		return
	}
	teamID := parts[0]
	agentID := parts[2]

	if err := h.k.Teams().RemoveMember(r.Context(), teamID, agentID); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// HandleListTeamMembers lists all members of a team.
func (h *Handlers) HandleListTeamMembers(w http.ResponseWriter, r *http.Request) {
	// Extract from path: /api/v1/teams/{id}/members
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/teams/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || validateEntityID(parts[0]) != nil {
		writeError(w, http.StatusBadRequest, "invalid team id")
		return
	}
	teamID := parts[0]

	members, err := h.k.Teams().Members(r.Context(), teamID)
	if err != nil {
		writeInternalError(w, err)
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

// HandleStats returns memory statistics for an entity.
func (h *Handlers) HandleStats(w http.ResponseWriter, r *http.Request) {
	entityID, ok := extractPathID(r, "/api/v1/stats/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid entity_id in path")
		return
	}

	stats, err := h.k.Stats(r.Context(), entityID)
	if err != nil {
		writeInternalError(w, err)
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
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// HandleSampleMemories returns a representative sample of memories using server-side SQL.
func (h *Handlers) HandleSampleMemories(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id") // optional
	limit := clampLimit(r.URL.Query().Get("limit"), 150)

	memories, err := h.k.SampleMemories(r.Context(), entityID, limit)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMemoryJSONSlice(memories))
}

// HandleHealth returns server health status.
func (h *Handlers) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"version":     version,
		"timestamp":   time.Now().Format(time.RFC3339),
		"sse_clients": h.hub.ClientCount(),
	})
}
