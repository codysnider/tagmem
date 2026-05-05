# CLI Profiling Design

Date: 2026-05-03

## Summary

Add an opt-in profiling mode for real CLI commands so we can measure end-to-end latency and internal phase timing for the actual user-visible path, rather than guessing from microbenchmarks.

## Goals

- Measure real CLI latency for commands we care about.
- Attribute that latency to a small number of meaningful phases.
- Keep default CLI behavior unchanged.
- Use the resulting evidence to choose the next true performance fix.

## Non-Goals

- Shipping profiling output by default.
- Full `pprof`/sampling integration in this phase.
- Profiling every command in the CLI immediately.

## Recommended Approach

Add an opt-in profiling mode activated by an environment variable such as `TAGMEM_PROFILE=1`.

When enabled, the CLI prints a compact timing summary to `stderr` for supported commands.

## Scope

First targets:

- `tagmem add`
- `tagmem search`

If straightforward, `tagmem ingest` may follow later, but it is not required for the first pass.

## Timing Model

### Common phases

- resolve paths
- resolve provider
- repo init

### `add` phases

- total add command time
- source blob ensure
- SQLite metadata mutation
- vector mutation

### `search` phases

- total search command time
- query embedding
- vector query
- SQLite candidate fetch
- rerank
- source hydration

## Output

Recommended output shape:

- human-readable, compact, one block to `stderr`
- only when profiling is enabled

Example:

```text
[profile] command=add
  resolve_paths: 0.4ms
  resolve_provider: 1.1ms
  repo_init: 3.8ms
  add_total: 24.7ms
    source_blob: 2.0ms
    sqlite_mutation: 4.5ms
    vector_mutation: 17.9ms
```

Nested formatting is optional. The main requirement is readable attribution.

## Design Constraints

- default behavior remains unchanged when profiling is off
- profiling should add minimal code intrusion
- timings should be emitted from real command paths, not synthetic-only helpers
- the phase boundaries should map to user-meaningful work, not every tiny function call

## Recommendation

Proceed with opt-in CLI profiling for `add` and `search` first. This gives us the fastest path to trustworthy evidence for the next performance decision.
