# Copilot Instructions — keyoku-engine

## Project Overview

Go binary providing a persistent AI memory engine with semantic search, knowledge graph, adaptive heartbeat, and memory decay. Single binary, SQLite storage, HNSW vector index.

## Architecture

```
cmd/keyoku-server/    HTTP server (chi router, handlers, SSE, validation)
engine/               Core pipeline (remember, search, dedup, conflict, decay, entities, relationships, graph)
storage/              SQLite persistence (memories, entities, relationships, heartbeat, schedules, teams)
vectorindex/          HNSW approximate nearest neighbor index (in-memory, persisted to .hnsw sidecar)
llm/                  LLM provider abstraction (OpenAI, Google/Gemini, Anthropic, Ollama)
heartbeat/            Signal evaluation, watcher (adaptive tick loop), delivery (CLI-based)
```

## Key Patterns

### HTTP Handlers (cmd/keyoku-server/)
- All handlers follow: parse request, validate, call engine, write JSON response
- Auth via Bearer token middleware (X-Session-Token header)
- Errors return structured JSON `{"error": "message"}` with appropriate status codes
- Never return generic 500 without logging the underlying error

### Storage Layer (storage/)
- SQLite with WAL mode, foreign keys enabled
- All queries use parameterized statements — never interpolate user input into SQL
- Schema auto-migrates on startup via `initDB()`
- Key tables: memories, entities, relationships, entity_mentions, heartbeat_actions, surfaced_memories, topic_surfacings, schedules, teams, team_members
- `sync.RWMutex` protects concurrent access — read lock for queries, write lock for mutations

### Vector Index (vectorindex/)
- HNSW implementation with cosine distance
- Persisted to `.hnsw` binary sidecar file alongside SQLite DB
- Always validate bounds on neighbor indices: `neighborIx >= 0 && neighborIx < len(h.nodes)`
- Load() must validate: magic bytes, dimensions match config, positive node counts, connection indices in range
- searchLayer can return empty results on corrupt graphs — callers must handle `len(results) == 0`
- Add/Remove/Search all hold the mutex — keep critical sections short

### Heartbeat System (heartbeat/)
- `evaluateShouldAct()` is the 11-step decision pipeline — changes here affect all users
- Signal tiers: Immediate (weight 10), Elevated (5), Normal (3), Low (1)
- Confluence thresholds by autonomy: act=8, suggest=12, observe=20
- Response cooldown multiplier: rate<0.1 = 10x, rate<0.3 = 3x, else 1x
- Signal fingerprinting: SHA256 of memory IDs, 3-layer dedup (fingerprint + entity overlap 85% + content hash)
- Watcher runs adaptive tick loop with 4 interval multipliers (RecentAct, TimePeriod, Signal, Velocity)

### Engine Pipeline (engine/)
- Remember flow: significance filter → token budget → LLM extraction → dedup → conflict detection → store → entity extraction → relationship detection → graph update
- Dedup uses both semantic similarity (embedding cosine > 0.92) and content hash
- Decay follows Ebbinghaus formula: stability grows with each recall, unused memories fade
- Entity resolution: exact name → alias → embedding similarity (0.85 threshold) → create new
- Relationship strength: weighted average of evidence count (0.4), recency (0.3), confidence (0.3)
- Graph traversal: BFS, max depth 5, filters edges below 0.3 strength

### LLM Providers (llm/)
- Provider interface: ExtractMemories(), GenerateEmbedding(), GenerateEmbeddings() (batch), Summarize()
- Providers: openai, google, anthropic, ollama
- Ollama supports configurable embedding dimensions via OLLAMA_EMBEDDING_DIMS
- Never hardcode API keys — always read from environment variables

## Concurrency

- Storage: `sync.RWMutex` on SQLiteStore — read lock for selects, write lock for inserts/updates/deletes
- HNSW: `sync.RWMutex` on index — all operations acquire appropriate lock
- Watcher: runs in its own goroutine, communicates via channels
- SSE hub: concurrent client map with mutex protection
- Engine: not thread-safe by design — callers serialize access

## Testing

- Standard Go testing: `go test ./...`
- Test helpers: `NewForTesting()` constructors, in-memory SQLite databases
- HNSW tests include binary file construction for corruption scenarios
- Heartbeat tests cover signal evaluation, storage operations, nudge protocol
- Test files live alongside source: `*_test.go`

## CI/CD

- `ci.yml`: runs on push/PR to main — Go build + test on ubuntu-latest
- `release.yml`: on v* tags — multi-platform build (linux/darwin, amd64/arm64), creates GitHub release with binaries
- Binary naming: `keyoku-{os}-{arch}` (e.g. keyoku-darwin-arm64)

## What to Flag

- Any change to tier weights, confluence thresholds, or cooldown multipliers in heartbeat_decide.go
- Missing bounds checks on HNSW neighbor indices (source of panics)
- SQL queries with string interpolation instead of parameterized statements
- Missing mutex locks on storage or vector index operations
- Changes to the remember pipeline order (dedup must happen before store)
- New LLM provider calls that don't respect the Provider interface
- Decay threshold changes (Stale=0.3, Archive=0.1, Delete=0.01)
- Missing error handling on storage operations (never silently swallow errors)
- HNSW Load() changes that skip validation of persisted data
- Changes to entity resolution order (exact → alias → semantic → new)
