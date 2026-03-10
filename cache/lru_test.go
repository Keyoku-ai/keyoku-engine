// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
package cache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func makeMem(id, entityID string) *storage.Memory {
	return &storage.Memory{ID: id, EntityID: entityID}
}

func makeVec(val float32, dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = val
	}
	return v
}

func TestLRU_PutAndGet(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 3, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), makeVec(0.1, 4))
	c.Put(makeMem("b", "e1"), makeVec(0.2, 4))
	c.Put(makeMem("c", "e1"), makeVec(0.3, 4))

	if c.Len() != 3 {
		t.Fatalf("Expected 3 entries, got %d", c.Len())
	}

	entry, ok := c.Get("b")
	if !ok || entry.Memory.ID != "b" {
		t.Error("Expected to find 'b'")
	}

	_, ok = c.Get("missing")
	if ok {
		t.Error("Expected not to find 'missing'")
	}
}

func TestLRU_Eviction(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 2, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), makeVec(0.1, 4))
	c.Put(makeMem("b", "e1"), makeVec(0.2, 4))
	// Cache is full (a, b). Adding c should evict a (least recently used).
	c.Put(makeMem("c", "e1"), makeVec(0.3, 4))

	if c.Len() != 2 {
		t.Fatalf("Expected 2 entries, got %d", c.Len())
	}

	_, ok := c.Get("a")
	if ok {
		t.Error("Expected 'a' to be evicted")
	}

	_, ok = c.Get("b")
	if !ok {
		t.Error("Expected 'b' to still exist")
	}
	_, ok = c.Get("c")
	if !ok {
		t.Error("Expected 'c' to still exist")
	}
}

func TestLRU_PromotionOnGet(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 2, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), makeVec(0.1, 4))
	c.Put(makeMem("b", "e1"), makeVec(0.2, 4))
	// 'a' is LRU. Accessing it promotes it.
	c.Get("a")
	// Now 'b' is LRU. Adding 'c' should evict 'b'.
	c.Put(makeMem("c", "e1"), makeVec(0.3, 4))

	_, ok := c.Get("a")
	if !ok {
		t.Error("Expected 'a' to survive (was promoted)")
	}
	_, ok = c.Get("b")
	if ok {
		t.Error("Expected 'b' to be evicted")
	}
}

func TestLRU_PromotionOnPut(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 2, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), makeVec(0.1, 4))
	c.Put(makeMem("b", "e1"), makeVec(0.2, 4))
	// Re-putting 'a' promotes it.
	c.Put(makeMem("a", "e1"), makeVec(0.5, 4))
	// Now 'b' is LRU. Adding 'c' should evict 'b'.
	c.Put(makeMem("c", "e1"), makeVec(0.3, 4))

	_, ok := c.Get("a")
	if !ok {
		t.Error("Expected 'a' to survive (was re-put)")
	}
	_, ok = c.Get("b")
	if ok {
		t.Error("Expected 'b' to be evicted")
	}
}

func TestLRU_Remove(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 3, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), makeVec(0.1, 4))
	c.Put(makeMem("b", "e1"), makeVec(0.2, 4))
	c.Remove("a")

	if c.Len() != 1 {
		t.Fatalf("Expected 1 entry, got %d", c.Len())
	}
	_, ok := c.Get("a")
	if ok {
		t.Error("Expected 'a' to be removed")
	}

	// Removing non-existent key is a no-op
	c.Remove("nonexistent")
	if c.Len() != 1 {
		t.Fatal("Expected no change after removing non-existent key")
	}
}

func TestLRU_Search(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.7})

	// Insert vectors pointing in different directions
	// query = [1,0,0,0], similar = [0.9,0.1,0,0], different = [0,0,1,0]
	similar := []float32{0.9, 0.1, 0, 0}
	different := []float32{0, 0, 1, 0}
	query := []float32{1, 0, 0, 0}

	c.Put(makeMem("similar", "e1"), similar)
	c.Put(makeMem("different", "e1"), different)

	results := c.Search(query, 10, 0.1)
	if len(results) == 0 {
		t.Fatal("Expected at least one result")
	}
	if results[0].Memory.ID != "similar" {
		t.Errorf("Expected 'similar' as best match, got '%s'", results[0].Memory.ID)
	}
	if results[0].Similarity < 0.9 {
		t.Errorf("Expected high similarity for similar vector, got %f", results[0].Similarity)
	}
}

func TestLRU_SearchWithEntityFilter(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.7})

	vec := []float32{1, 0, 0, 0}
	c.Put(makeMem("a", "e1"), vec)
	c.Put(makeMem("b", "e2"), vec)

	results := c.SearchWithEntityFilter(vec, "e1", 10, 0.0)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result for e1, got %d", len(results))
	}
	if results[0].Memory.ID != "a" {
		t.Error("Expected result from e1")
	}
}

func TestLRU_SearchMinScore(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), []float32{0, 0, 1, 0})
	query := []float32{1, 0, 0, 0}

	// With high minScore, orthogonal vector should be filtered
	results := c.Search(query, 10, 0.9)
	if len(results) != 0 {
		t.Errorf("Expected 0 results with high minScore, got %d", len(results))
	}
}

func TestLRU_BestScore(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.7})

	c.Put(makeMem("a", "e1"), []float32{1, 0, 0, 0})
	c.Put(makeMem("b", "e1"), []float32{0, 1, 0, 0})

	query := []float32{1, 0, 0, 0}
	best := c.BestScore(query, "e1")
	if best < 0.99 {
		t.Errorf("Expected best score ~1.0, got %f", best)
	}

	// Different entity returns 0
	best = c.BestScore(query, "e2")
	if best != 0 {
		t.Errorf("Expected 0 for missing entity, got %f", best)
	}
}

func TestLRU_EmptySearch(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.7})

	results := c.Search([]float32{1, 0, 0}, 10, 0.0)
	if results != nil {
		t.Error("Expected nil for empty cache")
	}

	results = c.Search(nil, 10, 0.0)
	if results != nil {
		t.Error("Expected nil for nil query")
	}
}

func TestLRU_ConcurrentAccess(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 100, HitThreshold: 0.7})
	var wg sync.WaitGroup

	// Concurrent puts
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("mem-%d", i)
			c.Put(makeMem(id, "e1"), makeVec(float32(i)*0.01, 4))
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("mem-%d", i)
			c.Get(id)
			c.Search(makeVec(float32(i)*0.01, 4), 5, 0.0)
		}(i)
	}

	wg.Wait()

	if c.Len() > 100 {
		t.Errorf("Cache exceeded capacity: %d", c.Len())
	}
}

func TestLRU_HitThreshold(t *testing.T) {
	c := NewLRU(LRUConfig{MaxEntries: 10, HitThreshold: 0.85})
	if c.HitThreshold() != 0.85 {
		t.Errorf("Expected threshold 0.85, got %f", c.HitThreshold())
	}
}
