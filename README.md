# Keyoku Engine

Intelligent memory engine for AI agents. Extract, store, search, decay, and consolidate memories — all locally with SQLite and in-process vector search. No external databases required.

## Features

- **Memory extraction** — LLM-powered structured extraction from conversations (OpenAI, Anthropic, Gemini)
- **Vector search** — In-process HNSW index with OpenAI embeddings (or custom endpoint)
- **Tiered retrieval** — Hot LRU cache → HNSW vector → FTS fallback for scale
- **Lifecycle management** — Ebbinghaus decay, consolidation, archival, and purge via background jobs
- **Deduplication & conflict detection** — Semantic dedup (0.95 threshold) and contradiction detection
- **Entity & relationship graphs** — Auto-extracted entity mentions and relationships
- **Heartbeat check** — Zero-token local query for pending work, deadlines, and decaying memories
- **Scheduling** — Cron-tagged memories with acknowledgment tracking
- **Teams** — Multi-agent memory visibility boundaries (private, team, global)
- **Event bus** — Async SSE stream for memory lifecycle events
- **Pure Go** — No CGO; uses `modernc.org/sqlite` for portable builds

## Quick Start

### As a Go library

```go
import keyoku "github.com/keyoku-ai/keyoku-engine"

k, err := keyoku.New(keyoku.Config{
    DBPath:             "./memories.db",
    ExtractionProvider: "openai",
    OpenAIAPIKey:       os.Getenv("OPENAI_API_KEY"),
})
defer k.Close()

// Store memories from a conversation
result, _ := k.Remember(ctx, keyoku.RememberInput{
    EntityID: "user-123",
    Messages: messages,
})

// Search
memories, _ := k.Search(ctx, keyoku.SearchInput{
    EntityID: "user-123",
    Query:    "what are their preferences?",
    Limit:    5,
})

// Zero-token heartbeat check
heartbeat, _ := k.HeartbeatCheck(ctx, keyoku.HeartbeatCheckInput{
    EntityIDs: []string{"user-123"},
})
```

### As an HTTP server

```bash
# Build
make build

# Run
export OPENAI_API_KEY="sk-..."
export KEYOKU_SESSION_TOKEN="any-value"
./bin/keyoku-server --db ./memories.db
```

Default port: `18900` (override with `--port` or `KEYOKU_PORT` env var).

## API

### Memory

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/remember` | Extract & store memories from messages |
| POST | `/api/v1/search` | Vector similarity search |
| GET | `/api/v1/memories` | List memories (paginated) |
| GET | `/api/v1/memories/{id}` | Get single memory |
| DELETE | `/api/v1/memories/{id}` | Delete memory |
| DELETE | `/api/v1/memories` | Delete all memories for entity |
| GET | `/api/v1/memories/sample` | Representative sample |

### Heartbeat & Monitoring

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/heartbeat/check` | Zero-token action detection |
| POST | `/api/v1/watcher/start` | Start continuous heartbeat monitor |
| POST | `/api/v1/watcher/stop` | Stop watcher |
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/stats` | Global stats |
| GET | `/api/v1/stats/{entity_id}` | Per-entity stats |

### Scheduling

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/schedule` | Create scheduled memory |
| POST | `/api/v1/schedule/ack` | Mark schedule as executed |
| PUT | `/api/v1/schedule/{id}` | Update schedule |
| DELETE | `/api/v1/schedule/{id}` | Cancel schedule |
| GET | `/api/v1/scheduled` | List active schedules |

### Teams

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/teams` | Create team |
| GET | `/api/v1/teams/{id}` | Get team |
| POST | `/api/v1/teams/{id}/members` | Add member |
| DELETE | `/api/v1/teams/{id}/members/{agent_id}` | Remove member |

### Events

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/events` | SSE event stream |
| POST | `/api/v1/consolidate` | Trigger consolidation |

## Architecture

```
┌─────────────────────────────────────────────┐
│                 keyoku-server                │
│              (HTTP + SSE layer)              │
├─────────────────────────────────────────────┤
│                  Keyoku Core                 │
│  ┌──────────┐ ┌──────────┐ ┌─────────────┐ │
│  │ Remember │ │  Search  │ │  Heartbeat  │ │
│  │ Extract  │ │  Tiered  │ │  Zero-Token │ │
│  └────┬─────┘ └────┬─────┘ └──────┬──────┘ │
│       │             │              │         │
│  ┌────▼─────────────▼──────────────▼──────┐ │
│  │              Engine Layer              │ │
│  │  dedup · conflict · entity · decay     │ │
│  │  graph · ranker · scorer · retrieval   │ │
│  └────────────────┬───────────────────────┘ │
│                   │                          │
│  ┌────────────────▼───────────────────────┐ │
│  │           Storage (SQLite)             │ │
│  │  memories · entities · relationships   │ │
│  │  schemas · agent_state · teams         │ │
│  └────────────────┬───────────────────────┘ │
│                   │                          │
│  ┌──────┐  ┌──────▼──────┐  ┌───────────┐  │
│  │ LRU  │  │ HNSW Vector │  │ FTS (SQL) │  │
│  │ Tier1│  │   Tier 2    │  │  Tier 3   │  │
│  └──────┘  └─────────────┘  └───────────┘  │
│                                              │
│  ┌──────────────────────────────────────┐   │
│  │         Background Jobs              │   │
│  │  decay · consolidation · archival    │   │
│  │  purge · eviction                    │   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
         │                    │
    ┌────▼────┐         ┌────▼────┐
    │   LLM   │         │Embedder │
    │ Extract │         │ Vectors │
    └─────────┘         └─────────┘
```

## LLM Providers

| Provider | Extraction | Embedding | Custom Base URL |
|----------|-----------|-----------|-----------------|
| OpenAI | gpt-4o-mini (default) | text-embedding-3-small | Yes |
| Anthropic | claude-3-5-haiku-latest | — | Yes |
| Google Gemini | gemini-3-flash-preview | — | — |

Custom base URLs support OpenRouter, LiteLLM, and self-hosted endpoints.

## Configuration

```go
keyoku.Config{
    DBPath:             "./keyoku.db",
    ExtractionProvider: "openai",        // "openai", "anthropic", "google"
    ExtractionModel:    "gpt-4o-mini",
    OpenAIAPIKey:       "sk-...",
    EmbeddingModel:     "text-embedding-3-small",

    // Deduplication
    DeduplicationEnabled:    true,
    SemanticDuplicateThresh: 0.95,
    NearDuplicateThresh:     0.85,

    // Lifecycle
    SchedulerEnabled: true,
    DecayThreshold:   0.3,
    ArchivalDays:     30,

    // Tiered retrieval
    HotCacheSize:   500,
    MaxHNSWEntries: 10000,
    MaxStorageMB:   500,
}
```

Environment variable overrides: `KEYOKU_PORT`, `KEYOKU_DB_PATH`, `KEYOKU_EXTRACTION_PROVIDER`, `KEYOKU_EXTRACTION_MODEL`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`.

## Development

```bash
make test          # Run all tests
make test-race     # With race detector
make bench         # Benchmarks
make build         # Build for current platform
make cross         # Cross-compile (darwin/linux x arm64/amd64)
make lint          # golangci-lint
```

Requires Go 1.24+.

## License

See [LICENSE](LICENSE) for details.
