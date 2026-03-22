// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		model = "gpt-5-mini"
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

// tempFixed returns true for models that don't support custom temperature (e.g., gpt-5-mini, o1, o3).
func (o *OpenAIProvider) tempFixed() bool {
	return strings.HasPrefix(o.model, "gpt-5-mini") || strings.HasPrefix(o.model, "o1") || strings.HasPrefix(o.model, "o3")
}

// openaiExtractionSchema is computed once from the shared canonical schema.
var openaiExtractionSchema = ForOpenAIExtraction()

func (o *OpenAIProvider) ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	prompt := FormatPrompt(req)

	params := openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory extraction system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "extraction_response",
					Schema: openaiExtractionSchema,
					Strict: openai.Bool(true),
				},
			},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.2)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

	params := openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory consolidation system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.3)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

	responseSchema := ForOpenAI(CustomExtractionResponseSchema(req.Schema))

	params := openai.ChatCompletionNewParams{
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
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.2)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

	stateSchema := ForOpenAI(StateExtractionResponseSchema())

	params := openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a state extraction system for AI agent workflows. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "state_extraction_response",
					Schema: stateSchema,
					Strict: openai.Bool(true),
				},
			},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.2)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

	params := openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory conflict detection system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "conflict_check_response",
					Schema: ForOpenAI(ConflictCheckSchema()),
					Strict: openai.Bool(true),
				},
			},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.2)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

	params := openai.ChatCompletionNewParams{
		Model: o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory importance evaluation system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "importance_reeval_response",
					Schema: ForOpenAI(ImportanceReEvalSchema()),
					Strict: openai.Bool(true),
				},
			},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.2)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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
	prompt := FormatActionPriorityPrompt(req)
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are an AI agent executive function that prioritizes actions."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   "action_priority",
				Strict: openai.Bool(true),
				Schema: ForOpenAI(ActionPrioritySchema()),
			}},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.3)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

func (o *OpenAIProvider) AnalyzeHeartbeatContext(ctx context.Context, req HeartbeatAnalysisRequest) (*HeartbeatAnalysisResponse, error) {
	prompt := FormatHeartbeatAnalysisPrompt(req)
	schema := HeartbeatAnalysisSchemaForVerbosity(req.Verbosity)
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are an AI agent's memory and planning system. Always respond with valid JSON only."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   "heartbeat_analysis",
				Strict: openai.Bool(true),
				Schema: ForOpenAI(schema),
			}},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.3)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("OpenAI heartbeat analysis failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result HeartbeatAnalysisResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI heartbeat analysis response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	prompt := FormatGraphSummaryPrompt(req)
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a knowledge graph reasoning system."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   "graph_summary",
				Strict: openai.Bool(true),
				Schema: ForOpenAI(GraphSummarySchema()),
			}},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.3)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
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

func (o *OpenAIProvider) RerankMemories(ctx context.Context, req RerankRequest) (*RerankResponse, error) {
	prompt := FormatRerankPrompt(req)
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(o.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a memory relevance ranker."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   "rerank_response",
				Strict: openai.Bool(true),
				Schema: ForOpenAI(RerankSchema()),
			}},
		},
	}
	if !o.tempFixed() {
		params.Temperature = openai.Float(0.1)
	}
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("OpenAI rerank failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices")
	}

	var result RerankResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI rerank response: %w", err)
	}

	return &result, nil
}

func (o *OpenAIProvider) IsLite() bool { return false }

// ExtractMemoriesCore is a pass-through — OpenAI handles the full schema fine.
func (o *OpenAIProvider) ExtractMemoriesCore(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	resp, err := o.ExtractMemories(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Entities = nil
	resp.Relationships = nil
	return resp, nil
}

// ExtractGraph is a pass-through — OpenAI handles the full schema fine.
func (o *OpenAIProvider) ExtractGraph(ctx context.Context, req ExtractionRequest) (*GraphExtractionResponse, error) {
	resp, err := o.ExtractMemories(ctx, req)
	if err != nil {
		return nil, err
	}
	return &GraphExtractionResponse{
		Entities:      resp.Entities,
		Relationships: resp.Relationships,
	}, nil
}
