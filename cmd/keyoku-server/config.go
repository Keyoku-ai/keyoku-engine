// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"os"
	"strconv"

	keyoku "github.com/keyoku-ai/keyoku-engine"
)

// ServerConfig holds configuration for the HTTP sidecar server.
type ServerConfig struct {
	Port                int    `json:"port"`
	DBPath              string `json:"db_path"`
	ExtractionProvider  string `json:"extraction_provider"`
	ExtractionModel     string `json:"extraction_model"`
	OpenAIAPIKey        string `json:"openai_api_key"`
	GeminiAPIKey        string `json:"gemini_api_key"`
	AnthropicAPIKey     string `json:"anthropic_api_key"`
	OpenAIBaseURL       string `json:"openai_base_url"`
	AnthropicBaseURL    string `json:"anthropic_base_url"`
	EmbeddingBaseURL    string `json:"embedding_base_url"`
	EmbeddingProvider   string `json:"embedding_provider"`
	EmbeddingModel      string `json:"embedding_model"`
	GeminiBackend       string `json:"gemini_backend"`
	GCloudProject       string `json:"gcloud_project"`
	OllamaBaseURL       string `json:"ollama_base_url"`
	OllamaAPIKey        string `json:"ollama_api_key"`
	OllamaEmbeddingDims int    `json:"ollama_embedding_dims"`
	EmbeddingDimensions int    `json:"embedding_dimensions"`
	SchedulerEnabled    *bool  `json:"scheduler_enabled"`
	QuietHoursEnabled   *bool  `json:"quiet_hours_enabled"`
	QuietHourStart      *int   `json:"quiet_hour_start"`
	QuietHourEnd        *int   `json:"quiet_hour_end"`
	QuietHoursTimezone  string `json:"quiet_hours_timezone"`

	// Heartbeat delivery
	DeliveryMethod    string `json:"delivery_method"`     // "cli" or ""
	DeliveryCommand   string `json:"delivery_command"`    // e.g. "openclaw"
	DeliveryChannel   string `json:"delivery_channel"`    // e.g. "telegram"
	DeliveryRecipient string `json:"delivery_recipient"`  // e.g. "-4970078838"
	DeliverySessionID string `json:"delivery_session_id"` // e.g. "telegram:group:-4970078838"
	AdaptiveHeartbeat *bool  `json:"adaptive_heartbeat"`  // enable dynamic tick interval

	// Auto-start watcher on boot
	WatcherAutoStart    *bool  `json:"watcher_auto_start"`
	WatcherEntityIDs    string `json:"watcher_entity_ids"`    // comma-separated
	WatcherBaseInterval int    `json:"watcher_base_interval"` // ms
	WatcherMinInterval  int    `json:"watcher_min_interval"`  // ms
	WatcherMaxInterval  int    `json:"watcher_max_interval"`  // ms
}

// DefaultServerConfig returns a server config with sensible defaults.
func DefaultServerConfig() ServerConfig {
	enabled := true
	return ServerConfig{
		Port:               18900,
		DBPath:             "./keyoku.db",
		ExtractionProvider: "gemini",
		ExtractionModel:    "gemini-2.5-flash",
		EmbeddingModel:     "gemini-embedding-001",
		SchedulerEnabled:   &enabled,
	}
}

// LoadServerConfig loads config from a JSON file, falling back to env vars.
func LoadServerConfig(path string) (ServerConfig, error) {
	cfg := DefaultServerConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}

	// Environment variable overrides
	// KEYOKU_PORT is parsed in main.go via flag override
	if v := os.Getenv("KEYOKU_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("KEYOKU_EXTRACTION_PROVIDER"); v != "" {
		cfg.ExtractionProvider = v
	}
	if v := os.Getenv("KEYOKU_EXTRACTION_MODEL"); v != "" {
		cfg.ExtractionModel = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.OpenAIAPIKey = v
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		cfg.GeminiAPIKey = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		cfg.AnthropicAPIKey = v
	}
	if v := os.Getenv("KEYOKU_EMBEDDING_PROVIDER"); v != "" {
		cfg.EmbeddingProvider = v
	}
	if v := os.Getenv("KEYOKU_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := os.Getenv("KEYOKU_EMBEDDING_DIMENSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.EmbeddingDimensions = n
		}
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		cfg.OpenAIBaseURL = v
	}
	if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
		cfg.AnthropicBaseURL = v
	}
	if v := os.Getenv("EMBEDDING_BASE_URL"); v != "" {
		cfg.EmbeddingBaseURL = v
	}
	if v := os.Getenv("KEYOKU_GEMINI_BACKEND"); v != "" {
		cfg.GeminiBackend = v
	}
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); v != "" {
		cfg.GCloudProject = v
	}
	if v := os.Getenv("OLLAMA_BASE_URL"); v != "" {
		cfg.OllamaBaseURL = v
	}
	if v := os.Getenv("OLLAMA_API_KEY"); v != "" {
		cfg.OllamaAPIKey = v
	}
	if v := os.Getenv("OLLAMA_EMBEDDING_DIMS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.OllamaEmbeddingDims = n
		}
	}
	if v := os.Getenv("KEYOKU_QUIET_HOURS_ENABLED"); v != "" {
		enabled := v == "true" || v == "1"
		cfg.QuietHoursEnabled = &enabled
	}
	if v := os.Getenv("KEYOKU_QUIET_HOUR_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 23 {
			cfg.QuietHourStart = &n
		}
	}
	if v := os.Getenv("KEYOKU_QUIET_HOUR_END"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 23 {
			cfg.QuietHourEnd = &n
		}
	}
	if v := os.Getenv("KEYOKU_QUIET_HOURS_TIMEZONE"); v != "" {
		cfg.QuietHoursTimezone = v
	}
	if v := os.Getenv("KEYOKU_DELIVERY_METHOD"); v != "" {
		cfg.DeliveryMethod = v
	}
	if v := os.Getenv("KEYOKU_DELIVERY_COMMAND"); v != "" {
		cfg.DeliveryCommand = v
	}
	if v := os.Getenv("KEYOKU_DELIVERY_CHANNEL"); v != "" {
		cfg.DeliveryChannel = v
	}
	if v := os.Getenv("KEYOKU_DELIVERY_RECIPIENT"); v != "" {
		cfg.DeliveryRecipient = v
	}
	if v := os.Getenv("KEYOKU_DELIVERY_SESSION_ID"); v != "" {
		cfg.DeliverySessionID = v
	}
	if v := os.Getenv("KEYOKU_ADAPTIVE_HEARTBEAT"); v != "" {
		enabled := v == "true" || v == "1"
		cfg.AdaptiveHeartbeat = &enabled
	}
	if v := os.Getenv("KEYOKU_WATCHER_AUTO_START"); v != "" {
		enabled := v == "true" || v == "1"
		cfg.WatcherAutoStart = &enabled
	}
	if v := os.Getenv("KEYOKU_WATCHER_ENTITY_IDS"); v != "" {
		cfg.WatcherEntityIDs = v
	}
	if v := os.Getenv("KEYOKU_WATCHER_BASE_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WatcherBaseInterval = n
		}
	}
	if v := os.Getenv("KEYOKU_WATCHER_MIN_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WatcherMinInterval = n
		}
	}
	if v := os.Getenv("KEYOKU_WATCHER_MAX_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.WatcherMaxInterval = n
		}
	}

	return cfg, nil
}

// ToKeyokuConfig converts server config to the keyoku library config.
func (sc ServerConfig) ToKeyokuConfig() keyoku.Config {
	cfg := keyoku.DefaultConfig(sc.DBPath)

	if sc.ExtractionProvider != "" {
		cfg.ExtractionProvider = sc.ExtractionProvider
	}
	if sc.ExtractionModel != "" {
		cfg.ExtractionModel = sc.ExtractionModel
	}
	if sc.OpenAIAPIKey != "" {
		cfg.OpenAIAPIKey = sc.OpenAIAPIKey
	}
	if sc.GeminiAPIKey != "" {
		cfg.GeminiAPIKey = sc.GeminiAPIKey
	}
	if sc.AnthropicAPIKey != "" {
		cfg.AnthropicAPIKey = sc.AnthropicAPIKey
	}
	if sc.EmbeddingProvider != "" {
		cfg.EmbeddingProvider = sc.EmbeddingProvider
	}
	if sc.EmbeddingModel != "" {
		cfg.EmbeddingModel = sc.EmbeddingModel
	}
	if sc.EmbeddingDimensions > 0 {
		cfg.EmbeddingDimensions = sc.EmbeddingDimensions
	}
	if sc.OpenAIBaseURL != "" {
		cfg.OpenAIBaseURL = sc.OpenAIBaseURL
	}
	if sc.AnthropicBaseURL != "" {
		cfg.AnthropicBaseURL = sc.AnthropicBaseURL
	}
	if sc.EmbeddingBaseURL != "" {
		cfg.EmbeddingBaseURL = sc.EmbeddingBaseURL
	}
	if sc.GeminiBackend != "" {
		cfg.GeminiBackend = sc.GeminiBackend
	}
	if sc.GCloudProject != "" {
		cfg.GCloudProject = sc.GCloudProject
	}
	if sc.OllamaBaseURL != "" {
		cfg.OllamaBaseURL = sc.OllamaBaseURL
	}
	if sc.OllamaAPIKey != "" {
		cfg.OllamaAPIKey = sc.OllamaAPIKey
	}
	if sc.OllamaEmbeddingDims > 0 {
		cfg.OllamaEmbeddingDims = sc.OllamaEmbeddingDims
	}
	if sc.SchedulerEnabled != nil {
		cfg.SchedulerEnabled = *sc.SchedulerEnabled
	}
	if sc.QuietHoursEnabled != nil {
		cfg.QuietHoursEnabled = *sc.QuietHoursEnabled
	}
	if sc.QuietHourStart != nil {
		cfg.QuietHourStart = *sc.QuietHourStart
	}
	if sc.QuietHourEnd != nil {
		cfg.QuietHourEnd = *sc.QuietHourEnd
	}
	if sc.QuietHoursTimezone != "" {
		cfg.QuietHoursTimezone = sc.QuietHoursTimezone
	}

	return cfg
}
