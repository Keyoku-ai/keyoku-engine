# Deployment Guide

This guide covers deploying keyoku-server in production environments.

## Environment Variables

### Required

| Variable | Description |
|---|---|
| `KEYOKU_SESSION_TOKEN` | Authentication token for all API requests (sent as `Bearer` token). Must be set or the server will refuse to start. |

### Server

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_PORT` | `18900` | HTTP listen port. Can also be set via `--port` flag. |
| `KEYOKU_DB_PATH` | `./keyoku.db` | Path to the SQLite database file. Can also be set via `--db` flag. |
| `KEYOKU_CORS_ORIGINS` | *(localhost + sentai.dev/cloud)* | Comma-separated list of additional allowed CORS origins. |

### LLM Provider (Extraction)

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_EXTRACTION_PROVIDER` | `gemini` | LLM provider for memory extraction. One of: `gemini`, `openai`, `anthropic`, `ollama`. |
| `KEYOKU_EXTRACTION_MODEL` | `gemini-2.5-flash` | Model name for extraction. Examples: `gpt-5-mini`, `claude-haiku-4-5-20251001`. |
| `OPENAI_API_KEY` | *(none)* | API key for OpenAI (extraction and/or embeddings). |
| `GEMINI_API_KEY` | *(none)* | API key for Google Gemini (extraction and/or embeddings). |
| `ANTHROPIC_API_KEY` | *(none)* | API key for Anthropic Claude (extraction only). |
| `OPENAI_BASE_URL` | `https://api.openai.com` | Custom base URL for OpenAI-compatible APIs (OpenRouter, LiteLLM, etc.). |
| `ANTHROPIC_BASE_URL` | *(none)* | Custom base URL for Anthropic-compatible APIs. |

### Embeddings

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_EMBEDDING_PROVIDER` | *(matches extraction provider)* | Embedding provider. One of: `openai`, `gemini`, `ollama`. |
| `KEYOKU_EMBEDDING_MODEL` | `gemini-embedding-001` (gemini) or `text-embedding-3-small` (openai) | Embedding model name. |
| `EMBEDDING_BASE_URL` | *(falls back to `OPENAI_BASE_URL`)* | Custom base URL for embeddings. |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Base URL for Ollama server (used for both extraction and embeddings when provider is `ollama`). |
| `OLLAMA_API_KEY` | *(none)* | Optional API key if Ollama is behind auth middleware. |
| `OLLAMA_EMBEDDING_DIMS` | `768` | Vector dimensions for the Ollama embedding model. Must match your model's output size. |

### Quiet Hours

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_QUIET_HOURS_ENABLED` | `false` | Suppress non-immediate heartbeats during quiet hours. Set to `true` or `1`. |
| `KEYOKU_QUIET_HOUR_START` | `23` | Hour (0-23) when quiet hours begin. |
| `KEYOKU_QUIET_HOUR_END` | `7` | Hour (0-23) when quiet hours end. |
| `KEYOKU_QUIET_HOURS_TIMEZONE` | `America/Los_Angeles` | IANA timezone for quiet hours. |

### Heartbeat Delivery

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_DELIVERY_METHOD` | *(none)* | Delivery method for heartbeat messages. Currently supports `cli`. |
| `KEYOKU_DELIVERY_COMMAND` | `openclaw` | CLI command to execute for delivery (supports multi-word, e.g. `docker exec kumo openclaw`). |
| `KEYOKU_DELIVERY_CHANNEL` | *(none)* | Delivery channel (e.g. `telegram`, `slack`). |
| `KEYOKU_DELIVERY_RECIPIENT` | *(none)* | Delivery recipient ID (e.g. `-4970078838` for Telegram group). |
| `KEYOKU_DELIVERY_SESSION_ID` | *(auto-derived)* | Explicit session ID for routing (e.g. `telegram:group:-4970078838`). If empty, derived from `{channel}:group:{recipient}`. |
| `KEYOKU_ADAPTIVE_HEARTBEAT` | `false` | Enable adaptive tick intervals based on time-of-day, signal velocity, and recent activity. |

### Watcher Auto-Start

| Variable | Default | Description |
|---|---|---|
| `KEYOKU_WATCHER_AUTO_START` | `false` | Start the heartbeat watcher automatically on server boot. |
| `KEYOKU_WATCHER_ENTITY_IDS` | *(none)* | Comma-separated entity IDs to watch on auto-start. |
| `KEYOKU_WATCHER_BASE_INTERVAL` | `300000` | Base tick interval in milliseconds (5 minutes). |
| `KEYOKU_WATCHER_MIN_INTERVAL` | `60000` | Minimum tick interval in milliseconds (1 minute). |
| `KEYOKU_WATCHER_MAX_INTERVAL` | `900000` | Maximum tick interval in milliseconds (15 minutes). |

### Config File

Instead of environment variables, you can pass a JSON config file via the `--config` flag:

```bash
keyoku-server --config /etc/keyoku/config.json
```

The JSON fields mirror the environment variables. Environment variables take precedence over the config file. Example:

```json
{
  "port": 18900,
  "db_path": "/var/lib/keyoku/keyoku.db",
  "extraction_provider": "gemini",
  "extraction_model": "gemini-2.5-flash",
  "gemini_api_key": "your-key-here",
  "embedding_provider": "gemini",
  "embedding_model": "gemini-embedding-001",
  "scheduler_enabled": true,
  "quiet_hours_enabled": true,
  "quiet_hour_start": 23,
  "quiet_hour_end": 7,
  "quiet_hours_timezone": "America/New_York"
}
```

---

## Docker

### Dockerfile (multi-stage build)

```dockerfile
# Build stage
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o keyoku-server ./cmd/keyoku-server

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/keyoku-server .

# Data directory for SQLite
RUN mkdir -p /data
VOLUME /data

EXPOSE 18900

ENTRYPOINT ["./keyoku-server"]
CMD ["--db", "/data/keyoku.db"]
```

### docker-compose.yml

```yaml
version: "3.9"

services:
  keyoku:
    build: .
    ports:
      - "18900:18900"
    volumes:
      - keyoku-data:/data
    environment:
      KEYOKU_SESSION_TOKEN: "${KEYOKU_SESSION_TOKEN}"
      KEYOKU_DB_PATH: "/data/keyoku.db"
      KEYOKU_PORT: "18900"

      # LLM provider (pick one set)
      KEYOKU_EXTRACTION_PROVIDER: "gemini"
      KEYOKU_EXTRACTION_MODEL: "gemini-2.5-flash"
      GEMINI_API_KEY: "${GEMINI_API_KEY}"

      # Embeddings (defaults to matching the extraction provider)
      # KEYOKU_EMBEDDING_PROVIDER: "gemini"
      # KEYOKU_EMBEDDING_MODEL: "gemini-embedding-001"

      # Optional
      # KEYOKU_CORS_ORIGINS: "https://myapp.example.com"
      # KEYOKU_QUIET_HOURS_ENABLED: "true"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:18900/api/v1/health"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  keyoku-data:
```

Run it:

```bash
export KEYOKU_SESSION_TOKEN=$(openssl rand -hex 32)
export GEMINI_API_KEY="your-gemini-key"
docker compose up -d
```

---

## Systemd

### Unit file (`/etc/systemd/system/keyoku-server.service`)

```ini
[Unit]
Description=Keyoku Memory Engine
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=keyoku
Group=keyoku
WorkingDirectory=/opt/keyoku

ExecStart=/opt/keyoku/keyoku-server --db /var/lib/keyoku/keyoku.db

# Environment (or use EnvironmentFile)
EnvironmentFile=/etc/keyoku/keyoku.env

Restart=on-failure
RestartSec=5

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/keyoku
PrivateTmp=true

# Limits
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

### Environment file (`/etc/keyoku/keyoku.env`)

```bash
KEYOKU_SESSION_TOKEN=your-secure-token-here
KEYOKU_PORT=18900
KEYOKU_DB_PATH=/var/lib/keyoku/keyoku.db
KEYOKU_EXTRACTION_PROVIDER=gemini
KEYOKU_EXTRACTION_MODEL=gemini-2.5-flash
GEMINI_API_KEY=your-gemini-key
```

### Setup

```bash
# Create user and directories
sudo useradd -r -s /sbin/nologin keyoku
sudo mkdir -p /var/lib/keyoku /opt/keyoku /etc/keyoku
sudo chown keyoku:keyoku /var/lib/keyoku

# Copy binary
sudo cp keyoku-server /opt/keyoku/
sudo chmod 755 /opt/keyoku/keyoku-server

# Create env file (restrict permissions -- contains API keys)
sudo touch /etc/keyoku/keyoku.env
sudo chmod 600 /etc/keyoku/keyoku.env
sudo chown keyoku:keyoku /etc/keyoku/keyoku.env
# Edit /etc/keyoku/keyoku.env with your values

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable keyoku-server
sudo systemctl start keyoku-server
sudo journalctl -u keyoku-server -f
```

---

## Production Checklist

### Security

- [ ] Set `KEYOKU_SESSION_TOKEN` to a strong random value (at least 32 hex characters). All API requests (except `/api/v1/health`) require `Authorization: Bearer <token>`.
- [ ] Bind to localhost if only local processes need access. Use a reverse proxy (nginx, Caddy) for external access with TLS.
- [ ] Restrict `KEYOKU_CORS_ORIGINS` to only your application domains. Defaults include localhost origins and `sentai.dev`/`sentai.cloud`.
- [ ] Protect the environment file (`chmod 600`) since it contains API keys.
- [ ] The health endpoint (`GET /api/v1/health`) is intentionally unauthenticated for monitoring. Do not expose it to the public internet without a reverse proxy.

### Backups

Keyoku stores data in two files that must be backed up together:

1. **SQLite database** (`keyoku.db`) -- all memories, entities, relationships, and metadata. Also stores `keyoku.db-wal` and `keyoku.db-shm` in WAL mode.
2. **HNSW index** (`keyoku.db.hnsw`) -- the vector search index. If this file is lost, the index is automatically rebuilt from embedding BLOBs stored in SQLite on next startup.

Backup strategy:

```bash
# Safe backup using SQLite's .backup (does not require stopping the server)
sqlite3 /var/lib/keyoku/keyoku.db ".backup /backups/keyoku-$(date +%Y%m%d).db"

# Copy the HNSW index (optional -- rebuilds automatically if missing)
cp /var/lib/keyoku/keyoku.db.hnsw /backups/keyoku-$(date +%Y%m%d).db.hnsw
```

### Monitoring

- **Health endpoint**: `GET /api/v1/health` returns `200 OK` with `{"status": "ok", "timestamp": "...", "sse_clients": N}`. No authentication required.
- **Stats endpoint**: `GET /api/v1/stats` returns memory counts by type and state (requires auth).
- **Logs**: The server logs all requests (except health checks) to stdout. Use `journalctl` or Docker logs to monitor.

### Reverse Proxy (nginx example)

```nginx
upstream keyoku {
    server 127.0.0.1:18900;
}

server {
    listen 443 ssl;
    server_name keyoku.example.com;

    ssl_certificate     /etc/ssl/certs/keyoku.pem;
    ssl_certificate_key /etc/ssl/private/keyoku.key;

    location / {
        proxy_pass http://keyoku;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;

        # SSE support
        proxy_set_header Connection '';
        proxy_http_version 1.1;
        chunked_transfer_encoding off;
        proxy_buffering off;
        proxy_cache off;
    }
}
```

---

## Resource Requirements

### CPU

Keyoku itself is lightweight. CPU usage is dominated by:
- HNSW vector search (scales with number of memories and vector dimensions)
- LLM API calls for extraction and consolidation (network-bound, not CPU-bound)

A single core is sufficient for most workloads. HNSW search is single-threaded per query.

### Memory (RAM)

- **Base**: ~20-50 MB for the Go process and SQLite.
- **HNSW index**: Approximately `N * D * 4 bytes * (M + 1)` where N = number of memories, D = dimensions, M = HNSW connectivity (default 16).
  - 10K memories with 1536-dim OpenAI embeddings: ~1 GB
  - 10K memories with 3072-dim Gemini embeddings: ~2 GB
  - 100K memories with 1536-dim: ~10 GB
- The HNSW index lives entirely in memory. Plan RAM accordingly.

### Disk

- **SQLite database**: Roughly 5-10 KB per memory (content + metadata + embedding BLOB).
  - 10K memories: ~50-100 MB
  - 100K memories: ~500 MB - 1 GB
- **HNSW file**: Similar size to the in-memory footprint when serialized.
- WAL mode may temporarily double the database size during heavy writes.

### Embedding Dimensions Reference

| Provider | Model | Dimensions |
|---|---|---|
| OpenAI | `text-embedding-3-small` | 1536 |
| OpenAI | `text-embedding-3-large` | 3072 |
| Gemini | `gemini-embedding-001` | 3072 |
| Gemini | `text-embedding-004` | 768 |
| Ollama | `nomic-embed-text` | 768 |
| Ollama | `mxbai-embed-large` | 1024 |
| Ollama | `all-minilm` | 384 |
| Ollama | `bge-large` | 1024 |

Lower dimensions use less RAM and disk, and produce faster searches. `text-embedding-004` (768 dims) or Ollama's `nomic-embed-text` (768 dims) are good choices for resource-constrained deployments.

> **Switching embedding models**: If you change to a model with different dimensions on an existing database, the HNSW index will reject the new vectors (dimension mismatch). You must delete the database file and start fresh. Pick your embedding model once and stick with it. See the [Ollama guide](ollama.md) for details.
