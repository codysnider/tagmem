# SQLite Live Authority Design

Date: 2026-04-30

## Summary

Make `store.db` the only live metadata authority at runtime. `store.json` remains a compatibility/export mirror written from SQLite, but normal reads and searches never import from it again.

This directly targets the last major legitimate memory criticism: the external-refresh path still incurs large RSS spikes because normal repository reads can treat a changed JSON mirror as live input and rebuild state from it on demand.

## Goals

- Remove JSON-to-runtime metadata import from normal read/search paths.
- Eliminate refreshed-reader RSS spikes caused by JSON mirror resynchronization during ordinary reads.
- Keep `store.json` available as a compatibility/export artifact.
- Preserve CLI and MCP behavior.

## Non-Goals

- Removing `store.json` entirely in this phase.
- Changing ranking behavior.
- Reworking vector backend architecture.
- Reintroducing dual-authority sync semantics.

## Architecture

### Metadata Authority

- If `store.db` exists, it is the only live metadata source.
- If `store.db` does not exist and `store.json` exists, perform one-time migration from JSON into SQLite.
- After migration, `store.json` is never imported during normal reads or searches.

### JSON Mirror Role

`store.json` becomes one-way compatibility/export state:

- write path: SQLite -> JSON mirror
- read path: SQLite only

Allowed runtime mirror behaviors:

- if `store.json` is missing, regenerate it from SQLite when needed
- if `store.json` is externally edited, runtime reads ignore those edits

Disallowed runtime mirror behaviors:

- importing changed `store.json` during `Get`, `List`, `Search`, or `SearchDetailed`
- treating JSON drift as a reason to rebuild live metadata state on demand

## Read Path Behavior

Normal read/search paths should only require:

- SQLite metadata queries
- vector candidate queries
- source blob hydration for returned rows

They should not:

- parse `store.json`
- stream `store.json` back into SQLite
- rebuild the vector index because `store.json` changed externally

## Init and Recovery Behavior

### Supported startup cases

1. `store.db` exists, `store.json` exists
   - use SQLite
   - ignore JSON drift

2. `store.db` exists, `store.json` missing
   - rebuild `store.json` from SQLite
   - do not lose metadata

3. `store.db` missing, `store.json` exists
   - migrate JSON to SQLite once
   - rebuild vector index if needed

4. both missing
   - initialize empty store

### Unsupported workflow

External manual edits to `store.json` are no longer a supported live-update path.

## Testing Strategy

Regression coverage should prove:

- external `store.json` edits do not affect normal runtime reads once SQLite exists
- missing `store.json` is rebuilt from SQLite without data loss
- startup migration from JSON still works when SQLite is absent
- ordinary reads/searches no longer need mirror refresh logic

Benchmark coverage should include:

- cold read/search with SQLite-only authority
- warm read/search with SQLite-only authority
- the old refreshed-reader path should no longer exist as a normal benchmark mode

## Recommendation

Proceed by deleting JSON-import behavior from normal repository reads and searches, keeping JSON only as a mirror generated from SQLite.

This is the cleanest way to remove the remaining refresh-path memory spike without taking on a broader redesign.
