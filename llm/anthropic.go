// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

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

func NewAnthropicProvider(apiKey, model, baseURL string) (*AnthropicProvider, error) {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := anthropic.NewClient(opts...)
	return &AnthropicProvider{client: &client, model: model}, nil
}

func (a *AnthropicProvider) Name() string  { return "anthropic" }
func (a *AnthropicProvider) Model() string { return a.model }

func getExtractionToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "extract_memories",
		Description: anthropic.String("Extract memories from the input text"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: ForAnthropicProps(ForAnthropicExtraction()),
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
	consolidationSchema := addAnthropicConstraints(cloneSchema(ConsolidationSchema()))
	toolParam := anthropic.ToolParam{
		Name:        "consolidate_memories",
		Description: anthropic.String("Consolidate multiple similar memories into a single coherent memory"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: consolidationSchema["properties"].(map[string]interface{}),
			Required:   []string{"content", "confidence", "reasoning"},
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
	customSchema := addAnthropicConstraints(CustomExtractionResponseSchema(req.Schema))
	toolParam := anthropic.ToolParam{
		Name:        "extract_custom_data",
		Description: anthropic.String("Extract structured data according to the provided schema"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: customSchema["properties"].(map[string]interface{}),
			Required:   []string{"extracted_data", "confidence", "reasoning"},
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
	stateSchema := addAnthropicConstraints(StateExtractionResponseSchema())
	toolParam := anthropic.ToolParam{
		Name:        "extract_state",
		Description: anthropic.String("Extract workflow state from agent interactions"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: stateSchema["properties"].(map[string]interface{}),
			Required:   []string{"extracted_state", "changed_fields", "confidence", "reasoning"},
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
	conflictSchema := addAnthropicConstraints(ConflictCheckSchema())
	toolParam := anthropic.ToolParam{
		Name:        "detect_conflict",
		Description: anthropic.String("Detect whether two memories conflict"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: conflictSchema["properties"].(map[string]interface{}),
			Required:   []string{"contradicts", "conflict_type", "confidence", "explanation", "resolution"},
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
	importanceSchema := addAnthropicConstraints(ImportanceReEvalSchema())
	toolParam := anthropic.ToolParam{
		Name:        "reeval_importance",
		Description: anthropic.String("Re-evaluate the importance of a memory given new information"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: importanceSchema["properties"].(map[string]interface{}),
			Required:   []string{"new_importance", "reason", "should_update"},
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
	priorityProps := ForAnthropicProps(ActionPrioritySchema())
	// Add Anthropic-specific descriptions to help the model
	priorityProps["priority_action"].(map[string]interface{})["description"] = "The single most important action"
	priorityProps["action_items"].(map[string]interface{})["description"] = "All items ordered by priority"
	priorityProps["reasoning"].(map[string]interface{})["description"] = "Brief explanation of priority ordering"
	toolParam := anthropic.ToolParam{
		Name:        "prioritize_actions",
		Description: anthropic.String("Return the prioritized action plan"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: priorityProps,
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

func (a *AnthropicProvider) AnalyzeHeartbeatContext(ctx context.Context, req HeartbeatAnalysisRequest) (*HeartbeatAnalysisResponse, error) {
	schema := HeartbeatAnalysisSchemaForVerbosity(req.Verbosity)
	heartbeatProps := ForAnthropicProps(schema)
	// Add Anthropic-specific descriptions to help the model
	heartbeatProps["should_act"].(map[string]interface{})["description"] = "Whether the agent should act"
	heartbeatProps["action_brief"].(map[string]interface{})["description"] = "Summary tailored to autonomy level"
	heartbeatProps["recommended_actions"].(map[string]interface{})["description"] = "Specific actionable items"
	heartbeatProps["reasoning"].(map[string]interface{})["description"] = "Why these actions matter now"
	heartbeatProps["user_facing"].(map[string]interface{})["description"] = "Message to show the user"
	toolParam := anthropic.ToolParam{
		Name:        "heartbeat_analysis",
		Description: anthropic.String("Return the heartbeat context analysis"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: heartbeatProps,
		},
	}

	prompt := FormatHeartbeatAnalysisPrompt(req)
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are an AI agent's memory and planning system. Use the heartbeat_analysis tool to return your assessment."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("heartbeat_analysis"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic heartbeat analysis failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result HeartbeatAnalysisResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic heartbeat analysis response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error) {
	graphProps := ForAnthropicProps(GraphSummarySchema())
	// Add Anthropic-specific description
	graphProps["summary"].(map[string]interface{})["description"] = "Natural language summary of connections"
	toolParam := anthropic.ToolParam{
		Name:        "summarize_graph",
		Description: anthropic.String("Summarize the knowledge graph connections"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: graphProps,
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

func (a *AnthropicProvider) RerankMemories(ctx context.Context, req RerankRequest) (*RerankResponse, error) {
	toolParam := anthropic.ToolParam{
		Name:        "rerank_memories",
		Description: anthropic.String("Re-rank memory candidates by relevance to the query"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: ForAnthropicProps(RerankSchema()),
		},
	}

	prompt := FormatRerankPrompt(req)
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: "You are a memory relevance ranker. Use the rerank_memories tool to return your rankings."},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolParam}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("rerank_memories"),
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic rerank failed: %w", err)
	}

	toolInputStr, err := a.extractToolUseInput(resp)
	if err != nil {
		return nil, err
	}

	var result RerankResponse
	if err := json.Unmarshal([]byte(toolInputStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic rerank response: %w", err)
	}

	return &result, nil
}

func (a *AnthropicProvider) IsLite() bool { return false }

// ExtractMemoriesCore is a pass-through — Anthropic handles the full schema fine.
func (a *AnthropicProvider) ExtractMemoriesCore(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error) {
	resp, err := a.ExtractMemories(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Entities = nil
	resp.Relationships = nil
	return resp, nil
}

// ExtractGraph is a pass-through — Anthropic handles the full schema fine.
func (a *AnthropicProvider) ExtractGraph(ctx context.Context, req ExtractionRequest) (*GraphExtractionResponse, error) {
	resp, err := a.ExtractMemories(ctx, req)
	if err != nil {
		return nil, err
	}
	return &GraphExtractionResponse{
		Entities:      resp.Entities,
		Relationships: resp.Relationships,
	}, nil
}
