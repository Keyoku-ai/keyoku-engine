// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package vectorindex

// SearchResult represents a vector search result with ID and distance.
type SearchResult struct {
	ID       string
	Distance float32
}

// VectorIndex defines the interface for vector similarity search.
type VectorIndex interface {
	// Add inserts a vector with the given ID.
	Add(id string, vector []float32) error

	// Remove deletes a vector by ID.
	Remove(id string) error

	// Search finds the k nearest neighbors to the query vector.
	// Returns results sorted by distance (closest first).
	Search(query []float32, k int) ([]SearchResult, error)

	// Len returns the number of vectors in the index.
	Len() int

	// Save persists the index to disk.
	Save(path string) error

	// Load restores the index from disk.
	Load(path string) error
}
