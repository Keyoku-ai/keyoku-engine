// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"fmt"
	"log"

	"github.com/keyoku-ai/keyoku-engine/vectorindex"
)

func (s *SQLiteStore) rebuildIndexWithLogging(context string, cause error) error {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()

	log.Printf("WARN: HNSW rebuild triggered (context=%s): %v", context, cause)

	// Rebuild into a fresh index instance so we don't preserve potentially
	// corrupted in-memory graph state.
	cfg := s.index.Config()
	freshIndex := vectorindex.NewHNSW(cfg)

	// Block writers while rebuilding + swapping to avoid dropping concurrent
	// index updates during recovery.
	s.mu.Lock()
	defer s.mu.Unlock()

	rebuilt, skipped, err := s.rebuildIndex(freshIndex)
	if err != nil {
		return fmt.Errorf("rebuild failed (context=%s): %w", context, err)
	}

	s.index = freshIndex
	log.Printf("INFO: HNSW rebuild complete (context=%s rebuilt=%d skipped=%d)", context, rebuilt, skipped)
	return nil
}
