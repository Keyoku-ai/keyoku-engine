<div align="center">

  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/banner-light.svg">
    <img alt="keyoku-engine" src="assets/banner-light.svg" width="800">
  </picture>

  <p>
    <strong>The memory engine that makes AI agents feel human.</strong><br>
    <sub>Memories that live, decay, and consolidate. A knowledge graph that understands relationships.<br>A brain that decides what matters — all running locally in pure Go.<br>The backend behind <a href="https://github.com/Keyoku-ai/keyoku">Keyoku</a> — the workflow harness for coding agents.</sub>
  </p>

  <p>
    <a href="#what-this-is">What This Is</a> &bull;
    <a href="#quick-start">Quick Start</a> &bull;
    <a href="#the-brain">The Brain</a> &bull;
    <a href="#api">API</a> &bull;
    <a href="#architecture">Architecture</a>
  </p>

  [![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
  [![SQLite](https://img.shields.io/badge/SQLite-pure%20Go-003B57?style=flat-square&logo=sqlite&logoColor=white)](https://pkg.go.dev/modernc.org/sqlite)
  [![License: BSL 1.1](https://img.shields.io/badge/License-BSL%201.1-6366f1?style=flat-square)](LICENSE)
  [![GitHub Stars](https://img.shields.io/github/stars/Keyoku-ai/keyoku-engine?style=flat-square)](https://github.com/Keyoku-ai/keyoku-engine/stargazers)

</div>

<br>

## What This Is

Keyoku is not a vector database. It's a **cognitive engine** — the system that sits behind an AI agent and gives it the ability to remember, understand connections, and act on what it knows.

**Memory that lives.** Memories aren't rows in a table. They're extracted from conversations, deduplicated by meaning, merged when they overlap, and decay when unused — just like human memory. Important things stick. Irrelevant things fade.

**A knowledge graph that grows itself.** Every conversation automatically builds a graph of people, organizations, and how they relate. The agent doesn't just remember "Alice mentioned Bob" — it knows Alice works at Acme, Bob is her manager, and they're collaborating on a Q3 launch.

**A brain that doesn't wait to be asked.** The heartbeat system scans 12 signal categories across your entire memory store — deadlines, goals, sentiment shifts, relationship gaps, behavioral patterns — combines weak signals into decisions, enriches everything with the knowledge graph, and compiles it through an LLM into a single actionable output.

Zero external dependencies. Pure Go. SQLite + in-process HNSW. Your data never leaves your machine.

## Keyoku Harness Integration

The engine is the brain of [Keyoku](https://github.com/Keyoku-ai/keyoku) — the
workflow harness for coding agents. Point the harness at a running engine:

```bash
keyoku-server &                     # embedder only; Ollama works, no API key needed
export KEYOKU_ENGINE_URL=http://localhost:8080
```

The harness mirrors its knowledge layer (connector descriptions, mined
practice patterns, agent research, CLAUDE.md conventions) into the engine via
`POST /api/v1/seed` — the extraction-free door — and upgrades `knowledge_query`
to semantic search via `POST /api/v1/search`. Decay, dedup, and the knowledge
graph apply automatically. The harness degrades to local JSONL when no engine
is configured.

> **Subsystem status for the harness product:** storage, search, embeddings,
> dedup, decay, and the knowledge graph are the active core. The heartbeat /
> proactive-nudge subsystem predates the harness relaunch and is not used by
> it — it remains maintained for standalone memory-engine deployments and
> will be superseded by a slimmer trigger scan.

## Quick Start

### As a Go library

```bash
go get github.com/keyoku-ai/keyoku-engine
```

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

// Search by meaning
memories, _ := k.Search(ctx, keyoku.SearchInput{
    EntityID: "user-123",
    Query:    "what are their preferences?",
    Limit:    5,
})

// Zero-token heartbeat — no LLM call, pure local query
heartbeat, _ := k.HeartbeatCheck(ctx, keyoku.HeartbeatCheckInput{
    EntityIDs: []string{"user-123"},
})
```

### As an HTTP server

```bash
make build
export OPENAI_API_KEY="sk-..."
./bin/keyoku-server --db ./memories.db
```

Default port: `18900` (override with `--port` or `KEYOKU_PORT`).

## The Brain

The heartbeat is Keyoku's most important subsystem. It turns passive memory into active intelligence.

### How it works

Every tick, the brain runs a **zero-token scan** — 12 SQL-driven checks across your entire memory store, costing nothing. Only when it finds something worth acting on does it spend LLM tokens to compile a response.

### What it scans

| Signal | Example |
|--------|---------|
| **Scheduled tasks** | "Daily standup prep" fired at 9am |
| **Deadline proximity** | Project due in 47 minutes — forced immediate |
| **Pending work** | 3 unfinished plans, highest importance first |
| **Goal progress** | "API migration" went from 20% to 60% since last check |
| **Session continuity** | User was mid-conversation about deployment 2h ago |
| **Sentiment shift** | Mood declined over last 5 conversations |
| **Relationship gaps** | Haven't heard from Alice in 12 days |
| **Knowledge gaps** | Agent couldn't answer a question last week |
| **Behavioral patterns** | User typically does code reviews on Tuesdays |
| **Conflicts** | Two memories contradict each other |
| **Stale monitors** | A tracked plan hasn't been touched in 24h |
| **Decaying memories** | Important memory approaching decay threshold |

### What makes it smart

The brain doesn't just check signals — it **thinks about them**:

- **Confluence.** Five weak signals combine to trigger action. A fading memory + a sentiment dip + a relationship gap + a stale plan + a behavioral pattern = worth mentioning, even though none alone would be.

- **Response awareness.** If the user ignores nudges, the brain backs off — 3x cooldown at 30% response rate, 10x at under 10%. No spam.

- **Topic dedup.** Same topic won't resurface just because the memory ID changed. The brain checks entity overlap between current signals and recent decisions.

- **Deadline gradient.** A deadline in 47 minutes is not the same as one in 20 hours. Proximity scoring creates urgency naturally — and critical deadlines bypass quiet hours.

- **Progress detection.** When a goal improves (at_risk to on_track) or a silent contact re-engages, the brain notices and surfaces it as a positive change. Agents should acknowledge improvement, not just nag.

- **Pattern matching.** Nudges are matched to the user's behavioral patterns. If they typically do deep work on Wednesdays, the brain finds related plans and surfaces them.

- **Graph enrichment.** Before sending signals to the LLM, the brain traverses the knowledge graph — collecting entity relationships so the LLM understands WHO is involved and HOW they connect, not just raw text.

The LLM receives a structured prompt with signals, knowledge graph context, and positive changes. It outputs an action brief, recommended actions, urgency level, and a user-facing message. The brain gates this — the LLM can suppress a "should act" decision, but never promote one. The engine always has final say.

### Autonomy levels

| Level | Sees signals | Messages user | Takes action |
|-------|:---:|:---:|:---:|
| `observe` | Yes | No | No |
| `suggest` | Yes | Yes (as suggestions) | No |
| `act` | Yes | Yes | Yes |

## Memory Lifecycle

Memories flow through four states, managed automatically by background jobs:

```
ACTIVE ──decay──▶ STALE ──decay──▶ ARCHIVED ──decay──▶ DELETED
  ▲                                    │
  └────── access reinforcement ────────┘
```

**Decay** follows an Ebbinghaus curve with access-frequency boosting — memories that get retrieved resist decay. 10 accesses = 2.2x stability. 50 accesses = 2.96x stability. Stability varies by type: ephemeral memories last 3 days, identity memories last 365.

**Consolidation** runs hourly, merging similar memories (0.85+ similarity) via LLM synthesis — eliminating redundancy while preserving all important facts.

**Deduplication** catches duplicates at write time — both exact (hash) and semantic (0.95 threshold). Near-duplicates (0.75+) get merged instead of rejected.

**Conflict detection** uses LLM analysis to find contradictions and recommend resolution: keep existing, use new, merge, or ask the user.

## Knowledge Graph

Built automatically from every conversation:

- **Entities** — people, organizations, locations, products. Extracted via proper noun detection + type inference. Aliases tracked (max 10 per entity). Semantic matching at 0.85 threshold.

- **Relationships** — 40+ auto-detected types (works_at, manages, friend_of, married_to, etc.) with strength, confidence, and evidence tracking. Bidirectional inference.

- **Graph traversal** — BFS up to 5 hops, shortest path between entities, LLM-powered explanations of how two entities connect.

```go
// Find how Alice and Bob are connected
path, _ := k.Graph().FindPath(ctx, "owner-id", aliceID, bobID)

// Get a natural language explanation
explanation, _ := k.Graph().ExplainConnection(ctx, "owner-id", aliceID, bobID)
// => "Alice works at Acme Corp where Bob is a senior engineer.
//     They're both assigned to the Q3 product launch."
```

## API

<details>
<summary><strong>Memory</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/remember` | Extract & store memories from messages |
| POST | `/api/v1/search` | Vector similarity search |
| GET | `/api/v1/memories` | List memories (paginated) |
| GET | `/api/v1/memories/{id}` | Get single memory |
| DELETE | `/api/v1/memories/{id}` | Delete memory |
| DELETE | `/api/v1/memories` | Delete all memories for entity |
| GET | `/api/v1/memories/sample` | Representative sample |

</details>

<details>
<summary><strong>Heartbeat & Monitoring</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/heartbeat/check` | Zero-token signal detection |
| POST | `/api/v1/heartbeat/context` | Extended heartbeat with LLM analysis |
| POST | `/api/v1/watcher/start` | Start continuous heartbeat monitor |
| POST | `/api/v1/watcher/stop` | Stop watcher |
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/stats` | Global stats |
| GET | `/api/v1/stats/{entity_id}` | Per-entity stats |

</details>

<details>
<summary><strong>Scheduling</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/schedule` | Create scheduled memory |
| POST | `/api/v1/schedule/ack` | Mark schedule as executed |
| PUT | `/api/v1/schedule/{id}` | Update schedule |
| DELETE | `/api/v1/schedule/{id}` | Cancel schedule |
| GET | `/api/v1/scheduled` | List active schedules |

</details>

<details>
<summary><strong>Teams</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/teams` | Create team |
| GET | `/api/v1/teams/{id}` | Get team |
| POST | `/api/v1/teams/{id}/members` | Add member |
| DELETE | `/api/v1/teams/{id}/members/{agent_id}` | Remove member |

</details>

<details>
<summary><strong>Events</strong></summary>

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/events` | SSE event stream |
| POST | `/api/v1/consolidate` | Trigger consolidation |

</details>

## Architecture

```
┌──────────────────────────────────────────────┐
│         keyoku-server (HTTP + SSE)           │
│                                              │
│  ┌────────────┐ ┌──────────┐ ┌────────────┐  │
│  │  Remember  │ │  Search  │ │   Brain    │  │
│  │  Extract   │ │  Tiered  │ │ Heartbeat  │  │
│  └──────┬─────┘ └────┬─────┘ └─────┬──────┘  │
│         └─────────────┼─────────────┘        │
│                       ▼                      │
│  ┌────────────────────────────────────────┐  │
│  │           Engine Layer                 │  │
│  │  dedup · conflict · entity · decay     │  │
│  │  graph · ranker · scorer · retrieval   │  │
│  └────────────────────┬───────────────────┘  │
│                       ▼                      │
│  ┌────────────────────────────────────────┐  │
│  │        Storage (SQLite WAL)            │  │
│  │  memories · entities · relationships   │  │
│  │  teams · schemas · agent state         │  │
│  └──────┬───────────┬───────────┬─────────┘  │
│         ▼           ▼           ▼            │
│  ┌────────────┐ ┌──────────┐ ┌────────────┐  │
│  │  LRU Hot   │ │   HNSW   │ │  FTS (SQL) │  │
│  │   Cache    │ │  Vector  │ │   Tier 3   │  │
│  │   Tier 1   │ │  Tier 2  │ │            │  │
│  └────────────┘ └──────────┘ └────────────┘  │
│                                              │
│  ┌────────────────────────────────────────┐  │
│  │         Background Jobs                │  │
│  │  decay · consolidation · archival      │  │
│  │  purge · eviction                      │  │
│  └────────────────────────────────────────┘  │
└───────────────────┬──────────┬───────────────┘
                    │          │
             ┌──────▼─────┐ ┌─▼────────┐
             │    LLM     │ │ Embedder  │
             │   Extract  │ │  Vectors  │
             └────────────┘ └───────────┘
```

## LLM Providers

### Extraction Models

| Provider | Model | Notes |
|----------|-------|-------|
| Google Gemini | **gemini-3.1-flash-lite-preview** (recommended) | Cheapest and fastest, near-perfect quality |
| Google Gemini | gemini-2.5-flash | Thinking model, highest accuracy, slower |
| OpenAI | gpt-4.1-mini | Balanced speed and quality |
| OpenAI | gpt-4.1-nano | Cheapest OpenAI, slightly less reliable on complex schemas |
| Anthropic | claude-haiku-4-5-20251001 | Fast, top-tier quality |
| Ollama | llama3.2, qwen3.5:2b, etc. | Local inference, no API key required |

### Embedding Models

| Provider | Model | Dimensions | Notes |
|----------|-------|------------|-------|
| Google Gemini | gemini-embedding-001 | 3072 | Default — included with Gemini API key |
| OpenAI | text-embedding-3-small | 1536 | Default — included with OpenAI API key |
| Ollama | nomic-embed-text | 768 | Local, no API key required |
| Ollama | mxbai-embed-large | 1024 | Local, higher quality |

> **Note:** Anthropic does not offer embedding models. If you use Anthropic for extraction, pair it with Gemini, OpenAI, or Ollama for embeddings.

For local Ollama setup, see the [Ollama guide](docs/ollama.md).

Custom base URLs support OpenRouter, LiteLLM, and self-hosted endpoints.

## Configuration

```go
keyoku.Config{
    DBPath:             "./keyoku.db",
    ExtractionProvider: "google",        // "openai", "google", "anthropic"
    ExtractionModel:    "gemini-3.1-flash-lite-preview",
    GeminiAPIKey:       "AI...",
    EmbeddingModel:     "gemini-embedding-001",

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
make cross         # Cross-compile (darwin/linux × arm64/amd64)
make lint          # golangci-lint
```

Requires Go 1.24+.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Business Source License 1.1 — see [LICENSE](LICENSE) for details.

> [!NOTE]
> The BSL grants full usage rights for non-production and development use. Production use requires a commercial license until the change date (2029-03-10), after which the code converts to Apache 2.0.

<br>
<div align="center">
  <sub>Built by <a href="https://github.com/keyoku-ai">Keyoku</a></sub>
</div>
