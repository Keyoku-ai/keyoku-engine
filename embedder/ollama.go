// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package embedder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultOllamaMaxInputChars is the default per-text character limit sent to Ollama.
// Most Ollama embedding models (nomic-embed-text, mxbai-embed-large) have a ~2048-token
// context window. At ~4 chars/token this gives a safe ceiling of 8 000 characters.
const defaultOllamaMaxInputChars = 8000

// OllamaEmbedder implements Embedder using the native Ollama /api/embed endpoint.
// It is designed for local Ollama deployments and enforces the dimension constraints
// of models available in Ollama (768 or 1024).
type OllamaEmbedder struct {
	baseURL       string
	model         string
	dims          int
	maxInputChars int // per-text character limit before sending to Ollama
	client        *http.Client
	pullClient    *http.Client // no timeout; model pulls can take many minutes
}

// NewOllama creates an OllamaEmbedder. baseURL defaults to http://localhost:11434.
// dims must be 768 or 1024 to match the supported Ollama embedding models.
func NewOllama(baseURL, model string, dims int) (*OllamaEmbedder, error) {
	if dims <= 0 {
		return nil, fmt.Errorf("ollama embedder: dimensions must be a positive integer, got %d", dims)
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaEmbedder{
		baseURL:       baseURL,
		model:         model,
		dims:          dims,
		maxInputChars: defaultOllamaMaxInputChars,
		client:        &http.Client{Timeout: 300 * time.Second},
		pullClient:    &http.Client{}, // no timeout; rely on ctx deadline
	}, nil
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama: empty embedding response")
	}
	return results[0], nil
}

func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Truncate texts that exceed the model's context window.
	truncated := texts
	if e.maxInputChars > 0 {
		truncated = make([]string, len(texts))
		for i, t := range texts {
			if len(t) > e.maxInputChars {
				truncated[i] = t[:e.maxInputChars]
			} else {
				truncated[i] = t
			}
		}
	}

	reqBody := ollamaEmbedRequest{
		Model: e.model,
		Input: truncated,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	url := e.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: embedding request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("ollama: unmarshal response: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: empty embedding response")
	}

	embeddings := make([][]float32, len(result.Embeddings))
	for i, vec := range result.Embeddings {
		embeddings[i] = make([]float32, len(vec))
		for j, v := range vec {
			embeddings[i][j] = float32(v)
		}
	}
	return embeddings, nil
}

// EnsureModel checks whether the configured model is available on the Ollama server
// and pulls it if it is not. Pull progress is streamed by Ollama but consumed silently;
// the method only returns once the pull completes or ctx is cancelled.
// For large models the pull can take many minutes — set an appropriate ctx deadline.
func (e *OllamaEmbedder) EnsureModel(ctx context.Context) error {
	available, err := e.isModelAvailable(ctx)
	if err != nil {
		return fmt.Errorf("ollama: list models: %w", err)
	}
	if available {
		return nil
	}
	return e.pullModel(ctx)
}

// isModelAvailable calls GET /api/tags and returns true when e.model is listed.
// Matching is done after stripping the ":latest" tag so that "nomic-embed-text"
// matches "nomic-embed-text:latest" returned by the server.
func (e *OllamaEmbedder) isModelAvailable(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", e.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama /api/tags returned %d: %s", resp.StatusCode, string(body))
	}

	var tagsResp ollamaTagsResponse
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return false, fmt.Errorf("unmarshal tags response: %w", err)
	}

	want := normalizeModelName(e.model)
	for _, m := range tagsResp.Models {
		if normalizeModelName(m.Name) == want || normalizeModelName(m.Model) == want {
			return true, nil
		}
	}
	return false, nil
}

// pullModel streams POST /api/pull and reads the NDJSON response line by line.
// Each line is either a progress event or an error. The function returns nil only
// when a {"status":"success"} line is received.
func (e *OllamaEmbedder) pullModel(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{"model": e.model})
	if err != nil {
		return fmt.Errorf("ollama: marshal pull request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: create pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.pullClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: pull request: %w", err)
	}
	defer resp.Body.Close()

	// Non-200 responses have a plain JSON error body, not NDJSON.
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama /api/pull returned %d: %s", resp.StatusCode, string(errBody))
	}

	// Read the NDJSON stream line by line.
	type pullEvent struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event pullEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue // skip malformed lines
		}
		if event.Error != "" {
			return fmt.Errorf("ollama: pull failed: %s", event.Error)
		}
		if event.Status == "success" {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama: reading pull stream: %w", err)
	}
	return fmt.Errorf("ollama: pull stream ended without a success status")
}

// normalizeModelName strips the ":latest" suffix for comparison so that
// "nomic-embed-text" and "nomic-embed-text:latest" are treated as the same model.
func normalizeModelName(name string) string {
	return strings.TrimSuffix(name, ":latest")
}

type ollamaTagsResponse struct {
	Models []ollamaModelSummary `json:"models"`
}

type ollamaModelSummary struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}


func (e *OllamaEmbedder) Dimensions() int {
	return e.dims
}
