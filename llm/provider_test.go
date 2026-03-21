// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package llm

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// loadEnv reads a .env file and sets environment variables.
func loadEnv(t *testing.T) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	envPath := filepath.Join(filepath.Dir(filename), "..", ".env")
	f, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(parts[0], parts[1])
		}
	}
}

func getKey(t *testing.T, envVar string) string {
	t.Helper()
	loadEnv(t)
	key := os.Getenv(envVar)
	if key == "" {
		t.Skipf("%s not set, skipping integration test", envVar)
	}
	return key
}

// --- NewProvider tests (no API calls) ---

func TestNewProvider_NoAPIKey(t *testing.T) {
	_, err := NewProvider(ProviderConfig{Provider: "openai", APIKey: ""})
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestNewProvider_UnknownProvider(t *testing.T) {
	_, err := NewProvider(ProviderConfig{Provider: "unknown", APIKey: "key"})
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

// TestNewProvider_Ollama verifies the Ollama branch: no API key required, default/custom base URL and model.
// The returned provider is OpenAI-compatible (Ollama exposes /v1); Name() is "openai" but it points at Ollama.
func TestNewProvider_Ollama(t *testing.T) {
	t.Run("no API key required", func(t *testing.T) {
		p, err := NewProvider(ProviderConfig{Provider: "ollama"})
		if err != nil {
			t.Fatalf("Ollama with no API key: error = %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
		if p.Model() == "" {
			t.Error("Model() should not be empty")
		}
	})

	t.Run("custom model and base URL", func(t *testing.T) {
		p, err := NewProvider(ProviderConfig{
			Provider: "ollama",
			Model:    "llama3.2",
			BaseURL:  "http://myhost:11434",
		})
		if err != nil {
			t.Fatalf("Ollama with custom config: error = %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil provider")
		}
		if p.Model() != "llama3.2" {
			t.Errorf("Model() = %q, want llama3.2", p.Model())
		}
	})
}

func TestNewProvider_OpenAI(t *testing.T) {
	p, err := NewProvider(ProviderConfig{Provider: "openai", APIKey: "fake-key"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Model() != "gpt-5-mini" {
		t.Errorf("Model = %q, want default", p.Model())
	}
}

func TestNewProvider_Gemini(t *testing.T) {
	key := getKey(t, "GEMINI_API_KEY")
	p, err := NewProvider(ProviderConfig{Provider: "google", APIKey: key})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if p.Name() != "google" {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestNewProvider_Anthropic(t *testing.T) {
	p, err := NewProvider(ProviderConfig{Provider: "anthropic", APIKey: "fake-key"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Model() != "claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want default", p.Model())
	}
}

func TestNewProvider_CustomModel(t *testing.T) {
	p, err := NewProvider(ProviderConfig{Provider: "openai", APIKey: "fake-key", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if p.Model() != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", p.Model())
	}
}

// --- Prompt formatting tests (no API calls) ---

func TestFormatPrompt(t *testing.T) {
	prompt := FormatPrompt(ExtractionRequest{
		Content: "I work at Google as a software engineer",
	})
	if !strings.Contains(prompt, "I work at Google") {
		t.Error("prompt missing content")
	}
	if !strings.Contains(prompt, "No recent context") {
		t.Error("prompt should show no context")
	}
	if !strings.Contains(prompt, "No existing memories") {
		t.Error("prompt should show no memories")
	}
}

func TestFormatPrompt_WithContext(t *testing.T) {
	prompt := FormatPrompt(ExtractionRequest{
		Content:         "I like pizza",
		ConversationCtx: []string{"Hi there", "How are you?"},
		ExistingMemories: []string{"User lives in Seattle"},
	})
	if !strings.Contains(prompt, "Turn 1:") {
		t.Error("prompt missing conversation turns")
	}
	if !strings.Contains(prompt, "User lives in Seattle") {
		t.Error("prompt missing existing memories")
	}
}

func TestFormatConsolidationPrompt(t *testing.T) {
	prompt := FormatConsolidationPrompt(ConsolidationRequest{Memories: []string{"User likes pizza", "User enjoys Italian food"}})
	if !strings.Contains(prompt, "1. User likes pizza") {
		t.Error("prompt missing first memory")
	}
	if !strings.Contains(prompt, "2. User enjoys Italian food") {
		t.Error("prompt missing second memory")
	}
}

func TestFormatCustomExtractionPrompt(t *testing.T) {
	prompt := FormatCustomExtractionPrompt(CustomExtractionRequest{
		Content:    "Meeting with John at 3pm",
		SchemaName: "meeting",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"attendee": map[string]any{"type": "string"},
				"time":     map[string]any{"type": "string"},
			},
		},
	})
	if !strings.Contains(prompt, "meeting") {
		t.Error("prompt missing schema name")
	}
	if !strings.Contains(prompt, "Meeting with John") {
		t.Error("prompt missing content")
	}
}

func TestFormatStateExtractionPrompt(t *testing.T) {
	prompt := FormatStateExtractionPrompt(StateExtractionRequest{
		Content:    "Order confirmed",
		SchemaName: "order_state",
		Schema:     map[string]any{"type": "object"},
		AgentID:    "agent-1",
	})
	if !strings.Contains(prompt, "order_state") {
		t.Error("prompt missing schema name")
	}
	if !strings.Contains(prompt, "agent-1") {
		t.Error("prompt missing agent ID")
	}
}

func TestFormatStateExtractionPrompt_Defaults(t *testing.T) {
	prompt := FormatStateExtractionPrompt(StateExtractionRequest{
		Content:    "test",
		SchemaName: "test",
		Schema:     map[string]any{"type": "object"},
	})
	if !strings.Contains(prompt, "default") {
		t.Error("prompt should use 'default' agent when AgentID is empty")
	}
	if !strings.Contains(prompt, "No existing state") {
		t.Error("prompt should show no existing state")
	}
	if !strings.Contains(prompt, "No transition rules") {
		t.Error("prompt should show no transition rules")
	}
}

func TestFormatHeartbeatAnalysisPrompt_WithConversationHistory(t *testing.T) {
	prompt := FormatHeartbeatAnalysisPrompt(HeartbeatAnalysisRequest{
		ActivitySummary:  "PR #42 merged, deployment pending",
		RelevantMemories: []string{"[importance:0.9] PR #42 needs code review"},
		ConversationHistory: []string{
			"[Mar 21 10:00] user: How is PR #42 going?",
			"[Mar 21 10:01] assistant: PR #42 has been merged",
		},
		Autonomy:    "suggest",
		EntityID:    "user-1",
		PendingWork: []string{"Deploy v2.1 to staging"},
		SignalCount: 1,
	})

	if !strings.Contains(prompt, "PR #42") {
		t.Error("prompt should contain conversation about PR #42")
	}
	if !strings.Contains(prompt, "Recent Conversation History") {
		t.Error("prompt should contain conversation history section")
	}
	if !strings.Contains(prompt, "How is PR #42 going?") {
		t.Error("prompt should include actual conversation messages")
	}
	if !strings.Contains(prompt, "suggest") {
		t.Error("prompt should include autonomy level")
	}
	if !strings.Contains(prompt, "Deploy v2.1") {
		t.Error("prompt should include pending work signals")
	}
}

func TestFormatHeartbeatAnalysisPrompt_NoConversation(t *testing.T) {
	prompt := FormatHeartbeatAnalysisPrompt(HeartbeatAnalysisRequest{
		ActivitySummary: "Some signals detected",
		Autonomy:        "observe",
		EntityID:        "user-1",
	})

	if !strings.Contains(prompt, "observe") {
		t.Error("prompt should include autonomy level")
	}
	// Should still generate a valid prompt without conversation history
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

// --- Integration tests (require API keys, make real API calls) ---

// geminiCtx returns a context with a 60-second timeout for Gemini API calls.
func geminiCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func TestGemini_ExtractMemories(t *testing.T) {
	key := getKey(t, "GEMINI_API_KEY")

	p, err := NewGeminiProvider(key, "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatalf("NewGeminiProvider error = %v", err)
	}

	ctx, cancel := geminiCtx()
	defer cancel()
	resp, err := p.ExtractMemories(ctx, ExtractionRequest{
		Content: "My name is Alice and I work at Google. I love hiking on weekends.",
	})
	if err != nil {
		t.Fatalf("ExtractMemories error = %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected at least one extracted memory")
	}

	// Check that memories have valid types and scores
	for _, mem := range resp.Memories {
		if mem.Content == "" {
			t.Error("memory has empty content")
		}
		if !MemoryType(mem.Type).IsValid() {
			t.Errorf("invalid memory type: %q", mem.Type)
		}
		if mem.Importance < 0 || mem.Importance > 1 {
			t.Errorf("importance out of range: %v", mem.Importance)
		}
		if mem.Confidence < 0 || mem.Confidence > 1 {
			t.Errorf("confidence out of range: %v", mem.Confidence)
		}
	}
}

func TestGemini_ConsolidateMemories(t *testing.T) {
	key := getKey(t, "GEMINI_API_KEY")

	p, err := NewGeminiProvider(key, "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatalf("NewGeminiProvider error = %v", err)
	}

	ctx, cancel := geminiCtx()
	defer cancel()
	resp, err := p.ConsolidateMemories(ctx, ConsolidationRequest{
		Memories: []string{
			"User likes pizza",
			"User enjoys Italian food, especially pasta",
			"User prefers Italian cuisine for dinner",
		},
	})
	if err != nil {
		t.Fatalf("ConsolidateMemories error = %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty consolidated content")
	}
}

func TestGemini_ExtractWithSchema(t *testing.T) {
	key := getKey(t, "GEMINI_API_KEY")

	p, err := NewGeminiProvider(key, "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatalf("NewGeminiProvider error = %v", err)
	}

	ctx, cancel := geminiCtx()
	defer cancel()
	resp, err := p.ExtractWithSchema(ctx, CustomExtractionRequest{
		Content:    "Meeting with John Smith tomorrow at 3pm in Conference Room B",
		SchemaName: "meeting",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"attendee": map[string]any{"type": "string"},
				"time":     map[string]any{"type": "string"},
				"location": map[string]any{"type": "string"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ExtractWithSchema error = %v", err)
	}
	if resp.ExtractedData == nil {
		t.Error("expected non-nil extracted data")
	}
	if resp.Confidence <= 0 {
		t.Errorf("confidence = %v, want > 0", resp.Confidence)
	}
}

func TestGemini_ExtractState(t *testing.T) {
	key := getKey(t, "GEMINI_API_KEY")

	p, err := NewGeminiProvider(key, "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatalf("NewGeminiProvider error = %v", err)
	}

	ctx, cancel := geminiCtx()
	defer cancel()
	resp, err := p.ExtractState(ctx, StateExtractionRequest{
		Content:    "The order has been shipped and tracking number is ABC123",
		SchemaName: "order_state",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":          map[string]any{"type": "string"},
				"tracking_number": map[string]any{"type": "string"},
			},
		},
		AgentID: "order-agent",
	})
	if err != nil {
		t.Fatalf("ExtractState error = %v", err)
	}
	if resp.ExtractedState == nil {
		t.Error("expected non-nil extracted state")
	}
}

func TestOpenAI_ExtractMemories(t *testing.T) {
	key := getKey(t, "OPENAI_API_KEY")

	p, err := NewOpenAIProvider(key, "gpt-5-mini", "")
	if err != nil {
		t.Fatalf("NewOpenAIProvider error = %v", err)
	}

	resp, err := p.ExtractMemories(context.Background(), ExtractionRequest{
		Content: "I just moved to San Francisco. I'm a data scientist at Meta.",
	})
	if err != nil {
		t.Fatalf("ExtractMemories error = %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected at least one extracted memory")
	}

	for _, mem := range resp.Memories {
		if mem.Content == "" {
			t.Error("memory has empty content")
		}
		if !MemoryType(mem.Type).IsValid() {
			t.Errorf("invalid memory type: %q", mem.Type)
		}
	}
}

func TestOpenAI_ConsolidateMemories(t *testing.T) {
	key := getKey(t, "OPENAI_API_KEY")

	p, err := NewOpenAIProvider(key, "gpt-5-mini", "")
	if err != nil {
		t.Fatalf("NewOpenAIProvider error = %v", err)
	}

	resp, err := p.ConsolidateMemories(context.Background(), ConsolidationRequest{
		Memories: []string{
			"User works at Meta",
			"User is a data scientist at Meta",
		},
	})
	if err != nil {
		t.Fatalf("ConsolidateMemories error = %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty consolidated content")
	}
}

func TestAnthropic_ExtractMemories(t *testing.T) {
	key := getKey(t, "ANTHROPIC_API_KEY")

	p, err := NewAnthropicProvider(key, "claude-sonnet-4-5-20250929", "")
	if err != nil {
		t.Fatalf("NewAnthropicProvider error = %v", err)
	}

	resp, err := p.ExtractMemories(context.Background(), ExtractionRequest{
		Content: "I'm learning Go programming. My favorite editor is VS Code.",
	})
	if err != nil {
		t.Fatalf("ExtractMemories error = %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected at least one extracted memory")
	}

	for _, mem := range resp.Memories {
		if mem.Content == "" {
			t.Error("memory has empty content")
		}
		if !MemoryType(mem.Type).IsValid() {
			t.Errorf("invalid memory type: %q", mem.Type)
		}
	}
}

func TestAnthropic_ConsolidateMemories(t *testing.T) {
	key := getKey(t, "ANTHROPIC_API_KEY")

	p, err := NewAnthropicProvider(key, "claude-sonnet-4-5-20250929", "")
	if err != nil {
		t.Fatalf("NewAnthropicProvider error = %v", err)
	}

	resp, err := p.ConsolidateMemories(context.Background(), ConsolidationRequest{
		Memories: []string{
			"User is learning Go programming",
			"User is studying the Go language",
		},
	})
	if err != nil {
		t.Fatalf("ConsolidateMemories error = %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty consolidated content")
	}
}

// getOllamaLLMConfig returns base URL and chat model for Ollama LLM integration tests.
// Skips the test if OLLAMA_BASE_URL is not set (no local Ollama server).
func getOllamaLLMConfig(t *testing.T) (baseURL, model string) {
	t.Helper()
	loadEnv(t)
	baseURL = os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_BASE_URL not set, skipping Ollama LLM integration test")
	}
	model = os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3.2"
	}
	return baseURL, model
}

// TestOllama_ExtractMemories is an integration test that calls a real Ollama server.
// Run with: OLLAMA_BASE_URL=http://localhost:11434 [OLLAMA_MODEL=llama3.2] go test -run TestOllama_ExtractMemories ./llm/...
func TestOllama_ExtractMemories(t *testing.T) {
	baseURL, model := getOllamaLLMConfig(t)

	p, err := NewProvider(ProviderConfig{
		Provider: "ollama",
		BaseURL:  baseURL,
		Model:    model,
	})
	if err != nil {
		t.Fatalf("NewProvider(ollama) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := p.ExtractMemories(ctx, ExtractionRequest{
		Content: "My name is Sam and I work at Acme. I like coffee.",
	})
	if err != nil {
		t.Fatalf("ExtractMemories error = %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("expected at least one extracted memory")
	}
	for _, mem := range resp.Memories {
		if mem.Content == "" {
			t.Error("memory has empty content")
		}
		if !MemoryType(mem.Type).IsValid() {
			t.Errorf("invalid memory type: %q", mem.Type)
		}
	}
}
