// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package embedder

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// GeminiEmbedder implements Embedder using Google's Gemini embedding API.
type GeminiEmbedder struct {
	client *genai.Client
	model  string
	dims   int
}

// NewGemini creates a Gemini embedder using the google.golang.org/genai SDK.
// If overrideDims > 0, the API is asked to truncate output to that many dimensions
// (via OutputDimensionality), and the HNSW index is sized accordingly.
func NewGemini(apiKey, model string, overrideDims int) (*GeminiEmbedder, error) {
	return NewGeminiWithBackend(apiKey, model, overrideDims, "", "")
}

// NewGeminiWithBackend creates a Gemini embedder with explicit backend selection.
// backend: "vertex" for Vertex AI, anything else for Google AI Studio (default).
// project: GCP project ID, required for Vertex AI.
func NewGeminiWithBackend(apiKey, model string, overrideDims int, backend, project string) (*GeminiEmbedder, error) {
	if model == "" {
		model = "gemini-embedding-001"
	}
	dims := 3072 // gemini-embedding-001 default
	if model == "text-embedding-004" {
		dims = 768
	}
	if overrideDims > 0 {
		dims = overrideDims
	}
	ctx := context.Background()

	cfg := &genai.ClientConfig{}
	if backend == "vertex" {
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = project
		cfg.Location = "us-central1"
		// Vertex AI uses ADC — no API key.
	} else {
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = apiKey
	}

	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return &GeminiEmbedder{client: client, model: model, dims: dims}, nil
}

func (g *GeminiEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	batched, err := g.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batched) == 0 {
		return nil, fmt.Errorf("Gemini returned no embeddings")
	}
	return batched[0], nil
}

func (g *GeminiEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	contents := make([]*genai.Content, len(texts))
	for i, t := range texts {
		contents[i] = genai.NewContentFromText(t, "")
	}

	var ecCfg *genai.EmbedContentConfig
	if g.dims > 0 {
		od := int32(g.dims)
		ecCfg = &genai.EmbedContentConfig{OutputDimensionality: &od}
	}
	resp, err := g.client.Models.EmbedContent(ctx, g.model, contents, ecCfg)
	if err != nil {
		return nil, fmt.Errorf("Gemini embedding failed: %w", err)
	}

	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("Gemini returned %d embeddings for %d inputs", len(resp.Embeddings), len(texts))
	}

	result := make([][]float32, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		result[i] = emb.Values
	}
	return result, nil
}

func (g *GeminiEmbedder) Dimensions() int {
	return g.dims
}
