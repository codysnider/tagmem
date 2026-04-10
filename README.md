<p align="center">
  <img src="assets/logo.png" alt="tagmem logo" width="220">
</p>

# tagmem

`tagmem` is local memory storage and retrieval for LLM agents.

It is built around a simple model:

- `entries` store verbatim text
- `tags` are the primary way to organize and filter memory
- `depth` indicates how close a memory should stay to the surface
- `facts` store structured knowledge
- `diary` stores agent-specific notes

The system is local-first, retrieval-oriented, and designed to be usable through:

- CLI
- MCP

## Install

### Recommended: Docker

The recommended way to run `tagmem` is in Docker.

Published image:

```bash
ghcr.io/codysnider/tagmem
```

Run the CLI from the published image:

```bash
docker run --rm ghcr.io/codysnider/tagmem:latest help
```

Run the MCP server from the published image:

```bash
docker run -i --rm ghcr.io/codysnider/tagmem:latest mcp
```

### Build from source

If you want to build locally from source:

```bash
go build ./cmd/tagmem
```

This creates the `tagmem` binary in the current directory.

### Install with Go

If you want a direct Go-based install:

```bash
go install github.com/codysnider/tagmem/cmd/tagmem@latest
```

This is best used once a release/tag workflow is in place. Docker is still the preferred runtime path.

## Quick Start

Initialize storage:

```bash
tagmem init
```

Add an entry:

```bash
tagmem add --depth 0 --title "Working identity" --body "You are helping ship a local-first memory system."
```

Search:

```bash
tagmem search "identity"
tagmem search --depth 2 "auth migration"
tagmem search --tag auth "token refresh"
```

## Docker Workflow

The Docker workflow keeps model files, cache, and benchmark artifacts outside the repo in mounted volumes.

Default Docker data root:

```bash
$HOME/.local/share/tagmem
```

Override it if you want Docker state elsewhere:

```bash
export TAGMEM_DATA_ROOT=/path/to/tagmem-data
```

The helper `just` commands are primarily for development and benchmarking. Most users only need the published image or the `go install` path.

## Commands

Core commands:

- `tagmem init`
- `tagmem ingest`
- `tagmem split`
- `tagmem add`
- `tagmem list`
- `tagmem search`
- `tagmem show`
- `tagmem status`
- `tagmem context`
- `tagmem depths`
- `tagmem paths`
- `tagmem doctor`
- `tagmem repair`
- `tagmem mcp`
- `tagmem bench`

Examples:

```bash
tagmem ingest --mode files --depth 1 ~/projects/my_app
tagmem ingest --mode conversations --depth 2 ~/chats
tagmem ingest --mode conversations --extract general ~/chats
tagmem split ~/chats
tagmem status
tagmem context --depth 0
tagmem context --tag auth
tagmem show 1
```

## MCP

Run the MCP server over stdio:

```bash
tagmem mcp
```

Current MCP tools:

- `tagmem_status`
- `tagmem_paths`
- `tagmem_list_depths`
- `tagmem_list_tags`
- `tagmem_get_tag_map`
- `tagmem_list_entries`
- `tagmem_search`
- `tagmem_show_entry`
- `tagmem_check_duplicate`
- `tagmem_add_entry`
- `tagmem_delete_entry`
- `tagmem_kg_query`
- `tagmem_kg_add`
- `tagmem_kg_invalidate`
- `tagmem_kg_timeline`
- `tagmem_kg_stats`
- `tagmem_graph_traverse`
- `tagmem_find_bridges`
- `tagmem_graph_stats`
- `tagmem_diary_write`
- `tagmem_diary_read`
- `tagmem_doctor`

## Embedding Backends

### Embedded

The embedded backend runs locally.

Default embedded configuration:

```bash
export TAGMEM_EMBED_PROVIDER=embedded
export TAGMEM_EMBED_MODEL=bge-small-en-v1.5
export TAGMEM_EMBED_ACCEL=auto
```

### OpenAI-compatible

```bash
export TAGMEM_EMBED_PROVIDER=openai
export TAGMEM_OPENAI_MODEL=nomic-embed-text
export TAGMEM_OPENAI_BASE_URL=http://localhost:11434/v1
export TAGMEM_OPENAI_API_KEY=
```

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `TAGMEM_EMBED_PROVIDER` | `embedded` | Selects the embedding backend: `embedded`, `openai`, or `embedded-hash`. |
| `TAGMEM_EMBED_MODEL` | `bge-small-en-v1.5` | Selects the embedded local model. Supported values currently include `all-MiniLM-L6-v2`, `bge-small-en-v1.5`, and `bge-base-en-v1.5`. |
| `TAGMEM_EMBED_ACCEL` | `auto` | Embedded acceleration mode: `auto`, `cuda`, or `cpu`. |
| `TAGMEM_OPENAI_MODEL` | `nomic-embed-text` | Model name for OpenAI-compatible embeddings. |
| `OPENAI_MODEL` | unset | Fallback model name for OpenAI-compatible mode. |
| `TAGMEM_OPENAI_BASE_URL` | unset | Base URL for an OpenAI-compatible embeddings endpoint. If no path is provided, `/v1` is assumed. |
| `OPENAI_BASE_URL` | unset | Fallback base URL for OpenAI-compatible mode. |
| `OLLAMA_HOST` | unset | Convenience fallback base URL, normalized to `/v1` if used. |
| `TAGMEM_OPENAI_API_KEY` | unset | API key for an OpenAI-compatible endpoint. |
| `OPENAI_API_KEY` | unset | Fallback API key for OpenAI-compatible mode. |
| `TAGMEM_DATA_ROOT` | `$HOME/.local/share/tagmem` | Host-side root directory for Docker state, including XDG data, model caches, datasets, and benchmark results. |
| `TAGMEM_BENCH_ROOT` | Docker-only | Root path for benchmark outputs in the Docker workflow. |
| `TAGMEM_DATASET_ROOT` | Docker-only | Root path for benchmark datasets in the Docker workflow. |
| `XDG_CONFIG_HOME` | platform default | XDG config root used for config and identity files. |
| `XDG_DATA_HOME` | platform default | XDG data root used for storage, vectors, knowledge graph, diaries, and models. |
| `XDG_CACHE_HOME` | platform default | XDG cache root. |

## Storage Layout

- data: `~/.local/share/tagmem/store.json`
- vector index: `~/.local/share/tagmem/vector/`
- knowledge graph: `~/.local/share/tagmem/knowledge.json`
- diaries: `~/.local/share/tagmem/diaries/`
- models: `~/.local/share/tagmem/models/`
- config: `~/.config/tagmem/`
- cache: `~/.cache/tagmem/`

`tagmem` is local-first, keeps original text intact, avoids lossy memory dialects, and uses simple user-facing concepts: entries, tags, depth, facts, and diary.

## Benchmarks

Benchmarks are documented under `benchmarks/`.

That folder includes:

- methodology
- machine specs
- charts
- raw benchmark outputs
- comparison notes against published external baselines

Run the benchmark suite in Docker:

```bash
just bench-suite
```

Or run a specific benchmark directly:

```bash
tagmem bench perf
tagmem bench longmemeval /path/to/longmemeval_s_cleaned.json
tagmem bench locomo /path/to/locomo10.json
tagmem bench convomem --limit 25 --category all --cache-dir /tmp/convomem_cache
tagmem bench suite \
  --longmemeval /path/to/longmemeval_s_cleaned.json \
  --locomo /path/to/locomo10.json \
  --convomem-limit 25 \
  --out-dir ./bench-results
```
