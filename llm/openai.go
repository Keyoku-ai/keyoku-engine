// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// OpenAIProvider implements Provider using OpenAI's API.
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

func NewOpenAIProvider(apiKey, model, baseURL string) (*OpenAIProvider, error) {
	if model == "" {
		model = "gpt-4o-mini"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := openai.NewClient(opts...)
	return &OpenAIProvider{client: &client, model: model}, nil
}

func (o *OpenAIProvider) Name() string  { return "openai" }
func (o *OpenAIProvider) Model() string { return o.model }

var extractionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"memories": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content":            map[string]interface{}{"type": "string"},
					"type":               map[string]interface{}{"type": "string", "enum": []string{"IDENTITY", "PREFERENCE", "RELATIONSHIP", "EVENT", "ACTIVITY", "PLAN", "CONTEXT", "EPHEMERAL"}},
					"importance":         map[string]interface{}{"type": "number"},
					"confidence":         map[string]interface{}{"type": "number"},
					"sentiment":          map[string]interface{}{"type": "number"},
					"importance_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"confidence_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"hedging_detected":   map[string]interface{}{"type": "boolean"},
				},
				"required":             []string{"content", "type", "importance", "confidence", "sentiment", "importance_factors", "confidence_factors", "hedging_detected"},
				"additionalProperties": false,
			},
		},
		"entities": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"canonical_name": map[string]interface{}{"type": "string"},
					"type":           map[string]interface{}{"type": "string", "enum": []string{"PERSON", "ORGANIZATION", "LOCATION", "PRODUCT"}},
					"aliases":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"context":        map[string]interface{}{"type": "string"},
				},
				"required":             []string{"canonical_name", "type", "aliases", "context"},
				"additionalProperties": false,
			},
		},
		"relationships": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source":     map[string]interface{}{"type": "string"},
					"relation":   map[string]interface{}{"type": "string"},
					"target":     map[string]interface{}{"type": "string"},
					"confidence": map[string]interface{}{"type": "number"},
				},
				"required":             []string{"source", "relation", "target", "confidence"},
				"additionalProperties": false,
			},
		},
		"updates": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string"},
					"new_content": map[string]interface{}{"type": "string"},
					"reason":      map[string]interface{}{"type": "string"},
				},
				"required":             []string{"query", "new_content", "reason"},
				"additionalProperties": false,
			},
		},
		"deletes": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":  map[string]interface{}{"type": "string"},
					"reason": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"query", "reason"},
				"additionalProperties": false,
			},
		},
		"skipped": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text":   map[string]interface{}{"type": "string"},
					"reason": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"text", "reason"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"memories", "entities", "relationships", "updates", "deletes", "skipped"},
	"additionalProperties": false,
}

func (o *OpenAIProvider) ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	prompt := FormatPrompt(req)

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory extraction system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "extraction_response",
					Schema: extractionSchema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result ExtractionResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI JSON response: %w", err)
	}

	if err := validateResponse(&result); err != nil {
		return nil, fmt.Errorf("invalid extraction response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) ConsolidateMemories(ctx context.Context, req ConsolidationRequest) (*ConsolidationResponse, error) {
	prompt := FormatConsolidationPrompt(req)

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory consolidation system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: openai.Float(0.3),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI consolidation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result ConsolidationResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI consolidation response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) ExtractWithSchema(ctx context.Context, req CustomExtractionRequest) (*CustomExtractionResponse, error) {
	prompt := FormatCustomExtractionPrompt(req)

	responseSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"extracted_data": req.Schema,
			"confidence":     map[string]interface{}{"type": "number"},
			"reasoning":      map[string]interface{}{"type": "string"},
		},
		"required":             []string{"extracted_data", "confidence", "reasoning"},
		"additionalProperties": false,
	}

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a data extraction system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "custom_extraction_response",
					Schema: responseSchema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI custom extraction failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result CustomExtractionResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI custom extraction response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) ExtractState(ctx context.Context, req StateExtractionRequest) (*StateExtractionResponse, error) {
	prompt := FormatStateExtractionPrompt(req)

	responseSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"extracted_state":  map[string]interface{}{"type": "object"},
			"changed_fields":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"confidence":       map[string]interface{}{"type": "number"},
			"reasoning":        map[string]interface{}{"type": "string"},
			"suggested_action": map[string]interface{}{"type": "string"},
			"validation_error": map[string]interface{}{"type": "string"},
		},
		"required":             []string{"extracted_state", "changed_fields", "confidence", "reasoning"},
		"additionalProperties": false,
	}

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a state extraction system for AI agent workflows. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "state_extraction_response",
					Schema: responseSchema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI state extraction failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result StateExtractionResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI state extraction response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) DetectConflict(ctx context.Context, req ConflictCheckRequest) (*ConflictCheckResponse, error) {
	prompt := FormatConflictCheckPrompt(req)

	conflictSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"contradicts":   map[string]interface{}{"type": "boolean"},
			"conflict_type": map[string]interface{}{"type": "string", "enum": []string{"contradiction", "update", "temporal", "partial", "none"}},
			"confidence":    map[string]interface{}{"type": "number"},
			"explanation":   map[string]interface{}{"type": "string"},
			"resolution":    map[string]interface{}{"type": "string", "enum": []string{"use_new", "keep_existing", "merge", "keep_both"}},
		},
		"required":             []string{"contradicts", "conflict_type", "confidence", "explanation", "resolution"},
		"additionalProperties": false,
	}

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory conflict detection system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "conflict_check_response",
					Schema: conflictSchema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI conflict check failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result ConflictCheckResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI conflict check response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) ReEvaluateImportance(ctx context.Context, req ImportanceReEvalRequest) (*ImportanceReEvalResponse, error) {
	prompt := FormatImportanceReEvalPrompt(req)

	importanceSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"new_importance": map[string]interface{}{"type": "number"},
			"reason":         map[string]interface{}{"type": "string"},
			"should_update":  map[string]interface{}{"type": "boolean"},
		},
		"required":             []string{"new_importance", "reason", "should_update"},
		"additionalProperties": false,
	}

	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory importance evaluation system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "importance_reeval_response",
					Schema: importanceSchema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI importance re-eval failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result ImportanceReEvalResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI importance re-eval response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) PrioritizeActions(ctx context.Context, req ActionPriorityRequest) (*ActionPriorityResponse, error) {
	prioritySchema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:   "action_priority",
		Strict: openai.Bool(true),
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"priority_action": map[string]interface{}{"type": "string"},
				"action_items":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"reasoning":       map[string]interface{}{"type": "string"},
				"urgency":         map[string]interface{}{"type": "string", "enum": []string{"immediate", "soon", "can_wait"}},
			},
			"required":             []string{"priority_action", "action_items", "reasoning", "urgency"},
			"additionalProperties": false,
		},
	}

	prompt := FormatActionPriorityPrompt(req)
	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are an AI agent executive function that prioritizes actions."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: prioritySchema},
		},
		Temperature: openai.Float(0.3),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI action priority failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var priorityResult ActionPriorityResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &priorityResult); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI action priority response: %w", err)
	}

	return &priorityResult, nil
}

func (o *OpenAIProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	graphSchema := openai.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:   "graph_summary",
		Strict: openai.Bool(true),
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"summary":    map[string]interface{}{"type": "string"},
				"confidence": map[string]interface{}{"type": "number"},
			},
			"required":             []string{"summary", "confidence"},
			"additionalProperties": false,
		},
	}

	prompt := FormatGraphSummaryPrompt(req)
	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a knowledge graph reasoning system."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: graphSchema},
		},
		Temperature: openai.Float(0.3),
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI graph summary failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result GraphSummaryResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI graph summary response: %w", err)
	}

	return &result, nil
}
