# Pure-Go SQLite Metadata Store Design

Date: 2026-04-30

## Summary

Replace `store.json` as the primary mutable metadata store with a pure-Go SQLite database while keeping the current external repository behavior, the compressed shared `sources/` blob store, and the existing ChroMem vector index.

This design targets the biggest remaining operational problem in the current repository layer: every metadata mutation rewrites the entire JSON snapshot. The new design keeps the source deduplication work already completed, preserves CLI and MCP behavior, and reduces add/update/delete work from full-file rewrite operations to transactional row updates.

## Goals

- Reduce add/update/delete latency for the main repository metadata path.
- Preserve existing `store.Repository` behavior for callers.
- Keep verbatim source retrieval unchanged for `Get`, `Search`, `SearchDetailed`, and `show`-style flows.
- Keep cross-architecture and OS portability aligned with the current Docker-first project model by using a pure-Go SQLite driver.
- Preserve the current ChroMem index integration for this phase.
- Provide a safe migration path from legacy `store.json` stores.

## Non-Goals

- Replacing ChroMem in this phase.
- Changing search ranking behavior in this phase.
- Changing CLI or MCP command shapes in this phase.
- Automatically deleting legacy `store.json` after migration.
- Making metadata and vector mutations fully atomic across both storage systems.

## Recommended Approach

Use a pure-Go SQLite driver for metadata, keep shared source blobs on disk, and keep the existing ChroMem vector index.

This is preferred over:

- CGO SQLite drivers, which would unnecessarily expand native build constraints beyond the current Docker-first deployment model.
- Append-only JSON journals, which would reduce some write cost but create a custom persistence format without matching SQLite's transaction semantics.
- A daemon-only fix, which would improve perceived latency but would not remove the underlying whole-file rewrite bottleneck.

## Architecture

The repository layer remains the public facade.

Storage responsibilities become:

- `store.db`: authoritative metadata store for entries, tags, and repository meta state.
- `sources/`: authoritative verbatim source blob storage keyed by `source_ref`.
- `vector/`: derived semantic index keyed by entry id.

Callers continue to use the repository through the same top-level methods:

- `Add`
- `AddMany`
- `Get`
- `List`
- `ListMetadata`
- `Search`
- `SearchDetailed`
- `Update`
- `Delete`
- `RebuildIndex`

The public `Entry` shape remains stable, including hydrated `Source` on read paths that previously returned it.

## SQLite Driver Constraint

The implementation must use a pure-Go SQLite driver.

Reasoning:

- The repo is Docker-first and already uses native ONNX runtime constraints for supported builds, but metadata storage does not need to inherit CGO requirements.
- A pure-Go driver keeps metadata persistence portable across supported and future source-build environments.
- This avoids turning a repository-wide metadata dependency into an additional native-toolchain requirement.

The selected driver must support:

- standard `database/sql`
- file-backed databases
- transactions
- normal indexing and PRAGMA support

## Data Model

### Tables

#### `entries`

- `id INTEGER PRIMARY KEY`
- `depth INTEGER NOT NULL`
- `title TEXT NOT NULL`
- `body TEXT NOT NULL`
- `source_ref TEXT`
- `origin TEXT`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

#### `entry_tags`

- `entry_id INTEGER NOT NULL`
- `tag TEXT NOT NULL`
- `PRIMARY KEY (entry_id, tag)`

#### `meta`

- `key TEXT PRIMARY KEY`
- `value TEXT NOT NULL`

### Indexes

- `entries(updated_at DESC)`
- `entries(depth, updated_at DESC)`
- `entries(origin)`
- `entries(source_ref)`
- `entry_tags(tag, entry_id)`

### Design Notes

- `source_ref` points to the existing compressed source blob store.
- Full source text is not stored in SQLite.
- Tags stay normalized in `entry_tags`.
- No derivable or redundant fields are added.

## Source Blob Behavior

The current externalized source blob system remains in place.

Rules:

- `source_ref` is content-addressed.
- the blob must exist before the metadata row points at it.
- read paths hydrate full `Source` only for rows that are actually returned.
- metadata-only paths must avoid hydrating source blobs.

`ListMetadata` remains the lightweight path for internal callers such as status/tag/depth summaries and importer duplicate origin checks.

## Repository Behavior

### Add and AddMany

For each incoming entry:

1. validate required fields
2. derive tags if needed
3. ensure the `source_ref` blob exists
4. insert into `entries`
5. insert tags into `entry_tags`

All metadata work happens in one SQL transaction.

After the SQL transaction commits, index the new entries in ChroMem.

### Update

1. validate required fields
2. ensure the `source_ref` blob exists if source changed
3. update the `entries` row
4. replace `entry_tags`
5. commit SQL transaction
6. targeted reindex for the updated entry id

### Delete

1. delete `entry_tags` for the entry id
2. delete the `entries` row
3. commit SQL transaction
4. targeted delete from ChroMem by entry id

### ListMetadata

- query SQLite only
- no source blob hydration
- preserve current sorting semantics

### List, Get, Search, SearchDetailed

- query metadata from SQLite
- hydrate `Source` only for returned rows
- keep external behavior unchanged

## Concurrency Model

The current repository-level process lock remains in place for the first SQLite phase.

Why:

- it preserves the repo's existing process coordination model
- it avoids partial behavior drift while metadata and vector state are still maintained separately
- it provides one serialization point while migration risk is highest

Resulting model:

- one writer at a time through the repository
- multiple readers allowed through SQLite reads plus current repository coordination
- SQL transaction first
- vector mutation second

This is intentionally conservative. If the SQLite phase succeeds, the lock policy can be revisited later.

## Consistency and Failure Handling

SQLite metadata is authoritative.

The ChroMem vector index is treated as a derived structure.

Because SQL and ChroMem do not share a real atomic commit boundary, the design introduces explicit index state tracking in `meta`.

### `meta` Keys

- `index_state`: `ready` or `dirty`
- `schema_version`: repository metadata schema version
- optional future migration markers if needed

### Mutation Flow

For metadata-changing operations:

1. set `index_state=dirty`
2. perform and commit the SQL transaction
3. perform the vector mutation
4. if vector mutation succeeds, set `index_state=ready`
5. if vector mutation fails, leave `index_state=dirty` and return an error

### Behavior While Dirty

- `Get`, `List`, and `ListMetadata` continue to work
- source hydration continues to work
- `SearchDetailed` should fail fast with a clear index-needs-repair error
- `repair` / `RebuildIndex` rebuilds the vector index from SQLite metadata and then sets `index_state=ready`

Fail-fast search is preferred over automatic rebuild-on-read because it is more predictable and avoids surprise long-running rebuilds during normal traffic.

## Migration

### Startup Detection

On repository startup:

1. if `store.db` exists, use it
2. else if `store.json` exists, migrate it
3. else initialize a new empty SQLite store

### Migration Steps

1. load `store.json` through the existing migration-aware path
2. create `store.db`
3. create tables and indexes
4. insert all entries and tags in one SQL transaction
5. preserve existing `source_ref` values and existing `sources/` blobs
6. if legacy inline `source` values still exist, externalize them during migration before insertion
7. write `schema_version` and `index_state`
8. leave `store.json` on disk as a first-rollout backup, but stop using it once migration succeeds

### Migration Safety

- do not delete `store.json` automatically
- migration must be idempotent with respect to source blob creation
- startup should fail loudly on partial or corrupt migration, rather than silently mixing backends

## Query and Ranking Behavior

Search ranking behavior does not change in this phase.

Specifically:

- vector documents remain `Title + "\n\n" + Body`
- lexical overlap inputs remain title/body/tags/origin
- source blobs continue to be retrieval payload, not ranking input

The only intended change is the metadata persistence mechanism.

## Testing Strategy

### Migration Tests

- legacy `store.json` migrates into `store.db`
- legacy inline `source` values still load and are externalized
- migrated reads match pre-migration reads

### Repository Correctness Tests

- add/update/delete preserve external behavior
- `ListMetadata` avoids source hydration
- dirty index state is set and cleared correctly
- `RebuildIndex` recovers from induced vector update failure

### Performance Checks

- realistic `AddMany` latency on large stores
- single-entry `Update` latency on large stores
- single-entry `Delete` latency on large stores
- repository startup/open time
- memory use during load and search

These checks should be compared against the current baseline of:

- source blobs already externalized
- metadata still stored in JSON

## Rollout Plan

### Phase 1

- introduce pure-Go SQLite metadata storage behind the repository
- keep source blobs and ChroMem unchanged
- migrate legacy stores automatically on startup
- keep repository-level process lock

### Phase 2

- add targeted benchmark coverage for metadata mutation latency
- validate reduced write latency and lower memory pressure in realistic workloads

### Deferred Follow-Ups

- reconsider the repository lock policy once SQLite metadata proves stable
- consider broader shared-corpus filtering improvements for benchmark/query latency
- reconsider vector backend only if metadata improvements are insufficient

## Risks

### Risk: SQL and vector state diverge

Mitigation:

- explicit `index_state`
- fail-fast search when dirty
- deterministic `repair`

### Risk: migration bug on legacy stores

Mitigation:

- preserve `store.json`
- test inline-source migration and already-externalized-source migration

### Risk: pure-Go SQLite performance is slower than CGO SQLite

Mitigation:

- metadata workload here is mostly transactional row updates and indexed metadata reads
- the target is replacing whole-file JSON rewrites, not beating native SQLite microbenchmarks

### Risk: internal callers accidentally hydrate source blobs unnecessarily

Mitigation:

- keep and use `ListMetadata` for metadata-only paths
- add targeted tests for lightweight internal flows

## Recommendation

Proceed with a pure-Go SQLite metadata store while keeping:

- the current external repository API
- the current source blob store
- the current vector index

This is the smallest credible change that directly attacks the dominant remaining write-latency issue without expanding native portability risk.
