// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package embedder

import "context"

// Embedder defines the interface for text-to-vector embedding.
// Implementations can use OpenAI, local models (Ollama, ONNX), etc.
type Embedder interface {
	// Embed converts a single text to a vector embedding.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch converts multiple texts to vector embeddings.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the output vector dimensionality.
	Dimensions() int
}
