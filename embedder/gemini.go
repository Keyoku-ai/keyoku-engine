// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

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
func NewGemini(apiKey, model string) (*GeminiEmbedder, error) {
	if model == "" {
		model = "text-embedding-004"
	}
	dims := 768 // text-embedding-004 default
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
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
	contents := make([]*genai.Content, len(texts))
	for i, t := range texts {
		contents[i] = genai.NewContentFromText(t, "")
	}

	resp, err := g.client.Models.EmbedContent(ctx, g.model, contents, nil)
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
