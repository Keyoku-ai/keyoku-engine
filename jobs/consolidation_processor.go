// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// ConsolidationProcessor finds groups of similar memories and merges them.
type ConsolidationProcessor struct {
	store  storage.Store
	llm    llm.Provider
	logger *slog.Logger
	config ConsolidationJobConfig
	useLLM bool
}

// ConsolidationJobConfig holds configuration for consolidation processing.
type ConsolidationJobConfig struct {
	SimilarityThreshold         float64
	MinGroupSize                int
	BatchSize                   int
	MaxMergeSize                int
	RespectAgentBoundaries      bool
	RespectVisibilityBoundaries bool
	// ProactiveConsolidation enables merging active memories too (not just stale).
	// Uses a higher similarity threshold to only merge truly redundant active memories,
	// strengthening important memories before they have a chance to decay.
	ProactiveConsolidation          bool
	ProactiveSimilarityThreshold    float64 // Higher threshold for active memories (default: 0.92)
}

// DefaultConsolidationJobConfig returns default consolidation configuration.
func DefaultConsolidationJobConfig() ConsolidationJobConfig {
	return ConsolidationJobConfig{
		SimilarityThreshold:          0.85,
		MinGroupSize:                 2,
		BatchSize:                    500,
		MaxMergeSize:                 5,
		RespectAgentBoundaries:       true,
		RespectVisibilityBoundaries:  true,
		ProactiveConsolidation:       true,
		ProactiveSimilarityThreshold: 0.92,
	}
}

// NewConsolidationProcessor creates a new consolidation processor.
func NewConsolidationProcessor(store storage.Store, llmProvider llm.Provider, logger *slog.Logger, config ConsolidationJobConfig) *ConsolidationProcessor {
	if config.SimilarityThreshold <= 0 {
		config.SimilarityThreshold = 0.85
	}
	if config.MinGroupSize <= 0 {
		config.MinGroupSize = 2
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 500
	}
	if config.MaxMergeSize <= 0 {
		config.MaxMergeSize = 5
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ConsolidationProcessor{
		store:  store,
		llm:    llmProvider,
		logger: logger.With("processor", "consolidation"),
		config: config,
		useLLM: llmProvider != nil,
	}
}

func (p *ConsolidationProcessor) Type() JobType { return JobTypeConsolidation }

func (p *ConsolidationProcessor) Process(ctx context.Context) (*JobResult, error) {
	p.logger.Info("starting consolidation processing")

	entities, err := p.store.GetAllEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get entities: %w", err)
	}

	var totalProcessed, totalConsolidated, groupsFound int
	var proactiveConsolidated int

	for _, entityID := range entities {
		// Pass 1: Consolidate stale memories (existing behavior)
		groups, err := p.findSimilarGroups(ctx, entityID, storage.StateStale, p.config.SimilarityThreshold)
		if err != nil {
			p.logger.Error("failed to find similar stale groups", "entity", entityID, "error", err)
			continue
		}

		groupsFound += len(groups)
		for _, group := range groups {
			totalProcessed += len(group)
			consolidated, err := p.consolidateGroup(ctx, group)
			if err != nil {
				p.logger.Error("failed to consolidate group", "entity", entityID, "error", err)
				continue
			}
			if consolidated {
				totalConsolidated++
			}
		}

		// Pass 2: Proactive consolidation of active memories
		// Uses higher similarity threshold — only merge truly redundant active memories.
		// This strengthens important memories and reduces noise before decay kicks in.
		if p.config.ProactiveConsolidation {
			activeGroups, err := p.findSimilarGroups(ctx, entityID, storage.StateActive, p.config.ProactiveSimilarityThreshold)
			if err != nil {
				p.logger.Error("failed to find similar active groups", "entity", entityID, "error", err)
				continue
			}

			groupsFound += len(activeGroups)
			for _, group := range activeGroups {
				totalProcessed += len(group)
				consolidated, err := p.consolidateGroup(ctx, group)
				if err != nil {
					p.logger.Error("failed to consolidate active group", "entity", entityID, "error", err)
					continue
				}
				if consolidated {
					proactiveConsolidated++
					totalConsolidated++
				}
			}
		}
	}

	p.logger.Info("consolidation complete",
		"entities_processed", len(entities),
		"groups_found", groupsFound,
		"memories_processed", totalProcessed,
		"consolidations", totalConsolidated,
		"proactive_consolidations", proactiveConsolidated,
	)

	return &JobResult{
		ItemsProcessed: totalProcessed,
		ItemsAffected:  totalConsolidated,
		Details: map[string]any{
			"entities_processed":       len(entities),
			"groups_found":             groupsFound,
			"proactive_consolidations": proactiveConsolidated,
		},
	}, nil
}

func (p *ConsolidationProcessor) findSimilarGroups(ctx context.Context, entityID string, targetState storage.MemoryState, similarityThreshold float64) ([][]*storage.Memory, error) {
	query := storage.MemoryQuery{
		EntityID:   entityID,
		States:     []storage.MemoryState{targetState},
		Limit:      p.config.BatchSize,
		OrderBy:    "importance",
		Descending: true,
	}

	memories, err := p.store.QueryMemories(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(memories) < p.config.MinGroupSize {
		return nil, nil
	}

	groups := make([][]*storage.Memory, 0)
	processed := make(map[string]bool)

	for _, mem := range memories {
		if processed[mem.ID] || len(mem.Embedding) == 0 {
			continue
		}

		// Never consolidate cron-tagged memories — they represent explicit
		// schedules and must never be merged away or lose their tags.
		if hasCronTag(mem.Tags) {
			continue
		}

		// Decode embedding from blob
		embedding := decodeEmbeddingBlob(mem.Embedding)
		if len(embedding) == 0 {
			continue
		}

		similar, err := p.store.FindSimilar(ctx, embedding, entityID, p.config.MaxMergeSize, similarityThreshold)
		if err != nil {
			continue
		}

		if len(similar) >= p.config.MinGroupSize {
			group := make([]*storage.Memory, 0, len(similar))
			for _, s := range similar {
				if s.Memory.State != targetState || processed[s.Memory.ID] {
					continue
				}

				// Never pull cron-tagged memories into consolidation groups
				if hasCronTag(s.Memory.Tags) {
					continue
				}

				// Enforce agent boundaries: only consolidate memories from the same agent
				if p.config.RespectAgentBoundaries && s.Memory.AgentID != mem.AgentID {
					continue
				}

				// Enforce visibility boundaries: don't cross-contaminate visibility levels
				if p.config.RespectVisibilityBoundaries {
					if mem.Visibility == storage.VisibilityPrivate && s.Memory.AgentID != mem.AgentID {
						continue
					}
					if mem.Visibility == storage.VisibilityTeam && s.Memory.TeamID != mem.TeamID {
						continue
					}
				}

				group = append(group, s.Memory)
				processed[s.Memory.ID] = true
			}
			if len(group) >= p.config.MinGroupSize {
				groups = append(groups, group)
			}
		}
	}

	return groups, nil
}

func (p *ConsolidationProcessor) consolidateGroup(ctx context.Context, group []*storage.Memory) (bool, error) {
	if len(group) < 2 {
		return false, nil
	}

	// Select primary (highest importance)
	primary := group[0]
	for _, mem := range group[1:] {
		if mem.Importance > primary.Importance {
			primary = mem
		}
	}

	var consolidatedContent string
	var maxConfidence float64
	var totalImportance float64

	for _, mem := range group {
		totalImportance += mem.Importance
		if mem.Confidence > maxConfidence {
			maxConfidence = mem.Confidence
		}
	}

	// Try LLM consolidation
	if p.useLLM && p.llm != nil {
		contents := make([]string, len(group))
		importanceScores := make([]float64, len(group))
		sentimentValues := make([]float64, len(group))
		for i, mem := range group {
			contents[i] = mem.Content
			importanceScores[i] = mem.Importance
			sentimentValues[i] = mem.Sentiment
		}

		// Gather entity and relationship context
		entityCtx, relCtx, impFactors := p.gatherConsolidationContext(ctx, group)

		resp, err := p.llm.ConsolidateMemories(ctx, llm.ConsolidationRequest{
			Memories:            contents,
			EntityContext:       entityCtx,
			RelationshipContext: relCtx,
			ImportanceScores:    importanceScores,
			ImportanceFactors:   impFactors,
			SentimentValues:     sentimentValues,
		})
		if err != nil {
			p.logger.Warn("LLM consolidation failed, falling back to text-based", "error", err)
		} else if resp.Content != "" {
			consolidatedContent = resp.Content
			if resp.Confidence > maxConfidence {
				maxConfidence = resp.Confidence
			}
		}
	}

	// Fallback to text-based
	if consolidatedContent == "" {
		consolidatedContent = textBasedConsolidate(primary, group)
	}

	// Boosted importance
	avgImportance := totalImportance / float64(len(group))
	boostedImportance := avgImportance * (1 + 0.1*float64(len(group)-1))
	if boostedImportance > 1.0 {
		boostedImportance = 1.0
	}

	// Inherit most restrictive visibility from the group
	// private > team > global (private is most restrictive)
	mostRestrictive := primary.Visibility
	for _, mem := range group {
		if mem.Visibility == storage.VisibilityPrivate {
			mostRestrictive = storage.VisibilityPrivate
			break
		}
		if mem.Visibility == storage.VisibilityTeam && mostRestrictive == storage.VisibilityGlobal {
			mostRestrictive = storage.VisibilityTeam
		}
	}
	if mostRestrictive != "" && mostRestrictive != primary.Visibility {
		vis := string(mostRestrictive)
		p.store.UpdateMemory(ctx, primary.ID, storage.MemoryUpdate{Visibility: &vis})
	}

	// Build provenance chain: track all merged memory IDs
	mergedIDs := make([]string, 0, len(group))
	for _, mem := range group {
		mergedIDs = append(mergedIDs, mem.ID)
	}

	// Update primary with provenance
	derivedFrom := storage.StringSlice(mergedIDs)
	_, err := p.store.UpdateMemory(ctx, primary.ID, storage.MemoryUpdate{
		Content:     &consolidatedContent,
		Importance:  &boostedImportance,
		Confidence:  &maxConfidence,
		DerivedFrom: &derivedFrom,
	})
	if err != nil {
		return false, err
	}

	// Reactivate primary
	activeState := storage.StateActive
	p.store.UpdateMemory(ctx, primary.ID, storage.MemoryUpdate{State: &activeState})

	// Log history with provenance
	p.store.LogHistory(ctx, &storage.HistoryEntry{
		MemoryID:  primary.ID,
		Operation: "consolidation",
		Changes: map[string]any{
			"merged_from":      mergedIDs,
			"derived_from":     mergedIDs,
			"original_content": primary.Content,
			"new_content":      consolidatedContent,
		},
		Reason: fmt.Sprintf("consolidated %d similar memories", len(group)),
	})

	// Soft-delete the others
	for _, mem := range group {
		if mem.ID != primary.ID {
			p.store.DeleteMemory(ctx, mem.ID, false)
		}
	}

	return true, nil
}

// gatherConsolidationContext fetches entity/relationship context for a group of memories.
func (p *ConsolidationProcessor) gatherConsolidationContext(ctx context.Context, group []*storage.Memory) (entityCtx, relCtx, impFactors []string) {
	entitySet := make(map[string]*storage.Entity)
	factorSet := make(map[string]bool)

	// Collect entities mentioned across all memories in the group
	for _, mem := range group {
		entities, err := p.store.GetMemoryEntities(ctx, mem.ID)
		if err != nil {
			p.logger.Debug("failed to get entities for memory", "memory_id", mem.ID, "error", err)
			continue
		}
		for _, e := range entities {
			entitySet[e.ID] = e
		}
	}

	// Format entity context: "Alice (person)", "Google (organization)"
	for _, e := range entitySet {
		entityCtx = append(entityCtx, fmt.Sprintf("%s (%s)", e.CanonicalName, strings.ToLower(string(e.Type))))
	}

	// Collect relationships between the entities we found
	if len(entitySet) > 0 {
		ownerEntityID := group[0].EntityID
		for _, e := range entitySet {
			rels, err := p.store.GetEntityRelationships(ctx, ownerEntityID, e.ID, "outgoing")
			if err != nil {
				continue
			}
			for _, rel := range rels {
				// Only include if both sides are in our entity set
				if _, ok := entitySet[rel.TargetEntityID]; ok {
					sourceName := entitySet[rel.SourceEntityID].CanonicalName
					targetName := entitySet[rel.TargetEntityID].CanonicalName
					relCtx = append(relCtx, fmt.Sprintf("%s %s %s", sourceName, rel.RelationshipType, targetName))
				}
			}
		}
	}

	// Collect deduplicated importance factors from memory history
	// (stored on extraction, not directly on Memory — use a simple heuristic from type)
	for _, mem := range group {
		typeLabel := string(mem.Type)
		factor := fmt.Sprintf("%s information", strings.ToLower(typeLabel))
		if !factorSet[factor] {
			factorSet[factor] = true
			impFactors = append(impFactors, factor)
		}
	}

	return entityCtx, relCtx, impFactors
}

// --- helpers ---

func textBasedConsolidate(primary *storage.Memory, group []*storage.Memory) string {
	var contentParts []string
	for _, mem := range group {
		if mem.ID == primary.ID {
			contentParts = append(contentParts, mem.Content)
		} else {
			if !isContentRedundant(primary.Content, mem.Content) {
				unique := extractUniqueContent(primary.Content, mem.Content)
				if unique != "" {
					contentParts = append(contentParts, unique)
				}
			}
		}
	}
	return strings.Join(contentParts, " ")
}

func isContentRedundant(content1, content2 string) bool {
	c1 := strings.ToLower(strings.TrimSpace(content1))
	c2 := strings.ToLower(strings.TrimSpace(content2))

	if strings.Contains(c1, c2) || strings.Contains(c2, c1) {
		return true
	}

	words1 := strings.Fields(c1)
	words2 := strings.Fields(c2)
	if len(words2) == 0 {
		return true
	}

	wordSet := make(map[string]bool)
	for _, w := range words1 {
		wordSet[w] = true
	}
	matchCount := 0
	for _, w := range words2 {
		if wordSet[w] {
			matchCount++
		}
	}
	return float64(matchCount)/float64(len(words2)) >= 0.8
}

func extractUniqueContent(content1, content2 string) string {
	c1Words := strings.Fields(strings.ToLower(content1))
	c2Words := strings.Fields(content2)

	wordSet := make(map[string]bool)
	for _, w := range c1Words {
		wordSet[strings.ToLower(w)] = true
	}

	var unique []string
	for _, w := range c2Words {
		if !wordSet[strings.ToLower(w)] {
			unique = append(unique, w)
		}
	}
	if len(unique) < 3 {
		return ""
	}
	return strings.Join(unique, " ")
}

func getMemoryIDs(memories []*storage.Memory) []string {
	ids := make([]string, len(memories))
	for i, m := range memories {
		ids[i] = m.ID
	}
	return ids
}

// decodeEmbeddingBlob converts a byte blob to float32 slice.
func decodeEmbeddingBlob(data []byte) []float32 {
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
