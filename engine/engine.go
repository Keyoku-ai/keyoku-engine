// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

// Package engine implements the core memory processing pipeline.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

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

	// Query retrieval
	DefaultMinScore       float64 // minimum similarity threshold for HNSW search (default: 0.3)
	EnableFTSFallback     bool    // enable FTS keyword fallback after HNSW search (default: false)
	FTSBaselineSimilarity float64 // baseline similarity score for FTS results (default: 0.4)
	DiversityThreshold    float64 // similarity threshold for diversity enforcement (default: 0.9)

	// Importance re-evaluation thresholds
	ReEvalSimilarityMin float64 // min similarity for re-eval candidates (default: 0.5)
	ReEvalSimilarityMax float64 // max similarity for re-eval candidates (default: 0.85)
	ReEvalImportanceMin float64 // min importance for re-eval candidates (default: 0.5)
}

// DefaultEngineConfig returns a default engine configuration.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		ContextTurns:          5,
		DefaultMinScore:       0.3,
		FTSBaselineSimilarity: 0.4,
		DiversityThreshold:    0.9,
		ReEvalSimilarityMin:   0.5,
		ReEvalSimilarityMax:   0.85,
		ReEvalImportanceMin:   0.5,
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
	if config.DefaultMinScore <= 0 {
		config.DefaultMinScore = 0.3
	}
	if config.FTSBaselineSimilarity <= 0 {
		config.FTSBaselineSimilarity = 0.4
	}
	if config.DiversityThreshold <= 0 {
		config.DiversityThreshold = 0.9
	}
	if config.ReEvalSimilarityMin <= 0 {
		config.ReEvalSimilarityMin = 0.5
	}
	if config.ReEvalSimilarityMax <= 0 {
		config.ReEvalSimilarityMax = 0.85
	}
	if config.ReEvalImportanceMin <= 0 {
		config.ReEvalImportanceMin = 0.5
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

// --- helper functions ---

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func containsSubstring(content, query string) bool {
	return len(query) > 0 && len(content) > 0 && strings.Contains(content, query)
}

// findTargetMemory locates the memory that best matches a query string.
// It first tries exact substring matching against the provided similar results.
// If that fails, it falls back to embedding-based similarity search using
// FindSimilarWithOptions to respect agent/visibility scoping.
func (e *Engine) findTargetMemory(ctx context.Context, query string, similar []*storage.SimilarityResult, entityID string, opts storage.SimilarityOptions) *storage.Memory {
	// Reject very short queries — they produce unreliable embedding matches
	// and risk matching the wrong memory.
	if len(strings.Fields(query)) < 3 {
		// Still try exact substring as a safe fallback for short queries
		for _, sm := range similar {
			if containsSubstring(sm.Memory.Content, query) {
				return sm.Memory
			}
		}
		return nil
	}

	// Strategy 1: exact substring match among already-fetched similar results
	for _, sm := range similar {
		if containsSubstring(sm.Memory.Content, query) {
			return sm.Memory
		}
	}

	// Strategy 2: embedding similarity search with agent/visibility scoping
	if e.embedder == nil {
		return nil
	}
	queryEmbedding, err := e.embedder.Embed(ctx, query)
	if err != nil || len(queryEmbedding) == 0 {
		return nil
	}

	const minSimilarity = 0.85
	results, err := e.store.FindSimilarWithOptions(ctx, queryEmbedding, entityID, 5, minSimilarity, opts)
	if err != nil || len(results) == 0 {
		return nil
	}

	// Return the highest-scoring match (results are sorted by similarity)
	return results[0].Memory
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
