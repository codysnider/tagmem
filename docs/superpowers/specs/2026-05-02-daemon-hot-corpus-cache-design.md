# Daemon Hot Corpus Cache Design

Date: 2026-05-02

## Summary

Reduce warm interface latency by teaching the local daemon to own and reuse hot `InterfaceCorpus` instances across repeated queries, instead of reopening a corpus repository for every question.

## Goals

- Improve the real warm product path behind the interface benchmark.
- Keep the optimization local-only and daemon-backed.
- Preserve benchmark honesty by using the same repository/search path, just with a hot corpus cache.
- Avoid the failed giant shared-corpus scoped-search approach.

## Non-Goals

- Replacing the vector backend in this phase.
- Adding remote network service behavior.
- Designing a general corpus-eviction policy in the first pass.

## Recommended Approach

Add daemon-side corpus cache operations so the daemon can hold `InterfaceCorpus` repositories open in memory and serve repeated searches against them.

The daemon becomes the owner of hot corpus state for benchmark and future local product flows. The first version should keep the cache simple and process-lifetime-scoped.

## Architecture

### New Daemon Responsibilities

The daemon should be able to:

- ensure a corpus exists and is loaded: `ensure_corpus`
- search an already-cached corpus: `search_corpus`

Optional later operations may include:

- `close_corpus`
- `list_corpora`
- explicit eviction or TTL controls

### Corpus Key

Reuse the existing stable corpus-key logic already used by interface benchmark caching.

That means a corpus is identified by:

- corpus document IDs and content
- document mode/extract/depth
- created/updated timestamps
- provider/index key context as already encoded today

### Cached State

Each hot corpus entry should hold:

- corpus key
- open `store.Repository`
- root path for the corpus data
- metadata such as entry count and last access time if useful later

The daemon owns these instances and reuses them across requests.

## Request Flow

### `ensure_corpus`

Input:

- corpus key
- full corpus document list

Behavior:

- if corpus is already cached, return success immediately
- if corpus is not cached:
  - build/open it using the existing `InterfaceCorpusBuilder` logic
  - keep it resident in the daemon cache

### `search_corpus`

Input:

- corpus key
- query text
- result limit

Behavior:

- look up the hot corpus by key
- run normal repository search against that corpus
- return ranked origin IDs/results

## Benchmark Integration

For `LongMemEval interface`:

- per question, derive corpus documents as before
- derive the stable corpus key
- call `ensure_corpus`
- call `search_corpus`

This preserves the realistic per-question corpus shape, but removes repeated repo open/init cost from warm runs.

## Why This Is Better Than The Failed Shared-Corpus Approach

- it keeps scopes small and natural
- it does not need large allowlist emulation on the vector backend
- it works with the current ChroMem model instead of fighting it
- it optimizes a real daemon-backed product path, not just the benchmark harness

## Memory Trade-off

The daemon will retain hot corpora in memory, so warm latency improves at the cost of process memory growth.

For the first pass, that is acceptable if we keep the scope narrow:

- process-lifetime cache only
- no eviction policy yet
- benchmark/test usage proves reuse and latency improvement

If memory growth becomes a problem, a later follow-up can add:

- max corpus count
- LRU eviction
- explicit close operations

## Testing Strategy

### Daemon Tests

- ensure the daemon caches a corpus after first build
- ensure repeated `ensure_corpus` calls do not rebuild the corpus
- ensure `search_corpus` returns results from the cached corpus

### Benchmark/Interface Tests

- prove the interface benchmark path routes through daemon corpus operations
- prove warm repeated calls reuse cached hot corpora

### Performance Verification

- rerun Docker `LongMemEval` component + interface benchmark
- compare warm interface time before/after
- confirm interface quality metrics remain stable

## Recommendation

Proceed with a daemon-backed hot corpus cache for the interface benchmark and any future local corpus-scoped product paths. It is the most direct warm-latency improvement that fits the current backend architecture.
