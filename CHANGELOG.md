# Changelog

All notable changes to keyoku-engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.3.0] - 2026-03-13

### Added
- **Keyoku-driven heartbeat with CLI delivery** — heartbeat watcher now delivers messages autonomously via CLI (e.g., OpenClaw → Telegram) without requiring external orchestration
- Session ID routing for delivery (`--session-id` flag, auto-derived from channel + recipient)
- Adaptive tick intervals (time-of-day multipliers, signal velocity, cooldown after action)
- Quiet hours support with timezone-aware scheduling
- Watcher auto-start configuration via env vars and config file
- Cross-compiled release binaries (linux/darwin, amd64/arm64)

### Changed
- Split oversized files for maintainability: `sqlite.go` → 12 files, `heartbeat.go` → 5 files, `handlers.go` → 6 files
- Deduplicated LLM provider schemas into shared `llm/schemas.go`
- Added input validation layer (`validate.go`)
- Fixed `.gitignore` pattern that was excluding `cmd/keyoku-server/` directory

## [0.2.5] - 2026-03-10

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
