// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package embedder

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// loadEnv reads a .env file and sets environment variables.
func loadEnv(t *testing.T) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	envPath := filepath.Join(filepath.Dir(filename), "..", ".env")
	f, err := os.Open(envPath)
	if err != nil {
		return // .env not found, rely on existing env vars
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

func getOpenAIKey(t *testing.T) string {
	t.Helper()
	loadEnv(t)
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}
	return key
}

func TestOpenAIEmbedder_NewOpenAI(t *testing.T) {
	t.Run("default model dimensions", func(t *testing.T) {
		emb := NewOpenAI("fake-key", "text-embedding-3-small")
		if emb.Dimensions() != 1536 {
			t.Errorf("Dimensions = %d, want 1536", emb.Dimensions())
		}
	})

	t.Run("large model dimensions", func(t *testing.T) {
		emb := NewOpenAI("fake-key", "text-embedding-3-large")
		if emb.Dimensions() != 3072 {
			t.Errorf("Dimensions = %d, want 3072", emb.Dimensions())
		}
	})
}

func TestOpenAIEmbedder_Embed(t *testing.T) {
	key := getOpenAIKey(t)
	emb := NewOpenAI(key, "text-embedding-3-small")

	embedding, err := emb.Embed(context.Background(), "Hello, world!")
	if err != nil {
		t.Fatalf("Embed error = %v", err)
	}
	if len(embedding) != 1536 {
		t.Errorf("embedding length = %d, want 1536", len(embedding))
	}

	// Verify it's not all zeros
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

func TestOpenAIEmbedder_EmbedBatch(t *testing.T) {
	key := getOpenAIKey(t)
	emb := NewOpenAI(key, "text-embedding-3-small")

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
			if len(e) != 1536 {
				t.Errorf("embedding[%d] length = %d, want 1536", i, len(e))
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

func TestOpenAIEmbedder_SimilarTexts(t *testing.T) {
	key := getOpenAIKey(t)
	emb := NewOpenAI(key, "text-embedding-3-small")

	embeddings, err := emb.EmbedBatch(context.Background(), []string{
		"I love pizza",
		"Pizza is my favorite food",
		"Quantum physics experiments",
	})
	if err != nil {
		t.Fatalf("EmbedBatch error = %v", err)
	}

	// Similar texts (pizza) should have higher cosine similarity than dissimilar
	simPizza := cosineSim(embeddings[0], embeddings[1])
	simDiff := cosineSim(embeddings[0], embeddings[2])

	if simPizza <= simDiff {
		t.Errorf("similar texts similarity (%f) should be > dissimilar (%f)", simPizza, simDiff)
	}
}

// cosineSim computes cosine similarity between two vectors.
func cosineSim(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrt64(normA) * sqrt64(normB))
}

func sqrt64(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}
