// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package vectorindex

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDefaultHNSWConfig(t *testing.T) {
	cfg := DefaultHNSWConfig(1536)
	if cfg.Dimensions != 1536 {
		t.Errorf("Dimensions = %d, want 1536", cfg.Dimensions)
	}
	if cfg.M != 16 {
		t.Errorf("M = %d, want 16", cfg.M)
	}
	if cfg.EfConstruction != 200 {
		t.Errorf("EfConstruction = %d, want 200", cfg.EfConstruction)
	}
	if cfg.EfSearch != 50 {
		t.Errorf("EfSearch = %d, want 50", cfg.EfSearch)
	}
}

func newTestHNSW() *HNSW {
	return NewHNSW(DefaultHNSWConfig(3))
}

func TestHNSW_AddAndSearch_Single(t *testing.T) {
	h := newTestHNSW()
	if err := h.Add("a", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 1 {
		t.Fatalf("Len = %d, want 1", h.Len())
	}

	results, err := h.Search([]float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "a" {
		t.Errorf("Search = %v, want [{a, 0}]", results)
	}
}

func TestHNSW_AddAndSearch_Multiple(t *testing.T) {
	h := newTestHNSW()
	vectors := map[string][]float32{
		"x": {1, 0, 0},
		"y": {0, 1, 0},
		"z": {0, 0, 1},
	}
	for id, v := range vectors {
		if err := h.Add(id, v); err != nil {
			t.Fatal(err)
		}
	}
	if h.Len() != 3 {
		t.Fatalf("Len = %d, want 3", h.Len())
	}

	results, err := h.Search([]float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "x" {
		t.Errorf("nearest to [1,0,0] = %v, want x", results)
	}
}

func TestHNSW_Add_WrongDimensions(t *testing.T) {
	h := newTestHNSW()
	err := h.Add("a", []float32{1, 2})
	if err == nil {
		t.Error("expected error for wrong dimensions")
	}
}

func TestHNSW_Search_WrongDimensions(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	_, err := h.Search([]float32{1, 2}, 1)
	if err == nil {
		t.Error("expected error for wrong dimensions")
	}
}

func TestHNSW_Search_EmptyIndex(t *testing.T) {
	h := newTestHNSW()
	results, err := h.Search([]float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Errorf("Search on empty = %v, want nil", results)
	}
}

func TestHNSW_Search_KGreaterThanN(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0, 1, 0})

	results, err := h.Search([]float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("Search k=10 got %d results, want 2", len(results))
	}
}

func TestHNSW_Search_Ordering(t *testing.T) {
	h := newTestHNSW()
	// Vectors at different distances from [1,0,0]
	h.Add("close", []float32{0.9, 0.1, 0})
	h.Add("far", []float32{0, 0, 1})
	h.Add("mid", []float32{0.5, 0.5, 0})

	results, err := h.Search([]float32{1, 0, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// Results should be sorted by distance (closest first)
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Errorf("results not sorted: [%d].Distance=%v < [%d].Distance=%v",
				i, results[i].Distance, i-1, results[i-1].Distance)
		}
	}
}

func TestHNSW_Add_Update(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	h.Add("a", []float32{0, 1, 0}) // Update

	if h.Len() != 1 {
		t.Errorf("Len = %d, want 1 after update", h.Len())
	}

	// Should now be closest to [0,1,0]
	results, _ := h.Search([]float32{0, 1, 0}, 1)
	if len(results) != 1 || results[0].ID != "a" {
		t.Errorf("after update, search = %v", results)
	}
}

func TestHNSW_Remove_Existing(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0, 1, 0})

	if err := h.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 1 {
		t.Errorf("Len = %d, want 1", h.Len())
	}

	results, _ := h.Search([]float32{1, 0, 0}, 5)
	for _, r := range results {
		if r.ID == "a" {
			t.Error("removed node 'a' still found in search")
		}
	}
}

func TestHNSW_Remove_NonExistent(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	if err := h.Remove("nonexistent"); err != nil {
		t.Errorf("Remove non-existent returned error: %v", err)
	}
	if h.Len() != 1 {
		t.Errorf("Len = %d, want 1", h.Len())
	}
}

func TestHNSW_Remove_EntryPoint(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0, 1, 0})
	h.Add("c", []float32{0, 0, 1})

	// Remove entry point (first added node)
	h.Remove("a")
	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}

	// Index should still be functional
	results, err := h.Search([]float32{0, 1, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("search after removing entry point returned no results")
	}
}

func TestHNSW_Remove_All(t *testing.T) {
	h := newTestHNSW()
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0, 1, 0})

	h.Remove("a")
	h.Remove("b")

	if h.Len() != 0 {
		t.Errorf("Len = %d, want 0", h.Len())
	}

	results, _ := h.Search([]float32{1, 0, 0}, 5)
	if results != nil {
		t.Errorf("search on empty index = %v, want nil", results)
	}
}

func TestHNSW_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.hnsw")

	// Create and populate index
	h1 := newTestHNSW()
	h1.Add("a", []float32{1, 0, 0})
	h1.Add("b", []float32{0, 1, 0})
	h1.Add("c", []float32{0, 0, 1})

	if err := h1.Save(path); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	// Load into new index
	h2 := NewHNSW(DefaultHNSWConfig(3))
	if err := h2.Load(path); err != nil {
		t.Fatalf("Load error = %v", err)
	}

	if h2.Len() != 3 {
		t.Errorf("loaded Len = %d, want 3", h2.Len())
	}

	// Search should work the same
	results, err := h2.Search([]float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "a" {
		t.Errorf("loaded search = %v, want [{a, ~0}]", results)
	}
}

func TestHNSW_Load_NonExistentFile(t *testing.T) {
	h := newTestHNSW()
	err := h.Load("/nonexistent/path.hnsw")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestHNSW_Load_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.hnsw")
	os.WriteFile(path, []byte("not a valid hnsw file"), 0644)

	h := newTestHNSW()
	err := h.Load(path)
	if err == nil {
		t.Error("expected error for invalid file")
	}
}

func TestHNSW_Load_DimensionMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dim-mismatch.hnsw")

	h1 := NewHNSW(DefaultHNSWConfig(1536))
	if err := h1.Add("a", make([]float32, 1536)); err != nil {
		t.Fatalf("setup add error = %v", err)
	}
	if err := h1.Save(path); err != nil {
		t.Fatalf("save error = %v", err)
	}

	h2 := NewHNSW(DefaultHNSWConfig(768))
	err := h2.Load(path)
	if err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}

func TestHNSW_Load_InvalidNeighborIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-neighbor.hnsw")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer f.Close()

	// Header
	mustWrite := func(v any) {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatalf("binary write failed: %v", err)
		}
	}
	mustWrite(uint32(0x484E5357)) // magic HNSW
	mustWrite(int32(3))            // dims
	mustWrite(int32(16))           // M
	mustWrite(int32(0))            // max level
	mustWrite(int32(0))            // entry index
	mustWrite(int32(1))            // node count

	id := []byte("a")
	mustWrite(int32(len(id)))
	if _, err := f.Write(id); err != nil {
		t.Fatalf("write id: %v", err)
	}
	mustWrite([]float32{1, 0, 0}) // vector
	mustWrite(int32(1))           // layer count
	mustWrite(int32(1))           // conn count for layer 0
	mustWrite(int32(-1))          // INVALID connection index
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	h := newTestHNSW()
	err = h.Load(path)
	if err == nil {
		t.Fatal("expected invalid connection index error")
	}
}

func TestHNSW_Concurrent(t *testing.T) {
	h := NewHNSW(DefaultHNSWConfig(3))

	// Pre-populate some data
	for i := 0; i < 20; i++ {
		v := []float32{float32(i), float32(i + 1), float32(i + 2)}
		v = Normalize(v)
		h.Add(fmt.Sprintf("init-%d", i), v)
	}

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v := Normalize([]float32{float32(n), float32(n * 2), float32(n * 3)})
			h.Add(fmt.Sprintf("concurrent-%d", n), v)
		}(i)
	}

	// Concurrent searches
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Search([]float32{0.5, 0.5, 0.5}, 3)
		}()
	}

	wg.Wait()

	// Verify index is still consistent
	if h.Len() < 20 {
		t.Errorf("Len = %d, want >= 20", h.Len())
	}
}

func TestHNSW_LargerIndex(t *testing.T) {
	h := NewHNSW(DefaultHNSWConfig(3))

	// Add 100 vectors
	for i := 0; i < 100; i++ {
		v := Normalize([]float32{float32(i), float32(100 - i), float32(i % 10)})
		if err := h.Add(fmt.Sprintf("v%d", i), v); err != nil {
			t.Fatal(err)
		}
	}

	if h.Len() != 100 {
		t.Errorf("Len = %d, want 100", h.Len())
	}

	// Search should return k results
	results, err := h.Search(Normalize([]float32{1, 0, 0}), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Errorf("search returned %d results, want 5", len(results))
	}
}
