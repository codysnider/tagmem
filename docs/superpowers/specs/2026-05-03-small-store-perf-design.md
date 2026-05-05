# Small-Store Performance Design

Date: 2026-05-03

## Summary

Reduce small-workload add/update/delete overhead by removing `store.json` mirror writes from normal mutation paths. SQLite remains the only live metadata authority, and the JSON mirror becomes an explicit recovery/export artifact.

## Goals

- Improve add/update/delete latency for smaller workloads.
- Preserve the current live-authority model: SQLite is authoritative.
- Preserve repository behavior for normal CLI/MCP operations.
- Keep mirror regeneration available for startup recovery and explicit maintenance paths.

## Non-Goals

- Removing `store.json` entirely in this phase.
- Reworking vector mutation semantics.
- Introducing background mirror sync infrastructure in this phase.

## Recommended Approach

Stop rewriting `store.json` during normal repository mutations.

Instead:

- normal mutations update SQLite and vector state only
- the mirror is rebuilt only when explicitly needed

This is the simplest and highest-leverage way to attack the remaining small-store overhead without undoing the authority/memory work already done.

## Architecture

### Live Metadata Authority

- `store.db` remains the only live metadata source

### Mirror Role

- `store.json` remains a compatibility/export/recovery artifact
- it is allowed to be stale between rebuilds
- runtime reads do not depend on it

### Mutation Paths

For `AddMany`, `Update`, and `Delete`:

1. perform SQLite metadata mutation
2. perform vector mutation
3. update index state as needed
4. do not write the JSON mirror

### Mirror Rebuild Moments

Mirror rebuild remains allowed only in explicit or recovery-like paths, such as:

- startup when `store.db` exists and `store.json` is missing
- explicit rebuild/repair/mirror maintenance paths

## Testing Strategy

Regression coverage should prove:

- normal mutations do not recreate or rewrite the mirror
- missing mirror is still rebuilt when startup/recovery expects it
- repository reads/searches remain unchanged

Performance verification should compare before/after for:

- `BenchmarkRepositoryAddManySQLite`
- any small-workload mutation benchmark already present

## Recommendation

Proceed by removing mirror writes from hot mutation paths first. If that is not enough, only then consider deferred or explicit mirror maintenance commands.
