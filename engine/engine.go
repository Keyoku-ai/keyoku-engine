// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

// Package engine implements the core memory processing pipeline.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// EngineConfig holds configuration for the engine.
type EngineConfig struct {
	ContextTurns            int                // number of recent session messages for context (default: 5)
	TokenBudget             *TokenBudgetConfig // nil = unlimited
	Significance            *SignificanceConfig // nil = use defaults (enabled)
	EnableImportanceReEval  bool               // enable LLM-based importance re-evaluation for related memories
}

// DefaultEngineConfig returns a default engine configuration.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		ContextTurns: 5,
	}
}

// EventEmitter is a callback for emitting events from the engine.
// Parameters: eventType, entityID, agentID, teamID, data.
type EventEmitter func(eventType string, entityID string, agentID string, teamID string, data map[string]any)

// Engine is the main memory processing engine.
type Engine struct {
	provider llm.Provider
	embedder embedder.Embedder
	store    storage.Store
	scorer   *Scorer
	config   EngineConfig

	duplicateDetector    *DuplicateDetector
	conflictDetector     *ConflictDetector
	entityResolver       *EntityResolver
	relationshipDetector *RelationshipDetector
	retriever            *EnhancedRetriever
	graph                *GraphEngine

	tokenBudget         *TokenBudget
	significanceScorer  *SignificanceScorer
	emitter             EventEmitter
}

// NewEngine creates a new memory engine with the given dependencies.
func NewEngine(
	provider llm.Provider,
	emb embedder.Embedder,
	store storage.Store,
	config EngineConfig,
) *Engine {
	if config.ContextTurns <= 0 {
		config.ContextTurns = 5
	}

	graphEngine := NewGraphEngine(store, DefaultGraphConfig())

	// Initialize token budget
	var budget *TokenBudget
	if config.TokenBudget != nil {
		budget = NewTokenBudget(config.TokenBudget)
	} else {
		budget = NewTokenBudget(nil) // unlimited
	}

	// Initialize significance scorer
	var sigConfig SignificanceConfig
	if config.Significance != nil {
		sigConfig = *config.Significance
	} else {
		sigConfig = DefaultSignificanceConfig()
	}

	return &Engine{
		provider:             provider,
		embedder:             emb,
		store:                store,
		scorer:               NewScorer(),
		config:               config,
		duplicateDetector:    NewDuplicateDetector(store, emb, DefaultDuplicateConfig()),
		conflictDetector:     NewConflictDetector(store, provider, DefaultConflictConfig()),
		entityResolver:       NewEntityResolver(store, emb, DefaultEntityConfig()),
		relationshipDetector: NewRelationshipDetector(store, DefaultRelationshipConfig()),
		retriever:            NewEnhancedRetriever(store, emb, graphEngine, DefaultRetrievalConfig()),
		graph:                graphEngine,
		tokenBudget:          budget,
		significanceScorer:   NewSignificanceScorer(sigConfig),
	}
}

// SetEmitter sets the event emitter callback.
func (e *Engine) SetEmitter(emitter EventEmitter) { e.emitter = emitter }

// emit fires an event if an emitter is set.
func (e *Engine) emit(eventType string, entityID string, agentID string, teamID string, data map[string]any) {
	if e.emitter != nil {
		e.emitter(eventType, entityID, agentID, teamID, data)
	}
}

// Provider returns the LLM provider.
func (e *Engine) Provider() llm.Provider { return e.provider }

// Graph returns the graph engine for external use.
func (e *Engine) Graph() *GraphEngine { return e.graph }

// Retriever returns the enhanced retriever for external use.
func (e *Engine) Retriever() *EnhancedRetriever { return e.retriever }

// TokenBudget returns the token budget tracker.
func (e *Engine) TokenBudget() *TokenBudget { return e.tokenBudget }

// AddRequest represents a request to add memories.
type AddRequest struct {
	Content    string
	SessionID  string
	AgentID    string
	Source     string
	SchemaID   string // Optional: custom extraction schema ID
	TeamID     string                   // Team this memory belongs to
	Visibility storage.MemoryVisibility // Visibility level (defaults based on team membership)
}

// AddResult represents the result of adding memories.
type AddResult struct {
	MemoriesCreated     int
	MemoriesUpdated     int
	MemoriesDeleted     int
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
	Action     string // "created", "updated", "deleted", "skipped"
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

	// Process updates
	for _, update := range extractResp.Updates {
		detail, err := e.processUpdate(ctx, update, similarMemories, entityID)
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
		detail, err := e.processDelete(ctx, del, similarMemories, entityID)
		if err != nil {
			return nil, fmt.Errorf("failed to process delete: %w", err)
		}
		result.Details = append(result.Details, detail)
		if detail.Action == "deleted" {
			result.MemoriesDeleted++
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
func (e *Engine) processUpdate(ctx context.Context, update llm.MemoryUpdate, similar []*storage.SimilarityResult, entityID string) (MemoryDetail, error) {
	var targetMemory *storage.Memory
	for _, sm := range similar {
		if containsSubstring(sm.Memory.Content, update.Query) {
			targetMemory = sm.Memory
			break
		}
	}

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
func (e *Engine) processDelete(ctx context.Context, del llm.MemoryDelete, similar []*storage.SimilarityResult, entityID string) (MemoryDetail, error) {
	var targetMemory *storage.Memory
	for _, sm := range similar {
		if containsSubstring(sm.Memory.Content, del.Query) {
			targetMemory = sm.Memory
			break
		}
	}

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

// QueryRequest represents a memory query request.
type QueryRequest struct {
	Query     string
	Limit     int
	Mode      ScorerMode
	AgentID   string
	TeamAware bool    // When true, resolve team and apply visibility filtering
	TeamID    string  // Team ID for visibility resolution
	MinScore  float64 // Minimum similarity threshold (0 = use default 0.3)
}

// QueryResult represents a single query result.
type QueryResult struct {
	Memory *storage.Memory
	Score  ScoringResult
}

// Query retrieves memories relevant to a query.
// Uses hybrid retrieval: HNSW embedding search + FTS keyword fallback,
// then fuses results with reciprocal rank fusion for better recall.
func (e *Engine) Query(ctx context.Context, entityID string, req QueryRequest) ([]*QueryResult, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}

	embedding, err := e.embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	scorer := e.scorer
	if req.Mode != "" {
		scorer = NewScorerWithMode(req.Mode)
	}

	candidateCount := req.Limit * 3
	if candidateCount < 30 {
		candidateCount = 30
	}

	var candidates []*storage.SimilarityResult
	opts := storage.SimilarityOptions{
		AgentID: req.AgentID,
	}
	// When team-aware, build visibility context for filtering
	if req.TeamAware && req.AgentID != "" {
		opts.VisibilityFor = &storage.VisibilityContext{
			AgentID: req.AgentID,
			TeamID:  req.TeamID,
		}
	}
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = 0.3 // default threshold
	}
	if req.AgentID != "" || req.TeamAware {
		candidates, err = e.store.FindSimilarWithOptions(ctx, embedding, entityID, candidateCount, minScore, opts)
	} else {
		candidates, err = e.store.FindSimilar(ctx, embedding, entityID, candidateCount, minScore)
	}
	if err != nil {
		return nil, fmt.Errorf("similarity search failed: %w", err)
	}

	// Build result set from HNSW candidates
	seenIDs := make(map[string]bool, len(candidates))
	results := make([]*QueryResult, 0, len(candidates))
	for _, c := range candidates {
		seenIDs[c.Memory.ID] = true
		score := scorer.Score(ScoringInput{
			Similarity:     c.Similarity,
			CreatedAt:      c.Memory.CreatedAt,
			LastAccessedAt: c.Memory.LastAccessedAt,
			Stability:      c.Memory.Stability,
			Importance:     c.Memory.Importance,
			Confidence:     c.Memory.Confidence,
			AccessCount:    c.Memory.AccessCount,
		})
		results = append(results, &QueryResult{
			Memory: c.Memory,
			Score:  score,
		})
	}

	// Hybrid retrieval: FTS keyword fallback to catch memories that embedding misses.
	// Uses query expansion to generate multiple FTS queries from the original query.
	// E.g., "user's name" → also searches "name", "called", which match memories
	// containing "User's name is Marcus Chen".
	ftsQueries := expandQueryForFTS(req.Query)
	for _, ftsQuery := range ftsQueries {
		ftsResults, ftsErr := e.store.SearchFTSWithOptions(ctx, ftsQuery, entityID, candidateCount, opts)
		if ftsErr != nil || len(ftsResults) == 0 {
			continue
		}
		for _, mem := range ftsResults {
			if seenIDs[mem.ID] {
				continue
			}
			seenIDs[mem.ID] = true
			// FTS results get a baseline similarity of 0.4 (above minScore but below strong HNSW matches)
			score := scorer.Score(ScoringInput{
				Similarity:     0.4,
				CreatedAt:      mem.CreatedAt,
				LastAccessedAt: mem.LastAccessedAt,
				Stability:      mem.Stability,
				Importance:     mem.Importance,
				Confidence:     mem.Confidence,
				AccessCount:    mem.AccessCount,
			})
			results = append(results, &QueryResult{
				Memory: mem,
				Score:  score,
			})
		}
	}

	// Type-aware scoring: boost memories whose type matches the query intent.
	// E.g., "user's name" → boost IDENTITY, "future plans" → boost PLAN.
	queryType := detectQueryType(req.Query)
	if queryType != "" {
		for _, r := range results {
			if string(r.Memory.Type) == queryType {
				r.Score.TotalScore += 0.05 // small boost to prioritize matching types
			}
		}
	}

	sortResultsByScore(results)
	results = enforceDiversity(results, 0.9, e.store)

	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	// Update access stats
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Memory.ID
	}
	//nolint:errcheck // fire-and-forget stats update
	e.store.UpdateAccessStats(ctx, ids)

	// Spaced repetition: increase stability for accessed memories
	for _, r := range results {
		newStability := CalculateNewStabilityWithAccess(r.Memory.Stability, r.Memory.LastAccessedAt, r.Memory.AccessCount)
		if newStability > r.Memory.Stability {
			//nolint:errcheck // fire-and-forget stability update
			e.store.UpdateStability(ctx, r.Memory.ID, newStability)
		}
	}

	return results, nil
}

// GetAll returns all memories for the entity.
func (e *Engine) GetAll(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	if limit <= 0 {
		limit = 100
	}
	return e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{storage.StateActive, storage.StateStale},
		Limit:      limit,
		OrderBy:    "created_at",
		Descending: true,
	})
}

// GetByID retrieves a specific memory by ID.
func (e *Engine) GetByID(ctx context.Context, id string) (*storage.Memory, error) {
	return e.store.GetMemory(ctx, id)
}

// Delete removes a memory by ID.
func (e *Engine) Delete(ctx context.Context, id string) error {
	return e.store.DeleteMemory(ctx, id, false)
}

// DeleteAll removes all memories, entities, and relationships for the entity.
func (e *Engine) DeleteAll(ctx context.Context, entityID string) error {
	// Delete relationships first (FK constraints)
	if _, err := e.store.DeleteAllRelationshipsForOwner(ctx, entityID); err != nil {
		return err
	}
	if _, err := e.store.DeleteAllEntitiesForOwner(ctx, entityID); err != nil {
		return err
	}

	memories, err := e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: entityID,
		Limit:    10000,
	})
	if err != nil {
		return err
	}

	for _, m := range memories {
		if err := e.store.DeleteMemory(ctx, m.ID, true); err != nil {
			return err
		}
	}

	return nil
}

// Stats contains memory statistics.
type Stats struct {
	TotalMemories int
	ByType        map[storage.MemoryType]int
	ByState       map[storage.MemoryState]int
}

// GetStats returns statistics about stored memories.
func (e *Engine) GetStats(ctx context.Context, entityID string) (*Stats, error) {
	memories, err := e.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: entityID,
		Limit:    10000,
		States:   []storage.MemoryState{storage.StateActive, storage.StateStale, storage.StateArchived},
	})
	if err != nil {
		return nil, err
	}

	stats := &Stats{
		TotalMemories: len(memories),
		ByType:        make(map[storage.MemoryType]int),
		ByState:       make(map[storage.MemoryState]int),
	}

	for _, m := range memories {
		stats.ByType[m.Type]++
		stats.ByState[m.State]++
	}

	return stats, nil
}

// GetGlobalStats returns SQL-aggregated stats. Empty entityID = global.
func (e *Engine) GetGlobalStats(ctx context.Context, entityID string) (*storage.AggregatedStats, error) {
	return e.store.AggregateStats(ctx, entityID)
}

// GetSampleMemories returns a representative sample using server-side SQL.
func (e *Engine) GetSampleMemories(ctx context.Context, entityID string, limit int) ([]*storage.Memory, error) {
	return e.store.SampleMemories(ctx, entityID, limit)
}

// Close closes the engine and releases resources.
func (e *Engine) Close() error {
	if closer, ok := e.provider.(interface{ Close() error }); ok {
		_ = closer.Close() //nolint:errcheck
	}
	return nil
}

// extractAndStoreEntities extracts entities from a memory and stores them.
func (e *Engine) extractAndStoreEntities(ctx context.Context, ownerEntityID string, mem *storage.Memory, llmEntities []llm.ExtractedEntity) {
	var extracted []ExtractedEntity
	if len(llmEntities) > 0 {
		for _, le := range llmEntities {
			extracted = append(extracted, ExtractedEntity{
				Name:       le.CanonicalName,
				Type:       mapLLMEntityType(le.Type),
				Context:    le.Context,
				Confidence: 0.9,
			})
		}
	}

	for _, ext := range extracted {
		resolution, err := e.entityResolver.ResolveEntity(ctx, ownerEntityID, ext)
		if err != nil {
			continue
		}

		var entity *storage.Entity

		if resolution.IsNew {
			entity = &storage.Entity{
				OwnerEntityID: ownerEntityID,
				CanonicalName: ext.Name,
				Type:          ext.Type,
				TeamID:        mem.TeamID,
				Aliases:       []string{},
				Attributes:    map[string]any{},
			}

			if e.embedder != nil {
				emb, err := e.embedder.Embed(ctx, ext.Name)
				if err == nil {
					entity.Embedding = encodeEmbedding(emb)
				}
			}

			if err := e.store.CreateEntity(ctx, entity); err != nil {
				continue
			}
		} else {
			entity = resolution.ResolvedEntity
		}

		if entity != nil {
			//nolint:errcheck // fire-and-forget mention tracking
			e.store.CreateEntityMention(ctx, &storage.EntityMention{
				EntityID:       entity.ID,
				MemoryID:       mem.ID,
				MentionText:    ext.Name,
				ContextSnippet: ext.Context,
				Confidence:     ext.Confidence,
			})
		}
	}
}

// detectAndStoreRelationships detects relationships and stores them.
func (e *Engine) detectAndStoreRelationships(ctx context.Context, ownerEntityID string, mem *storage.Memory, llmRelationships []llm.ExtractedRelationship) {
	var detected []DetectedRelationship
	if len(llmRelationships) > 0 {
		for _, lr := range llmRelationships {
			detected = append(detected, DetectedRelationship{
				SourceEntity:     lr.Source,
				TargetEntity:     lr.Target,
				RelationshipType: lr.Relation,
				Confidence:       lr.Confidence,
				Evidence:         mem.Content,
				IsBidirectional:  false,
			})
		}
	}

	for _, rel := range detected {
		sourceEntity, _ := e.store.FindEntityByAlias(ctx, ownerEntityID, rel.SourceEntity)
		targetEntity, _ := e.store.FindEntityByAlias(ctx, ownerEntityID, rel.TargetEntity)

		if sourceEntity == nil || targetEntity == nil {
			continue
		}

		existingRel, _ := e.store.FindRelationship(ctx, ownerEntityID, sourceEntity.ID, targetEntity.ID, rel.RelationshipType)
		if existingRel != nil {
			if rel.Confidence > existingRel.Strength {
				newStrength := (existingRel.Strength + rel.Confidence) / 2
				//nolint:errcheck // fire-and-forget relationship update
				e.store.UpdateRelationship(ctx, existingRel.ID, map[string]any{
					"strength": newStrength,
				})
			}
			//nolint:errcheck // fire-and-forget evidence tracking
			e.store.CreateRelationshipEvidence(ctx, &storage.RelationshipEvidence{
				RelationshipID: existingRel.ID,
				MemoryID:       mem.ID,
				EvidenceText:   rel.Evidence,
				Confidence:     rel.Confidence,
			})
			//nolint:errcheck // fire-and-forget evidence count
			e.store.IncrementRelationshipEvidence(ctx, existingRel.ID)
			continue
		}

		relationship := &storage.Relationship{
			OwnerEntityID:    ownerEntityID,
			SourceEntityID:   sourceEntity.ID,
			TargetEntityID:   targetEntity.ID,
			RelationshipType: rel.RelationshipType,
			TeamID:           mem.TeamID,
			Strength:         rel.Confidence,
			Confidence:       rel.Confidence,
			IsBidirectional:  rel.IsBidirectional,
			Attributes:       map[string]any{},
		}

		if err := e.store.CreateRelationship(ctx, relationship); err != nil {
			continue
		}

		//nolint:errcheck // fire-and-forget evidence tracking
		e.store.CreateRelationshipEvidence(ctx, &storage.RelationshipEvidence{
			RelationshipID: relationship.ID,
			MemoryID:       mem.ID,
			EvidenceText:   rel.Evidence,
			Confidence:     rel.Confidence,
		})
	}
}

// mapLLMEntityType maps LLM entity type strings to storage.EntityType.
func mapLLMEntityType(llmType string) storage.EntityType {
	switch llmType {
	case "PERSON":
		return storage.EntityTypePerson
	case "ORGANIZATION":
		return storage.EntityTypeOrganization
	case "LOCATION":
		return storage.EntityTypeLocation
	case "PRODUCT":
		return storage.EntityTypeProduct
	default:
		return storage.EntityTypeOther
	}
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
		if sim.Similarity < 0.5 || sim.Similarity > 0.85 {
			continue
		}
		// Only high-value memory types
		if !reEvalTypes[sim.Memory.Type] {
			continue
		}
		// Only memories above importance threshold
		if sim.Memory.Importance <= 0.5 {
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

// --- helper functions ---

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func containsSubstring(content, query string) bool {
	return len(query) > 0 && len(content) > 0 &&
		(content == query || (len(content) >= len(query) &&
			(content[:len(query)] == query || content[len(content)-len(query):] == query)))
}

func sortResultsByScore(results []*QueryResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score.TotalScore > results[j-1].Score.TotalScore; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

// enforceDiversity removes results that are too similar to already-kept results.
// In the embedded version, we compare content strings since embeddings are stored as
// byte blobs in storage, not as float32 vectors directly on Memory.
func enforceDiversity(results []*QueryResult, threshold float64, store storage.Store) []*QueryResult {
	if len(results) <= 1 {
		return results
	}

	diverse := make([]*QueryResult, 0, len(results))
	for _, r := range results {
		isDuplicate := false
		for _, kept := range diverse {
			if r.Memory.Content == kept.Memory.Content {
				isDuplicate = true
				break
			}
		}
		if !isDuplicate {
			diverse = append(diverse, r)
		}
	}
	return diverse
}

// encodeEmbedding converts a float32 slice to bytes for SQLite BLOB storage.
func encodeEmbedding(embedding []float32) []byte {
	if len(embedding) == 0 {
		return nil
	}
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// DecodeEmbedding converts bytes from SQLite BLOB to float32 slice.
func DecodeEmbedding(data []byte) []float32 {
	if len(data) == 0 || len(data)%4 != 0 {
		return nil
	}
	result := make([]float32, len(data)/4)
	for i := range result {
		bits := uint32(data[i*4+0]) |
			uint32(data[i*4+1])<<8 |
			uint32(data[i*4+2])<<16 |
			uint32(data[i*4+3])<<24
		result[i] = math.Float32frombits(bits)
	}
	return result
}
