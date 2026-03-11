# Changelog

All notable changes to keyoku-engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- End-to-end stress tests and query expansion functionality
- Unit tests for TokenBudget, SignificanceScorer, and StateManager
- Comprehensive stress testing framework for memory management and eviction
- Eviction processor for HNSW index and storage management
- Global stats and sample memories endpoints
- Enhanced decay calculations with access frequency and importance boosting
- Entity listing endpoint for all known entity IDs
- Custom base URL support for OpenAI and Anthropic APIs (OpenRouter, LiteLLM)
- CRUD operations for scheduled memories
- Time-aware schedule system with cron tags
- Team management and visibility controls (private, team, global)
- Event-driven architecture with EventBus and Watcher (SSE)
- SPDX license identifiers and copyright notices on all source files

### Changed
- Enhanced deduplication thresholds
- Improved memory state filtering (active and stale states)
- Cleaned up code structure and removed unused blocks

### Removed
- Compiled binary from repository

## [0.1.0] - 2025-03-01

### Added
- Initial release of keyoku-engine
- LLM-powered memory extraction (OpenAI, Anthropic, Gemini)
- In-process HNSW vector index with OpenAI embeddings
- Tiered retrieval: LRU cache → HNSW → FTS fallback
- Ebbinghaus decay, consolidation, archival, and purge jobs
- Semantic deduplication and conflict detection
- Entity and relationship graph extraction
- Zero-token heartbeat check system
- HTTP server with REST API
- SQLite storage with pure Go driver (no CGO)
