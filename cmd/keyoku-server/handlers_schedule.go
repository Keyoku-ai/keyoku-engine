// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"net/http"
)

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
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged", "memory_id": req.MemoryID})
}

// HandleListScheduled returns all cron-tagged memories for an entity.
func (h *Handlers) HandleListScheduled(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")
	if err := validateEntityID(entityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agentID := r.URL.Query().Get("agent_id")

	memories, err := h.k.ListScheduled(r.Context(), entityID, agentID)
	if err != nil {
		writeInternalError(w, err)
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
	if req.CronTag == "" {
		writeError(w, http.StatusBadRequest, "cron_tag is required")
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

	mem, err := h.k.CreateSchedule(r.Context(), req.EntityID, req.AgentID, req.Content, req.CronTag)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toMemoryJSON(mem))
}

// HandleUpdateSchedule modifies an existing scheduled memory's cron tag and/or content.
func (h *Handlers) HandleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := extractPathID(r, "/api/v1/schedule/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid schedule id")
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

// HandleDiagnoseSchedules returns lifecycle diagnostics for all scheduled memories.
// Answers "why didn't my reminder fire?" with state, due time, and any issues.
func (h *Handlers) HandleDiagnoseSchedules(w http.ResponseWriter, r *http.Request) {
	entityID := r.URL.Query().Get("entity_id")
	if err := validateEntityID(entityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agentID := r.URL.Query().Get("agent_id")

	diagnostics, err := h.k.DiagnoseSchedules(r.Context(), entityID, agentID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, diagnostics)
}

// HandleCancelSchedule archives a scheduled memory, cancelling the schedule.
func (h *Handlers) HandleCancelSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := extractPathID(r, "/api/v1/schedule/")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid schedule id")
		return
	}

	if err := h.k.CancelSchedule(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "memory_id": id})
}
