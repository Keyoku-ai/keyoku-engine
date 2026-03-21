// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// AddRequest represents a request to add memories.
type AddRequest struct {
	Content    string
	SessionID  string
	AgentID    string
	Source     string
	SchemaID   string                   // Optional: custom extraction schema ID
	TeamID     string                   // Team this memory belongs to
	Visibility storage.MemoryVisibility // Visibility level (defaults based on team membership)
	CreatedAt  time.Time                // Optional: override created_at timestamp (for simulation/migration)
}

// AddResult represents the result of adding memories.
type AddResult struct {
	MemoriesCreated     int
	MemoriesUpdated     int
	MemoriesDeleted     int
	MemoriesResolved    int
	Skipped             int
	Details             []MemoryDetail
	CustomExtractionID  string         // ID of custom extraction if schema was used
	CustomExtractedData map[string]any // The extracted custom data
}

// MemoryDetail contains information about a processed memory.
type MemoryDetail struct {
	ID         string
	Content    string
	Type       storage.MemoryType
	Importance float64
	Confidence float64
	Action     string // "created", "updated", "deleted", "resolved", "skipped"
	Reason     string
}

// Add extracts and stores memories from content.
func (e *Engine) Add(ctx context.Context, entityID string, req AddRequest) (*AddResult, error) {
	// Step 0: Significance filter — skip trivial content before any API calls
	if e.significanceScorer != nil {
		sigResult := e.significanceScorer.Score(req.Content)
		if sigResult.Skip {
			return &AddResult{
				Skipped: 1,
				Details: []MemoryDetail{{
					Content: req.Content,
					Action:  "skipped",
					Reason:  sigResult.Reason,
				}},
			}, nil
		}
	}

	// Step 0b: Token budget check — fall back to local-only if budget exceeded
	estimatedTokens := 600 // approximate tokens for extraction call
	if !e.tokenBudget.CanSpend(entityID, estimatedTokens) {
		e.tokenBudget.RecordExceeded(entityID)
		return &AddResult{
			Skipped: 1,
			Details: []MemoryDetail{{
				Content: req.Content,
				Action:  "skipped",
				Reason:  "token budget exceeded",
			}},
		}, nil
	}

	// Get conversation context
	contextMsgs, err := e.store.GetRecentSessionMessages(ctx, entityID, e.config.ContextTurns)
	if err != nil {
		return nil, fmt.Errorf("failed to get session context: %w", err)
	}

	var conversationCtx []string
	for _, msg := range contextMsgs {
		conversationCtx = append(conversationCtx, fmt.Sprintf("[%s]: %s", msg.Role, msg.Content))
	}

	// Embed content to find similar existing memories
	queryEmbedding, err := e.embedder.Embed(ctx, req.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Build visibility context for similarity search so dedup/conflict only considers visible memories
	var visibilityFor *storage.VisibilityContext
	if req.TeamID != "" || req.AgentID != "" {
		agentID := req.AgentID
		if agentID == "" {
			agentID = "default"
		}
		visibilityFor = &storage.VisibilityContext{AgentID: agentID, TeamID: req.TeamID}
	}

	similarMemories, err := e.store.FindSimilarWithOptions(ctx, queryEmbedding, entityID, 10, 0.5, storage.SimilarityOptions{
		AgentID:       req.AgentID,
		VisibilityFor: visibilityFor,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find similar memories: %w", err)
	}

	var existingMemories []string
	for _, sm := range similarMemories {
		existingMemories = append(existingMemories, sm.Memory.Content)
	}

	// Extract memories using LLM
	extractResp, err := e.provider.ExtractMemories(ctx, llm.ExtractionRequest{
		Content:          req.Content,
		ConversationCtx:  conversationCtx,
		ExistingMemories: existingMemories,
	})
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	// Record token usage (estimate based on content length)
	tokenEstimate := len(req.Content)/4 + 400 // rough: input/4 + base output
	e.tokenBudget.Record(entityID, tokenEstimate)

	result := &AddResult{}

	// Process new memories
	for _, extracted := range extractResp.Memories {
		detail, err := e.processNewMemory(ctx, extracted, req, entityID, extractResp.Entities, extractResp.Relationships)
		if err != nil {
			return nil, fmt.Errorf("failed to process memory: %w", err)
		}
		result.Details = append(result.Details, detail)
		if detail.Action == "created" {
			result.MemoriesCreated++
		}
	}

	// Build similarity options for update/delete fallback searches (same scoping as initial search)
	simOpts := storage.SimilarityOptions{
		AgentID:       req.AgentID,
		VisibilityFor: visibilityFor,
	}

	// Process updates
	for _, update := range extractResp.Updates {
		detail, err := e.processUpdate(ctx, update, similarMemories, entityID, simOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to process update: %w", err)
		}
		result.Details = append(result.Details, detail)
		if detail.Action == "updated" {
			result.MemoriesUpdated++
		}
	}

	// Process deletes
	for _, del := range extractResp.Deletes {
		detail, err := e.processDelete(ctx, del, similarMemories, entityID, simOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to process delete: %w", err)
		}
		result.Details = append(result.Details, detail)
		if detail.Action == "deleted" {
			result.MemoriesDeleted++
		}
	}

	// Process resolves
	for _, res := range extractResp.Resolves {
		detail, err := e.processResolve(ctx, res, similarMemories, entityID, simOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to process resolve: %w", err)
		}
		result.Details = append(result.Details, detail)
		if detail.Action == "resolved" {
			result.MemoriesResolved++
		}
	}

	// Track skipped content
	for _, skip := range extractResp.Skipped {
		result.Details = append(result.Details, MemoryDetail{
			Content: skip.Text,
			Action:  "skipped",
			Reason:  skip.Reason,
		})
		result.Skipped++
	}

	// Adaptive importance re-evaluation: check if new content changes importance of related memories
	if e.config.EnableImportanceReEval && e.provider != nil && result.MemoriesCreated > 0 {
		e.reEvaluateRelatedImportance(ctx, entityID, req.Content, similarMemories)
	}

	// Run custom schema extraction if schema_id is provided (runs alongside default extraction)
	if req.SchemaID != "" {
		customResult, err := e.runCustomExtraction(ctx, entityID, req, conversationCtx)
		if err != nil {
			// Log warning but don't fail the entire operation
			fmt.Printf("warning: custom schema extraction failed: %v\n", err)
		} else if customResult != nil {
			result.CustomExtractionID = customResult.ID
			result.CustomExtractedData = customResult.ExtractedData
		}
	}

	// Store the conversation turn
	turnNumber := len(contextMsgs) + 1
	e.store.AddSessionMessage(ctx, &storage.SessionMessage{ //nolint:errcheck // fire-and-forget
		EntityID:   entityID,
		SessionID:  req.SessionID,
		Role:       "user",
		Content:    req.Content,
		TurnNumber: turnNumber,
	})

	return result, nil
}

// processNewMemory creates a new memory from an extraction.
func (e *Engine) processNewMemory(ctx context.Context, extracted llm.ExtractedMemory, req AddRequest, entityID string, llmEntities []llm.ExtractedEntity, llmRelationships []llm.ExtractedRelationship) (MemoryDetail, error) {
	hash := hashContent(extracted.Content)

	// Embed for semantic checks
	embedding, err := e.embedder.Embed(ctx, extracted.Content)
	if err != nil {
		return MemoryDetail{}, fmt.Errorf("failed to embed: %w", err)
	}

	// Check for duplicates
	dupResult, err := e.duplicateDetector.CheckDuplicate(ctx, entityID, extracted.Content, embedding, hash)
	if err != nil {
		return MemoryDetail{}, fmt.Errorf("duplicate check failed: %w", err)
	}

	switch dupResult.Action {
	case "skip":
		return MemoryDetail{
			ID:      dupResult.ExistingMemory.ID,
			Content: extracted.Content,
			Action:  "skipped",
			Reason:  dupResult.Reason,
		}, nil

	case "merge":
		merged, err := e.duplicateDetector.MergeMemories(ctx, dupResult.ExistingMemory, extracted.Content, extracted.Importance)
		if err != nil {
			return MemoryDetail{}, fmt.Errorf("merge failed: %w", err)
		}
		e.emit("memory.merged", entityID, req.AgentID, req.TeamID, map[string]any{
			"memory":           merged,
			"original_content": dupResult.ExistingMemory.Content,
			"merged_content":   merged.Content,
		})
		return MemoryDetail{
			ID:         merged.ID,
			Content:    merged.Content,
			Type:       merged.Type,
			Importance: merged.Importance,
			Confidence: merged.Confidence,
			Action:     "updated",
			Reason:     "merged with near-duplicate",
		}, nil
	}

	// Determine memory type
	memType := storage.MemoryType(extracted.Type)
	if !memType.IsValid() {
		memType = storage.TypeContext
	}

	// Check for conflicts
	conflictResult, err := e.conflictDetector.DetectConflicts(ctx, entityID, extracted.Content, embedding, memType)
	if err != nil {
		// Non-fatal
		fmt.Printf("warning: conflict detection failed: %v\n", err)
	}

	if conflictResult != nil && conflictResult.HasConflict && len(conflictResult.Conflicts) > 0 {
		conflict := conflictResult.Conflicts[0]
		e.emit("conflict.detected", entityID, req.AgentID, req.TeamID, map[string]any{
			"new_content":      extracted.Content,
			"existing_content": conflict.ExistingMemory.Content,
			"conflict_type":    string(conflict.ConflictType),
			"resolution":       string(conflict.Resolution),
			"explanation":      conflict.Explanation,
			"confidence":       conflict.Confidence,
		})

		switch conflict.Resolution {
		case ResolutionUseNew:
			if conflict.ExistingMemory != nil {
				archivedState := storage.StateArchived
				e.store.UpdateMemory(ctx, conflict.ExistingMemory.ID, storage.MemoryUpdate{ //nolint:errcheck // fire-and-forget
					State: &archivedState,
				})
				e.store.LogHistory(ctx, &storage.HistoryEntry{ //nolint:errcheck // fire-and-forget
					MemoryID:  conflict.ExistingMemory.ID,
					Operation: "superseded",
					Changes: map[string]any{
						"new_content": extracted.Content,
						"conflict":    string(conflict.ConflictType),
					},
					Reason: conflict.Explanation,
				})
			}

		case ResolutionKeepExisting:
			return MemoryDetail{
				ID:      conflict.ExistingMemory.ID,
				Content: extracted.Content,
				Action:  "skipped",
				Reason:  "conflicts with existing memory: " + conflict.Explanation,
			}, nil

		case ResolutionMerge:
			if conflict.ResolvedContent != "" {
				extracted.Content = conflict.ResolvedContent
				hash = hashContent(extracted.Content)
				embedding, _ = e.embedder.Embed(ctx, extracted.Content)
			}

		case ResolutionAskUser:
			extracted.ConfidenceFactors = append(extracted.ConfidenceFactors, "conflict_flagged: "+conflict.Explanation)
		}
	}

	// Track provenance from conflict resolution
	var derivedFrom storage.StringSlice
	if conflictResult != nil && conflictResult.HasConflict && len(conflictResult.Conflicts) > 0 {
		conflict := conflictResult.Conflicts[0]
		if conflict.ExistingMemory != nil && (conflict.Resolution == ResolutionUseNew || conflict.Resolution == ResolutionMerge) {
			derivedFrom = append(derivedFrom, conflict.ExistingMemory.ID)
		}
	}

	stability := memType.StabilityDays()
	now := time.Now()
	createdAt := req.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	agentID := req.AgentID
	if agentID == "" {
		agentID = "default"
	}

	// Resolve team and visibility
	teamID := req.TeamID
	visibility := req.Visibility
	if visibility == "" {
		if teamID != "" {
			visibility = storage.VisibilityTeam // share-by-default for team agents
		} else {
			visibility = storage.VisibilityPrivate
		}
	}

	// Encode embedding as bytes for SQLite backup
	embeddingBytes := encodeEmbedding(embedding)

	mem := &storage.Memory{
		EntityID:           entityID,
		AgentID:            agentID,
		TeamID:             teamID,
		Visibility:         visibility,
		Content:            extracted.Content,
		Hash:               hash,
		Embedding:          embeddingBytes,
		Type:               memType,
		Importance:         extracted.Importance,
		Confidence:         extracted.Confidence,
		Stability:          stability,
		Sentiment:          extracted.Sentiment,
		AccessCount:        0,
		LastAccessedAt:     &now,
		State:              storage.StateActive,
		DerivedFrom:        derivedFrom,
		Source:             req.Source,
		SessionID:          req.SessionID,
		ExtractionProvider: e.provider.Name(),
		ExtractionModel:    e.provider.Model(),
		ImportanceFactors:  extracted.ImportanceFactors,
		ConfidenceFactors:  extracted.ConfidenceFactors,
		Tags:               extracted.Tags,
		CreatedAt:          createdAt,
	}

	if err := e.store.CreateMemory(ctx, mem); err != nil {
		return MemoryDetail{}, err
	}

	e.emit("memory.created", entityID, req.AgentID, req.TeamID, map[string]any{
		"memory":     mem,
		"content":    mem.Content,
		"type":       string(mem.Type),
		"importance": mem.Importance,
		"sentiment":  mem.Sentiment,
	})

	// Log history
	//nolint:errcheck // fire-and-forget logging
	e.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  mem.ID,
		Operation: "create",
		Changes:   map[string]any{"content": extracted.Content},
		Reason:    "extracted from input",
	})

	// Extract and store entities (LLM-extracted preferred)
	e.extractAndStoreEntities(ctx, entityID, mem, llmEntities)

	// Detect and store relationships (LLM-extracted preferred)
	e.detectAndStoreRelationships(ctx, entityID, mem, llmRelationships)

	return MemoryDetail{
		ID:         mem.ID,
		Content:    extracted.Content,
		Type:       memType,
		Importance: extracted.Importance,
		Confidence: extracted.Confidence,
		Action:     "created",
	}, nil
}

// processUpdate handles an LLM-suggested update to an existing memory.
func (e *Engine) processUpdate(ctx context.Context, update llm.MemoryUpdate, similar []*storage.SimilarityResult, entityID string, opts storage.SimilarityOptions) (MemoryDetail, error) {
	targetMemory := e.findTargetMemory(ctx, update.Query, similar, entityID, opts)
	if targetMemory == nil {
		return MemoryDetail{
			Content: update.Query,
			Action:  "skipped",
			Reason:  "no matching memory found for update",
		}, nil
	}

	newContent := update.NewContent
	updatePayload := storage.MemoryUpdate{
		Content: &newContent,
	}
	// Pass tags through to storage if the LLM provided them (e.g., schedule changes)
	if len(update.Tags) > 0 {
		tags := update.Tags
		updatePayload.Tags = &tags
	}
	_, err := e.store.UpdateMemory(ctx, targetMemory.ID, updatePayload)
	if err != nil {
		return MemoryDetail{}, err
	}

	e.emit("memory.updated", entityID, targetMemory.AgentID, targetMemory.TeamID, map[string]any{
		"memory":      targetMemory,
		"old_content": targetMemory.Content,
		"new_content": newContent,
		"reason":      update.Reason,
	})

	//nolint:errcheck // fire-and-forget logging
	e.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  targetMemory.ID,
		Operation: "update",
		Changes: map[string]any{
			"old_content": targetMemory.Content,
			"new_content": newContent,
		},
		Reason: update.Reason,
	})

	return MemoryDetail{
		ID:      targetMemory.ID,
		Content: newContent,
		Type:    targetMemory.Type,
		Action:  "updated",
		Reason:  update.Reason,
	}, nil
}

// processDelete handles an LLM-suggested deletion.
func (e *Engine) processDelete(ctx context.Context, del llm.MemoryDelete, similar []*storage.SimilarityResult, entityID string, opts storage.SimilarityOptions) (MemoryDetail, error) {
	targetMemory := e.findTargetMemory(ctx, del.Query, similar, entityID, opts)
	if targetMemory == nil {
		return MemoryDetail{
			Content: del.Query,
			Action:  "skipped",
			Reason:  "no matching memory found for delete",
		}, nil
	}

	if err := e.store.DeleteMemory(ctx, targetMemory.ID, false); err != nil {
		return MemoryDetail{}, err
	}

	e.emit("memory.deleted", entityID, targetMemory.AgentID, targetMemory.TeamID, map[string]any{
		"memory":  targetMemory,
		"content": targetMemory.Content,
		"reason":  del.Reason,
	})

	//nolint:errcheck // fire-and-forget logging
	e.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  targetMemory.ID,
		Operation: "delete",
		Changes:   map[string]any{"content": targetMemory.Content},
		Reason:    del.Reason,
	})

	return MemoryDetail{
		ID:      targetMemory.ID,
		Content: targetMemory.Content,
		Type:    targetMemory.Type,
		Action:  "deleted",
		Reason:  del.Reason,
	}, nil
}

// processResolve handles an LLM-suggested resolution of an existing memory.
func (e *Engine) processResolve(ctx context.Context, res llm.MemoryResolve, similar []*storage.SimilarityResult, entityID string, opts storage.SimilarityOptions) (MemoryDetail, error) {
	targetMemory := e.findTargetMemory(ctx, res.Query, similar, entityID, opts)
	if targetMemory == nil {
		return MemoryDetail{
			Content: res.Query,
			Action:  "skipped",
			Reason:  "no matching memory found for resolve",
		}, nil
	}

	if err := e.store.ResolveMemory(ctx, targetMemory.ID); err != nil {
		return MemoryDetail{}, err
	}

	e.emit("memory.resolved", entityID, targetMemory.AgentID, targetMemory.TeamID, map[string]any{
		"memory":  targetMemory,
		"content": targetMemory.Content,
		"reason":  res.Reason,
	})

	//nolint:errcheck // fire-and-forget logging
	e.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  targetMemory.ID,
		Operation: "resolve",
		Changes:   map[string]any{"content": targetMemory.Content},
		Reason:    res.Reason,
	})

	return MemoryDetail{
		ID:      targetMemory.ID,
		Content: targetMemory.Content,
		Type:    targetMemory.Type,
		Action:  "resolved",
		Reason:  res.Reason,
	}, nil
}

// runCustomExtraction runs custom schema extraction alongside default extraction.
func (e *Engine) runCustomExtraction(ctx context.Context, entityID string, req AddRequest, conversationCtx []string) (*storage.CustomExtraction, error) {
	schema, err := e.store.GetSchema(ctx, req.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema: %w", err)
	}
	if schema == nil {
		return nil, fmt.Errorf("schema not found: %s", req.SchemaID)
	}
	if !schema.IsActive {
		return nil, fmt.Errorf("schema is not active: %s", req.SchemaID)
	}

	extractResp, err := e.provider.ExtractWithSchema(ctx, llm.CustomExtractionRequest{
		Content:         req.Content,
		Schema:          schema.SchemaDefinition,
		SchemaName:      schema.Name,
		ConversationCtx: conversationCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("custom extraction failed: %w", err)
	}

	extraction := &storage.CustomExtraction{
		EntityID:           entityID,
		SchemaID:           req.SchemaID,
		ExtractedData:      extractResp.ExtractedData,
		ExtractionProvider: e.provider.Name(),
		ExtractionModel:    e.provider.Model(),
		Confidence:         extractResp.Confidence,
	}

	if err := e.store.CreateCustomExtraction(ctx, extraction); err != nil {
		return nil, fmt.Errorf("failed to store custom extraction: %w", err)
	}

	return extraction, nil
}

// reEvaluateRelatedImportance checks if new content should change the importance of related existing memories.
// Only fires for high-importance IDENTITY/PLAN/RELATIONSHIP memories in the ambiguous similarity zone (0.5-0.85).
func (e *Engine) reEvaluateRelatedImportance(ctx context.Context, entityID string, newContent string, similarMemories []*storage.SimilarityResult) {
	reEvalTypes := map[storage.MemoryType]bool{
		storage.TypeIdentity:     true,
		storage.TypePlan:         true,
		storage.TypeRelationship: true,
	}

	for _, sim := range similarMemories {
		// Only ambiguous similarity zone — too similar means it's the same fact, too different means unrelated
		if sim.Similarity < e.config.ReEvalSimilarityMin || sim.Similarity > e.config.ReEvalSimilarityMax {
			continue
		}
		// Only high-value memory types
		if !reEvalTypes[sim.Memory.Type] {
			continue
		}
		// Only memories above importance threshold
		if sim.Memory.Importance <= e.config.ReEvalImportanceMin {
			continue
		}
		// Respect token budget
		if !e.tokenBudget.CanSpend(entityID, 150) {
			break
		}

		// Collect related memory contents for context
		var relatedContents []string
		for _, other := range similarMemories {
			if other.Memory.ID != sim.Memory.ID {
				relatedContents = append(relatedContents, other.Memory.Content)
			}
			if len(relatedContents) >= 3 {
				break
			}
		}

		resp, err := e.provider.ReEvaluateImportance(ctx, llm.ImportanceReEvalRequest{
			NewContent:        newContent,
			ExistingContent:   sim.Memory.Content,
			CurrentImportance: sim.Memory.Importance,
			CurrentType:       string(sim.Memory.Type),
			RelatedMemories:   relatedContents,
		})
		if err != nil {
			continue // LLM failure is non-fatal
		}

		e.tokenBudget.Record(entityID, 150)

		if !resp.ShouldUpdate {
			continue
		}

		// Clamp importance to valid range
		newImportance := resp.NewImportance
		if newImportance < 0 {
			newImportance = 0
		}
		if newImportance > 1 {
			newImportance = 1
		}

		_, err = e.store.UpdateMemory(ctx, sim.Memory.ID, storage.MemoryUpdate{
			Importance: &newImportance,
		})
		if err != nil {
			continue
		}

		e.emit("importance.changed", entityID, sim.Memory.AgentID, sim.Memory.TeamID, map[string]any{
			"memory":         sim.Memory,
			"old_importance": sim.Memory.Importance,
			"new_importance": newImportance,
			"reason":         resp.Reason,
			"trigger":        newContent,
		})

		//nolint:errcheck // fire-and-forget logging
		e.store.LogHistory(ctx, &storage.HistoryEntry{
			MemoryID:  sim.Memory.ID,
			Operation: "importance_reeval",
			Changes: map[string]any{
				"old_importance": sim.Memory.Importance,
				"new_importance": newImportance,
				"trigger":        newContent,
			},
			Reason: resp.Reason,
		})
	}
}
