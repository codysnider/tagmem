# Daemon Hot Corpus Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce warm interface latency by letting the local daemon own and reuse hot corpus repositories across repeated interface queries.

**Architecture:** Add daemon-side `ensure_corpus` and `search_corpus` operations, keep a process-lifetime hot corpus cache keyed by the existing stable interface corpus key, and switch `LongMemEval interface` to talk to that daemon cache instead of reopening a corpus repository for every question.

**Tech Stack:** Go 1.25, local Unix socket daemon, existing daemon protocol/client, existing interface corpus keying logic, SQLite metadata store, ChroMem vector index, `rtk go test`, Docker LongMemEval benchmark workflow.

---

## File Structure

### Files to modify

- `internal/daemon/protocol.go`
  - add corpus-cache request/response payloads
- `internal/daemon/backend.go`
  - add hot corpus cache state and corpus operations
- `internal/daemon/server_test.go`
  - add ensure/search corpus daemon tests
- `internal/bench/interface.go`
  - add daemon corpus client helper path
- `internal/bench/interface_test.go`
  - verify daemon corpus reuse routing
- `internal/bench/longmemeval.go`
  - switch interface benchmark to daemon corpus operations

## Task 1: Extend daemon protocol and backend with hot corpus cache operations

**Files:**
- Modify: `internal/daemon/protocol.go`
- Modify: `internal/daemon/backend.go`
- Modify: `internal/daemon/server_test.go`

- [ ] **Step 1: Write the failing daemon corpus-cache tests**

Add tests for:

- `TestDaemonEnsureCorpusCachesOnce`
- `TestDaemonSearchCorpusReturnsResults`

The tests should prove:

- the first `ensure_corpus` builds/loads a corpus
- a second `ensure_corpus` for the same key reuses it instead of rebuilding
- `search_corpus` can query the cached corpus and return ranked origin IDs

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/daemon -run 'TestDaemon(EnsureCorpusCachesOnce|SearchCorpusReturnsResults)' -v`

Expected: FAIL because the protocol/backend do not support corpus cache operations yet.

- [ ] **Step 3: Add protocol messages for corpus operations**

Update `internal/daemon/protocol.go` to support command payloads for:

- `ensure_corpus`
  - `key`
  - `documents`
- `search_corpus`
  - `key`
  - `query`
  - `limit`

Define any small payload structs needed for clean decode/encode handling.

- [ ] **Step 4: Add backend corpus cache state and handlers**

Update `internal/daemon/backend.go`:

- add a daemon-local corpus cache map keyed by corpus key
- each cache entry should hold:
  - open `InterfaceCorpus`
  - maybe `entryCount`
- add handler branches for:
  - `ensure_corpus`
  - `search_corpus`

Behavior:

- `ensure_corpus`: build/open corpus once, cache it, return cache hit/miss info if useful
- `search_corpus`: query the cached corpus and return ranked origin IDs

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/daemon -run 'TestDaemon(EnsureCorpusCachesOnce|SearchCorpusReturnsResults)' -v`

Expected: PASS

## Task 2: Add daemon-backed bench helper path

**Files:**
- Modify: `internal/bench/interface.go`
- Modify: `internal/bench/interface_test.go`

- [ ] **Step 1: Write the failing daemon bench helper tests**

Add tests for:

- `TestInterfaceCorpusDaemonSearchUsesEnsureThenSearch`
- `TestInterfaceCorpusDaemonSearchReusesCachedCorpus`

The tests should prove:

- the bench helper first ensures a corpus, then searches it
- repeated calls for the same corpus key reuse the cached daemon corpus rather than rebuilding

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/bench -run 'TestInterfaceCorpusDaemon.*' -v`

Expected: FAIL because the bench layer does not yet use daemon corpus operations.

- [ ] **Step 3: Add daemon bench helper path**

Update `internal/bench/interface.go`:

- keep existing direct corpus builder path intact
- add a daemon-backed helper that:
  - derives the corpus key
  - sends `ensure_corpus`
  - sends `search_corpus`

Use a small seam or helper so tests can observe the call order and reuse behavior without requiring a full CLI/MCP stack.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `rtk go test ./internal/bench -run 'TestInterfaceCorpusDaemon.*' -v`

Expected: PASS

## Task 3: Switch LongMemEval interface to daemon-backed hot corpus reuse

**Files:**
- Modify: `internal/bench/longmemeval.go`

- [ ] **Step 1: Write a failing focused LongMemEval interface test**

Add/adjust a test proving:

- repeated interface queries for overlapping corpora go through the daemon-backed helper path instead of reopening a corpus directly per question

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/bench -run 'TestLongMemEvalInterfaceUsesDaemonCorpusCache' -v`

Expected: FAIL because `RunLongMemEvalInterfaceWithOptions` still uses direct per-question corpus open/query behavior.

- [ ] **Step 3: Switch the interface benchmark to daemon-backed hot corpus operations**

Update `internal/bench/longmemeval.go`:

- keep the current per-question corpus shape
- replace per-question `builder.NewCorpus(...); corpus.Search(...)` with the daemon-backed ensure/search helper path
- preserve output and metric calculation behavior

- [ ] **Step 4: Run the focused test to verify it passes**

Run: `rtk go test ./internal/bench -run 'TestLongMemEvalInterfaceUsesDaemonCorpusCache' -v`

Expected: PASS

## Task 4: Re-run Docker LongMemEval and compare warm latency

**Files:**
- No additional code files required unless tiny benchmark glue is needed

- [ ] **Step 1: Run package verification**

Run:

```bash
rtk go test ./internal/daemon ./internal/bench ./internal/store ./internal/importer ./internal/cli ./internal/mcp ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run Docker LongMemEval benchmark in both modes and a warm rerun**

Run:

```bash
TAGMEM_BENCH_PATH=both ./scripts/cmd/docker-bench-longmemeval/run.sh
TAGMEM_BENCH_PATH=interface ./scripts/cmd/docker-bench-longmemeval/run.sh
```

Expected:

- component metrics remain effectively unchanged
- warm interface time improves materially relative to the last good non-experimental warm baseline
- interface recall remains comparable

- [ ] **Step 3: Record the before/after numbers in the final handoff**

Include:

- component recall/time
- warm interface recall/time before
- warm interface recall/time after
- whether the daemon-backed hot corpus cache is a viable direction

## Self-Review

### Spec coverage

- daemon hot corpus cache operations: Task 1
- bench helper daemon path: Task 2
- LongMemEval interface switch: Task 3
- real Docker benchmark comparison: Task 4

### Placeholder scan

- No placeholders remain.

### Type consistency

- corpus keying, ensure/search operations, and daemon cache reuse are used consistently.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-02-daemon-hot-corpus-cache.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
