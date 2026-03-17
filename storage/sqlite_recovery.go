// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"fmt"
	"log"
)

func (s *SQLiteStore) rebuildIndexWithLogging(context string, cause error) error {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()

	log.Printf("WARN: HNSW rebuild triggered (context=%s): %v", context, cause)
	rebuilt, skipped, err := s.rebuildIndex()
	if err != nil {
		return fmt.Errorf("rebuild failed (context=%s): %w", context, err)
	}
	log.Printf("INFO: HNSW rebuild complete (context=%s rebuilt=%d skipped=%d)", context, rebuilt, skipped)
	return nil
}
