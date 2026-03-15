# Ollama / Local LLM Setup

Run Keyoku entirely on local hardware using [Ollama](https://ollama.com) for both memory extraction and embeddings. No cloud API keys required.

## Prerequisites

- [Ollama](https://ollama.com/download) installed and running
- A chat model for extraction (e.g. `llama3.2`, `qwen3.5:2b`)
- An embedding model (e.g. `nomic-embed-text`, `mxbai-embed-large`)

Pull models before starting Keyoku:

```bash
ollama pull llama3.2
ollama pull nomic-embed-text
```

## Environment Variables

```env
KEYOKU_EXTRACTION_PROVIDER=ollama
KEYOKU_EXTRACTION_MODEL=llama3.2
KEYOKU_EMBEDDING_PROVIDER=ollama
KEYOKU_EMBEDDING_MODEL=nomic-embed-text
OLLAMA_EMBEDDING_DIMS=768
OLLAMA_BASE_URL=http://localhost:11434
```

`OLLAMA_API_KEY` is optional -- only needed if your Ollama instance is behind auth middleware.

## Choosing an Embedding Model

Set `OLLAMA_EMBEDDING_DIMS` to match your model's output dimensions exactly. Mismatched dimensions will cause errors.

| Model | Dimensions | Notes |
|---|---|---|
| `nomic-embed-text` | 768 | Good general-purpose, widely tested |
| `mxbai-embed-large` | 1024 | Higher quality, more RAM |
| `bge-large` | 1024 | Strong multilingual support |
| `all-minilm` | 384 | Smallest, fastest, lower quality |

You can check any model's dimensions on its [Ollama library page](https://ollama.com/library) under the model's manifest (`embedding_length`).

## Choosing an Extraction Model

Any Ollama chat model works. Larger models produce better memory extraction but are slower.

| Model | Size | Speed |
|---|---|---|
| `llama3.2` | 3B | Fast, good for most use cases |
| `qwen3.5:2b` | 2B | Fastest, slightly lower quality |
| `llama3.2:8b` | 8B | Best quality, slower |

## Dimension Mismatch

The HNSW vector index is built for a specific dimension size. If you switch to an embedding model with different dimensions on an existing database, you'll see errors like:

```
HNSW search failed: expected 1536 dimensions, got 768
```

**To fix this**, delete the database file and restart:

```bash
rm /path/to/keyoku.db /path/to/keyoku.db.hnsw
```

All stored memories will be lost. This is why it's important to pick your embedding model once and stick with it. If you need to switch models, export your memories first using the API.

## Docker Compose Example

```yaml
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
      KEYOKU_EXTRACTION_PROVIDER: "ollama"
      KEYOKU_EXTRACTION_MODEL: "llama3.2"
      KEYOKU_EMBEDDING_PROVIDER: "ollama"
      KEYOKU_EMBEDDING_MODEL: "nomic-embed-text"
      OLLAMA_EMBEDDING_DIMS: "768"
      OLLAMA_BASE_URL: "http://host.docker.internal:11434"
    restart: unless-stopped

volumes:
  keyoku-data:
```

> Note: Use `host.docker.internal` (macOS/Windows) or `172.17.0.1` (Linux) to reach Ollama running on the host from inside Docker.

## Hybrid Setup

You can mix local and cloud providers. For example, use Ollama for extraction (free) and OpenAI for embeddings (higher quality):

```env
KEYOKU_EXTRACTION_PROVIDER=ollama
KEYOKU_EXTRACTION_MODEL=llama3.2
OLLAMA_BASE_URL=http://localhost:11434

KEYOKU_EMBEDDING_PROVIDER=openai
KEYOKU_EMBEDDING_MODEL=text-embedding-3-small
OPENAI_API_KEY=sk-...
```

Or use a cloud LLM for extraction and Ollama for embeddings (cheaper):

```env
KEYOKU_EXTRACTION_PROVIDER=gemini
KEYOKU_EXTRACTION_MODEL=gemini-2.5-flash
GEMINI_API_KEY=AIza...

KEYOKU_EMBEDDING_PROVIDER=ollama
KEYOKU_EMBEDDING_MODEL=nomic-embed-text
OLLAMA_EMBEDDING_DIMS=768
OLLAMA_BASE_URL=http://localhost:11434
```

## Auto-Pull

Keyoku automatically pulls the embedding model from Ollama if it's not already downloaded. This happens on first startup and may take a few minutes depending on model size and connection speed.

## Timeouts

Local models are slower than cloud APIs. The Ollama embedder uses a 300-second timeout by default, which should be sufficient for most hardware. If you see timeout errors on slow machines, ensure your model is fully loaded (`ollama list` to verify).
