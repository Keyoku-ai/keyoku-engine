// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.
// Package cache provides a hot LRU cache for Tier 1 memory retrieval.
// It stores recently accessed memories with their decoded embeddings
// for sub-millisecond brute-force cosine search over a small set.
package cache

import (
	"container/list"
	"sort"
	"sync"

	"github.com/keyoku-ai/keyoku-engine/storage"
	"github.com/keyoku-ai/keyoku-engine/vectorindex"
)

// LRUConfig configures the hot cache.
type LRUConfig struct {
	MaxEntries   int     // Maximum cached items (default: 500)
	HitThreshold float64 // Minimum similarity to short-circuit search (default: 0.7)
}

// DefaultLRUConfig returns sensible defaults for the hot cache.
func DefaultLRUConfig() LRUConfig {
	return LRUConfig{
		MaxEntries:   500,
		HitThreshold: 0.7,
	}
}

// CacheEntry holds a memory and its decoded embedding for fast cosine comparison.
type CacheEntry struct {
	Memory    *storage.Memory
	Embedding []float32
}

// CacheSearchResult pairs a cached memory with its similarity to a query.
type CacheSearchResult struct {
	Memory     *storage.Memory
	Similarity float64
}

// LRU is a thread-safe LRU cache for hot memory retrieval.
type LRU struct {
	mu           sync.RWMutex
	capacity     int
	hitThreshold float64
	entries      map[string]*list.Element // memory ID → list element
	order        *list.List               // front = most recent, back = least recent
}

// NewLRU creates a new LRU cache.
func NewLRU(config LRUConfig) *LRU {
	if config.MaxEntries <= 0 {
		config.MaxEntries = 500
	}
	if config.HitThreshold <= 0 {
		config.HitThreshold = 0.7
	}
	return &LRU{
		capacity:     config.MaxEntries,
		hitThreshold: config.HitThreshold,
		entries:      make(map[string]*list.Element, config.MaxEntries),
		order:        list.New(),
	}
}

// Put adds or promotes a memory in the cache. If the cache is full,
// the least recently used entry is evicted.
func (c *LRU) Put(mem *storage.Memory, embedding []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := &CacheEntry{Memory: mem, Embedding: embedding}

	// Already cached — update and promote to front
	if el, ok := c.entries[mem.ID]; ok {
		el.Value = entry
		c.order.MoveToFront(el)
		return
	}

	// Evict LRU if at capacity
	if c.order.Len() >= c.capacity {
		back := c.order.Back()
		if back != nil {
			evicted := back.Value.(*CacheEntry)
			delete(c.entries, evicted.Memory.ID)
			c.order.Remove(back)
		}
	}

	el := c.order.PushFront(entry)
	c.entries[mem.ID] = el
}

// Get retrieves a cache entry by ID and promotes it to the front.
func (c *LRU) Get(id string) (*CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*CacheEntry), true
}

// Remove evicts a specific entry from the cache.
func (c *LRU) Remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.entries[id]; ok {
		delete(c.entries, id)
		c.order.Remove(el)
	}
}

// Search performs brute-force cosine similarity search over all cached embeddings.
// Returns results sorted by similarity descending, filtered by minScore.
// For 500 entries × 1536 dims, this takes under 1ms.
func (c *LRU) Search(query []float32, limit int, minScore float64) []*CacheSearchResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.order.Len() == 0 || len(query) == 0 {
		return nil
	}

	results := make([]*CacheSearchResult, 0, min(limit, c.order.Len()))

	for el := c.order.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*CacheEntry)
		if len(entry.Embedding) != len(query) {
			continue
		}
		sim := float64(vectorindex.CosineSimilarity(query, entry.Embedding))
		if sim >= minScore {
			results = append(results, &CacheSearchResult{
				Memory:     entry.Memory,
				Similarity: sim,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// SearchWithEntityFilter is like Search but filters by entity ID.
func (c *LRU) SearchWithEntityFilter(query []float32, entityID string, limit int, minScore float64) []*CacheSearchResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.order.Len() == 0 || len(query) == 0 {
		return nil
	}

	results := make([]*CacheSearchResult, 0, min(limit, c.order.Len()))

	for el := c.order.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*CacheEntry)
		if entry.Memory.EntityID != entityID {
			continue
		}
		if len(entry.Embedding) != len(query) {
			continue
		}
		sim := float64(vectorindex.CosineSimilarity(query, entry.Embedding))
		if sim >= minScore {
			results = append(results, &CacheSearchResult{
				Memory:     entry.Memory,
				Similarity: sim,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// BestScore returns the highest cosine similarity of any cached entry to the query.
// Returns 0.0 if the cache is empty.
func (c *LRU) BestScore(query []float32, entityID string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var best float64
	for el := c.order.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*CacheEntry)
		if entry.Memory.EntityID != entityID {
			continue
		}
		if len(entry.Embedding) != len(query) {
			continue
		}
		sim := float64(vectorindex.CosineSimilarity(query, entry.Embedding))
		if sim > best {
			best = sim
		}
	}
	return best
}

// HitThreshold returns the configured minimum similarity for a cache hit.
func (c *LRU) HitThreshold() float64 {
	return c.hitThreshold
}

// Len returns the number of entries in the cache.
func (c *LRU) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order.Len()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
