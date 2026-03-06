package main

import (
	"encoding/json"
	"os"

	keyoku "github.com/keyoku-ai/keyoku-embedded"
)

// ServerConfig holds configuration for the HTTP sidecar server.
type ServerConfig struct {
	Port               int    `json:"port"`
	DBPath             string `json:"db_path"`
	ExtractionProvider string `json:"extraction_provider"`
	ExtractionModel    string `json:"extraction_model"`
	OpenAIAPIKey       string `json:"openai_api_key"`
	GeminiAPIKey       string `json:"gemini_api_key"`
	AnthropicAPIKey    string `json:"anthropic_api_key"`
	OpenAIBaseURL      string `json:"openai_base_url"`
	AnthropicBaseURL   string `json:"anthropic_base_url"`
	EmbeddingBaseURL   string `json:"embedding_base_url"`
	EmbeddingModel     string `json:"embedding_model"`
	SchedulerEnabled   *bool  `json:"scheduler_enabled"`
}

// DefaultServerConfig returns a server config with sensible defaults.
func DefaultServerConfig() ServerConfig {
	enabled := true
	return ServerConfig{
		Port:               18900,
		DBPath:             "./keyoku.db",
		ExtractionProvider: "gemini",
		ExtractionModel:    "gemini-3-flash-preview",
		EmbeddingModel:     "text-embedding-3-small",
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
	if v := os.Getenv("KEYOKU_PORT"); v != "" {
		// parsed below in main
	}
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
	if v := os.Getenv("KEYOKU_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
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
	if sc.EmbeddingModel != "" {
		cfg.EmbeddingModel = sc.EmbeddingModel
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
	if sc.SchedulerEnabled != nil {
		cfg.SchedulerEnabled = *sc.SchedulerEnabled
	}

	return cfg
}
