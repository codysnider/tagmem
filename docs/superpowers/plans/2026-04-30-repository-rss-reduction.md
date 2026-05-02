# Repository RSS Reduction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce steady-state repository RSS by removing full metadata residency from normal read/search paths and materializing only the rows needed per request.

**Architecture:** Keep SQLite as the metadata authority and ChroMem as the vector authority, but stop using a full in-memory `Snapshot` as the normal read model. Read paths should query SQLite directly, search should fetch only candidate rows by ID, and the query embedding cache should be bounded so memory scales more with request size than corpus size.

**Tech Stack:** Go 1.25, SQLite metadata store (`database/sql` + `modernc.org/sqlite`), ChroMem persistent vector collection, externalized gzip source blobs, `rtk go test`, focused benchmark coverage.

---

## File Structure

### Files to create

- `internal/store/query_cache.go`
  - Bounded query embedding cache implementation

### Files to modify

- `internal/store/sqlite_queries.go`
  - Add candidate-row fetch helpers by ID
  - Move metadata summary queries fully into SQLite
- `internal/store/repository.go`
  - Remove normal dependence on fully resident `Snapshot`
  - Route `Get`, `List`, `ListMetadata`, `DepthCounts`, `DuplicateCheck`, and `SearchDetailed` to SQLite-on-demand paths
  - Bound query cache usage
- `internal/store/repository_test.go`
  - Add behavior-preserving regression tests for no-snapshot reads and candidate-scoped search
- `internal/store/repository_benchmark_test.go`
  - Add memory-oriented microbenchmarks for init/list/search paths

### Files that should remain behaviorally unchanged

- `internal/cli/*`
- `internal/mcp/*`
- `internal/importer/*`

## Task 1: Add candidate-scoped SQLite fetch helpers and bounded query cache

**Files:**
- Create: `internal/store/query_cache.go`
- Modify: `internal/store/sqlite_queries.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write the failing helper/cache tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryQueryEmbeddingCacheEvictsOldEntries(t *testing.T) {
	t.Parallel()

	cache := newQueryEmbeddingCache(2)
	cache.put("one", []float32{1})
	cache.put("two", []float32{2})
	cache.put("three", []float32{3})

	if _, ok := cache.get("one"); ok {
		t.Fatal("cache.get(one) = ok, want evicted")
	}
	if value, ok := cache.get("two"); !ok || len(value) != 1 || value[0] != 2 {
		t.Fatalf("cache.get(two) = (%v, %v), want retained value", value, ok)
	}
	if value, ok := cache.get("three"); !ok || len(value) != 1 || value[0] != 3 {
		t.Fatalf("cache.get(three) = (%v, %v), want retained value", value, ok)
	}
}

func TestSQLiteListEntriesByIDsPreservesRequestedSubset(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	first, _ := repo.Add(AddEntry{Depth: 1, Title: "First", Body: "alpha", Tags: []string{"one"}})
	second, _ := repo.Add(AddEntry{Depth: 2, Title: "Second", Body: "beta", Tags: []string{"two"}})
	third, _ := repo.Add(AddEntry{Depth: 3, Title: "Third", Body: "gamma", Tags: []string{"three"}})

	entries, err := sqliteListEntriesByIDs(repo.metaDB, []int{third.ID, first.ID})
	if err != nil {
		t.Fatalf("sqliteListEntriesByIDs() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].ID != third.ID || entries[1].ID != first.ID {
		t.Fatalf("entries IDs = [%d %d], want [%d %d]", entries[0].ID, entries[1].ID, third.ID, first.ID)
	}
	if entries[0].Tags[0] != "three" || entries[1].Tags[0] != "one" {
		t.Fatalf("entries tags = %+v, want hydrated tags", entries)
	}
	_ = second
	_ = third
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepositoryQueryEmbeddingCacheEvictsOldEntries|TestSQLiteListEntriesByIDsPreservesRequestedSubset' -v`

Expected: FAIL because the bounded cache and candidate fetch helper do not exist yet.

- [ ] **Step 3: Create the bounded query cache**

Create `internal/store/query_cache.go`:

```go
package store

import "sync"

type queryEmbeddingCache struct {
	mu       sync.Mutex
	capacity int
	order    []string
	values   map[string][]float32
}

func newQueryEmbeddingCache(capacity int) *queryEmbeddingCache {
	if capacity < 1 {
		capacity = 1
	}
	return &queryEmbeddingCache{capacity: capacity, values: map[string][]float32{}}
}

func (c *queryEmbeddingCache) get(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return append([]float32(nil), value...), true
}

func (c *queryEmbeddingCache) put(key string, value []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.values[key]; !exists {
		c.order = append(c.order, key)
	}
	c.values[key] = append([]float32(nil), value...)
	for len(c.order) > c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.values, oldest)
	}
}
```

- [ ] **Step 4: Add candidate-scoped SQLite fetch helper**

Update `internal/store/sqlite_queries.go` with:

```go
func sqliteListEntriesByIDs(db *sql.DB, ids []int) ([]Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := db.Query(`
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM entries
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite entries by id: %w", err)
	}
	defer rows.Close()

	byID := map[int]Entry{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		byID[entry.ID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tagsByID, err := loadTags(db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok {
			continue
		}
		entry.Tags = tagsByID[id]
		out = append(out, entry)
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/store -run 'TestRepositoryQueryEmbeddingCacheEvictsOldEntries|TestSQLiteListEntriesByIDsPreservesRequestedSubset' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/query_cache.go internal/store/sqlite_queries.go internal/store/repository_test.go
git commit -m "refactor: add bounded query cache and candidate fetch helpers"
```

### Task 2: Remove full-metadata residency from read and summary paths

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/sqlite_queries.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write the failing summary/read-path tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryDepthCountsUsesSQLiteMetadataWhenSnapshotEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "A", Body: "one"})
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "B", Body: "two"})
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "C", Body: "three"})

	repo.snapshot = Snapshot{}
	repo.loaded = false

	depths, err := repo.DepthCounts()
	if err != nil {
		t.Fatalf("DepthCounts() error = %v", err)
	}
	if len(depths) != 2 || depths[0].Depth != 1 || depths[0].Count != 1 || depths[1].Depth != 2 || depths[1].Count != 2 {
		t.Fatalf("depths = %+v, want sqlite-backed counts", depths)
	}
}

func TestRepositoryDuplicateCheckHydratesMatchesWithoutSnapshotMap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Identity", Body: "core memory", Source: "full duplicate source text"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	repo.snapshot = Snapshot{}
	repo.loaded = false

	matches, err := repo.DuplicateCheck("core memory", 0)
	if err != nil {
		t.Fatalf("DuplicateCheck() error = %v", err)
	}
	if len(matches) == 0 || matches[0].Entry.ID != entry.ID {
		t.Fatalf("matches = %+v, want entry %d", matches, entry.ID)
	}
	if matches[0].Entry.Source != "full duplicate source text" {
		t.Fatalf("matches[0].Entry.Source = %q, want hydrated source", matches[0].Entry.Source)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepositoryDepthCountsUsesSQLiteMetadataWhenSnapshotEmpty|TestRepositoryDuplicateCheckHydratesMatchesWithoutSnapshotMap' -v`

Expected: FAIL because these paths still depend on `loadLocked()`/snapshot-backed full metadata residency.

- [ ] **Step 3: Route summary and duplicate-check paths directly to SQLite**

Update `internal/store/sqlite_queries.go` with:

```go
func sqliteDepthCounts(db *sql.DB) ([]DepthSummary, error) {
	rows, err := db.Query(`SELECT depth, COUNT(*) FROM entries GROUP BY depth ORDER BY depth`)
	if err != nil {
		return nil, fmt.Errorf("query sqlite depth counts: %w", err)
	}
	defer rows.Close()
	out := []DepthSummary{}
	for rows.Next() {
		var depth int
		var count int
		if err := rows.Scan(&depth, &count); err != nil {
			return nil, err
		}
		out = append(out, DepthSummary{Depth: depth, Count: count})
	}
	return out, rows.Err()
}
```

Update `internal/store/repository.go`:

```go
func (r *Repository) DepthCounts() ([]DepthSummary, error) {
	var summaries []DepthSummary
	err := r.withSharedLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		var err error
		summaries, err = sqliteDepthCounts(r.metaDB)
		return err
	})
	return summaries, err
}
```

Refactor `DuplicateCheck` to fetch only matched candidate rows by ID using `sqliteListEntriesByIDs` instead of `snapshot.Entries`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `rtk go test ./internal/store -run 'TestRepositoryDepthCountsUsesSQLiteMetadataWhenSnapshotEmpty|TestRepositoryDuplicateCheckHydratesMatchesWithoutSnapshotMap' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/repository.go internal/store/sqlite_queries.go internal/store/repository_test.go
git commit -m "refactor: read summaries and duplicate checks from sqlite"
```

### Task 3: Refactor SearchDetailed to fetch only candidate rows from SQLite

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write the failing candidate-scoped search regression test**

Add to `internal/store/repository_test.go`:

```go
func TestRepositorySearchDetailedWorksWithoutResidentSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Auth decision", Body: "token refresh uses rotating bearer tokens", Tags: []string{"auth", "security"}, Source: "full auth source"})
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "Garden", Body: "tomatoes need warmer soil", Tags: []string{"garden"}, Source: "full garden source"})

	repo.snapshot = Snapshot{}
	repo.loaded = false

	results, err := repo.SearchDetailed(Query{Text: "token authentication", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 || results[0].Entry.Title != "Auth decision" {
		t.Fatalf("results = %+v, want Auth decision first", results)
	}
	if results[0].Entry.Source != "full auth source" {
		t.Fatalf("results[0].Entry.Source = %q, want hydrated source", results[0].Entry.Source)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestRepositorySearchDetailedWorksWithoutResidentSnapshot -v`

Expected: FAIL because `SearchDetailed` still builds a whole-corpus `entriesByID` map from `snapshot.Entries`.

- [ ] **Step 3: Fetch only candidate rows from SQLite in SearchDetailed**

Update `internal/store/repository.go` inside `SearchDetailed`:

```go
candidateIDs := make([]int, 0, len(results))
	for _, result := range results {
		id, err := strconv.Atoi(result.ID)
		if err != nil {
			continue
		}
		candidateIDs = append(candidateIDs, id)
	}

	candidateEntries, err := sqliteListEntriesByIDs(r.metaDB, candidateIDs)
	if err != nil {
		return err
	}
	entriesByID := make(map[string]Entry, len(candidateEntries))
	for _, entry := range candidateEntries {
		entriesByID[strconv.Itoa(entry.ID)] = entry
	}
```

Remove the full-corpus map construction from `snapshot.Entries`.

Keep:

- current scoring logic
- current tag/depth filters
- current dirty-index fail-fast checks
- source hydration only on final returned rows

- [ ] **Step 4: Run the test to verify it passes**

Run: `rtk go test ./internal/store -run TestRepositorySearchDetailedWorksWithoutResidentSnapshot -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/repository.go internal/store/repository_test.go
git commit -m "refactor: fetch search candidates from sqlite on demand"
```

### Task 4: Bound query cache and add RSS-oriented benchmarks

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_benchmark_test.go`

- [ ] **Step 1: Write the failing benchmark-oriented cache assertion**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryQueryEmbeddingCacheIsBounded(t *testing.T) {
	t.Parallel()

	repo := &Repository{queryCache: newQueryEmbeddingCache(2)}
	repo.queryCache.put("one", []float32{1})
	repo.queryCache.put("two", []float32{2})
	repo.queryCache.put("three", []float32{3})

	if _, ok := repo.queryCache.get("one"); ok {
		t.Fatal("repo.queryCache still contains evicted key one")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestRepositoryQueryEmbeddingCacheIsBounded -v`

Expected: FAIL until the repository switches from raw map usage to the bounded cache type.

- [ ] **Step 3: Wire repository queryEmbedding to the bounded cache**

Update `internal/store/repository.go`:

```go
type Repository struct {
	// existing fields
	queryCache *queryEmbeddingCache
}

func NewRepository(path, indexPath string, provider vector.Provider) *Repository {
	return &Repository{
		// existing fields
		queryCache: newQueryEmbeddingCache(256),
	}
}

func (r *Repository) queryEmbedding(text string) ([]float32, error) {
	if r.queryCache != nil {
		if vector, ok := r.queryCache.get(text); ok {
			return vector, nil
		}
	}
	vector, err := r.provider.Func(context.Background(), text)
	if err != nil {
		return nil, err
	}
	if r.queryCache != nil {
		r.queryCache.put(text, vector)
	}
	return vector, nil
}
```

- [ ] **Step 4: Extend repository benchmarks for init/list/search**

Update `internal/store/repository_benchmark_test.go`:

```go
func BenchmarkRepositoryListMetadataSQLite(b *testing.B) {
	root := b.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	for i := 0; i < 1000; i++ {
		_, _ = repo.Add(AddEntry{Depth: i % 3, Title: "Entry " + strconv.Itoa(i), Body: "body " + strconv.Itoa(i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := repo.ListMetadata(Query{Limit: 100})
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) == 0 {
			b.Fatal("ListMetadata() returned no entries")
		}
	}
}

func BenchmarkRepositorySearchDetailedSQLite(b *testing.B) {
	root := b.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	for i := 0; i < 1000; i++ {
		_, _ = repo.Add(AddEntry{Depth: i % 3, Title: "Entry " + strconv.Itoa(i), Body: "auth token body " + strconv.Itoa(i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := repo.SearchDetailed(Query{Text: "auth token", Limit: 10})
		if err != nil {
			b.Fatal(err)
		}
		if len(results) == 0 {
			b.Fatal("SearchDetailed() returned no results")
		}
	}
}
```

- [ ] **Step 5: Run final verification**

Run:

```bash
rtk go test ./internal/store ./internal/importer ./internal/cli ./internal/mcp ./cmd/tagmem
/usr/bin/go test ./internal/store -run '^$' -bench 'BenchmarkRepository(AddManySQLite|ListMetadataSQLite|SearchDetailedSQLite)' -benchmem
```

Expected:

- all tests pass
- all three benchmarks print results with `ns/op`, `B/op`, and `allocs/op`

- [ ] **Step 6: Commit**

```bash
git add internal/store/query_cache.go internal/store/sqlite_queries.go internal/store/repository.go internal/store/repository_test.go internal/store/repository_benchmark_test.go
git commit -m "refactor: reduce repository rss with sqlite-on-demand reads"
```

## Self-Review

### Spec coverage

- remove normal full-metadata residency: Tasks 2 and 3
- candidate-scoped search materialization: Task 3
- bounded query embedding cache: Tasks 1 and 4
- preserve lazy source hydration: Tasks 2 and 3
- add memory-oriented benchmark coverage: Task 4

### Placeholder scan

- No `TBD`, `TODO`, or deferred placeholders remain.
- Each task contains exact files, concrete code, and explicit commands.

### Type consistency

- `queryEmbeddingCache`, `sqliteListEntriesByIDs`, `ListMetadata`, `SearchDetailed`, and lazy source hydration behavior are used consistently.
- The plan preserves current repository semantics while changing the memory model underneath.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-repository-rss-reduction.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
