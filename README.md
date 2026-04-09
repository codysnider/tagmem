<p align="center">
  <img src="assets/logo.png" alt="tagmem logo" width="220">
</p>

# tagmem

`tagmem` is a local Go binary for tagged, depth-aware memory storage and retrieval for LLM agents.

## Reproduce Benchmarks

Run the full Docker/GPU benchmark suite with the recommended default model:

```bash
cd /home/cody/tiered-memory && just datasets && just bench-suite
```

Results are written to:

```bash
/data/tagmem/bench-results/
```

Publishable benchmark docs and copied raw JSON outputs live in:

```bash
benchmarks/
```

The model is intentionally plain:

- `depth 0` for always-load identity, operating context, and critical facts
- `depth 1..n` for progressively deeper background and archive material
- one local data store per user, resolved through XDG directories
- a terminal UI for browsing, filtering, and inspecting entries quickly

## Current shape

This first cut focuses on a solid local foundation instead of feature parity with MemPalace:

- single binary
- persistent local storage in `~/.local/share/tagmem/store.json`
- persistent embedded vector index in `~/.local/share/tagmem/vector/`
- config and cache roots resolved through standard XDG directories
- CLI for `init`, `ingest`, `split`, `add`, `list`, `search`, `context`, `status`, `show`, `depths`, `paths`, `doctor`, `repair`, `mcp`, `bench`, and `tui`
- TUI with depth and tag navigation, search, entry management, and detail inspection

The system is verbatim-first:

- entries are stored as raw text chunks
- semantic search runs over the original text
- there is no lossy shorthand or compression dialect in the storage path

Search is semantic through an embedded local vector index powered by `chromem-go`.

Embedding backend is configurable via environment variables, so you can switch between the built-in local provider and any OpenAI-compatible embeddings endpoint without changing commands.

## Install

```bash
go install github.com/codysnider/tagmem/cmd/tagmem@latest
```

## Docker Workflow

All build, doctor, and benchmark commands can run in Docker with persistent caches under `/data/tagmem`.

Build the dev image:

```bash
just build
```

Prepare datasets under `/data/tagmem/datasets`:

```bash
just datasets
```

Run doctor in Docker:

```bash
just doctor
```

Run LongMemEval in Docker:

```bash
TIERED_MEMORY_EMBED_MODEL=bge-small-en-v1.5 just bench-longmemeval
```

Run the full suite in Docker:

```bash
TIERED_MEMORY_EMBED_MODEL=bge-small-en-v1.5 just bench-suite
```

Run a Docker end-to-end smoke flow for ingest, context, and doctor:

```bash
just e2e-smoke
```

## Principles

- local-first
- CPU-only by default
- original text stays intact
- no lossy memory dialect
- simple user-facing concepts: depths, tags, facts, diary

## Usage

## Embedding Backends

Default embedded local mode:

```bash
export TIERED_MEMORY_EMBED_PROVIDER=embedded
export TIERED_MEMORY_EMBED_MODEL=bge-small-en-v1.5
```

OpenAI-compatible mode:

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

Standard `OPENAI_BASE_URL` and `OPENAI_API_KEY` are also accepted as fallbacks. `OLLAMA_HOST` is accepted as a convenience fallback and is normalized to `/v1`.

Each backend gets its own persistent vector index under `~/.local/share/tiered-memory/vector/`, so switching providers or models does not mix embeddings from different endpoints.

If `tagmem doctor` reports that the endpoint is reachable but no embeddings were returned, the server is usually exposing a chat model instead of an embeddings model.

Initialize local storage:

```bash
tagmem init
```

Add an entry:

```bash
tagmem add --depth 0 --title "Working identity" --body "You are helping ship a local-first memory system."
```

Ingest a project or notes directory:

```bash
tagmem ingest --mode files --depth 1 ~/projects/my_app
```

Respect `.gitignore` by default, or include selected ignored paths:

```bash
tagmem ingest --mode files --include-ignored data/schema.sql,docs/archive.md ~/projects/my_app
```

Ingest conversation exports:

```bash
tagmem ingest --mode conversations --depth 2 ~/chats
```

Use a more general extraction strategy for conversations:

```bash
tagmem ingest --mode conversations --extract general ~/chats
```

Split transcript mega-files before ingesting:

```bash
tagmem split ~/chats
```

List entries:

```bash
tagmem list
tagmem list --depth 0
```

Search:

```bash
tagmem search "identity"
tagmem search --depth 2 "auth migration"
```

Show one entry:

```bash
tagmem show 1
```

Render compact context for startup:

```bash
tagmem context
tagmem context --depth 0
tagmem context --tag auth
```

Show storage status:

```bash
tagmem status
```

Browse with the TUI:

```bash
tagmem tui
```

If no command is provided, the binary opens the TUI.

Validate the current embedding backend:

```bash
tagmem doctor
```

Rebuild the vector index from stored entries:

```bash
tagmem repair
```

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

Run local performance benchmarks:

```bash
tagmem bench perf
```

Run a fuller local performance benchmark with JSON output:

```bash
tagmem bench perf --entries 500 --searches 100 --out ./perf.json
```

Run LongMemEval retrieval benchmark:

```bash
tagmem bench longmemeval /path/to/longmemeval_s_cleaned.json
```

Run LoCoMo retrieval benchmark:

```bash
tagmem bench locomo /path/to/locomo10.json
```

Run ConvoMem retrieval benchmark:

```bash
tagmem bench convomem --limit 25 --category all --cache-dir /tmp/convomem_cache
```

Run a benchmark suite:

```bash
tagmem bench suite \
  --longmemeval /tmp/longmemeval-data/longmemeval_s_cleaned.json \
  --locomo /tmp/locomo/data/locomo10.json \
  --convomem-limit 25 \
  --out-dir ./bench-results
```

## vLLM Embeddings Example

`examples/vllm-embeddings-compose.yml` contains a minimal dedicated embeddings service using `BAAI/bge-small-en-v1.5`.

Example environment:

```bash
export TIERED_MEMORY_EMBED_PROVIDER=openai
export TIERED_MEMORY_OPENAI_BASE_URL=http://10.20.0.2:7870/v1
export TIERED_MEMORY_OPENAI_MODEL=BAAI/bge-small-en-v1.5
export TIERED_MEMORY_OPENAI_API_KEY=
```

Then verify it with:

```bash
tagmem doctor
```

## Benchmark Datasets

LongMemEval data:

```bash
mkdir -p /tmp/longmemeval-data
curl -fsSL -o /tmp/longmemeval-data/longmemeval_s_cleaned.json \
  https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_s_cleaned.json
```

LoCoMo data:

```bash
git clone https://github.com/snap-research/locomo.git /tmp/locomo
```

ConvoMem data is downloaded automatically on first run and cached under `--cache-dir`.

## TUI keys

- `Left` / `Right`: move between `Tags`, `Depths`, and `Entries`
- `Up` / `Down`: navigate lists and forms
- `/`: focus the search box
- `H`: help overlay
- `A` / `E` / `X`: add, edit, delete
- `I` / `C`: import from path or clipboard
- `S` / `D` / `R`: status, doctor, reload
- `Q`: quit

## Storage layout

- data: `~/.local/share/tagmem/store.json`
- vector index: `~/.local/share/tagmem/vector/`
- knowledge graph: `~/.local/share/tagmem/knowledge.json`
- diaries: `~/.local/share/tagmem/diaries/`
- config: `~/.config/tagmem/`
- cache: `~/.cache/tagmem/`

On non-Linux systems, the same code uses the platform-specific directories returned by Go's `os.UserConfigDir`, `os.UserDataDir`, and `os.UserCacheDir`.
