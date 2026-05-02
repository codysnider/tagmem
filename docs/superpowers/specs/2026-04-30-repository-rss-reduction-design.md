# Repository RSS Reduction Design

Date: 2026-04-30

## Summary

Reduce steady-state repository memory usage by stopping the normal in-process residency of the full metadata corpus. SQLite becomes the authoritative read source for metadata, and repository read/search paths materialize only the rows needed for each request.

This directly targets the strongest remaining memory criticism: the repository layer should not scale its resident metadata memory with the entire store size when it already has a transactional metadata database available.

## Goals

- Reduce steady-state RSS of the main repository/search path.
- Remove full-metadata residency as the normal repository read model.
- Keep CLI and MCP behavior unchanged.
- Keep ranking behavior unchanged.
- Keep source blob lazy hydration behavior unchanged.

## Non-Goals

- Replacing ChroMem in this phase.
- Removing the JSON mirror entirely in this phase.
- Changing benchmark methodology in this phase.
- Redesigning repository mutation semantics again.

## Problem Statement

The repository currently still retains too much metadata state in-process relative to what is needed per request.

Even with SQLite metadata and externalized source blobs in place, memory can still be inflated by:

- full metadata snapshot residency
- full-corpus maps built during reads/search
- caches with unbounded or oversized growth
- read logic that scales memory with total store size rather than request size

The goal of this pass is to make repository memory scale more with request size than with corpus size.

## Recommended Approach

Stop treating the in-memory `Snapshot` as the normal backing store for repository reads. Query SQLite on demand for metadata, fetch only candidate rows needed for search, and hydrate source blobs only for returned rows.

This is preferred over:

- only adding cache caps, which reduces symptoms but leaves the main full-store residency problem intact
- jumping immediately to a service-only model, which helps process duplication but not the per-process memory shape

## Architecture

### Repository Resident State

The repository should keep only minimal process state in memory:

- SQLite DB handle
- vector DB / collection handle
- small bounded caches only where justified

It should no longer keep the full metadata corpus resident as the normal active read model.

### Metadata Reads

The following paths should query SQLite directly:

- `Get`
- `ListMetadata`
- `List`
- `SearchDetailed` candidate hydration
- `DuplicateCheck` candidate hydration

Materialized `Entry` values should be built only for rows actually needed by the request.

### Source Hydration

The existing source blob design remains unchanged:

- metadata rows carry `source_ref`
- full `Source` is hydrated lazily from `sources/`
- only returned rows should be hydrated

### JSON Mirror

The JSON mirror can remain temporarily for compatibility and debugging, but it should no longer drive the repository’s resident read model.

That means:

- it is not the normal source for `Get`, `List`, or `SearchDetailed`
- it is not the normal in-memory cache backing reads
- if it remains, it acts as compatibility/export state rather than active metadata state

## Search Memory Model

### Current Principle

Search memory should scale primarily with:

- candidate count
- result count

and not with:

- total repository row count

### Candidate Handling

`SearchDetailed` should:

1. query the vector index for candidate IDs
2. fetch only those candidate rows from SQLite
3. fetch only the tags for those rows
4. compute ranking/scoring over only those candidates
5. hydrate `Source` only for final returned rows

It should not build a whole-corpus `entriesByID` map from all metadata rows just to score a small candidate set.

### Empty or Keywordless Search

For empty or low-signal text branches that fall back to list-style behavior, continue to use `List(q)` semantics so filters and hydration remain correct, but do so without restoring a full in-memory corpus model.

## Caching Strategy

Only two cache classes are allowed in this pass, and both must be bounded.

### Query Embedding Cache

Keep the query embedding cache, but bound it.

Requirements:

- fixed max size
- oldest-entry eviction or simple LRU behavior
- no unbounded growth with long-lived process uptime

### Optional Small Hydration Cache

If profiling shows repeated candidate-row hydration is hot, allow a very small cache keyed by:

- entry id
- updated_at

This cache should be optional and easy to remove if it adds complexity without clear benefit.

### Explicitly Avoided

Do not add caches for:

- full `entries` table
- full `entry_tags` table
- entire candidate maps across unrelated searches

## Repository Read Behavior

### `ListMetadata`

`ListMetadata` becomes the canonical lightweight metadata path.

It should:

- query SQLite directly
- avoid source hydration
- apply depth/tag filters and sorting in SQL or in a limited result set as appropriate

### `List`

`List` should behave as:

- `ListMetadata`
- then hydrate only the returned rows

### `Get`

`Get` should fetch a single row plus its tags from SQLite, then hydrate source if needed.

### `SearchDetailed`

`SearchDetailed` should:

- preserve current ranking behavior
- preserve current dirty-index fail-fast behavior
- preserve current filters
- avoid full-metadata residency

## Compatibility

This pass must preserve external behavior.

That means:

- CLI output remains logically unchanged
- MCP returned structures remain logically unchanged
- filters, ordering, and hydration remain consistent with current behavior
- vector scoring behavior remains unchanged

If there is any latency trade-off on very small stores due to additional SQLite queries, that is acceptable only if behavior stays correct and memory on larger stores improves materially.

## Testing Strategy

### Regression Tests

Add tests that verify behavior stays identical while memory strategy changes underneath:

- `Get` still returns hydrated source
- `ListMetadata` still avoids source hydration
- `List` still returns hydrated source and correct ordering
- `SearchDetailed` still preserves tag/depth filtering and score ordering
- empty/keywordless search still behaves like filtered list semantics

### Memory-Oriented Benchmarks

Add or extend benchmarks for:

- repository init/open on larger stores
- large-list path
- search path over larger metadata sets

Capture at least:

- elapsed time
- allocations
- peak or approximate RSS where feasible in benchmark harnesses or scripted measurement

### Comparison Target

Compare against the current post-SQLite, post-source-dedupe baseline rather than the older JSON-only architecture.

The question for this pass is:

- did steady-state repository memory go down
- without changing behavior

## Risks

### Risk: More SQLite reads increase latency on tiny stores

Mitigation:

- keep queries focused and indexed
- cap caches sensibly
- measure small-store regressions, but prioritize large-store RSS reduction

### Risk: Search path accidentally changes ordering or filtering

Mitigation:

- preserve existing search regression coverage
- add targeted candidate-fetch ordering tests

### Risk: JSON mirror and SQLite drift logic gets more confusing

Mitigation:

- keep JSON mirror explicitly out of normal read ownership
- limit this pass to reducing read-path residency rather than reworking mirror semantics again

## Rollout Plan

### Phase 1

- remove normal full-corpus metadata residency
- route reads and candidate hydration to SQLite directly

### Phase 2

- bound the query embedding cache
- optionally add a tiny row hydration cache if profiling justifies it

### Phase 3

- add memory-oriented benchmark coverage and compare against the current baseline

## Success Criteria

The design is successful when all of the following are true:

- repository steady-state RSS no longer scales primarily with full metadata corpus residency
- search memory scales more with candidate/result size than with total corpus size
- source hydration remains lazy and correct
- external CLI/MCP behavior remains unchanged
- benchmark and regression coverage prove behavior parity and lower memory use on larger stores

## Recommendation

Proceed with a focused repository RSS reduction pass centered on SQLite-on-demand reads and candidate-scoped search materialization.

This is the most direct way to address the remaining legitimate memory criticism without taking on a much larger service or vector-backend redesign.
