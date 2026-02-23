package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements Provider using Anthropic's Claude API.
type AnthropicProvider struct {
	client *anthropic.Client
	model  string
}

func NewAnthropicProvider(apiKey, model string) (*AnthropicProvider, error) {
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{client: &client, model: model}, nil
}

func (a *AnthropicProvider) Name() string  { return "anthropic" }
func (a *AnthropicProvider) Model() string { return a.model }

func getExtractionToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "extract_memories",
		Description: anthropic.String("Extract memories from the input text"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"memories": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"content":            map[string]interface{}{"type": "string"},
							"type":               map[string]interface{}{"type": "string", "enum": []string{"IDENTITY", "PREFERENCE", "RELATIONSHIP", "EVENT", "ACTIVITY", "PLAN", "CONTEXT", "EPHEMERAL"}},
							"importance":         map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
							"confidence":         map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
							"sentiment":          map[string]interface{}{"type": "number", "minimum": -1, "maximum": 1},
							"importance_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
							"confidence_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
							"hedging_detected":   map[string]interface{}{"type": "boolean"},
						},
						"required": []string{"content", "type", "importance", "confidence", "sentiment"},
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
						"required": []string{"canonical_name", "type"},
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
							"confidence": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
						},
						"required": []string{"source", "relation", "target", "confidence"},
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
						"required": []string{"query", "new_content", "reason"},
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
						"required": []string{"query", "reason"},
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
						"required": []string{"text", "reason"},
					},
				},
			},
		},
	}
}

func (a *AnthropicProvider) extractToolUseInput(resp *anthropic.Message) (string, error) {
	for _, block := range resp.Content {
		if toolUse, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			return toolUse.JSON.Input.Raw(), nil
		}
	}
	return "", fmt.Errorf("Anthropic did not return tool_use block")
}

func (a *AnthropicProvider) ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	prompt := FormatPrompt(req)
	toolParam := getExtractionToolParam()

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: "You are a memory extraction system. Use the extract_memories tool to return your analysis."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("extract_memories"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic message failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result ExtractionResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic tool response: %w", err)
	}

	if err := validateResponse(&result); err != nil {
		return nil, fmt.Errorf("invalid extraction response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) ConsolidateMemories(ctx context.Context, req ConsolidationRequest) (*ConsolidationResponse, error) {
	prompt := FormatConsolidationPrompt(req)
	toolParam := anthropic.ToolParam{
		Name:        "consolidate_memories",
		Description: anthropic.String("Consolidate multiple similar memories into a single coherent memory"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"content":    map[string]interface{}{"type": "string"},
				"confidence": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
				"reasoning":  map[string]interface{}{"type": "string"},
			},
			Required: []string{"content", "confidence", "reasoning"},
		},
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 2048,
		System: []anthropic.TextBlockParam{
			{Text: "You are a memory consolidation system. Use the consolidate_memories tool to return the merged memory."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("consolidate_memories"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic consolidation failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result ConsolidationResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic consolidation response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) ExtractWithSchema(ctx context.Context, req CustomExtractionRequest) (*CustomExtractionResponse, error) {
	prompt := FormatCustomExtractionPrompt(req)
	toolParam := anthropic.ToolParam{
		Name:        "extract_custom_data",
		Description: anthropic.String("Extract structured data according to the provided schema"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"extracted_data": req.Schema,
				"confidence":     map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
				"reasoning":      map[string]interface{}{"type": "string"},
			},
			Required: []string{"extracted_data", "confidence", "reasoning"},
		},
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: "You are a data extraction system. Use the extract_custom_data tool to return your analysis."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("extract_custom_data"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic custom extraction failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result CustomExtractionResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic custom extraction response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) ExtractState(ctx context.Context, req StateExtractionRequest) (*StateExtractionResponse, error) {
	prompt := FormatStateExtractionPrompt(req)
	toolParam := anthropic.ToolParam{
		Name:        "extract_state",
		Description: anthropic.String("Extract workflow state from agent interactions"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"extracted_state":  map[string]interface{}{"type": "object"},
				"changed_fields":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"confidence":       map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
				"reasoning":        map[string]interface{}{"type": "string"},
				"suggested_action": map[string]interface{}{"type": "string"},
				"validation_error": map[string]interface{}{"type": "string"},
			},
			Required: []string{"extracted_state", "changed_fields", "confidence", "reasoning"},
		},
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: "You are a state extraction system for AI agent workflows. Use the extract_state tool to return the extracted state."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("extract_state"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic state extraction failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result StateExtractionResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic state extraction response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) DetectConflict(ctx context.Context, req ConflictCheckRequest) (*ConflictCheckResponse, error) {
	prompt := FormatConflictCheckPrompt(req)
	toolParam := anthropic.ToolParam{
		Name:        "detect_conflict",
		Description: anthropic.String("Detect whether two memories conflict"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"contradicts":   map[string]interface{}{"type": "boolean"},
				"conflict_type": map[string]interface{}{"type": "string", "enum": []string{"contradiction", "update", "temporal", "partial", "none"}},
				"confidence":    map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
				"explanation":   map[string]interface{}{"type": "string"},
				"resolution":    map[string]interface{}{"type": "string", "enum": []string{"use_new", "keep_existing", "merge", "keep_both"}},
			},
			Required: []string{"contradicts", "conflict_type", "confidence", "explanation", "resolution"},
		},
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are a memory conflict detection system. Use the detect_conflict tool to return your analysis."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("detect_conflict"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic conflict check failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result ConflictCheckResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic conflict check response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) ReEvaluateImportance(ctx context.Context, req ImportanceReEvalRequest) (*ImportanceReEvalResponse, error) {
	prompt := FormatImportanceReEvalPrompt(req)
	toolParam := anthropic.ToolParam{
		Name:        "reeval_importance",
		Description: anthropic.String("Re-evaluate the importance of a memory given new information"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"new_importance": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
				"reason":         map[string]interface{}{"type": "string"},
				"should_update":  map[string]interface{}{"type": "boolean"},
			},
			Required: []string{"new_importance", "reason", "should_update"},
		},
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are a memory importance evaluation system. Use the reeval_importance tool to return your assessment."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("reeval_importance"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic importance re-eval failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result ImportanceReEvalResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic importance re-eval response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) PrioritizeActions(ctx context.Context, req ActionPriorityRequest) (*ActionPriorityResponse, error) {
	toolParam := anthropic.ToolParam{
		Name:        "prioritize_actions",
		Description: anthropic.String("Return the prioritized action plan"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"priority_action": map[string]interface{}{"type": "string", "description": "The single most important action"},
				"action_items":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "All items ordered by priority"},
				"reasoning":       map[string]interface{}{"type": "string", "description": "Brief explanation of priority ordering"},
				"urgency":         map[string]interface{}{"type": "string", "enum": []string{"immediate", "soon", "can_wait"}},
			},
		},
	}

	prompt := FormatActionPriorityPrompt(req)
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are an AI agent executive function that prioritizes actions. Use the prioritize_actions tool to return your assessment."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("prioritize_actions"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic action priority failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var priorityResult ActionPriorityResponse
	if err := json.Unmarshal([]byte(toolInputStr), &priorityResult); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic action priority response: %w", err)
	}

	return &priorityResult, nil
}

func (a *AnthropicProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	toolParam := anthropic.ToolParam{
		Name:        "summarize_graph",
		Description: anthropic.String("Summarize the knowledge graph connections"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]interface{}{
				"summary":    map[string]interface{}{"type": "string", "description": "Natural language summary of connections"},
				"confidence": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
			},
		},
	}

	prompt := FormatGraphSummaryPrompt(req)
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are a knowledge graph reasoning system. Use the summarize_graph tool to return your analysis."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("summarize_graph"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic graph summary failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result GraphSummaryResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic graph summary response: %w", err)
	}

	return &result, nil
}
