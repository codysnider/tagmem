# Small-Store Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve small-workload add/update/delete performance by removing `store.json` writes from normal mutation paths while keeping SQLite as the only live metadata authority.

**Architecture:** Normal repository mutations update SQLite metadata and vector state only. `store.json` becomes a recovery/export mirror rebuilt only in explicit startup/recovery-style paths, not during hot-path mutations.

**Tech Stack:** Go 1.25, SQLite metadata store, existing source blob store, ChroMem vector index, repository tests/benchmarks, `rtk go test`.

---

## File Structure

### Files to modify

- `internal/store/repository.go`
- `internal/store/repository_test.go`
- `internal/store/repository_benchmark_test.go`

## Task 1: Remove JSON mirror writes from normal mutation paths

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing tests for mirror-free mutations**

Add tests proving:

- `AddMany` does not recreate or rewrite a missing mirror
- `Update` does not recreate or rewrite a missing mirror
- `Delete` does not recreate or rewrite a missing mirror

One possible shape:

```go
func TestRepositoryMutationsDoNotRewriteJSONMirror(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	entry, err := repo.Add(AddEntry{Depth: 1, Title: "A", Body: "body"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	infoBefore, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}
	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store.json) error = %v", err)
	}

	if _, _, err := repo.Update(entry.ID, AddEntry{Depth: 1, Title: "A", Body: "updated"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("Stat(store.json) error = %v, want missing mirror to stay missing", err)
	}

	if _, err := repo.Add(AddEntry{Depth: 1, Title: "B", Body: "body"}); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("Stat(store.json) error = %v, want missing mirror to stay missing", err)
	}

	if _, err := repo.Delete(entry.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("Stat(store.json) error = %v, want missing mirror to stay missing", err)
	}

	_ = infoBefore
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `rtk go test ./internal/store -run TestRepositoryMutationsDoNotRewriteJSONMirror -v`

Expected: FAIL because current mutation paths still write the mirror.

- [ ] **Step 3: Remove mirror writes from AddMany, Update, Delete**

Update `internal/store/repository.go`:

- remove `shouldWriteJSONMirrorAfterMutationLocked()` gating from normal mutations
- remove `saveStoreLocked(snapshot)` from the hot path of:
  - `AddMany`
  - `Update`
  - `Delete`
- keep:
  - SQLite metadata mutation
  - vector mutation
  - index-state handling

Target shape in each mutation path:

```go
if err := r.applyMetadataMutationLocked(...); err != nil {
	return err
}
if err := r.indexEntriesImpl(...); err != nil {
	return err
}
if err := r.setIndexStateLocked("ready"); err != nil {
	return err
}
return nil
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `rtk go test ./internal/store -run TestRepositoryMutationsDoNotRewriteJSONMirror -v`

Expected: PASS

## Task 2: Preserve explicit mirror rebuild behavior where it is still needed

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write or tighten mirror-rebuild tests**

Ensure focused tests still prove:

- `Init()` rebuilds missing mirror from SQLite
- `RebuildIndex()` can rebuild a missing mirror if that is part of the current recovery contract

Add or tighten tests as needed, for example:

```go
func TestRepositoryInitRebuildsMissingJSONMirrorFromSQLite(t *testing.T) { /* existing or tightened */ }
func TestRepositoryRebuildIndexRebuildsMissingJSONMirrorFromSQLite(t *testing.T) { /* existing or tightened */ }
```

- [ ] **Step 2: Run the tests to verify current behavior**

Run: `rtk go test ./internal/store -run 'TestRepository(InitRebuildsMissingJSONMirrorFromSQLite|RebuildIndexRebuildsMissingJSONMirrorFromSQLite)' -v`

Expected: PASS or a clear failure if mirror rebuild behavior needs tightening after the hot-path removal.

- [ ] **Step 3: Adjust explicit recovery paths if necessary**

If needed, keep mirror rebuild only in recovery/startup paths:

- `Init()`
- `RebuildIndex()`

and not in ordinary mutation paths.

- [ ] **Step 4: Re-run the tests**

Run: `rtk go test ./internal/store -run 'TestRepository(InitRebuildsMissingJSONMirrorFromSQLite|RebuildIndexRebuildsMissingJSONMirrorFromSQLite|MutationsDoNotRewriteJSONMirror)' -v`

Expected: PASS

## Task 3: Measure the small-workload improvement

**Files:**
- Modify: `internal/store/repository_benchmark_test.go` only if a small mutation benchmark needs cleanup

- [ ] **Step 1: Run current focused tests**

Run:

```bash
rtk go test ./internal/store ./internal/importer ./internal/cli ./internal/mcp ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run the small-store benchmark suite**

Run:

```bash
/usr/bin/go test ./internal/store -run '^$' -bench 'BenchmarkRepository(AddManySQLite|ListMetadataSQLite(Cold)?|SearchDetailedSQLite(Cold)?|InitRebuildsMissingJSONMirror)' -benchmem -benchtime=1x
```

Expected:

- all benchmarks print results
- `BenchmarkRepositoryAddManySQLite` improves versus the current mirror-writing version

- [ ] **Step 3: Record the before/after numbers in the final handoff**

Include:

- add-many before/after
- any effect on list/search cold/warm paths
- what still remains if improvement is not enough

## Self-Review

### Spec coverage

- hot mutations no longer write JSON mirror: Task 1
- mirror rebuild retained for startup/recovery only: Task 2
- benchmark comparison for small-store perf: Task 3

### Placeholder scan

- No placeholders remain.

### Type consistency

- SQLite remains the live authority throughout.
- mirror rebuild stays explicit and recovery-oriented.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-03-small-store-perf.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
