// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package embedder

import (
	"context"
	"os"
	"testing"
)

func getOllamaConfig(t *testing.T) (baseURL, model string) {
	t.Helper()
	loadEnv(t)
	baseURL = os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_BASE_URL not set, skipping integration test")
	}
	model = os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	return baseURL, model
}

func TestNewOllama_DimensionValidation(t *testing.T) {
	valid := []int{256, 384, 512, 768, 1024, 1536, 3072}
	for _, dims := range valid {
		emb, err := NewOllama("", "any-model", dims)
		if err != nil {
			t.Errorf("NewOllama dims=%d: unexpected error: %v", dims, err)
		}
		if emb == nil {
			t.Errorf("NewOllama dims=%d: expected non-nil embedder", dims)
		} else if emb.Dimensions() != dims {
			t.Errorf("Dimensions() = %d, want %d", emb.Dimensions(), dims)
		}
	}

	invalid := []int{0, -1, -768}
	for _, dims := range invalid {
		_, err := NewOllama("", "any-model", dims)
		if err == nil {
			t.Errorf("NewOllama dims=%d: expected error, got nil", dims)
		}
	}
}

func TestNewOllama_DefaultBaseURL(t *testing.T) {
	emb, err := NewOllama("", "any-model", 768)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb.baseURL != "http://localhost:11434" {
		t.Errorf("baseURL = %q, want %q", emb.baseURL, "http://localhost:11434")
	}
}

func TestNewOllama_CustomBaseURL(t *testing.T) {
	emb, err := NewOllama("http://myserver:11434", "any-model", 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb.baseURL != "http://myserver:11434" {
		t.Errorf("baseURL = %q, want %q", emb.baseURL, "http://myserver:11434")
	}
}

func TestNormalizeModelName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"nomic-embed-text", "nomic-embed-text"},
		{"nomic-embed-text:latest", "nomic-embed-text"},
		{"mxbai-embed-large:latest", "mxbai-embed-large"},
		{"mxbai-embed-large:v1", "mxbai-embed-large:v1"},
	}
	for _, c := range cases {
		if got := normalizeModelName(c.in); got != c.want {
			t.Errorf("normalizeModelName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOllamaEmbedder_EnsureModel(t *testing.T) {
	baseURL, model := getOllamaConfig(t)

	emb, err := NewOllama(baseURL, model, 768)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}

	if err := emb.EnsureModel(context.Background()); err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}
}

func TestOllamaEmbedder_Embed(t *testing.T) {
	baseURL, model := getOllamaConfig(t)

	emb, err := NewOllama(baseURL, model, 768)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}

	embedding, err := emb.Embed(context.Background(), "Hello, world!")
	if err != nil {
		t.Fatalf("Embed error = %v", err)
	}
	if len(embedding) == 0 {
		t.Fatal("embedding is empty")
	}

	allZero := true
	for _, v := range embedding {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("embedding is all zeros")
	}
}

func TestOllamaEmbedder_EmbedBatch(t *testing.T) {
	baseURL, model := getOllamaConfig(t)

	emb, err := NewOllama(baseURL, model, 768)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}

	t.Run("multiple texts", func(t *testing.T) {
		embeddings, err := emb.EmbedBatch(context.Background(), []string{
			"The cat sat on the mat",
			"Machine learning is fascinating",
		})
		if err != nil {
			t.Fatalf("EmbedBatch error = %v", err)
		}
		if len(embeddings) != 2 {
			t.Fatalf("embeddings count = %d, want 2", len(embeddings))
		}
		for i, e := range embeddings {
			if len(e) == 0 {
				t.Errorf("embedding[%d] is empty", i)
			}
		}
	})

	t.Run("empty input", func(t *testing.T) {
		embeddings, err := emb.EmbedBatch(context.Background(), nil)
		if err != nil {
			t.Fatalf("EmbedBatch error = %v", err)
		}
		if embeddings != nil {
			t.Errorf("expected nil for empty input, got %d", len(embeddings))
		}
	})
}
