// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package embedder

import (
	"context"
	"os"
	"testing"
)

func getGeminiKey(t *testing.T) string {
	t.Helper()
	loadEnv(t)
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set, skipping integration test")
	}
	return key
}

func TestGeminiEmbedder_NewGemini(t *testing.T) {
	t.Run("default model dimensions", func(t *testing.T) {
		emb, err := NewGemini("fake-key", "", 0)
		if err != nil {
			t.Fatalf("NewGemini error = %v", err)
		}
		if emb.Dimensions() != 3072 {
			t.Errorf("Dimensions = %d, want 3072", emb.Dimensions())
		}
	})

	t.Run("custom model", func(t *testing.T) {
		emb, err := NewGemini("fake-key", "gemini-embedding-001", 0)
		if err != nil {
			t.Fatalf("NewGemini error = %v", err)
		}
		if emb.model != "gemini-embedding-001" {
			t.Errorf("model = %q, want gemini-embedding-001", emb.model)
		}
	})
}

func TestGeminiEmbedder_Embed(t *testing.T) {
	key := getGeminiKey(t)
	emb, err := NewGemini(key, "gemini-embedding-001")
	if err != nil {
		t.Fatalf("NewGemini error = %v", err)
	}

	embedding, err := emb.Embed(context.Background(), "Hello, world!")
	if err != nil {
		t.Fatalf("Embed error = %v", err)
	}
	if len(embedding) != 3072 {
		t.Errorf("embedding length = %d, want 3072", len(embedding))
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

func TestGeminiEmbedder_EmbedBatch(t *testing.T) {
	key := getGeminiKey(t)
	emb, err := NewGemini(key, "gemini-embedding-001")
	if err != nil {
		t.Fatalf("NewGemini error = %v", err)
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
			if len(e) != 3072 {
				t.Errorf("embedding[%d] length = %d, want 3072", i, len(e))
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

func TestGeminiEmbedder_SimilarTexts(t *testing.T) {
	key := getGeminiKey(t)
	emb, err := NewGemini(key, "gemini-embedding-001")
	if err != nil {
		t.Fatalf("NewGemini error = %v", err)
	}

	embeddings, err := emb.EmbedBatch(context.Background(), []string{
		"I love pizza",
		"Pizza is my favorite food",
		"Quantum physics experiments",
	})
	if err != nil {
		t.Fatalf("EmbedBatch error = %v", err)
	}

	simPizza := cosineSim(embeddings[0], embeddings[1])
	simDiff := cosineSim(embeddings[0], embeddings[2])

	if simPizza <= simDiff {
		t.Errorf("similar texts similarity (%f) should be > dissimilar (%f)", simPizza, simDiff)
	}
}
