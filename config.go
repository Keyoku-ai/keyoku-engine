// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package keyoku

import "time"

// Config holds all configuration for Keyoku Embedded.
// Unlike the cloud version, this uses struct-based config (no env vars).
type Config struct {
	// Database path for SQLite (e.g., "./keyoku.db" or ":memory:" for testing)
	DBPath string

	// Extraction LLM
	ExtractionProvider string // "gemini", "openai", "anthropic", "ollama"
	ExtractionModel    string // e.g., "gemini-3-flash-preview", "gpt-5-mini", "claude-haiku-4-5-20251001"

	// API Keys
	GeminiAPIKey    string
	OpenAIAPIKey    string
	AnthropicAPIKey string

	// Base URLs (optional — for OpenRouter, LiteLLM, or custom endpoints)
	OpenAIBaseURL    string // e.g., "https://openrouter.ai/api" or "http://localhost:4000"
	AnthropicBaseURL string // e.g., "https://openrouter.ai/api"
	EmbeddingBaseURL string // e.g., custom embedding endpoint (defaults to OpenAI)

	// Ollama (local inference — no API key required for standard installs)
	OllamaBaseURL       string // e.g., "http://localhost:11434" (default)
	OllamaAPIKey        string // optional: set when Ollama is behind bearer-auth middleware
	OllamaEmbeddingDims int    // vector dimensions for the Ollama embedding model (768 or 1024)

	// Embeddings
	EmbeddingProvider   string // "openai" (default), "gemini", "ollama"
	EmbeddingModel      string // default: "text-embedding-3-small" (openai) or "gemini-embedding-001" (gemini)
	EmbeddingDimensions int    // override output dimensions (0 = use model default; Gemini supports truncation via OutputDimensionality)

	// Behavior
	MaxExtractTokens int // default: 4000
	ContextTurns     int // default: 5

	// Deduplication
	DeduplicationEnabled    bool    // default: true
	SemanticDuplicateThresh float64 // default: 0.95
	NearDuplicateThresh     float64 // default: 0.85

	// Conflict detection
	ConflictDetectionEnabled bool    // default: true
	ConflictSimilarityThresh float64 // default: 0.6

	// Entity resolution
	EntityExtractionEnabled bool    // default: true
	EntityMatchThreshold    float64 // default: 0.85
	MaxEntityAliases        int     // default: 10

	// Relationship detection
	RelationshipDetectionEnabled bool    // default: true
	MinRelationshipConfidence    float64 // default: 0.6

	// Background jobs
	SchedulerEnabled       bool          // default: true
	SchedulerCheckInterval time.Duration // default: 60s

	// Decay
	DecayBatchSize     int     // default: 1000
	DecayThreshold     float64 // default: 0.3
	ArchivalDays       int     // default: 30
	PurgeRetentionDays int     // default: 90

	// Heartbeat quiet hours — suppress non-immediate heartbeats during this window.
	// Disabled by default (heartbeats work anytime). Set QuietHoursEnabled=true to activate.
	QuietHoursEnabled  bool   // default: false
	QuietHourStart     int    // hour in QuietHoursTimezone (0-23), default: 23 (11pm)
	QuietHourEnd       int    // hour in QuietHoursTimezone (0-23), default: 7 (7am)
	QuietHoursTimezone string // IANA timezone (e.g., "America/Los_Angeles"), default: "America/Los_Angeles"

	// Team behavior
	DefaultVisibility string // "private", "team", or "global" (default: "private"; overridden to "team" when agent has a team)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig(dbPath string) Config {
	return Config{
		DBPath:             dbPath,
		ExtractionProvider: "openai",
		ExtractionModel:    "gpt-5-mini",
		EmbeddingModel:     "text-embedding-3-small",
		MaxExtractTokens:   4000,
		ContextTurns:       5,

		DeduplicationEnabled:    true,
		SemanticDuplicateThresh: 0.95,
		NearDuplicateThresh:     0.85,

		ConflictDetectionEnabled: true,
		ConflictSimilarityThresh: 0.6,

		EntityExtractionEnabled: true,
		EntityMatchThreshold:    0.85,
		MaxEntityAliases:        10,

		RelationshipDetectionEnabled: true,
		MinRelationshipConfidence:    0.6,

		SchedulerEnabled:       true,
		SchedulerCheckInterval: 60 * time.Second,

		DecayBatchSize:     1000,
		DecayThreshold:     0.3,
		ArchivalDays:       30,
		PurgeRetentionDays: 90,

		DefaultVisibility: "private",
	}
}
