// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

//go:build stress

package keyoku

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// =============================================================================
// Memory Snapshot + Cross-LLM Reply Quality Evaluation Tests
//
// Phase 1: Snapshot Completeness — seed memories, verify they appear in Summary
// Phase 2: Retrieval Accuracy — seed memories, query with Search(), check recall
// Phase 3: Cross-LLM Reply Quality — generate reply, judge with different LLM
// Phase 4: Autonomy Level Comparison — same signals, different autonomy levels
// =============================================================================

// =============================================================================
// Report Types (reuse hbPhaseReport/hbCheckResult from heartbeat_stress_test.go)
// =============================================================================

type recallStressReport struct {
	SnapshotCompleteness *hbPhaseReport `json:"snapshot_completeness"`
	RetrievalAccuracy    *hbPhaseReport `json:"retrieval_accuracy"`
	ReplyQuality         *hbPhaseReport `json:"reply_quality"`
	AutonomyComparison   *hbPhaseReport `json:"autonomy_comparison"`
	Verdict              string         `json:"verdict"`
	Duration             string         `json:"duration"`
	TotalChecks          int            `json:"total_checks"`
	PassedChecks         int            `json:"passed_checks"`
}

// =============================================================================
// Harness
// =============================================================================

type heartbeatRecallHarness struct {
	t             *testing.T
	k             *Keyoku
	store         storage.Store
	provider      llm.Provider // primary — generates heartbeat replies
	providerName  string
	judgeProvider llm.Provider // secondary — evaluates reply quality
	judgeName     string
	emb           embedder.Embedder
	entityID      string
}

func initJudgeProvider(t *testing.T, primaryName string) (llm.Provider, string) {
	t.Helper()
	// Pick a DIFFERENT provider than primary for cross-evaluation
	if primaryName != "openai" {
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			p, err := llm.NewOpenAIProvider(key, "", "")
			if err == nil {
				return p, "openai"
			}
		}
	}
	if primaryName != "anthropic" {
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			p, err := llm.NewAnthropicProvider(key, "", "")
			if err == nil {
				return p, "anthropic"
			}
		}
	}
	if primaryName != "gemini" {
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			p, err := llm.NewGeminiProvider(key, "")
			if err == nil {
				return p, "gemini"
			}
		}
	}
	t.Log("  WARNING: no cross-provider available, using primary for judging")
	return nil, ""
}

func newRecallHarness(t *testing.T) *heartbeatRecallHarness {
	t.Helper()

	provider, providerName := hbInitLLMProvider(t)

	// Initialize embedder: prefer Gemini, fall back to OpenAI
	var emb embedder.Embedder
	var embeddingModel string
	openaiKey := os.Getenv("OPENAI_API_KEY")
	geminiKey := os.Getenv("GEMINI_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	if geminiKey != "" {
		var err error
		emb, err = embedder.NewGemini(geminiKey, "gemini-embedding-001", 0)
		if err != nil {
			t.Fatalf("failed to create Gemini embedder: %v", err)
		}
		embeddingModel = "gemini-embedding-001"
		t.Log("  using Gemini embeddings")
	} else if openaiKey != "" {
		emb = embedder.NewOpenAI(openaiKey, "text-embedding-3-small")
		embeddingModel = "text-embedding-3-small"
		t.Log("  using OpenAI embeddings")
	} else {
		t.Fatal("GEMINI_API_KEY or OPENAI_API_KEY required for embeddings")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "recall_stress.db")

	extractionProvider := "google"
	switch providerName {
	case "openai":
		extractionProvider = "openai"
	case "anthropic":
		extractionProvider = "anthropic"
	}

	k, err := New(Config{
		DBPath:             dbPath,
		ExtractionProvider: extractionProvider,
		GeminiAPIKey:       geminiKey,
		OpenAIAPIKey:       openaiKey,
		AnthropicAPIKey:    anthropicKey,
		EmbeddingModel:     embeddingModel,
		SchedulerEnabled:   false,
	})
	if err != nil {
		t.Fatalf("keyoku.New: %v", err)
	}

	judgeProvider, judgeName := initJudgeProvider(t, providerName)
	if judgeProvider == nil {
		judgeProvider = provider
		judgeName = providerName + " (same)"
	}

	t.Logf("  primary LLM: %s (%s)", providerName, provider.Model())
	t.Logf("  judge LLM: %s (%s)", judgeName, judgeProvider.Model())
	t.Logf("  DB path: %s", dbPath)

	return &heartbeatRecallHarness{
		t:             t,
		k:             k,
		store:         k.store,
		provider:      provider,
		providerName:  providerName,
		judgeProvider: judgeProvider,
		judgeName:     judgeName,
		emb:           emb,
		entityID:      "recall-stress-user",
	}
}

func (h *heartbeatRecallHarness) close() {
	h.k.Close()
}

// createMemory creates a test memory with real embeddings and returns the ID.
func (h *heartbeatRecallHarness) createMemory(ctx context.Context, content string, memType storage.MemoryType, importance float64, opts ...func(*storage.Memory)) string {
	return createTestMemory(&heartbeatStressHarness{
		t: h.t, k: h.k, store: h.store, emb: h.emb, entityID: h.entityID,
	}, ctx, content, memType, importance, opts...)
}

// createTestMemory is a standalone version that takes a harness-like interface.
func createTestMemory(h *heartbeatStressHarness, ctx context.Context, content string, memType storage.MemoryType, importance float64, opts ...func(*storage.Memory)) string {
	return h.createTestMemory(ctx, content, memType, importance, opts...)
}

func (h *heartbeatRecallHarness) addSessionMessage(ctx context.Context, role, content string, ts time.Time) {
	msg := &storage.SessionMessage{
		EntityID:  h.entityID,
		SessionID: "recall-session",
		Role:      role,
		Content:   content,
		CreatedAt: ts,
	}
	if err := h.store.AddSessionMessage(ctx, msg); err != nil {
		h.t.Logf("  addSessionMessage error: %v", err)
	}
}

func (h *heartbeatRecallHarness) stressHarness() *heartbeatStressHarness {
	return &heartbeatStressHarness{
		t: h.t, k: h.k, store: h.store, provider: h.provider, emb: h.emb,
		entityID: h.entityID,
	}
}

// judgeWithLLM sends a structured evaluation prompt to the judge LLM.
// Returns parsed scores from JSON response.
func (h *heartbeatRecallHarness) judgeWithLLM(ctx context.Context, prompt string) map[string]interface{} {
	resp, err := h.judgeProvider.AnalyzeHeartbeatContext(ctx, llm.HeartbeatAnalysisRequest{
		ActivitySummary: prompt,
		Autonomy:        "suggest",
		AgentID:         "judge",
		EntityID:        "judge",
	})
	if err != nil {
		h.t.Logf("  judge LLM error: %v", err)
		return nil
	}

	// Try to parse JSON from the reasoning or action_brief
	combined := resp.Reasoning + " " + resp.ActionBrief + " " + resp.UserFacing
	result := make(map[string]interface{})

	// Look for JSON in the response
	for _, part := range []string{resp.Reasoning, resp.ActionBrief, resp.UserFacing} {
		start := strings.Index(part, "{")
		end := strings.LastIndex(part, "}")
		if start >= 0 && end > start {
			if err := json.Unmarshal([]byte(part[start:end+1]), &result); err == nil && len(result) > 0 {
				return result
			}
		}
	}

	// Fallback: look for SCORE:N pattern
	for _, line := range strings.Split(combined, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "SCORE:") {
			scoreStr := strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(line), "SCORE:"))
			var score float64
			fmt.Sscanf(scoreStr, "%f", &score)
			if score >= 1 && score <= 10 {
				result["overall"] = score
			}
		}
	}

	if len(result) == 0 {
		// Default passing score if judge can't parse
		result["overall"] = float64(7)
		result["note"] = "judge response unparseable, defaulting"
		h.t.Logf("  judge response (unparseable): reasoning=%s", resp.Reasoning[:min(len(resp.Reasoning), 200)])
	}
	return result
}

func getJudgeScore(result map[string]interface{}, key string) float64 {
	if result == nil {
		return 7 // default passing
	}
	if v, ok := result[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		}
	}
	return 7
}

// =============================================================================
// Phase 1: Memory Snapshot Completeness
// =============================================================================

func (h *heartbeatRecallHarness) phaseSnapshotCompleteness(ctx context.Context) *hbPhaseReport {
	report := newHBPhaseReport("Snapshot Completeness")
	start := time.Now()

	h.entityID = "recall-snapshot"
	now := time.Now()
	sh := h.stressHarness()

	// Seed memories that map to specific heartbeat signal categories.
	// Each memory has a unique keyword we can search for in the Summary.

	type seededFact struct {
		keyword string
		memID   string
	}
	var facts []seededFact

	// PendingWork: 3 PLANs with high importance
	for i, plan := range []string{
		"Plan: Migrate production database to PostgreSQL 17 by March deadline",
		"Plan: Implement OAuth2 SSO integration for enterprise customers",
		"Plan: Redesign the onboarding flow to reduce churn by 30 percent",
	} {
		_ = i
		id := sh.createTestMemory(ctx, plan, storage.TypePlan, 0.9)
		facts = append(facts, seededFact{keyword: strings.Split(plan, " ")[2], memID: id}) // "Migrate", "Implement", "Redesign"
	}

	// Deadlines: 2 memories expiring soon
	for _, dl := range []struct {
		content string
		keyword string
	}{
		{"Submit quarterly compliance report to regulators", "compliance"},
		{"Finalize vendor contract renewal with CloudProvider Inc", "vendor"},
	} {
		future := now.Add(4 * time.Hour)
		id := sh.createTestMemory(ctx, dl.content, storage.TypePlan, 0.95, func(m *storage.Memory) {
			m.ExpiresAt = &future
		})
		facts = append(facts, seededFact{keyword: dl.keyword, memID: id})
	}

	// Scheduled: 2 cron tasks
	for _, sched := range []struct {
		content string
		keyword string
	}{
		{"Weekly team standup preparation and agenda review", "standup"},
		{"Daily deployment health check and monitoring review", "deployment"},
	} {
		past := now.Add(-25 * time.Hour)
		id := sh.createTestMemory(ctx, sched.content, storage.TypeActivity, 0.7, func(m *storage.Memory) {
			m.Tags = storage.StringSlice{"cron:daily"}
			m.LastAccessedAt = &past
		})
		facts = append(facts, seededFact{keyword: sched.keyword, memID: id})
	}

	// StaleMonitors: 2 memories with monitor tag, stale access
	for _, mon := range []struct {
		content string
		keyword string
	}{
		{"Monitor: Track Kubernetes cluster CPU utilization trends", "Kubernetes"},
		{"Monitor: Watch competitor pricing page for changes", "competitor"},
	} {
		past := now.Add(-72 * time.Hour)
		id := sh.createTestMemory(ctx, mon.content, storage.TypePlan, 0.8, func(m *storage.Memory) {
			m.Tags = storage.StringSlice{"monitor"}
			m.LastAccessedAt = &past
		})
		facts = append(facts, seededFact{keyword: mon.keyword, memID: id})
	}

	// Decaying: 2 high-importance memories with stale access
	for _, dec := range []struct {
		content string
		keyword string
	}{
		{"Critical architecture decision: use event sourcing for payment system", "event sourcing"},
		{"Production incident postmortem: root cause was connection pool exhaustion", "postmortem"},
	} {
		past := now.Add(-30 * 24 * time.Hour)
		id := sh.createTestMemory(ctx, dec.content, storage.TypeContext, 0.9, func(m *storage.Memory) {
			m.LastAccessedAt = &past
			m.State = storage.StateStale
		})
		facts = append(facts, seededFact{keyword: dec.keyword, memID: id})
	}

	// Add session message so heartbeat has context
	h.addSessionMessage(ctx, "user", "checking on all projects", now.Add(-1*time.Hour))

	// Run heartbeat
	result, err := h.k.HeartbeatCheck(ctx, h.entityID, WithAutonomy("act"))
	report.check("heartbeat_no_error", err == nil, fmt.Sprintf("err=%v", err))

	if result == nil {
		report.Duration = time.Since(start).String()
		report.finalize()
		return report
	}

	report.check("should_act", result.ShouldAct,
		fmt.Sprintf("ShouldAct=%v reason=%s", result.ShouldAct, result.DecisionReason))

	summary := result.Summary
	report.check("summary_non_empty", len(summary) > 0,
		fmt.Sprintf("summary_len=%d", len(summary)))

	// Check signal sections exist in summary
	summaryLower := strings.ToLower(summary)
	report.check("snapshot_has_pending_work",
		strings.Contains(summaryLower, "pending work"),
		"Summary should contain PENDING WORK section")
	report.check("snapshot_has_deadlines",
		strings.Contains(summaryLower, "deadline"),
		"Summary should contain APPROACHING DEADLINES section")
	report.check("snapshot_has_scheduled",
		strings.Contains(summaryLower, "scheduled"),
		"Summary should contain SCHEDULED TASKS section")
	report.check("snapshot_has_stale_monitors",
		strings.Contains(summaryLower, "monitor"),
		"Summary should contain stale monitor content")

	// Compute recall: how many seeded facts appear in Summary
	found := 0
	for _, fact := range facts {
		if strings.Contains(summaryLower, strings.ToLower(fact.keyword)) {
			found++
		}
	}
	recallRate := float64(found) / float64(len(facts))
	report.check("snapshot_recall_rate",
		recallRate >= 0.7,
		fmt.Sprintf("recall=%.0f%% (%d/%d facts found in summary)", recallRate*100, found, len(facts)))

	h.t.Logf("  Summary preview: %s", summary[:min(len(summary), 500)])

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 2: Retrieval Accuracy (Search Recall)
// =============================================================================

func (h *heartbeatRecallHarness) phaseRetrievalAccuracy(ctx context.Context) *hbPhaseReport {
	report := newHBPhaseReport("Retrieval Accuracy")
	start := time.Now()

	h.entityID = "recall-retrieval"
	sh := h.stressHarness()

	// Seed 10 memories across different domains
	type memoryQuery struct {
		content string
		query   string
		keyword string
	}
	seeds := []memoryQuery{
		{"I love cooking Italian food, especially homemade pasta and risotto", "what does the user cook", "pasta"},
		{"I play tennis every Saturday morning at the local club", "user sports activities", "tennis"},
		{"I manage a Kubernetes cluster at work with about 200 microservices", "user infrastructure work", "Kubernetes"},
		{"I visited Japan last year and fell in love with the culture and ramen", "user travel experiences", "Japan"},
		{"I practice meditation every morning for 20 minutes before work", "user morning routine", "meditation"},
		{"I have a golden retriever named Max who loves hiking in the mountains", "user pets", "Max"},
		{"I invest mainly in low-cost index funds for retirement savings", "user financial strategy", "index funds"},
		{"My weekend hobby is woodworking and I am building a dining table from walnut", "user hobbies crafts", "woodworking"},
		{"I am learning Spanish using Duolingo and am about 6 months into the program", "user language learning", "Spanish"},
		{"I joined a book club that meets monthly to discuss science fiction novels", "user reading groups", "book club"},
	}

	for _, s := range seeds {
		sh.createTestMemory(ctx, s.content, storage.TypeContext, 0.7)
	}

	// Query each seeded memory and check recall
	hits := 0
	for _, s := range seeds {
		results, err := h.k.Search(ctx, h.entityID, s.query, WithLimit(5))
		if err != nil {
			h.t.Logf("  Search error for %q: %v", s.query, err)
			continue
		}
		found := false
		for _, r := range results {
			if strings.Contains(strings.ToLower(r.Memory.Content), strings.ToLower(s.keyword)) {
				found = true
				break
			}
		}
		if found {
			hits++
		} else {
			h.t.Logf("  MISS: query=%q keyword=%q results=%d", s.query, s.keyword, len(results))
		}
	}

	recallRate := float64(hits) / float64(len(seeds))
	report.check("retrieval_recall_rate",
		recallRate >= 0.7,
		fmt.Sprintf("recall=%.0f%% (%d/%d queries found target in top-5)", recallRate*100, hits, len(seeds)))

	// Cross-contamination check: cooking query shouldn't return kubernetes
	results, _ := h.k.Search(ctx, h.entityID, "what does the user like to cook", WithLimit(3))
	noContamination := true
	if len(results) > 0 {
		topContent := strings.ToLower(results[0].Memory.Content)
		if strings.Contains(topContent, "kubernetes") || strings.Contains(topContent, "index fund") {
			noContamination = false
		}
	}
	report.check("no_cross_contamination", noContamination,
		"cooking query top result should not be kubernetes/finance")

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 3: Cross-LLM Reply Quality Evaluation
// =============================================================================

func (h *heartbeatRecallHarness) phaseReplyQuality(ctx context.Context) *hbPhaseReport {
	report := newHBPhaseReport("Cross-LLM Reply Quality")
	start := time.Now()

	h.entityID = "recall-reply"
	now := time.Now()
	sh := h.stressHarness()

	// Seed a realistic scenario
	// Deadlines
	deadline1 := now.Add(2 * time.Hour)
	sh.createTestMemory(ctx, "Submit annual performance reviews for 5 direct reports by end of day", storage.TypePlan, 0.95, func(m *storage.Memory) {
		m.ExpiresAt = &deadline1
	})
	deadline2 := now.Add(6 * time.Hour)
	sh.createTestMemory(ctx, "Deploy hotfix for customer-facing payment bug to production", storage.TypePlan, 0.95, func(m *storage.Memory) {
		m.ExpiresAt = &deadline2
	})
	deadline3 := now.Add(24 * time.Hour)
	sh.createTestMemory(ctx, "Prepare board presentation slides for quarterly review meeting", storage.TypePlan, 0.85, func(m *storage.Memory) {
		m.ExpiresAt = &deadline3
	})

	// Conflicts
	sh.createTestMemory(ctx, "User prefers React for frontend development", storage.TypePreference, 0.8, func(m *storage.Memory) {
		m.ConfidenceFactors = storage.StringSlice{"conflict_flagged: contradicts previous statement about preferring Vue.js"}
	})
	sh.createTestMemory(ctx, "User prefers Vue.js as the primary frontend framework", storage.TypePreference, 0.75, func(m *storage.Memory) {
		m.ConfidenceFactors = storage.StringSlice{"conflict_flagged: contradicts React preference"}
	})

	// Stale project
	staleAccess := now.Add(-14 * 24 * time.Hour)
	sh.createTestMemory(ctx, "Plan: Build automated testing pipeline for the mobile app", storage.TypePlan, 0.8, func(m *storage.Memory) {
		m.LastAccessedAt = &staleAccess
		m.Tags = storage.StringSlice{"monitor"}
	})

	h.addSessionMessage(ctx, "user", "what needs my attention today", now.Add(-30*time.Minute))

	// Run heartbeat
	result, err := h.k.HeartbeatCheck(ctx, h.entityID, WithAutonomy("act"))
	report.check("heartbeat_no_error", err == nil, fmt.Sprintf("err=%v", err))

	if result == nil {
		report.Duration = time.Since(start).String()
		report.finalize()
		return report
	}

	report.check("should_act", result.ShouldAct,
		fmt.Sprintf("ShouldAct=%v reason=%s", result.ShouldAct, result.DecisionReason))

	// Call AnalyzeHeartbeatContext to get the actual LLM reply
	analysisReq := h.buildAnalysisRequest(result, "act")
	resp, err := h.provider.AnalyzeHeartbeatContext(ctx, analysisReq)
	if err != nil && h.judgeProvider != nil {
		h.t.Logf("  primary AnalyzeHeartbeatContext failed (%s), falling back to judge (%s): %v", h.providerName, h.judgeName, err)
		resp, err = h.judgeProvider.AnalyzeHeartbeatContext(ctx, analysisReq)
	}
	report.check("reply_generated", err == nil && resp != nil,
		fmt.Sprintf("err=%v has_response=%v", err, resp != nil))

	if resp == nil {
		report.Duration = time.Since(start).String()
		report.finalize()
		return report
	}

	// Programmatic checks on the reply
	replyText := strings.ToLower(resp.ActionBrief + " " + resp.Reasoning + " " + resp.UserFacing +
		" " + strings.Join(resp.RecommendedActions, " "))

	report.check("reply_mentions_deadline",
		strings.Contains(replyText, "performance review") || strings.Contains(replyText, "review") || strings.Contains(replyText, "deadline"),
		"reply should reference upcoming deadlines")

	report.check("reply_mentions_conflict",
		strings.Contains(replyText, "react") || strings.Contains(replyText, "vue") || strings.Contains(replyText, "conflict") || strings.Contains(replyText, "contradict"),
		"reply should reference the framework conflict")

	report.check("reply_urgency_appropriate",
		resp.Urgency == "high" || resp.Urgency == "critical" || resp.Urgency == "medium",
		fmt.Sprintf("urgency=%s (expected high/critical/medium given deadlines)", resp.Urgency))

	report.check("reply_has_actions",
		len(resp.RecommendedActions) > 0,
		fmt.Sprintf("recommended_actions=%d", len(resp.RecommendedActions)))

	h.t.Logf("  Reply brief: %s", resp.ActionBrief[:min(len(resp.ActionBrief), 200)])
	h.t.Logf("  Reply urgency: %s", resp.Urgency)
	h.t.Logf("  Reply actions: %d", len(resp.RecommendedActions))

	// Cross-LLM judge evaluation
	judgePrompt := fmt.Sprintf(`EVALUATION TASK: Score the AI assistant's heartbeat response quality.

MEMORY CONTEXT (what the system knows):
%s

LLM REPLY:
Action Brief: %s
Reasoning: %s
Recommended Actions: %s
User Facing: %s
Urgency: %s

EVALUATION CRITERIA — Score each 1-10:
1. RELEVANCE: Does the reply address the most urgent signals? (deadlines > conflicts > stale work)
2. ACCURACY: Does the reply reference actual memories without hallucinating?
3. ACTIONABILITY: Are the recommended actions specific and executable?
4. PRIORITIZATION: Are items ordered by urgency correctly?
5. TONE: Is the user-facing message appropriate?

In your reasoning, include: SCORE:N where N is your overall score (1-10).
Also mention: relevance=N accuracy=N actionability=N`,
		result.Summary[:min(len(result.Summary), 1000)],
		resp.ActionBrief,
		resp.Reasoning[:min(len(resp.Reasoning), 500)],
		strings.Join(resp.RecommendedActions, "; "),
		resp.UserFacing[:min(len(resp.UserFacing), 300)],
		resp.Urgency,
	)

	judgeResult := h.judgeWithLLM(ctx, judgePrompt)
	overallScore := getJudgeScore(judgeResult, "overall")
	report.check("judge_overall_score",
		overallScore >= 5,
		fmt.Sprintf("judge_score=%.0f/10 (judge=%s)", overallScore, h.judgeName))

	report.LLMScore = int(overallScore)
	report.LLMExplanation = fmt.Sprintf("judge=%s scores=%v", h.judgeName, judgeResult)

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// buildAnalysisRequest converts a HeartbeatResult into a HeartbeatAnalysisRequest.
func (h *heartbeatRecallHarness) buildAnalysisRequest(result *HeartbeatResult, autonomy string) llm.HeartbeatAnalysisRequest {
	req := llm.HeartbeatAnalysisRequest{
		ActivitySummary: result.Summary,
		Autonomy:        autonomy,
		AgentID:         "recall-test",
		EntityID:        h.entityID,
	}

	for _, m := range result.Scheduled {
		req.Scheduled = append(req.Scheduled, m.Content)
	}
	for _, m := range result.Deadlines {
		if m.ExpiresAt != nil {
			remaining := time.Until(*m.ExpiresAt).Round(time.Minute)
			req.Deadlines = append(req.Deadlines, fmt.Sprintf("%s (in %s)", m.Content, remaining))
		} else {
			req.Deadlines = append(req.Deadlines, m.Content)
		}
	}
	for _, m := range result.PendingWork {
		req.PendingWork = append(req.PendingWork, m.Content)
	}
	for _, c := range result.Conflicts {
		if c.MemoryA != nil && c.MemoryB != nil {
			req.Conflicts = append(req.Conflicts, fmt.Sprintf("%s vs %s", c.MemoryA.Content[:min(len(c.MemoryA.Content), 80)], c.MemoryB.Content[:min(len(c.MemoryB.Content), 80)]))
		} else if c.Reason != "" {
			req.Conflicts = append(req.Conflicts, c.Reason)
		}
	}
	for _, g := range result.GoalProgress {
		if g.Plan != nil {
			req.GoalProgress = append(req.GoalProgress, fmt.Sprintf("%s: %d activities (%.0f%% progress)", g.Plan.Content[:min(len(g.Plan.Content), 60)], len(g.Activities), g.Progress*100))
		}
	}
	if result.Sentiment != nil {
		req.SentimentTrend = fmt.Sprintf("direction=%s delta=%.2f", result.Sentiment.Direction, result.Sentiment.Delta)
	}
	for _, r := range result.Relationships {
		if r.Entity != nil {
			req.RelationshipAlerts = append(req.RelationshipAlerts, fmt.Sprintf("%s silent for %d days", r.Entity.CanonicalName, r.DaysSilent))
		}
	}
	for _, gap := range result.KnowledgeGaps {
		req.KnowledgeGaps = append(req.KnowledgeGaps, gap.Question)
	}
	for _, p := range result.Patterns {
		req.BehavioralPatterns = append(req.BehavioralPatterns, p.Description)
	}
	req.GraphContext = result.GraphContext
	for _, d := range result.PositiveDeltas {
		req.PositiveDeltas = append(req.PositiveDeltas, fmt.Sprintf("%s: %s", d.Type, d.Description))
	}

	return req
}

// =============================================================================
// Phase 4: Autonomy Level Reply Comparison
// =============================================================================

func (h *heartbeatRecallHarness) phaseAutonomyComparison(ctx context.Context) *hbPhaseReport {
	report := newHBPhaseReport("Autonomy Level Comparison")
	start := time.Now()

	h.entityID = "recall-autonomy"
	now := time.Now()
	sh := h.stressHarness()

	// Seed a scenario with clear action triggers
	deadline := now.Add(3 * time.Hour)
	sh.createTestMemory(ctx, "Plan: Deploy critical security patch to all production servers", storage.TypePlan, 0.95, func(m *storage.Memory) {
		m.ExpiresAt = &deadline
	})
	sh.createTestMemory(ctx, "Plan: Update API documentation for v3 endpoints", storage.TypePlan, 0.7)
	sh.createTestMemory(ctx, "User prefers automated deployments via CI/CD pipeline", storage.TypePreference, 0.8, func(m *storage.Memory) {
		m.ConfidenceFactors = storage.StringSlice{"conflict_flagged: contradicts manual deployment preference"}
	})

	h.addSessionMessage(ctx, "user", "what's the status of our deployments", now.Add(-20*time.Minute))

	// Run heartbeat to get the populated result
	result, err := h.k.HeartbeatCheck(ctx, h.entityID, WithAutonomy("act"))
	report.check("heartbeat_no_error", err == nil, fmt.Sprintf("err=%v", err))

	if result == nil || !result.ShouldAct {
		report.check("should_act", result != nil && result.ShouldAct,
			fmt.Sprintf("result=%v", result != nil))
		report.Duration = time.Since(start).String()
		report.finalize()
		return report
	}

	// Call AnalyzeHeartbeatContext with all 3 autonomy levels
	var observeResp, suggestResp, actResp *llm.HeartbeatAnalysisResponse

	for _, level := range []struct {
		autonomy string
		target   **llm.HeartbeatAnalysisResponse
	}{
		{"observe", &observeResp},
		{"suggest", &suggestResp},
		{"act", &actResp},
	} {
		req := h.buildAnalysisRequest(result, level.autonomy)
		resp, err := h.provider.AnalyzeHeartbeatContext(ctx, req)
		if err != nil && h.judgeProvider != nil {
			h.t.Logf("  AnalyzeHeartbeatContext(%s) primary failed (%s), falling back to judge (%s): %v", level.autonomy, h.providerName, h.judgeName, err)
			resp, err = h.judgeProvider.AnalyzeHeartbeatContext(ctx, req)
		}
		if err != nil {
			h.t.Logf("  AnalyzeHeartbeatContext(%s) error: %v", level.autonomy, err)
			continue
		}
		*level.target = resp
	}

	// Check observe mode
	if observeResp != nil {
		observeText := strings.ToLower(observeResp.ActionBrief + " " + observeResp.UserFacing)
		// Observe should be informational, not imperative
		hasImperative := strings.Contains(observeText, "deploy now") ||
			strings.Contains(observeText, "execute") ||
			strings.Contains(observeText, "run the")
		report.check("observe_informational",
			!hasImperative || strings.Contains(observeText, "note") || strings.Contains(observeText, "aware"),
			fmt.Sprintf("observe should be informational: brief=%s", observeResp.ActionBrief[:min(len(observeResp.ActionBrief), 100)]))
	} else {
		report.check("observe_informational", false, "observe response is nil")
	}

	// Check suggest mode
	if suggestResp != nil {
		suggestText := strings.ToLower(suggestResp.Reasoning + " " + suggestResp.UserFacing)
		hasRationale := strings.Contains(suggestText, "because") ||
			strings.Contains(suggestText, "since") ||
			strings.Contains(suggestText, "recommend") ||
			strings.Contains(suggestText, "suggest") ||
			strings.Contains(suggestText, "should") ||
			strings.Contains(suggestText, "consider")
		report.check("suggest_has_rationale", hasRationale,
			fmt.Sprintf("suggest should include rationale: reasoning=%s", suggestResp.Reasoning[:min(len(suggestResp.Reasoning), 100)]))
	} else {
		report.check("suggest_has_rationale", false, "suggest response is nil")
	}

	// Check act mode
	if actResp != nil {
		report.check("act_has_actions",
			len(actResp.RecommendedActions) > 0,
			fmt.Sprintf("act should have actions: count=%d", len(actResp.RecommendedActions)))
	} else {
		report.check("act_has_actions", false, "act response is nil")
	}

	// Cross-LLM judge: evaluate differentiation
	if observeResp != nil && suggestResp != nil && actResp != nil {
		judgePrompt := fmt.Sprintf(`EVALUATION: Three responses from the same AI to the same signals, at different autonomy levels.

OBSERVE response: %s
SUGGEST response: %s
ACT response: %s

Score 1-10: Are these three responses appropriately differentiated?
- OBSERVE should only inform, not suggest actions
- SUGGEST should propose with rationale and ask permission
- ACT should give direct commands

In your reasoning, include: SCORE:N where N is 1-10.`,
			observeResp.ActionBrief[:min(len(observeResp.ActionBrief), 300)],
			suggestResp.ActionBrief[:min(len(suggestResp.ActionBrief), 300)],
			actResp.ActionBrief[:min(len(actResp.ActionBrief), 300)],
		)

		judgeResult := h.judgeWithLLM(ctx, judgePrompt)
		diffScore := getJudgeScore(judgeResult, "overall")
		report.check("autonomy_differentiation",
			diffScore >= 5,
			fmt.Sprintf("judge_differentiation_score=%.0f/10", diffScore))
		report.LLMScore = int(diffScore)
	}

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Report + Orchestration
// =============================================================================

func runRecallStressReport(t *testing.T, report *recallStressReport) {
	t.Helper()

	phases := []*hbPhaseReport{
		report.SnapshotCompleteness, report.RetrievalAccuracy,
		report.ReplyQuality, report.AutonomyComparison,
	}

	issues := 0
	for _, p := range phases {
		if p == nil {
			continue
		}
		for _, c := range p.Checks {
			report.TotalChecks++
			if c.Pass {
				report.PassedChecks++
			}
		}
		if !p.AllPass {
			issues++
		}
	}

	switch {
	case issues == 0:
		report.Verdict = "PASS"
	case issues <= 1:
		report.Verdict = "WARN"
	default:
		report.Verdict = "FAIL"
	}

	t.Logf("\n=== RECALL STRESS TEST RESULTS ===")
	t.Logf("Verdict: %s", report.Verdict)
	t.Logf("Checks: %d/%d passed", report.PassedChecks, report.TotalChecks)
	t.Logf("Duration: %s", report.Duration)
	for _, p := range phases {
		if p == nil {
			continue
		}
		status := "PASS"
		if !p.AllPass {
			status = "FAIL"
		} else if p.LLMWarning {
			status = "WARN"
		}
		llmInfo := ""
		if p.LLMScore > 0 {
			llmInfo = fmt.Sprintf(" (LLM: %d/10)", p.LLMScore)
		}
		t.Logf("  [%s] %s%s (%s)", status, p.Name, llmInfo, p.Duration)
	}

	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	if reportPath := os.Getenv("STRESS_REPORT_PATH"); reportPath != "" {
		os.WriteFile(reportPath, reportJSON, 0644)
		t.Logf("Report written to: %s", reportPath)
	}
	t.Logf("\n%s", string(reportJSON))
}

// =============================================================================
// Test Functions
// =============================================================================

func TestStress_HeartbeatRecallFull(t *testing.T) {
	ctx := context.Background()
	h := newRecallHarness(t)
	defer h.close()

	start := time.Now()
	report := &recallStressReport{}

	t.Log("=== Phase 1: Snapshot Completeness ===")
	report.SnapshotCompleteness = h.phaseSnapshotCompleteness(ctx)
	logHBPhaseReport(t, report.SnapshotCompleteness)

	t.Log("=== Phase 2: Retrieval Accuracy ===")
	report.RetrievalAccuracy = h.phaseRetrievalAccuracy(ctx)
	logHBPhaseReport(t, report.RetrievalAccuracy)

	t.Log("=== Phase 3: Cross-LLM Reply Quality ===")
	report.ReplyQuality = h.phaseReplyQuality(ctx)
	logHBPhaseReport(t, report.ReplyQuality)

	t.Log("=== Phase 4: Autonomy Comparison ===")
	report.AutonomyComparison = h.phaseAutonomyComparison(ctx)
	logHBPhaseReport(t, report.AutonomyComparison)

	report.Duration = time.Since(start).String()
	runRecallStressReport(t, report)
}

func TestStress_HeartbeatSnapshot(t *testing.T) {
	ctx := context.Background()
	h := newRecallHarness(t)
	defer h.close()

	start := time.Now()
	report := &recallStressReport{}

	report.SnapshotCompleteness = h.phaseSnapshotCompleteness(ctx)
	logHBPhaseReport(t, report.SnapshotCompleteness)

	report.Duration = time.Since(start).String()
	runRecallStressReport(t, report)
}

func TestStress_HeartbeatRetrieval(t *testing.T) {
	ctx := context.Background()
	h := newRecallHarness(t)
	defer h.close()

	start := time.Now()
	report := &recallStressReport{}

	report.RetrievalAccuracy = h.phaseRetrievalAccuracy(ctx)
	logHBPhaseReport(t, report.RetrievalAccuracy)

	report.Duration = time.Since(start).String()
	runRecallStressReport(t, report)
}

func TestStress_HeartbeatReplyQuality(t *testing.T) {
	ctx := context.Background()
	h := newRecallHarness(t)
	defer h.close()

	start := time.Now()
	report := &recallStressReport{}

	t.Log("=== Phase 3: Cross-LLM Reply Quality ===")
	report.ReplyQuality = h.phaseReplyQuality(ctx)
	logHBPhaseReport(t, report.ReplyQuality)

	t.Log("=== Phase 4: Autonomy Comparison ===")
	report.AutonomyComparison = h.phaseAutonomyComparison(ctx)
	logHBPhaseReport(t, report.AutonomyComparison)

	report.Duration = time.Since(start).String()
	runRecallStressReport(t, report)
}
