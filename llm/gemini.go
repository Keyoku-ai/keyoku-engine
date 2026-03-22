// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/genai"
)

// GeminiProvider implements Provider using Google's Gemini API via the new google.golang.org/genai SDK.
type GeminiProvider struct {
	client    *genai.Client
	model     string
	liteMode bool // lite models use simplified schemas for complex methods
}

func NewGeminiProvider(apiKey, model string) (*GeminiProvider, error) {
	return NewGeminiProviderWithBackend(apiKey, model, "", "")
}

// NewGeminiProviderWithBackend creates a Gemini provider with explicit backend selection.
// backend: "vertex" for Vertex AI, anything else for Google AI Studio (default).
// project: GCP project ID, required for Vertex AI.
func NewGeminiProviderWithBackend(apiKey, model, backend, project string) (*GeminiProvider, error) {
	if model == "" {
		model = "gemini-3.1-flash-lite-preview"
	}
	liteMode := strings.Contains(model, "lite")
	ctx := context.Background()

	cfg := &genai.ClientConfig{}
	if backend == "vertex" {
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = project
		cfg.Location = "us-central1"
		// Vertex AI uses Application Default Credentials (ADC) — no API key.
		// Run: gcloud auth application-default login
	} else {
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = apiKey
	}

	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return &GeminiProvider{client: client, model: model, liteMode: liteMode}, nil
}

func (g *GeminiProvider) IsLite() bool { return g.liteMode }

func (g *GeminiProvider) Name() string  { return "google" }
func (g *GeminiProvider) Model() string { return g.model }

// schemaToJsonSchema converts a typed *genai.Schema to a map[string]any suitable for ResponseJsonSchema.
// Gemini 3+ models require ResponseJsonSchema (raw JSON Schema with lowercase types) instead of
// ResponseSchema (typed struct with uppercase types like "STRING", "OBJECT").
func schemaToJsonSchema(s *genai.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{}

	// Convert uppercase genai types to lowercase JSON Schema types
	switch s.Type {
	case genai.TypeString:
		m["type"] = "string"
	case genai.TypeNumber:
		m["type"] = "number"
	case genai.TypeBoolean:
		m["type"] = "boolean"
	case genai.TypeArray:
		m["type"] = "array"
	case genai.TypeObject:
		m["type"] = "object"
	default:
		m["type"] = strings.ToLower(string(s.Type))
	}

	if s.Items != nil {
		m["items"] = schemaToJsonSchema(s.Items)
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for k, v := range s.Properties {
			props[k] = schemaToJsonSchema(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	return m
}

// generate is a helper that calls GenerateContent with the given config and returns the text.
// For lite models, it automatically minimizes thinking to maximize output token budget for JSON.
// For Gemini 3+ models, it converts ResponseSchema to ResponseJsonSchema (the new API format).
// Gemini 3+ uses ThinkingLevel; Gemini 2.x uses ThinkingBudget.
func (g *GeminiProvider) generate(ctx context.Context, prompt string, config *genai.GenerateContentConfig) (string, error) {
	isGemini3 := strings.Contains(g.model, "3.")

	// Gemini 3+ models: convert ResponseSchema → ResponseJsonSchema (new API format)
	if isGemini3 && config.ResponseSchema != nil {
		config.ResponseJsonSchema = schemaToJsonSchema(config.ResponseSchema)
		config.ResponseSchema = nil
	}

	if g.liteMode && config.ThinkingConfig == nil {
		// Lite models: override temperature to 1.0 (Google's recommendation for structured output)
		config.Temperature = genai.Ptr[float32](1.0)
		if isGemini3 {
			// Gemini 3+ models: use ThinkingLevel + generous output budget
			config.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelMinimal,
			}
			if config.MaxOutputTokens == 0 {
				config.MaxOutputTokens = 65536
			}
		} else {
			// Gemini 2.x models: use ThinkingBudget=0
			noThinking := int32(0)
			config.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingBudget: &noThinking,
			}
		}
	}
	resp, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(prompt), config)
	if err != nil {
		return "", err
	}
	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("Gemini returned empty response")
	}
	return text, nil
}

func (g *GeminiProvider) ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	// Lite mode: split into two simpler calls run in parallel.
	if g.liteMode {
		var (
			core    *ExtractionResponse
			graph   *GraphExtractionResponse
			coreErr error
			graphErr error
			wg      sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			core, coreErr = g.ExtractMemoriesCore(ctx, req)
		}()
		go func() {
			defer wg.Done()
			graph, graphErr = g.ExtractGraph(ctx, req)
		}()
		wg.Wait()

		if coreErr != nil {
			return nil, coreErr
		}
		if graphErr == nil {
			core.Entities = graph.Entities
			core.Relationships = graph.Relationships
		}
		return core, nil
	}

	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGeminiExtraction(),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini extraction failed: %w", err)
	}

	var result ExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini JSON response: %w", err)
	}
	if err := validateResponse(&result); err != nil {
		return nil, fmt.Errorf("invalid extraction response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ConsolidateMemories(ctx context.Context, req ConsolidationRequest) (*ConsolidationResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		Temperature:      genai.Ptr[float32](0.3),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatConsolidationPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini consolidation failed: %w", err)
	}

	var result ConsolidationResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini consolidation response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ExtractWithSchema(ctx context.Context, req CustomExtractionRequest) (*CustomExtractionResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(CustomExtractionResponseSchema(req.Schema)),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatCustomExtractionPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini custom extraction failed: %w", err)
	}

	var result CustomExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini custom extraction response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ExtractState(ctx context.Context, req StateExtractionRequest) (*StateExtractionResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(StateExtractionResponseSchema()),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatStateExtractionPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini state extraction failed: %w", err)
	}

	var result StateExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini state extraction response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) DetectConflict(ctx context.Context, req ConflictCheckRequest) (*ConflictCheckResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(ConflictCheckSchema()),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatConflictCheckPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini conflict check failed: %w", err)
	}

	var result ConflictCheckResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini conflict check response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ReEvaluateImportance(ctx context.Context, req ImportanceReEvalRequest) (*ImportanceReEvalResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(ImportanceReEvalSchema()),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatImportanceReEvalPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini importance re-eval failed: %w", err)
	}

	var result ImportanceReEvalResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini importance re-eval response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) PrioritizeActions(ctx context.Context, req ActionPriorityRequest) (*ActionPriorityResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(ActionPrioritySchema()),
		Temperature:      genai.Ptr[float32](0.3),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatActionPriorityPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini action priority failed: %w", err)
	}

	var result ActionPriorityResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini action priority response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) AnalyzeHeartbeatContext(ctx context.Context, req HeartbeatAnalysisRequest) (*HeartbeatAnalysisResponse, error) {
	schema := HeartbeatAnalysisSchemaForVerbosity(req.Verbosity)
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(schema),
		Temperature:      genai.Ptr[float32](0.3),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatHeartbeatAnalysisPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini heartbeat analysis failed: %w", err)
	}

	var result HeartbeatAnalysisResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini heartbeat analysis response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(GraphSummarySchema()),
		Temperature:      genai.Ptr[float32](0.3),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatGraphSummaryPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini graph summary failed: %w", err)
	}

	var result GraphSummaryResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini graph summary response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) RerankMemories(ctx context.Context, req RerankRequest) (*RerankResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGemini(RerankSchema()),
		Temperature:      genai.Ptr[float32](0.1),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatRerankPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini rerank failed: %w", err)
	}

	var result RerankResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini rerank response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ExtractMemoriesCore(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGeminiCoreExtraction(),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini core extraction failed: %w", err)
	}

	var result ExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini core extraction response: %w", err)
	}
	if err := validateResponse(&result); err != nil {
		return nil, fmt.Errorf("invalid core extraction response: %w", err)
	}
	return &result, nil
}

func (g *GeminiProvider) ExtractGraph(ctx context.Context, req ExtractionRequest) (*GraphExtractionResponse, error) {
	config := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   ForGeminiGraphExtraction(),
		Temperature:      genai.Ptr[float32](0.2),
		TopP:             genai.Ptr[float32](0.8),
	}

	text, err := g.generate(ctx, FormatPrompt(req), config)
	if err != nil {
		return nil, fmt.Errorf("Gemini graph extraction failed: %w", err)
	}

	var result GraphExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini graph extraction response: %w", err)
	}
	return &result, nil
}
