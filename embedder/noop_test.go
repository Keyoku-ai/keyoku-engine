// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package embedder

import (
	"context"
	"testing"
)

func TestNoopEmbedder_Dimensions(t *testing.T) {
	tests := []int{3, 128, 1536, 3072}
	for _, dims := range tests {
		emb := NewNoop(dims)
		if got := emb.Dimensions(); got != dims {
			t.Errorf("Dimensions() = %d, want %d", got, dims)
		}
	}
}

func TestNoopEmbedder_Embed(t *testing.T) {
	emb := NewNoop(5)
	vec, err := emb.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed error = %v", err)
	}
	if len(vec) != 5 {
		t.Fatalf("Embed len = %d, want 5", len(vec))
	}
	for i, v := range vec {
		if v != 0 {
			t.Errorf("Embed[%d] = %v, want 0", i, v)
		}
	}
}

func TestNoopEmbedder_EmbedBatch(t *testing.T) {
	emb := NewNoop(3)

	t.Run("multiple texts", func(t *testing.T) {
		results, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
		if err != nil {
			t.Fatalf("EmbedBatch error = %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("EmbedBatch len = %d, want 3", len(results))
		}
		for i, vec := range results {
			if len(vec) != 3 {
				t.Errorf("EmbedBatch[%d] len = %d, want 3", i, len(vec))
			}
		}
	})

	t.Run("empty input", func(t *testing.T) {
		results, err := emb.EmbedBatch(context.Background(), []string{})
		if err != nil {
			t.Fatalf("EmbedBatch error = %v", err)
		}
		if len(results) != 0 {
			t.Errorf("EmbedBatch(empty) len = %d, want 0", len(results))
		}
	})
}
