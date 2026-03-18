// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/keyoku-ai/keyoku-engine/vectorindex"
	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite + HNSW vector index.
type SQLiteStore struct {
	db        *sql.DB
	mu        sync.Mutex   // serialized writes
	rebuildMu sync.Mutex   // serialized index rebuilds
	indexMu   sync.RWMutex // protects index pointer swaps
	index     *vectorindex.HNSW
	dbPath    string
}

// NewSQLite creates a new SQLite-backed store with an HNSW vector index.
func NewSQLite(dbPath string, dimensions int) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Single writer, multiple readers
	db.SetMaxOpenConns(1)

	hnswCfg := vectorindex.DefaultHNSWConfig(dimensions)
	index := vectorindex.NewHNSW(hnswCfg)

	s := &SQLiteStore{
		db:     db,
		index:  index,
		dbPath: dbPath,
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	// Try to load HNSW from disk, rebuild from BLOBs if it fails.
	hnswPath := dbPath + ".hnsw"
	if err := index.Load(hnswPath); err != nil {
		if rebuildErr := s.rebuildIndexWithLogging("startup-load", fmt.Errorf("load %s: %w", hnswPath, err)); rebuildErr != nil {
			db.Close()
			return nil, fmt.Errorf("failed to rebuild HNSW index after load error: %w", rebuildErr)
		}
	}

	return s, nil
}

func (s *SQLiteStore) Close() error {
	// Persist HNSW index
	if s.dbPath != "" && s.dbPath != ":memory:" {
		s.currentIndex().Save(s.dbPath + ".hnsw")
	}
	return s.db.Close()
}

func (s *SQLiteStore) currentIndex() *vectorindex.HNSW {
	s.indexMu.RLock()
	idx := s.index
	s.indexMu.RUnlock()
	return idx
}

func (s *SQLiteStore) swapIndex(index *vectorindex.HNSW) {
	s.indexMu.Lock()
	s.index = index
	s.indexMu.Unlock()
}

// ExecRaw executes a raw SQL statement. Intended for testing with precise control over timestamps.
func (s *SQLiteStore) ExecRaw(ctx context.Context, query string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
