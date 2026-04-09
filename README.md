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
- TUI

## Install

### Recommended: Docker

The recommended way to run `tagmem` is in Docker.

Build the development image:

```bash
just build
```

Open a shell in the container:

```bash
just shell
```

Run the embedded model health check in Docker:

```bash
just doctor
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

Browse with the TUI:

```bash
tagmem tui
```

If no command is provided, the TUI opens.

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

Prepare datasets:

```bash
just datasets
```

Run the benchmark suite:

```bash
just bench-suite
```

Run an end-to-end smoke flow:

```bash
just e2e-smoke
```

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
- `tagmem tui`

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

## TUI

The TUI is intended to be the main operator interface.

Navigation:

- `Left` / `Right`: move between `Tags`, `Depths`, and `Entries`
- `Up` / `Down`: navigate lists and forms
- `/`: focus search
- `H`: help overlay

Actions:

- `A`: add entry
- `E`: edit selected entry
- `X`: delete selected entry
- `I`: import from path
- `C`: import from clipboard
- `S`: status
- `D`: doctor
- `R`: reload
- `Q`: quit

Forms:

- `Tab`, `Up`, `Down`: move fields
- `Ctrl+S`: save or submit
- `Esc`: cancel

## MCP

Run the MCP server over stdio:

```bash
tagmem mcp
```

Current MCP tools:

- `tiered_memory_status`
- `tiered_memory_paths`
- `tiered_memory_list_depths`
- `tiered_memory_list_tags`
- `tiered_memory_get_tag_map`
- `tiered_memory_list_entries`
- `tiered_memory_search`
- `tiered_memory_show_entry`
- `tiered_memory_check_duplicate`
- `tiered_memory_add_entry`
- `tiered_memory_delete_entry`
- `tiered_memory_kg_query`
- `tiered_memory_kg_add`
- `tiered_memory_kg_invalidate`
- `tiered_memory_kg_timeline`
- `tiered_memory_kg_stats`
- `tiered_memory_graph_traverse`
- `tiered_memory_find_bridges`
- `tiered_memory_graph_stats`
- `tiered_memory_diary_write`
- `tiered_memory_diary_read`
- `tiered_memory_doctor`

## Embedding Backends

### Embedded

The embedded backend runs locally.

Default embedded configuration:

```bash
export TIERED_MEMORY_EMBED_PROVIDER=embedded
export TIERED_MEMORY_EMBED_MODEL=bge-small-en-v1.5
export TIERED_MEMORY_EMBED_ACCEL=auto
```

### OpenAI-compatible

```bash
export TIERED_MEMORY_EMBED_PROVIDER=openai
export TIERED_MEMORY_OPENAI_MODEL=nomic-embed-text
export TIERED_MEMORY_OPENAI_BASE_URL=http://localhost:11434/v1
export TIERED_MEMORY_OPENAI_API_KEY=
```

Short aliases are also supported:

```bash
export TM_EMBED_PROVIDER=openai
export TM_OPENAI_MODEL=nomic-embed-text
export TM_OPENAI_BASE_URL=http://localhost:11434/v1
export TM_OPENAI_API_KEY=
```

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `TIERED_MEMORY_EMBED_PROVIDER` | `embedded` | Selects the embedding backend: `embedded`, `openai`, or `embedded-hash`. |
| `TM_EMBED_PROVIDER` | unset | Short alias for `TIERED_MEMORY_EMBED_PROVIDER`. |
| `TIERED_MEMORY_EMBED_MODEL` | `bge-small-en-v1.5` | Selects the embedded local model. Supported values currently include `all-MiniLM-L6-v2`, `bge-small-en-v1.5`, and `bge-base-en-v1.5`. |
| `TM_EMBED_MODEL` | unset | Short alias for `TIERED_MEMORY_EMBED_MODEL`. |
| `TIERED_MEMORY_EMBED_ACCEL` | `auto` | Embedded acceleration mode: `auto`, `cuda`, or `cpu`. |
| `TM_EMBED_ACCEL` | unset | Short alias for `TIERED_MEMORY_EMBED_ACCEL`. |
| `TIERED_MEMORY_OPENAI_MODEL` | `nomic-embed-text` | Model name for OpenAI-compatible embeddings. |
| `TM_OPENAI_MODEL` | unset | Short alias for `TIERED_MEMORY_OPENAI_MODEL`. |
| `OPENAI_MODEL` | unset | Fallback model name for OpenAI-compatible mode. |
| `TIERED_MEMORY_OPENAI_BASE_URL` | unset | Base URL for an OpenAI-compatible embeddings endpoint. If no path is provided, `/v1` is assumed. |
| `TM_OPENAI_BASE_URL` | unset | Short alias for `TIERED_MEMORY_OPENAI_BASE_URL`. |
| `OPENAI_BASE_URL` | unset | Fallback base URL for OpenAI-compatible mode. |
| `OLLAMA_HOST` | unset | Convenience fallback base URL, normalized to `/v1` if used. |
| `TIERED_MEMORY_OPENAI_API_KEY` | unset | API key for an OpenAI-compatible endpoint. |
| `TM_OPENAI_API_KEY` | unset | Short alias for `TIERED_MEMORY_OPENAI_API_KEY`. |
| `OPENAI_API_KEY` | unset | Fallback API key for OpenAI-compatible mode. |
| `TAGMEM_DATA_ROOT` | `$HOME/.local/share/tagmem` | Host-side root directory for Docker state, including XDG data, model caches, datasets, and benchmark results. |
| `TIERED_MEMORY_BENCH_ROOT` | Docker-only | Root path for benchmark outputs in the Docker workflow. |
| `TIERED_MEMORY_DATASET_ROOT` | Docker-only | Root path for benchmark datasets in the Docker workflow. |
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

## Principles

- local-first
- original text stays intact
- no lossy memory dialect
- tags are primary, depth is secondary
- simple user-facing concepts: depths, tags, facts, diary

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
