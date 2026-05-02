# SQLite Live Authority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `store.db` the only live metadata authority so normal repository reads and searches never import `store.json` again.

**Architecture:** Keep SQLite as the sole runtime metadata source, keep `store.json` as a one-way mirror generated from SQLite, and remove JSON-refresh logic from normal read/search paths. Startup still migrates legacy JSON when SQLite is absent, and missing mirrors can be regenerated from SQLite.

**Tech Stack:** Go 1.25, SQLite metadata store, existing source blob store, ChroMem vector index, repository-level locking, `rtk go test`, focused repository benchmarks.

---

## File Structure

### Files to modify

- `internal/store/repository.go`
  - Remove JSON-import behavior from normal read/search paths
  - Simplify mirror authority handling
- `internal/store/sqlite_migration.go`
  - Keep one-time migration logic only
  - Add mirror rebuild helper from SQLite if needed
- `internal/store/repository_test.go`
  - Replace external-JSON-refresh expectations with SQLite-authority expectations
- `internal/store/repository_benchmark_test.go`
  - Remove refreshed-reader benchmark mode as a normal path and add replacement mirror-regeneration coverage if needed

## Task 1: Stop treating external JSON edits as live metadata input

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing authority tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryIgnoresExternalJSONEditWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Stable", Body: "before body", Source: "before source"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	snapshot := readSnapshotFile(t, storePath)
	snapshot.Entries[0].Body = "after external edit"
	snapshot.Entries[0].SourceRef = sourceRef("after external source")
	writeSnapshotFile(t, storePath, snapshot)

	loaded, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry")
	}
	if loaded.Body != "before body" || loaded.Source != "before source" {
		t.Fatalf("loaded = %+v, want SQLite-backed values to win", loaded)
	}
}

func TestRepositorySearchIgnoresExternalJSONEditWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	_, err := repo.Add(AddEntry{Depth: 1, Title: "Stable", Body: "sqlite token body", Source: "sqlite source"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	snapshot := readSnapshotFile(t, storePath)
	snapshot.Entries[0].Body = "external drift body"
	writeSnapshotFile(t, storePath, snapshot)

	results, err := repo.SearchDetailed(Query{Text: "sqlite token", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 || results[0].Entry.Body != "sqlite token body" {
		t.Fatalf("results = %+v, want SQLite-backed body", results)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepository(IgnoresExternalJSONEditWhenSQLiteExists|SearchIgnoresExternalJSONEditWhenSQLiteExists)' -v`

Expected: FAIL because the current read/search paths still import JSON drift.

- [ ] **Step 3: Remove JSON drift import from normal read/search paths**

Update `internal/store/repository.go` so `ensureSQLiteReadModelLocked()` becomes a metadata-store availability check only, not a JSON-refresh mechanism.

Target shape:

```go
func (r *Repository) ensureSQLiteReadModelLocked() error {
	return r.ensureMetadataStoreLocked()
}
```

Also remove JSON-stamp comparison from normal `loadLocked()` / read-path entry points so external JSON edits are ignored when SQLite exists.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `rtk go test ./internal/store -run 'TestRepository(IgnoresExternalJSONEditWhenSQLiteExists|SearchIgnoresExternalJSONEditWhenSQLiteExists)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/repository.go internal/store/repository_test.go
git commit -m "refactor: ignore json drift when sqlite metadata exists"
```

### Task 2: Rebuild missing JSON mirrors from SQLite instead of importing them

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/sqlite_migration.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing mirror-regeneration tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryInitRebuildsMissingJSONMirrorFromSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Mirror", Body: "mirror body", Source: "mirror source"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store.json) error = %v", err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("Stat(store.json) error = %v", err)
	}
	loaded, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || loaded.Title != "Mirror" {
		t.Fatalf("loaded = %+v, want regenerated mirror-backed entry", loaded)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestRepositoryInitRebuildsMissingJSONMirrorFromSQLite -v`

Expected: FAIL if init no longer treats JSON as input but has not yet regenerated the mirror.

- [ ] **Step 3: Add SQLite-to-JSON mirror regeneration helper**

Update `internal/store/sqlite_migration.go` with a helper shaped like:

```go
func (r *Repository) rebuildJSONMirrorFromSQLiteLocked() error {
	snapshot, err := sqliteLoadSnapshot(r.metaDB)
	if err != nil {
		return err
	}
	return r.saveStoreLocked(snapshot)
}
```

Then update startup/init logic so:

- if `store.db` exists and `store.json` is missing, rebuild `store.json` from SQLite
- do not import `store.json` back into SQLite in this case

- [ ] **Step 4: Run the test to verify it passes**

Run: `rtk go test ./internal/store -run TestRepositoryInitRebuildsMissingJSONMirrorFromSQLite -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/repository.go internal/store/sqlite_migration.go internal/store/repository_test.go
git commit -m "feat: rebuild missing json mirrors from sqlite"
```

### Task 3: Remove mirror-refresh benchmarks and add SQLite-authority benchmarks

**Files:**
- Modify: `internal/store/repository_benchmark_test.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing benchmark-scope assertion**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryReadModelDoesNotImportJSONMirrorStamp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	if _, err := repo.Add(AddEntry{Depth: 1, Title: "A", Body: "body"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := repo.ensureSQLiteReadModelLocked(); err != nil {
		t.Fatalf("ensureSQLiteReadModelLocked() error = %v", err)
	}
	value, err := repo.metaValue(metaKeyJSONMirrorStamp)
	if err != nil {
		t.Fatalf("metaValue(json_mirror_stamp) error = %v", err)
	}
	if value == "" {
		t.Fatalf("json_mirror_stamp = empty, expected mirror metadata to remain set")
	}
}
```

This is a weak guard but forces the path to stay mirror-aware only as metadata, not as a live import trigger.

- [ ] **Step 2: Update benchmarks to remove refreshed-reader mode as a normal path**

Modify `internal/store/repository_benchmark_test.go`:

- keep:
  - `BenchmarkRepositoryAddManySQLite`
  - `BenchmarkRepositoryListMetadataSQLite`
  - `BenchmarkRepositoryListMetadataSQLiteCold`
  - `BenchmarkRepositorySearchDetailedSQLite`
  - `BenchmarkRepositorySearchDetailedSQLiteCold`
- remove or rename away the normal refreshed-reader benchmarks:
  - `BenchmarkRepositoryListMetadataSQLiteRefreshed`
  - `BenchmarkRepositorySearchDetailedSQLiteRefreshed`

Replace them with a mirror-regeneration benchmark if needed, e.g.:

```go
func BenchmarkRepositoryInitRebuildsMissingJSONMirror(b *testing.B) {
	for i := 0; i < b.N; i++ {
		root := b.TempDir()
		storePath := filepath.Join(root, "store.json")
		indexPath := filepath.Join(root, "vector")
		repo := NewRepository(storePath, indexPath, fakeembed.Provider())
		for j := 0; j < 1000; j++ {
			_, _ = repo.Add(AddEntry{Depth: j % 3, Title: "Entry " + strconv.Itoa(j), Body: "body " + strconv.Itoa(j)})
		}
		_ = os.Remove(storePath)
		repo = NewRepository(storePath, indexPath, fakeembed.Provider())
		b.StartTimer()
		if err := repo.Init(); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
	}
}
```

- [ ] **Step 3: Run final verification**

Run:

```bash
rtk go test ./internal/store ./internal/importer ./internal/cli ./internal/mcp ./cmd/tagmem
/usr/bin/go test ./internal/store -run '^$' -bench 'BenchmarkRepository(AddManySQLite|ListMetadataSQLite(Cold)?|SearchDetailedSQLite(Cold)?|InitRebuildsMissingJSONMirror)' -benchmem -benchtime=1x
```

Expected:

- tests pass
- benchmarks print without the old refreshed-reader benchmark names

- [ ] **Step 4: Commit**

```bash
git add internal/store/repository.go internal/store/sqlite_migration.go internal/store/repository_test.go internal/store/repository_benchmark_test.go
git commit -m "refactor: make sqlite the only live metadata authority"
```

## Self-Review

### Spec coverage

- external JSON edits no longer affect normal runtime reads: Task 1
- missing JSON mirrors are rebuilt from SQLite: Task 2
- one-way mirror semantics are reflected in tests/benchmarks: Task 3

### Placeholder scan

- No `TBD`, `TODO`, or deferred placeholders remain.
- Each task contains concrete code and explicit commands.

### Type consistency

- SQLite remains the live authority throughout.
- `store.json` is treated as mirror/export state only.
- read/search paths no longer rely on JSON import semantics.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-sqlite-live-authority.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
