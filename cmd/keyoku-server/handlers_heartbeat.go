// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"fmt"
	"net/http"
	"time"

	keyoku "github.com/keyoku-ai/keyoku-engine"
	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// HandleHeartbeatCheck performs a zero-token heartbeat check.
func (h *Handlers) HandleHeartbeatCheck(w http.ResponseWriter, r *http.Request) {
	var req heartbeatCheckRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	if err := validateOptionalID(req.TeamID, "team_id"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	if req.VirtualNow != "" {
		if t, err := time.Parse(time.RFC3339, req.VirtualNow); err == nil {
			opts = append(opts, keyoku.WithVirtualNow(t))
		}
	}

	result, err := h.k.HeartbeatCheck(r.Context(), req.EntityID, opts...)
	if err != nil {
		writeInternalError(w, err)
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
	if err := validateEntityID(req.EntityID); err != nil {
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

	// 1. Heartbeat check — all signal types including extended checks
	hbOpts := []keyoku.HeartbeatOption{
		keyoku.WithMaxResults(req.MaxResults),
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

	// Pass autonomy level for intelligent ShouldAct evaluation
	autonomy := req.Autonomy
	if autonomy == "" {
		autonomy = "suggest"
	}
	hbOpts = append(hbOpts, keyoku.WithAutonomy(autonomy))

	if req.InConversation {
		hbOpts = append(hbOpts, keyoku.WithInConversation(true))
	}
	if req.SignalsOnly {
		hbOpts = append(hbOpts, keyoku.WithSignalsOnly(true))
	}
	if req.Verbosity != "" {
		hbOpts = append(hbOpts, keyoku.WithVerbosity(req.Verbosity))
	}
	if req.Analyze {
		if provider := h.k.Provider(); provider != nil {
			hbOpts = append(hbOpts, keyoku.WithLLMPrioritization(provider, "", ""))
		}
	}
	if req.VirtualNow != "" {
		if t, err := time.Parse(time.RFC3339, req.VirtualNow); err == nil {
			hbOpts = append(hbOpts, keyoku.WithVirtualNow(t))
		}
	}

	// Build optional parameter overrides
	var params keyoku.HeartbeatParams
	hasOverrides := false
	if req.SignalCooldownNormal != "" {
		if d, err := time.ParseDuration(req.SignalCooldownNormal); err == nil {
			params.SignalCooldownNormal = d
			hasOverrides = true
		}
	}
	if req.SignalCooldownLow != "" {
		if d, err := time.ParseDuration(req.SignalCooldownLow); err == nil {
			params.SignalCooldownLow = d
			hasOverrides = true
		}
	}
	if req.NudgeAfterSilence != "" {
		if d, err := time.ParseDuration(req.NudgeAfterSilence); err == nil {
			params.NudgeAfterSilence = d
			hasOverrides = true
		}
	}
	if req.MaxNudgesPerDay > 0 {
		params.MaxNudgesPerDay = req.MaxNudgesPerDay
		hasOverrides = true
	}
	if req.NudgeMaxInterval != "" {
		if d, err := time.ParseDuration(req.NudgeMaxInterval); err == nil {
			params.NudgeMaxInterval = d
			hasOverrides = true
		}
	}
	if hasOverrides {
		hbOpts = append(hbOpts, keyoku.WithHeartbeatParams(&params))
	}

	hbResult, err := h.k.HeartbeatCheck(r.Context(), req.EntityID, hbOpts...)
	if err != nil {
		writeInternalError(w, fmt.Errorf("heartbeat check failed: %w", err))
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

	resp := heartbeatContextResponse{
		ShouldAct:          hbResult.ShouldAct,
		DecisionReason:     hbResult.DecisionReason,
		HighestUrgencyTier: hbResult.HighestUrgencyTier,
		NudgeContext:       hbResult.NudgeContext,
		Scheduled:        toMemoryJSONSlice(hbResult.Scheduled),
		Deadlines:        toMemoryJSONSlice(hbResult.Deadlines),
		PendingWork:      toMemoryJSONSlice(hbResult.PendingWork),
		Conflicts:        conflicts,
		RelevantMemories: relevantMemories,
		Summary:          hbResult.Summary,
	}

	// Populate extended signals
	for _, g := range hbResult.GoalProgress {
		resp.GoalProgress = append(resp.GoalProgress, goalProgressJSON{
			Plan:       toMemoryJSON(g.Plan),
			Activities: toMemoryJSONSlice(g.Activities),
			Progress:   g.Progress,
			DaysLeft:   g.DaysLeft,
			Status:     g.Status,
		})
	}
	if hbResult.Continuity != nil {
		resp.Continuity = &continuityJSON{
			LastMemories:     toMemoryJSONSlice(hbResult.Continuity.LastSessionMemories),
			SessionAgeHours:  hbResult.Continuity.SessionAge.Hours(),
			WasInterrupted:   hbResult.Continuity.WasInterrupted,
			ResumeSuggestion: hbResult.Continuity.ResumeSuggestion,
		}
	}
	if hbResult.Sentiment != nil {
		resp.SentimentTrend = &sentimentTrendJSON{
			RecentAvg:   hbResult.Sentiment.RecentAvg,
			PreviousAvg: hbResult.Sentiment.PreviousAvg,
			Direction:   hbResult.Sentiment.Direction,
			Delta:       hbResult.Sentiment.Delta,
			Notable:     toMemoryJSONSlice(hbResult.Sentiment.Notable),
		}
	}
	for _, ra := range hbResult.Relationships {
		resp.RelationshipAlerts = append(resp.RelationshipAlerts, relationshipAlertJSON{
			EntityName:   ra.Entity.CanonicalName,
			DaysSilent:   ra.DaysSilent,
			RelatedPlans: toMemoryJSONSlice(ra.RelatedPlans),
			Urgency:      ra.Urgency,
		})
	}
	for _, kg := range hbResult.KnowledgeGaps {
		resp.KnowledgeGaps = append(resp.KnowledgeGaps, knowledgeGapJSON{
			Question: kg.Question,
			AskedAt:  kg.AskedAt.Format(time.RFC3339),
		})
	}
	for _, bp := range hbResult.Patterns {
		resp.BehavioralPatterns = append(resp.BehavioralPatterns, behavioralPatternJSON{
			Description: bp.Description,
			Confidence:  bp.Confidence,
			DayOfWeek:   bp.DayOfWeek,
			Topics:      bp.Topics,
		})
	}

	// v2: Populate intelligence metadata
	resp.InConversation = hbResult.InConversation
	resp.TimePeriod = hbResult.TimePeriod
	resp.EscalationLevel = hbResult.EscalationLevel
	resp.ResponseRate = hbResult.ResponseRate
	resp.ConfluenceScore = hbResult.ConfluenceScore
	resp.GraphContext = hbResult.GraphContext
	resp.MemoryVelocity = hbResult.MemoryVelocity
	resp.MemoryVelocityHigh = hbResult.MemoryVelocityHigh

	// Populate recent heartbeat messages for dedup
	agentIDForMsgs := req.AgentID
	if agentIDForMsgs == "" {
		agentIDForMsgs = "default"
	}
	recentMsgs, msgErr := h.k.Store().GetRecentHeartbeatMessages(r.Context(), req.EntityID, agentIDForMsgs, 5)
	if msgErr == nil {
		for _, m := range recentMsgs {
			resp.RecentMessages = append(resp.RecentMessages, m.Message)
		}
	}
	for _, d := range hbResult.PositiveDeltas {
		resp.PositiveDeltas = append(resp.PositiveDeltas, positiveDeltaJSON{
			Type:        d.Type,
			Description: d.Description,
			EntityID:    d.EntityID,
		})
	}

	// 4. LLM analysis — use the engine-owned analysis path so developer_trace is
	// populated consistently for detailed/debug verbosity.
	if req.Analyze && resp.ShouldAct && hbResult.EnhancedAnalysis != nil {
		analysisResult := hbResult.EnhancedAnalysis
		resp.Analysis = &heartbeatAnalysisJSON{
			ShouldAct:          analysisResult.ShouldAct,
			ActionBrief:        analysisResult.ActionBrief,
			RecommendedActions: analysisResult.RecommendedActions,
			Urgency:            analysisResult.Urgency,
			Reasoning:          analysisResult.Reasoning,
			Autonomy:           analysisResult.Autonomy,
			UserFacing:         analysisResult.UserFacing,
			Evidence:           analysisResult.Evidence,
			LinkedEntities:     analysisResult.LinkedEntities,
		}
		// LLM can only suppress should_act (gate), never promote it
		if resp.ShouldAct && !analysisResult.ShouldAct {
			resp.ShouldAct = false
			resp.DecisionReason = "suppress_llm"
		}

		if analysisResult.DeveloperTrace != nil {
			resp.DeveloperTrace = &developerTraceJSON{
				SignalFingerprint:    analysisResult.DeveloperTrace.SignalFingerprint,
				SignalClassification: analysisResult.DeveloperTrace.SignalClassification,
				DecisionReason:       analysisResult.DeveloperTrace.DecisionReason,
				CooldownState:        analysisResult.DeveloperTrace.CooldownState,
				ConfluenceScore:      analysisResult.DeveloperTrace.ConfluenceScore,
				ConfluenceThreshold:  analysisResult.DeveloperTrace.ConfluenceThreshold,
				ResponseRate:         analysisResult.DeveloperTrace.ResponseRate,
				TimePeriod:           analysisResult.DeveloperTrace.TimePeriod,
				EscalationLevel:      analysisResult.DeveloperTrace.EscalationLevel,
				MemoryVelocity:       analysisResult.DeveloperTrace.MemoryVelocity,
				LLMLatencyMs:         analysisResult.DeveloperTrace.LLMLatencyMs,
				RawPrompt:            analysisResult.DeveloperTrace.RawPrompt,
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleRecordHeartbeatMessage stores the actual message text sent in a heartbeat.
func (h *Handlers) HandleRecordHeartbeatMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string `json:"entity_id"`
		AgentID  string `json:"agent_id"`
		ActionID string `json:"action_id"`
		Message  string `json:"message"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	if err := validateEntityID(req.EntityID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.AgentID == "" {
		req.AgentID = "default"
	}
	msg := &storage.HeartbeatMessage{
		EntityID: req.EntityID,
		AgentID:  req.AgentID,
		ActionID: req.ActionID,
		Message:  req.Message,
	}
	if err := h.k.Store().RecordHeartbeatMessage(r.Context(), msg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record message: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": msg.ID})
}
