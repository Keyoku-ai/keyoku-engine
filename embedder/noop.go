package embedder

import "context"

// NoopEmbedder returns zero vectors. Useful for testing without API calls.
type NoopEmbedder struct {
	dims int
}

// NewNoop creates a no-op embedder that returns zero vectors of the given dimension.
func NewNoop(dimensions int) *NoopEmbedder {
	return &NoopEmbedder{dims: dimensions}
}

func (n *NoopEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, n.dims), nil
}

func (n *NoopEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, n.dims)
	}
	return result, nil
}

func (n *NoopEmbedder) Dimensions() int {
	return n.dims
}
