// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package engine

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// BenchmarkTier1CacheSearch benchmarks brute-force cosine search over the hot cache.
func BenchmarkTier1CacheSearch(b *testing.B) {
	config := DefaultTieredRetrieverConfig()
	config.HotCacheSize = 500
	tr := NewTieredRetriever(&mockStore{}, config, nil)

	// Populate cache with 500 memories
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		mem := &storage.Memory{
			ID:       randomID(i),
			EntityID: "bench-entity",
			Content:  "benchmark memory content",
			State:    storage.StateActive,
		}
		vec := randomEmbedding(rng, 384) // typical small model dimension
		tr.OnMemoryCreated(mem, vec)
	}

	query := randomEmbedding(rng, 384)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.cache.SearchWithEntityFilter(query, "bench-entity", 10, 0.0)
	}
}

// BenchmarkRankCalculator benchmarks the ranking formula.
func BenchmarkRankCalculator(b *testing.B) {
	rc := &RankCalculator{}
	now := time.Now()
	lastWeek := now.Add(-7 * 24 * time.Hour)
	mem := &storage.Memory{
		Importance:     0.7,
		AccessCount:    15,
		LastAccessedAt: &lastWeek,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc.Rank(mem)
	}
}

// BenchmarkRankMemories benchmarks sorting N memories by rank.
func BenchmarkRankMemories(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		memories := generateMemories(n)
		rc := &RankCalculator{}

		b.Run(randomID(n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rc.RankMemories(memories)
			}
		})
	}
}

// BenchmarkMergeResults benchmarks merging two result sets.
func BenchmarkMergeResults(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	a := make([]*storage.SimilarityResult, 50)
	bResults := make([]*storage.SimilarityResult, 50)
	for i := 0; i < 50; i++ {
		a[i] = &storage.SimilarityResult{
			Memory:     &storage.Memory{ID: randomID(i)},
			Similarity: rng.Float64(),
		}
		bResults[i] = &storage.SimilarityResult{
			Memory:     &storage.Memory{ID: randomID(i + 50)},
			Similarity: rng.Float64(),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mergeResults(a, bResults, 20)
	}
}

// BenchmarkDecodeEmbedding benchmarks decoding a float32 embedding from bytes.
func BenchmarkDecodeEmbedding(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vec := randomEmbedding(rng, 1536) // OpenAI ada-002 dimension
	mem := &storage.Memory{
		Embedding: encodeTestEmbedding(vec),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeEmbeddingFromMemory(mem)
	}
}

// --- helpers ---

func randomID(i int) string {
	return "mem-" + string(rune('A'+i%26)) + string(rune('a'+i/26%26))
}

func randomEmbedding(rng *rand.Rand, dims int) []float32 {
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = rng.Float32()
	}
	// Normalize
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

func encodeTestEmbedding(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4+0] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func generateMemories(n int) []*storage.Memory {
	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	memories := make([]*storage.Memory, n)
	for i := 0; i < n; i++ {
		lastAccess := now.Add(-time.Duration(rng.Intn(720)) * time.Hour)
		memories[i] = &storage.Memory{
			ID:             randomID(i),
			Importance:     rng.Float64(),
			AccessCount:    rng.Intn(100),
			LastAccessedAt: &lastAccess,
		}
	}
	return memories
}
