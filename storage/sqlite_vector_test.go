// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"errors"
	"testing"
)

func TestShouldAttemptHNSWRebuild(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "dimension mismatch", err: errors.New("expected 1536 dimensions, got 384"), want: false},
		{name: "hnsw runtime error", err: errors.New("HNSW search failed: corrupted graph"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAttemptHNSWRebuild(tt.err); got != tt.want {
				t.Fatalf("shouldAttemptHNSWRebuild() = %v, want %v", got, tt.want)
			}
		})
	}
}
