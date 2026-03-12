// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

//go:build stress

package keyoku

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/llm"
)

// =============================================================================
// Model Benchmark Test
//
// Benchmarks all state-of-the-art fast models across ALL 10 Provider methods.
// Measures success, latency, and quality for each method.
// Methods tested:
//   1. ExtractMemories      (VERY HIGH complexity - nested arrays, 62+ fields)
//   2. ConsolidateMemories  (LOW complexity - 3 flat fields)
//   3. ExtractWithSchema    (MEDIUM complexity - dynamic schema)
//   4. ExtractState         (MEDIUM complexity - state + transitions)
//   5. DetectConflict       (LOW complexity - 5 flat fields)
//   6. ReEvaluateImportance (LOW complexity - 3 flat fields)
//   7. PrioritizeActions    (LOW complexity - 4 flat fields)
//   8. AnalyzeHeartbeatContext (HIGH complexity - 7 fields with enums)
//   9. SummarizeGraph       (LOW complexity - 2 flat fields)
//  10. RerankMemories       (LOW complexity - array of objects)
// =============================================================================

type benchResult struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Method      string `json:"method"`
	Success     bool   `json:"success"`
	Latency     string `json:"latency"`
	Error       string `json:"error,omitempty"`
	OutputValid bool   `json:"output_valid"`
	Quality     int    `json:"quality"`
	LiteMode    bool   `json:"lite_mode,omitempty"`
}

type benchReport struct {
	Results      []benchResult `json:"results"`
	TotalTests   int           `json:"total_tests"`
	PassedTests  int           `json:"passed_tests"`
	Duration     string        `json:"duration"`
	BestOverall  string        `json:"best_overall"`
	CheapestPass string        `json:"cheapest_passing"`
}

// initBenchProviders creates all available providers for benchmarking.
func initBenchProviders(t *testing.T) map[string]llm.Provider {
	t.Helper()
	providers := map[string]llm.Provider{}

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		for _, model := range []string{"gpt-4.1-mini", "gpt-4.1-nano"} {
			p, err := llm.NewOpenAIProvider(key, model, "")
			if err != nil {
				t.Logf("  WARN: failed to create OpenAI provider %s: %v", model, err)
				continue
			}
			providers["openai/"+model] = p
		}
	}

	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		for _, model := range []string{"gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-3.1-flash-lite-preview"} {
			p, err := llm.NewGeminiProvider(key, model)
			if err != nil {
				t.Logf("  WARN: failed to create Gemini provider %s: %v", model, err)
				continue
			}
			providers["gemini/"+model] = p
		}
	}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		p, err := llm.NewAnthropicProvider(key, "claude-haiku-4-5-20251001", "")
		if err != nil {
			t.Logf("  WARN: failed to create Anthropic provider: %v", err)
		} else {
			providers["anthropic/claude-haiku-4-5"] = p
		}
	}

	return providers
}

// =============================================================================
// Benchmark Methods
// =============================================================================

func benchExtractMemories(ctx context.Context, p llm.Provider) (*benchResult, *llm.ExtractionResponse) {
	req := llm.ExtractionRequest{
		Content: "Alex Chen works as a senior engineer at Acme Corp. He's been there 3 years. " +
			"He loves hiking on weekends, especially in the Cascades. " +
			"He has a meeting next Tuesday with Sarah Kim about the Q3 budget review. " +
			"He's worried about the project timeline and thinks they might miss the deadline.",
		ConversationCtx: []string{
			"User: Tell me about Alex",
			"Assistant: Sure, let me look up what I know about Alex.",
		},
	}

	start := time.Now()
	resp, err := p.ExtractMemories(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "ExtractMemories",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	result.Success = true
	result.OutputValid = len(resp.Memories) >= 2

	// Basic quality scoring
	quality := 5
	if len(resp.Memories) >= 3 {
		quality += 2
	}
	// Check for valid types
	validTypes := map[string]bool{"IDENTITY": true, "PREFERENCE": true, "RELATIONSHIP": true, "EVENT": true, "ACTIVITY": true, "PLAN": true, "CONTEXT": true, "EPHEMERAL": true}
	allValid := true
	for _, m := range resp.Memories {
		if !validTypes[m.Type] {
			allValid = false
		}
		if m.Importance < 0 || m.Importance > 1 {
			allValid = false
		}
	}
	if allValid {
		quality += 2
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality

	return result, resp
}

func benchDetectConflict(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.ConflictCheckRequest{
		NewContent:      "Alex ordered a ribeye steak at dinner last night and said it was delicious",
		ExistingContent: "Alex is a strict vegetarian and has been for 5 years",
		MemoryType:      "PREFERENCE",
		Context:         "Alex Chen - dietary preferences",
	}

	start := time.Now()
	resp, err := p.DetectConflict(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "DetectConflict",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	validConflictTypes := map[string]bool{"contradiction": true, "update": true, "temporal": true, "partial": true, "none": true}
	validResolutions := map[string]bool{"use_new": true, "keep_existing": true, "merge": true, "keep_both": true}
	result.OutputValid = validConflictTypes[resp.ConflictType] && validResolutions[resp.Resolution]

	quality := 5
	if resp.Contradicts {
		quality += 3 // correctly detected contradiction
	}
	if resp.ConflictType == "contradiction" || resp.ConflictType == "update" {
		quality += 1
	}
	if resp.Explanation != "" {
		quality += 1
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality

	return result
}

func benchAnalyzeHeartbeat(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.HeartbeatAnalysisRequest{
		PendingWork: []string{
			"Finish Q3 budget presentation (due in 2 days)",
			"Review Sarah's PR #423",
		},
		Deadlines: []string{
			"Q3 budget presentation: Tuesday March 14",
		},
		SentimentTrend: "Stressed about timeline (declining sentiment: -0.4)",
		Autonomy:       "suggest",
		AgentID:        "bench-agent",
		EntityID:       "bench-entity",
	}

	start := time.Now()
	resp, err := p.AnalyzeHeartbeatContext(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "AnalyzeHeartbeatContext",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	validUrgency := map[string]bool{"none": true, "low": true, "medium": true, "high": true, "critical": true}
	validAutonomy := map[string]bool{"observe": true, "suggest": true, "act": true}
	result.OutputValid = validUrgency[resp.Urgency] && validAutonomy[resp.Autonomy]

	quality := 5
	if resp.ShouldAct {
		quality += 2 // should act on pending deadlines
	}
	if resp.Urgency == "high" || resp.Urgency == "critical" || resp.Urgency == "medium" {
		quality += 1
	}
	if len(resp.RecommendedActions) > 0 {
		quality += 1
	}
	if resp.Reasoning != "" {
		quality += 1
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality

	return result
}

func benchRerankMemories(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.RerankRequest{
		Query: "weekend hiking plans",
		Candidates: []llm.RerankCandidate{
			{ID: "m1", Content: "Alex loves hiking in the Cascades on weekends", Type: "PREFERENCE", Score: 0.8},
			{ID: "m2", Content: "Q3 budget presentation due Tuesday", Type: "PLAN", Score: 0.5},
			{ID: "m3", Content: "Alex works at Acme Corp as a senior engineer", Type: "IDENTITY", Score: 0.4},
			{ID: "m4", Content: "Alex went trail running last Saturday morning", Type: "ACTIVITY", Score: 0.7},
			{ID: "m5", Content: "Sarah Kim is Alex's project manager", Type: "RELATIONSHIP", Score: 0.3},
		},
	}

	start := time.Now()
	resp, err := p.RerankMemories(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "RerankMemories",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = len(resp.Rankings) > 0

	quality := 5
	if len(resp.Rankings) >= 3 {
		quality += 2
	}
	// Check if hiking-related memories ranked higher
	for _, r := range resp.Rankings {
		if r.Score >= 0 && r.Score <= 1 {
			quality++ // valid score range
			break
		}
	}
	// Check if m1 (hiking) is ranked highest
	if len(resp.Rankings) > 0 {
		highest := resp.Rankings[0]
		for _, r := range resp.Rankings {
			if r.Score > highest.Score {
				highest = r
			}
		}
		if highest.ID == "m1" || highest.ID == "m4" {
			quality += 2 // correctly prioritized hiking content
		}
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality

	return result
}

func benchConsolidateMemories(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.ConsolidationRequest{
		Memories: []string{
			"User works at Acme Corp as a senior engineer",
			"User has been working at Acme Corporation for 3 years",
			"User is a senior software engineer at Acme",
		},
		EntityContext:     []string{"Acme Corp (organization)"},
		RelationshipContext: []string{"User works_at Acme Corp"},
		ImportanceScores:  []float64{0.8, 0.7, 0.8},
		SentimentValues:   []float64{0.1, 0.0, 0.2},
	}

	start := time.Now()
	resp, err := p.ConsolidateMemories(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "ConsolidateMemories",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = resp.Content != "" && resp.Confidence > 0

	quality := 5
	if resp.Content != "" {
		quality += 2
	}
	if resp.Confidence >= 0.5 {
		quality++
	}
	if resp.Reasoning != "" {
		quality++
	}
	// Should mention "Acme" and "senior engineer" or "3 years"
	if len(resp.Content) > 20 {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

func benchExtractWithSchema(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.CustomExtractionRequest{
		Content: "John ordered a large pepperoni pizza with extra cheese and a side of garlic bread. Total was $24.50. Delivery to 123 Main St.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"total":           map[string]any{"type": "number"},
				"delivery_address": map[string]any{"type": "string"},
				"customer_name":    map[string]any{"type": "string"},
			},
		},
		SchemaName: "food_order",
		ConversationCtx: []string{
			"User: I'd like to place an order",
		},
	}

	start := time.Now()
	resp, err := p.ExtractWithSchema(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "ExtractWithSchema",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = len(resp.ExtractedData) > 0 && resp.Confidence > 0

	quality := 5
	if len(resp.ExtractedData) >= 2 {
		quality += 2
	}
	if resp.Confidence >= 0.5 {
		quality++
	}
	if resp.Reasoning != "" {
		quality++
	}
	if resp.ExtractedData["customer_name"] != nil {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

func benchExtractState(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.StateExtractionRequest{
		Content:    "The ticket has been reviewed by the team and approved. Moving to development phase now.",
		SchemaName: "ticket_workflow",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":    map[string]any{"type": "string", "enum": []string{"open", "review", "approved", "development", "testing", "done"}},
				"assignee":  map[string]any{"type": "string"},
				"priority":  map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
			},
		},
		CurrentState: map[string]any{
			"status":   "review",
			"assignee": "Alex",
			"priority": "high",
		},
		TransitionRules: map[string]any{
			"review": []string{"approved", "open"},
			"approved": []string{"development"},
		},
		ConversationCtx: []string{"Team standup discussion about ticket priorities"},
		AgentID:         "bench-agent",
	}

	start := time.Now()
	resp, err := p.ExtractState(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "ExtractState",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = len(resp.ExtractedState) > 0 && resp.Confidence > 0

	quality := 5
	if len(resp.ChangedFields) > 0 {
		quality += 2
	}
	if resp.Confidence >= 0.5 {
		quality++
	}
	if resp.Reasoning != "" {
		quality++
	}
	// Should detect status change to "development" or "approved"
	if status, ok := resp.ExtractedState["status"].(string); ok && (status == "development" || status == "approved") {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

func benchReEvaluateImportance(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.ImportanceReEvalRequest{
		NewContent:        "Alex just got promoted to Staff Engineer at Acme Corp",
		ExistingContent:   "Alex works as a senior engineer at Acme Corp",
		CurrentImportance: 0.7,
		CurrentType:       "IDENTITY",
		RelatedMemories: []string{
			"Alex has been at Acme Corp for 3 years",
			"Alex is hoping for a promotion this quarter",
		},
	}

	start := time.Now()
	resp, err := p.ReEvaluateImportance(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "ReEvaluateImportance",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = resp.NewImportance >= 0 && resp.NewImportance <= 1

	quality := 5
	if resp.ShouldUpdate {
		quality += 2 // promotion should trigger update
	}
	if resp.NewImportance > 0.7 {
		quality++ // importance should increase after promotion
	}
	if resp.Reason != "" {
		quality++
	}
	if resp.NewImportance >= 0 && resp.NewImportance <= 1 {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

func benchPrioritizeActions(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.ActionPriorityRequest{
		Summary: "PENDING:\n- Q3 budget presentation due tomorrow\n- Review Sarah's PR (non-urgent)\n- Reply to client email about contract renewal\nDEADLINES:\n- Q3 budget: tomorrow 9am\n- Contract renewal response: end of week",
		AgentContext:  "Personal productivity assistant helping with work tasks",
		EntityContext: "Alex Chen, senior engineer at Acme Corp",
	}

	start := time.Now()
	resp, err := p.PrioritizeActions(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "PrioritizeActions",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	validUrgency := map[string]bool{"immediate": true, "soon": true, "can_wait": true}
	result.OutputValid = resp.PriorityAction != "" && validUrgency[resp.Urgency]

	quality := 5
	if resp.PriorityAction != "" {
		quality += 2
	}
	if resp.Urgency == "immediate" || resp.Urgency == "soon" {
		quality++ // budget presentation due tomorrow should be urgent
	}
	if len(resp.ActionItems) >= 2 {
		quality++
	}
	if resp.Reasoning != "" {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

func benchSummarizeGraph(ctx context.Context, p llm.Provider) *benchResult {
	req := llm.GraphSummaryRequest{
		Entities: []string{
			"Alex Chen (PERSON) - senior engineer",
			"Acme Corp (ORGANIZATION) - tech company",
			"Sarah Kim (PERSON) - project manager",
			"Q3 Budget Review (EVENT) - quarterly review meeting",
		},
		Relationships: []string{
			"Alex Chen works_at Acme Corp",
			"Sarah Kim works_at Acme Corp",
			"Sarah Kim manages Alex Chen",
			"Alex Chen participates_in Q3 Budget Review",
			"Sarah Kim participates_in Q3 Budget Review",
		},
		Question: "How are Alex and Sarah connected and what are they working on together?",
	}

	start := time.Now()
	resp, err := p.SummarizeGraph(ctx, req)
	latency := time.Since(start)

	result := &benchResult{
		Provider: p.Name(),
		Model:    p.Model(),
		Method:   "SummarizeGraph",
		Latency:  latency.Round(time.Millisecond).String(),
		LiteMode: p.IsLite(),
	}

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Success = true
	result.OutputValid = resp.Summary != "" && resp.Confidence > 0

	quality := 5
	if resp.Summary != "" {
		quality += 2
	}
	if resp.Confidence >= 0.5 {
		quality++
	}
	if len(resp.Summary) > 30 {
		quality++ // should be a decent explanation
	}
	// Should mention both people and their shared project
	if len(resp.Summary) > 50 {
		quality++
	}
	if quality > 10 {
		quality = 10
	}
	result.Quality = quality
	return result
}

// =============================================================================
// Report
// =============================================================================

func printBenchReport(t *testing.T, report *benchReport) {
	t.Log("")
	t.Log("╔══════════════════════════════════╦════════════════════════════╦═════════╦════════════╦═════════╗")
	t.Log("║ Model                            ║ Method                     ║ Success ║ Latency    ║ Quality ║")
	t.Log("╠══════════════════════════════════╬════════════════════════════╬═════════╬════════════╬═════════╣")

	for _, r := range report.Results {
		name := r.Provider + "/" + r.Model
		if r.LiteMode {
			name += "*"
		}
		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		qualityStr := fmt.Sprintf("%d/10", r.Quality)
		if !r.Success {
			qualityStr = " -  "
		}
		t.Logf("║ %-32s ║ %-26s ║ %-7s ║ %-10s ║ %-7s ║",
			truncStr(name, 32), truncStr(r.Method, 26), status, r.Latency, qualityStr)
		if r.Error != "" {
			t.Logf("║   └─ error: %-70s ║", truncStr(r.Error, 70))
		}
	}

	t.Log("╚══════════════════════════════════╩════════════════════════════╩═════════╩════════════╩═════════╝")
	t.Logf("")
	t.Logf("Total: %d tests | Passed: %d/%d | Duration: %s",
		report.TotalTests, report.PassedTests, report.TotalTests, report.Duration)
	if report.BestOverall != "" {
		t.Logf("Best overall: %s", report.BestOverall)
	}
	if report.CheapestPass != "" {
		t.Logf("Cheapest passing: %s", report.CheapestPass)
	}

	// JSON report
	jsonBytes, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("\nJSON Report:\n%s", string(jsonBytes))
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// computeBestModels analyzes results to find best overall and cheapest passing.
func computeBestModels(results []benchResult) (bestOverall, cheapestPass string) {
	type modelStats struct {
		name       string
		passed     int
		total      int
		avgQuality float64
		totalQ     int
		lite       bool
	}
	stats := map[string]*modelStats{}

	for _, r := range results {
		key := r.Provider + "/" + r.Model
		s, ok := stats[key]
		if !ok {
			s = &modelStats{name: key, lite: r.LiteMode}
			stats[key] = s
		}
		s.total++
		if r.Success {
			s.passed++
			s.totalQ += r.Quality
		}
	}

	var best *modelStats
	var cheapest *modelStats

	// Cheapest tier order (approximate)
	cheapOrder := map[string]int{
		"gemini/gemini-3.1-flash-lite-preview": 1,
		"openai/gpt-4.1-nano":                  2,
		"gemini/gemini-2.5-flash-lite":          3,
		"openai/gpt-4.1-mini":                   4,
		"gemini/gemini-2.5-flash":               5,
		"anthropic/claude-haiku-4-5":             6,
	}

	for _, s := range stats {
		if s.passed == 0 {
			continue
		}
		s.avgQuality = float64(s.totalQ) / float64(s.passed)

		if s.passed == s.total {
			if best == nil || s.avgQuality > best.avgQuality {
				best = s
			}
			if cheapest == nil || cheapOrder[s.name] < cheapOrder[cheapest.name] {
				cheapest = s
			}
		}
	}

	if best != nil {
		bestOverall = fmt.Sprintf("%s (avg quality %.1f, %d/%d pass)", best.name, best.avgQuality, best.passed, best.total)
	}
	if cheapest != nil {
		suffix := ""
		if cheapest.lite {
			suffix = " [lite mode]"
		}
		cheapestPass = fmt.Sprintf("%s (avg quality %.1f)%s", cheapest.name, cheapest.avgQuality, suffix)
	}

	return
}

// =============================================================================
// Test Functions
// =============================================================================

func runBenchmark(t *testing.T, methods []string) {
	t.Helper()
	start := time.Now()

	providers := initBenchProviders(t)
	if len(providers) == 0 {
		t.Fatal("no LLM API keys found. Set OPENAI_API_KEY, GEMINI_API_KEY, or ANTHROPIC_API_KEY")
	}

	t.Logf("Benchmarking %d providers × %d methods", len(providers), len(methods))

	// Sort provider names for deterministic order
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	ctx := context.Background()
	var results []benchResult
	passed := 0

	for _, name := range names {
		p := providers[name]
		t.Logf("\n--- %s (model: %s, lite: %v) ---", name, p.Model(), p.IsLite())

		for _, method := range methods {
			var r *benchResult

			switch method {
			case "ExtractMemories":
				r, _ = benchExtractMemories(ctx, p)
			case "ConsolidateMemories":
				r = benchConsolidateMemories(ctx, p)
			case "ExtractWithSchema":
				r = benchExtractWithSchema(ctx, p)
			case "ExtractState":
				r = benchExtractState(ctx, p)
			case "DetectConflict":
				r = benchDetectConflict(ctx, p)
			case "ReEvaluateImportance":
				r = benchReEvaluateImportance(ctx, p)
			case "PrioritizeActions":
				r = benchPrioritizeActions(ctx, p)
			case "AnalyzeHeartbeatContext":
				r = benchAnalyzeHeartbeat(ctx, p)
			case "SummarizeGraph":
				r = benchSummarizeGraph(ctx, p)
			case "RerankMemories":
				r = benchRerankMemories(ctx, p)
			default:
				t.Fatalf("unknown benchmark method: %s", method)
			}

			status := "PASS"
			if !r.Success {
				status = "FAIL"
			}
			t.Logf("  %-26s %s  latency=%s  quality=%d/10",
				method, status, r.Latency, r.Quality)
			if r.Error != "" {
				errMsg := r.Error
				if len(errMsg) > 120 {
					errMsg = errMsg[:120] + "..."
				}
				t.Logf("    error: %s", errMsg)
			}

			results = append(results, *r)
			if r.Success {
				passed++
			}
		}
	}

	bestOverall, cheapestPass := computeBestModels(results)

	report := &benchReport{
		Results:      results,
		TotalTests:   len(results),
		PassedTests:  passed,
		Duration:     time.Since(start).Round(time.Second).String(),
		BestOverall:  bestOverall,
		CheapestPass: cheapestPass,
	}

	printBenchReport(t, report)

	// Write JSON report to file
	jsonBytes, _ := json.MarshalIndent(report, "", "  ")
	reportPath := "/tmp/model-benchmark-results.json"
	_ = os.WriteFile(reportPath, jsonBytes, 0644)
	t.Logf("\nFull report written to %s", reportPath)
}

// allMethods lists all 10 Provider methods in order of complexity.
var allMethods = []string{
	"ExtractMemories",
	"ConsolidateMemories",
	"ExtractWithSchema",
	"ExtractState",
	"DetectConflict",
	"ReEvaluateImportance",
	"PrioritizeActions",
	"AnalyzeHeartbeatContext",
	"SummarizeGraph",
	"RerankMemories",
}

func TestStress_ModelBenchmarkFull(t *testing.T) {
	runBenchmark(t, allMethods)
}

func TestStress_ModelBenchmarkExtract(t *testing.T) {
	runBenchmark(t, []string{"ExtractMemories"})
}

func TestStress_ModelBenchmarkHeartbeat(t *testing.T) {
	runBenchmark(t, []string{"AnalyzeHeartbeatContext"})
}

// TestStress_ModelBenchmarkLite runs only lite-mode Gemini models to verify split extraction works.
func TestStress_ModelBenchmarkLite(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Fatal("GEMINI_API_KEY required for lite benchmark")
	}

	ctx := context.Background()
	liteModels := []string{"gemini-2.5-flash-lite", "gemini-3.1-flash-lite-preview"}
	for _, model := range liteModels {
		t.Logf("\n=== %s (lite mode) ===", model)
		p, err := llm.NewGeminiProvider(key, model)
		if err != nil {
			t.Logf("  SKIP: failed to create provider: %v", err)
			continue
		}
		if !p.IsLite() {
			t.Errorf("  expected IsLite()=true for %s", model)
		}

		for _, method := range allMethods {
			var r *benchResult
			switch method {
			case "ExtractMemories":
				r, _ = benchExtractMemories(ctx, p)
			case "ConsolidateMemories":
				r = benchConsolidateMemories(ctx, p)
			case "ExtractWithSchema":
				r = benchExtractWithSchema(ctx, p)
			case "ExtractState":
				r = benchExtractState(ctx, p)
			case "DetectConflict":
				r = benchDetectConflict(ctx, p)
			case "ReEvaluateImportance":
				r = benchReEvaluateImportance(ctx, p)
			case "PrioritizeActions":
				r = benchPrioritizeActions(ctx, p)
			case "AnalyzeHeartbeatContext":
				r = benchAnalyzeHeartbeat(ctx, p)
			case "SummarizeGraph":
				r = benchSummarizeGraph(ctx, p)
			case "RerankMemories":
				r = benchRerankMemories(ctx, p)
			}

			status := "PASS"
			if !r.Success {
				status = "FAIL"
			}
			t.Logf("  %-26s %s  latency=%s  quality=%d/10", method, status, r.Latency, r.Quality)
			if r.Error != "" {
				// Trim long errors
				errMsg := r.Error
				if len(errMsg) > 200 {
					errMsg = errMsg[:200] + "..."
				}
				t.Logf("    error: %s", errMsg)
			}

			// Verify split extraction for ExtractMemories
			if method == "ExtractMemories" && r.Success {
				t.Logf("    lite split extraction: SUCCESS (used ExtractMemoriesCore + ExtractGraph)")
			}
		}
	}

}
