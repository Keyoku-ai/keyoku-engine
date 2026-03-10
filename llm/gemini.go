// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// GeminiProvider implements Provider using Google's Gemini API.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

func NewGeminiProvider(apiKey, model string) (*GeminiProvider, error) {
	if model == "" {
		model = "gemini-3-flash-preview"
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return &GeminiProvider{client: client, model: model}, nil
}

func (g *GeminiProvider) Name() string  { return "google" }
func (g *GeminiProvider) Model() string { return g.model }

func (g *GeminiProvider) Close() error {
	return g.client.Close()
}

func (g *GeminiProvider) extractText(resp *genai.GenerateContentResponse) (string, error) {
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini returned empty response")
	}
	var text string
	for _, part := range resp.Candidates[0].Content.Parts {
		if t, ok := part.(genai.Text); ok {
			text += string(t)
		}
	}
	return text, nil
}

func (g *GeminiProvider) ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"memories": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"content":            {Type: genai.TypeString},
						"type":               {Type: genai.TypeString, Enum: []string{"IDENTITY", "PREFERENCE", "RELATIONSHIP", "EVENT", "ACTIVITY", "PLAN", "CONTEXT", "EPHEMERAL"}},
						"importance":         {Type: genai.TypeNumber},
						"confidence":         {Type: genai.TypeNumber},
						"sentiment":          {Type: genai.TypeNumber},
						"importance_factors": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
						"confidence_factors": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
						"hedging_detected":   {Type: genai.TypeBoolean},
					},
					Required: []string{"content", "type", "importance", "confidence", "sentiment"},
				},
			},
			"entities": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"canonical_name": {Type: genai.TypeString},
						"type":           {Type: genai.TypeString, Enum: []string{"PERSON", "ORGANIZATION", "LOCATION", "PRODUCT"}},
						"aliases":        {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
						"context":        {Type: genai.TypeString},
					},
					Required: []string{"canonical_name", "type"},
				},
			},
			"relationships": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"source":     {Type: genai.TypeString},
						"relation":   {Type: genai.TypeString},
						"target":     {Type: genai.TypeString},
						"confidence": {Type: genai.TypeNumber},
					},
					Required: []string{"source", "relation", "target", "confidence"},
				},
			},
			"updates": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"query":       {Type: genai.TypeString},
						"new_content": {Type: genai.TypeString},
						"reason":      {Type: genai.TypeString},
					},
					Required: []string{"query", "new_content", "reason"},
				},
			},
			"deletes": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"query":  {Type: genai.TypeString},
						"reason": {Type: genai.TypeString},
					},
					Required: []string{"query", "reason"},
				},
			},
			"skipped": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"text":   {Type: genai.TypeString},
						"reason": {Type: genai.TypeString},
					},
					Required: []string{"text", "reason"},
				},
			},
		},
	}
	model.SetTemperature(0.2)
	model.SetTopP(0.8)

	prompt := FormatPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini generation failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
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
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.SetTemperature(0.3)
	model.SetTopP(0.8)

	prompt := FormatConsolidationPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini consolidation failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result ConsolidationResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini consolidation response: %w", err)
	}

	return &result, nil
}

func (g *GeminiProvider) ExtractWithSchema(ctx context.Context, req CustomExtractionRequest) (*CustomExtractionResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"extracted_data": {Type: genai.TypeObject},
			"confidence":     {Type: genai.TypeNumber},
			"reasoning":      {Type: genai.TypeString},
		},
		Required: []string{"extracted_data", "confidence", "reasoning"},
	}
	model.SetTemperature(0.2)
	model.SetTopP(0.8)

	prompt := FormatCustomExtractionPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini custom extraction failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result CustomExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini custom extraction response: %w", err)
	}

	return &result, nil
}

func (g *GeminiProvider) ExtractState(ctx context.Context, req StateExtractionRequest) (*StateExtractionResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"extracted_state":  {Type: genai.TypeObject},
			"changed_fields":   {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"confidence":       {Type: genai.TypeNumber},
			"reasoning":        {Type: genai.TypeString},
			"suggested_action": {Type: genai.TypeString},
			"validation_error": {Type: genai.TypeString},
		},
		Required: []string{"extracted_state", "changed_fields", "confidence", "reasoning"},
	}
	model.SetTemperature(0.2)
	model.SetTopP(0.8)

	prompt := FormatStateExtractionPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini state extraction failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result StateExtractionResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini state extraction response: %w", err)
	}

	return &result, nil
}

func (g *GeminiProvider) DetectConflict(ctx context.Context, req ConflictCheckRequest) (*ConflictCheckResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"contradicts":   {Type: genai.TypeBoolean},
			"conflict_type": {Type: genai.TypeString, Enum: []string{"contradiction", "update", "temporal", "partial", "none"}},
			"confidence":    {Type: genai.TypeNumber},
			"explanation":   {Type: genai.TypeString},
			"resolution":    {Type: genai.TypeString, Enum: []string{"use_new", "keep_existing", "merge", "keep_both"}},
		},
		Required: []string{"contradicts", "conflict_type", "confidence", "explanation", "resolution"},
	}
	model.SetTemperature(0.2)
	model.SetTopP(0.8)

	prompt := FormatConflictCheckPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini conflict check failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result ConflictCheckResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini conflict check response: %w", err)
	}

	return &result, nil
}

func (g *GeminiProvider) ReEvaluateImportance(ctx context.Context, req ImportanceReEvalRequest) (*ImportanceReEvalResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"new_importance": {Type: genai.TypeNumber},
			"reason":         {Type: genai.TypeString},
			"should_update":  {Type: genai.TypeBoolean},
		},
		Required: []string{"new_importance", "reason", "should_update"},
	}
	model.SetTemperature(0.2)
	model.SetTopP(0.8)

	prompt := FormatImportanceReEvalPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini importance re-eval failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result ImportanceReEvalResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini importance re-eval response: %w", err)
	}

	return &result, nil
}

func (g *GeminiProvider) PrioritizeActions(ctx context.Context, req ActionPriorityRequest) (*ActionPriorityResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"priority_action": {Type: genai.TypeString},
			"action_items":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"reasoning":       {Type: genai.TypeString},
			"urgency":         {Type: genai.TypeString, Enum: []string{"immediate", "soon", "can_wait"}},
		},
		Required: []string{"priority_action", "action_items", "reasoning", "urgency"},
	}
	model.SetTemperature(0.3)
	model.SetTopP(0.8)

	prompt := FormatActionPriorityPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini action priority failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var priorityResult ActionPriorityResponse
	if err := json.Unmarshal([]byte(text), &priorityResult); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini action priority response: %w", err)
	}

	return &priorityResult, nil
}

func (g *GeminiProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	model := g.client.GenerativeModel(g.model)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":    {Type: genai.TypeString},
			"confidence": {Type: genai.TypeNumber},
		},
		Required: []string{"summary", "confidence"},
	}
	model.SetTemperature(0.3)
	model.SetTopP(0.8)

	prompt := FormatGraphSummaryPrompt(req)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return nil, fmt.Errorf("Gemini graph summary failed: %w", err)
	}

	text, err := g.extractText(resp)
	if err != nil {
		return nil, err
	}

	var result GraphSummaryResponse
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini graph summary response: %w", err)
	}

	return &result, nil
}
