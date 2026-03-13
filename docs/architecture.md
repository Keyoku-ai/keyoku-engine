# Architecture

This document describes the internal architecture of keyoku-engine.

## Overview

```
                    ┌─────────────────────────┐
                    │    HTTP API Server       │
                    │  cmd/keyoku-server/      │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │   Keyoku (root package)  │
                    │   keyoku.go              │
                    │   heartbeat.go           │
                    │   schedule.go            │
                    │   events.go              │
                    └────────────┬────────────┘
                                 │
          ┌──────────────────────┼──────────────────────┐
          │                      │                      │
┌─────────▼─────────┐ ┌─────────▼─────────┐ ┌─────────▼─────────┐
│      engine/      │ │      llm/         │ │     embedder/     │
│  Extract, dedup,  │ │  OpenAI, Gemini,  │ │  Text → vector    │
│  conflict, decay  │ │  Anthropic        │ │  (OpenAI, noop)   │
└─────────┬─────────┘ └───────────────────┘ └─────────┬─────────┘
          │                                           │
          │              ┌────────────────┐           │
          └──────────────▶   storage/     ◀───────────┘
                         │  SQLite (WAL)  │
                         └────────┬───────┘
                                  │
                         ┌────────▼───────┐
                         │  vectorindex/  │
                         │  HNSW index    │
                         └────────────────┘
```

## Package Responsibilities

### Root package (`keyoku`)

The public API surface. All external consumers import this package.

- **keyoku.go** — `Keyoku` struct: the main entry point. Wraps engine, embedder, LLM, storage, and scheduler. Exposes `Remember()`, `Search()`, `ListMemories()`, `DeleteMemory()`, etc.
- **heartbeat.go** — Types, constants, and `HeartbeatCheck()` orchestrator.
- **heartbeat_options.go** — `WithX()` option functions for heartbeat configuration.
- **heartbeat_decide.go** — `evaluateShouldAct()`, signal classification, fingerprinting, delta detection.
- **heartbeat_nudge.go** — `evaluateNudge()`, content filtering, topic repeat suppression.
- **heartbeat_time.go** — Time-of-day awareness, period classification, cooldown multipliers.
- **schedule.go** — Cron-tagged memory scheduling with acknowledgment tracking.
- **events.go** — Server-Sent Events (SSE) for real-time memory change notifications.
- **config.go** — `Config` struct with all knobs and `DefaultConfig()`.
- **watcher.go** — Heartbeat watcher with adaptive tick intervals and delivery integration.
- **delivery.go** — `DeliveryConfig` and `Deliverer` interface for heartbeat message delivery.
- **delivery_cli.go** — CLI-based deliverer (shells out to OpenClaw or similar CLI).
- **patterns.go** — Regex patterns for date/time extraction from natural language.

### `engine/`

Core memory processing pipeline:

- **engine.go** — `Engine` struct coordinating extraction → dedup → conflict → store.
- **dedup.go** — Semantic deduplication (cosine similarity) and content-hash dedup.
- **conflict.go** — Detects contradictory memories and resolves via recency/confidence.
- **decay.go** — Time-based relevance decay with configurable half-life.
- **retrieval.go** — Multi-signal ranked retrieval combining vector similarity, recency, access frequency, and importance.
- **ranker.go** — Scoring functions and retrieval mode presets (balanced, recent, important, historical, comprehensive).
- **scorer.go** — Composite scoring with configurable signal weights.
- **entity.go** — Entity extraction and resolution (canonical names, aliases, types).
- **relationship.go** — Relationship detection between entities with confidence tracking.
- **graph.go** — Knowledge graph construction from entities and relationships.
- **budget.go** — Token budget management for LLM extraction calls.
- **query_expand.go** — Query expansion for improved recall.

### `storage/`

Persistence layer:

- **interface.go** — `Store` interface defining all storage operations.
- **sqlite.go** — Core `SQLiteStore` struct, constructor, `Close`, `Ping`, `ExecRaw`.
- **sqlite_migrate.go** — Schema migrations and index rebuilds.
- **sqlite_memory.go** — CRUD operations for memories.
- **sqlite_vector.go** — Vector search (HNSW), FTS, embedding decode.
- **sqlite_queries.go** — Complex queries: recent, stale, aggregate stats, sampling.
- **sqlite_entity.go** — Entity CRUD, alias management, mentions.
- **sqlite_relationship.go** — Relationship CRUD, evidence tracking, path queries.
- **sqlite_schema.go** — Custom schemas and extraction results.
- **sqlite_agentstate.go** — Agent state persistence and history.
- **sqlite_team.go** — Team management and membership.
- **sqlite_heartbeat.go** — Heartbeat actions, nudge tracking, topic surfacing, message history.
- **sqlite_session.go** — Session message storage.
- **sqlite_helpers.go** — Shared scan functions, state transitions, batch operations.
- **models.go** — Data models: `Memory`, `Entity`, `Relationship`, `Team`, etc.
- **json_types.go** — JSON serialization helpers for SQLite columns.
- **errors.go** — Typed storage errors.

### `llm/`

LLM provider abstraction:

- **provider.go** — `Provider` interface and auto-detection logic.
- **schemas.go** — Canonical schema definitions shared across all providers.
- **openai.go** — OpenAI implementation (GPT-4o-mini default).
- **anthropic.go** — Anthropic/Claude implementation.
- **gemini.go** — Google Gemini implementation.
- **types.go** — `ExtractionResponse`, `ExtractedMemory`, `ExtractedEntity`, etc.
- **validate.go** — Response validation and sanitization.

### `embedder/`

Text-to-vector embedding:

- **embedder.go** — `Embedder` interface.
- **openai.go** — OpenAI text-embedding-3-small (default).
- **noop.go** — No-op embedder for testing.

### `vectorindex/`

In-process similarity search:

- **index.go** — `VectorIndex` interface.
- **hnsw.go** — HNSW (Hierarchical Navigable Small World) implementation.
- **math.go** — Cosine similarity and vector operations.

### `cache/`

Performance optimization:

- **lru.go** — Hot LRU cache for Tier 1 memory retrieval. Stores recently accessed memories with decoded embeddings for sub-millisecond brute-force cosine search.

### `jobs/`

Background maintenance:

- **scheduler.go** — In-memory job scheduler with configurable intervals.
- **decay_processor.go** — Applies time-based decay to memory relevance scores.
- **consolidation_processor.go** — Merges similar memories using LLM.
- **archival_processor.go** — Moves low-relevance memories to cold storage.
- **eviction_processor.go** — Removes memories below minimum thresholds.
- **purge_processor.go** — Permanently deletes soft-deleted memories after retention period.

### `cmd/keyoku-server/`

HTTP server binary:

- **main.go** — Server startup, signal handling, graceful shutdown.
- **handlers.go** — Shared helpers (`writeJSON`, `writeError`, `decodeBody`, etc.) and `Handlers` struct.
- **handlers_memory.go** — Memory CRUD and search handlers.
- **handlers_heartbeat.go** — Heartbeat check, context, and message recording.
- **handlers_schedule.go** — Schedule CRUD handlers.
- **handlers_watcher.go** — Watcher start/stop/watch/unwatch handlers.
- **handlers_team.go** — Team management, stats, health handlers.
- **config.go** — Server-specific config (port, CORS, delivery, watcher settings).
- **validate.go** — Input validation (IDs, content, query params).
- **sse.go** — SSE hub for real-time event streaming.

## Data Flow

### Remember (Store Memories)

```
Client POST /api/remember
    │
    ▼
Keyoku.Remember(entityId, content)
    │
    ├─▶ LLM.Extract(content)         → ExtractedMemory[]
    │
    ├─▶ Embedder.Embed(memory.content) → []float32
    │
    ├─▶ Engine.Deduplicate(memory)     → skip if duplicate
    │
    ├─▶ Engine.DetectConflicts(memory) → resolve or flag
    │
    ├─▶ Storage.CreateMemory(memory)   → SQLite
    │
    └─▶ VectorIndex.Add(id, embedding) → HNSW
```

### Search (Recall Memories)

```
Client POST /api/search
    │
    ▼
Keyoku.Search(entityId, query)
    │
    ├─▶ Cache.Search(query)            → hit? return immediately
    │
    ├─▶ Embedder.Embed(query)          → []float32
    │
    ├─▶ VectorIndex.Search(embedding)  → candidate IDs
    │
    ├─▶ Storage.GetMemoriesByIDs(ids)  → Memory[]
    │
    ├─▶ Ranker.Score(memories, query)  → ranked results
    │
    └─▶ Cache.Put(results)             → warm cache
```

### Heartbeat Check (Zero-Token)

```
Client POST /api/heartbeat/check
    │
    ▼
Keyoku.HeartbeatCheck(entityId)
    │
    ├─▶ Storage queries (no LLM, no embeddings):
    │     • Pending work (state = pending)
    │     • Deadlines (expires_at within window)
    │     • Scheduled (cron-tagged, unacknowledged)
    │     • Decaying (relevance below threshold)
    │     • Conflicts (unresolved)
    │     • Stale monitors (not accessed recently)
    │
    └─▶ Return HeartbeatCheckResponse
         { should_act, pending_work[], deadlines[], ... }
```

## Storage Schema

SQLite with WAL mode. Key tables:

| Table | Purpose |
|-------|---------|
| `memories` | Core memory storage with content, type, state, importance, confidence, sentiment, tags, embedding (blob) |
| `entities` | Extracted entities with canonical names, types, aliases |
| `relationships` | Entity-to-entity relationships with type, confidence, evidence count |
| `relationship_evidence` | Individual evidence items supporting relationships |
| `entity_mentions` | Links between memories and entities they mention |
| `event_history` | Audit log of all memory operations |
| `session_messages` | Conversation history for context-aware extraction |
| `teams` | Team definitions for multi-agent visibility |
| `team_members` | Agent membership in teams |

## Configuration

See `Config` in [config.go](../config.go) for all options. Key tuning parameters:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `SemanticDuplicateThresh` | 0.95 | Cosine similarity above which memories are considered duplicates |
| `NearDuplicateThresh` | 0.85 | Threshold for near-duplicate detection (triggers merge) |
| `ConflictSimilarityThresh` | 0.6 | Similarity threshold for conflict detection |
| `DecayThreshold` | 0.3 | Relevance score below which memories are flagged as decaying |
| `ArchivalDays` | 30 | Days before low-relevance memories are archived |
| `PurgeRetentionDays` | 90 | Days before soft-deleted memories are permanently purged |
