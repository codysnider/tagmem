# Pure-Go SQLite Metadata Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `store.json` as the primary mutable metadata store with a pure-Go SQLite database while preserving the current repository API, the externalized `sources/` blob store, and the existing ChroMem vector index.

**Architecture:** Keep `store.Repository` as the public facade, move entry/tag metadata into `store.db`, keep source blobs in `sources/`, and treat the vector index as derived state. Repository writes use one SQL transaction for metadata, followed by a targeted vector mutation, with an explicit `index_state` marker for recovery.

**Tech Stack:** Go 1.25, `database/sql`, `modernc.org/sqlite`, existing source blob gzip storage, ChroMem vector index, repository-level process locking, `rtk go test`.

---

## File Structure

### Files to create

- `internal/store/sqlite_metadata.go`
  - Opens `store.db`
  - Applies required PRAGMAs
  - Creates schema and indexes
  - Reads/writes `meta` keys
- `internal/store/sqlite_queries.go`
  - Entry and tag CRUD helpers
  - Read queries for `Get`, `List`, `ListMetadata`, and search result hydration input
- `internal/store/sqlite_migration.go`
  - Migrates legacy `store.json` into SQLite
  - Reuses existing source blob externalization rules
- `internal/store/sqlite_metadata_test.go`
  - Verifies schema bootstrap, meta state, and basic SQLite lifecycle
- `internal/store/repository_benchmark_test.go`
  - Benchmarks add/update/delete/startup after SQLite migration

### Files to modify

- `go.mod`
- `go.sum`
- `internal/store/repository.go`
  - Route metadata operations through SQLite
  - Keep vector and source blob behavior stable
  - Track `index_state`
- `internal/store/repository_test.go`
  - Extend correctness coverage for SQLite-backed repository behavior
- `internal/importer/importer.go`
  - Keep `ListMetadata` usage for lightweight origin checks
- `internal/cli/status.go`
  - Keep metadata-only status path
- `internal/cli/context.go`
  - Keep metadata-only context path
- `internal/mcp/server.go`
  - Keep metadata-only summary paths and hydrated read paths

### Files that should remain unchanged in behavior

- `internal/bench/*`
- `internal/vector/*`
- `scripts/cmd/docker-bench-*`

## Task 1: Add the SQLite dependency and schema bootstrap

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/store/sqlite_metadata.go`
- Create: `internal/store/sqlite_metadata_test.go`

- [ ] **Step 1: Write the failing schema bootstrap test**

```go
package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/codysnider/tagmem/internal/vector"
)

func TestRepositoryInitCreatesSQLiteMetadataStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())

	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	dbPath := filepath.Join(root, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%s) error = %v", dbPath, err)
	}
	defer db.Close()

	var schemaVersion string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&schemaVersion); err != nil {
		t.Fatalf("QueryRow(schema_version) error = %v", err)
	}
	if schemaVersion != "1" {
		t.Fatalf("schemaVersion = %q, want 1", schemaVersion)
	}

	var indexState string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'index_state'`).Scan(&indexState); err != nil {
		t.Fatalf("QueryRow(index_state) error = %v", err)
	}
	if indexState != "ready" {
		t.Fatalf("indexState = %q, want ready", indexState)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestRepositoryInitCreatesSQLiteMetadataStore -v`

Expected: FAIL because `store.db` is not created and the `meta` table does not exist.

- [ ] **Step 3: Add the pure-Go SQLite dependency**

Update `go.mod`:

```go
require (
	github.com/modelcontextprotocol/go-sdk v1.5.0
	github.com/philippgille/chromem-go v0.7.0
	github.com/yalue/onnxruntime_go v1.27.0
	golang.org/x/text v0.3.8
	modernc.org/sqlite v1.39.1
)
```

Run: `go mod tidy`

- [ ] **Step 4: Create the SQLite bootstrap helper**

Create `internal/store/sqlite_metadata.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = "1"

func (r *Repository) metadataPath() string {
	return filepath.Join(filepath.Dir(r.path), "store.db")
}

func openMetadataDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := applySQLitePragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := initSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applySQLitePragmas(db *sql.DB) error {
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}
	return nil
}

func initSQLiteSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY,
			depth INTEGER NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			source_ref TEXT,
			origin TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS entry_tags (
			entry_id INTEGER NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (entry_id, tag)
		);`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_updated_at ON entries(updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_depth_updated_at ON entries(depth, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_origin ON entries(origin);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_source_ref ON entries(source_ref);`,
		`CREATE INDEX IF NOT EXISTS idx_entry_tags_tag_entry_id ON entry_tags(tag, entry_id);`,
		`INSERT INTO meta(key, value) VALUES ('schema_version', '1') ON CONFLICT(key) DO NOTHING;`,
		`INSERT INTO meta(key, value) VALUES ('index_state', 'ready') ON CONFLICT(key) DO NOTHING;`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec schema statement: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 5: Wire repository init to create the metadata store**

Update `internal/store/repository.go`:

```go
type Repository struct {
	path       string
	indexPath  string
	sourceDir  string
	provider   vector.Provider
	metaDB     *sql.DB
	metaPath   string
	now        func() time.Time
	// existing fields remain
}

func NewRepository(path, indexPath string, provider vector.Provider) *Repository {
	return &Repository{
		path:      path,
		indexPath: indexPath,
		sourceDir: filepath.Join(filepath.Dir(path), "sources"),
		metaPath:  filepath.Join(filepath.Dir(path), "store.db"),
		provider:  provider,
		now:       time.Now,
		queryCache: map[string][]float32{},
	}
}

func (r *Repository) ensureMetadataStoreLocked() error {
	if r.metaDB != nil {
		return nil
	}
	db, err := openMetadataDB(r.metaPath)
	if err != nil {
		return fmt.Errorf("open metadata store: %w", err)
	}
	r.metaDB = db
	return nil
}
```

Then call `ensureMetadataStoreLocked()` from `Init()` before index setup.

- [ ] **Step 6: Run the test to verify it passes**

Run: `rtk go test ./internal/store -run TestRepositoryInitCreatesSQLiteMetadataStore -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/store/sqlite_metadata.go internal/store/sqlite_metadata_test.go internal/store/repository.go
git commit -m "feat: bootstrap sqlite metadata store"
```

### Task 2: Move repository reads to SQLite while preserving source hydration

**Files:**
- Create: `internal/store/sqlite_queries.go`
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing read-path tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryListMetadataOmitsHydratedSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	_, err := repo.Add(AddEntry{Depth: 1, Title: "Session", Body: "chunk body", Source: "full transcript source", Origin: "session.md"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	entries, err := repo.ListMetadata(Query{Limit: 0})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Source != "" {
		t.Fatalf("entries[0].Source = %q, want empty metadata-only source", entries[0].Source)
	}
	if entries[0].SourceRef == "" {
		t.Fatal("entries[0].SourceRef should be populated")
	}
}

func TestRepositoryGetHydratesSourceFromSQLiteMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	added, err := repo.Add(AddEntry{Depth: 1, Title: "Session", Body: "chunk body", Source: "hydrated source text", Origin: "session.md"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	entry, ok, err := repo.Get(added.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry")
	}
	if entry.Source != "hydrated source text" {
		t.Fatalf("entry.Source = %q, want hydrated source text", entry.Source)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepository(ListMetadataOmitsHydratedSource|GetHydratesSourceFromSQLiteMetadata)' -v`

Expected: FAIL because repository reads still come from the JSON snapshot model.

- [ ] **Step 3: Add SQLite read helpers**

Create `internal/store/sqlite_queries.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type sqliteEntryRow struct {
	ID        int
	Depth     int
	Title     string
	Body      string
	SourceRef string
	Origin    string
	CreatedAt string
	UpdatedAt string
}

func scanEntry(row scanner) (Entry, error) {
	var raw sqliteEntryRow
	if err := row.Scan(&raw.ID, &raw.Depth, &raw.Title, &raw.Body, &raw.SourceRef, &raw.Origin, &raw.CreatedAt, &raw.UpdatedAt); err != nil {
		return Entry{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, raw.CreatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, raw.UpdatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return Entry{ID: raw.ID, Depth: raw.Depth, Title: raw.Title, Body: raw.Body, SourceRef: strings.TrimSpace(raw.SourceRef), Origin: strings.TrimSpace(raw.Origin), CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func loadTags(db *sql.DB, entryIDs []int) (map[int][]string, error) {
	if len(entryIDs) == 0 {
		return map[int][]string{}, nil
	}
	query, args := buildInClause(`SELECT entry_id, tag FROM entry_tags WHERE entry_id IN `, entryIDs)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byEntry := map[int][]string{}
	for rows.Next() {
		var entryID int
		var tag string
		if err := rows.Scan(&entryID, &tag); err != nil {
			return nil, err
		}
		byEntry[entryID] = append(byEntry[entryID], tag)
	}
	return byEntry, rows.Err()
}
```

- [ ] **Step 4: Route `Get`, `List`, and `ListMetadata` through SQLite**

Update `internal/store/repository.go`:

```go
func (r *Repository) Get(id int) (Entry, bool, error) {
	var entry Entry
	found := false
	err := r.withSharedLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		row := r.metaDB.QueryRow(`SELECT id, depth, title, body, source_ref, origin, created_at, updated_at FROM entries WHERE id = ?`, id)
		loaded, err := scanEntry(row)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		tagsByEntry, err := loadTags(r.metaDB, []int{loaded.ID})
		if err != nil {
			return err
		}
		loaded.Tags = tagsByEntry[loaded.ID]
		entry = loaded
		found = true
		return nil
	})
	if err != nil || !found {
		return entry, found, err
	}
	hydrated, err := r.hydrateEntrySource(entry)
	if err != nil {
		return Entry{}, false, err
	}
	return hydrated, true, nil
}
```

Also update `listEntries` to build results from SQL queries instead of `snapshot.Entries`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/store -run 'TestRepository(ListMetadataOmitsHydratedSource|GetHydratesSourceFromSQLiteMetadata)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlite_queries.go internal/store/repository.go internal/store/repository_test.go
git commit -m "refactor: read repository metadata from sqlite"
```

### Task 3: Move mutations to SQLite and track index dirty state

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/store/sqlite_metadata.go`
- Modify: `internal/store/sqlite_queries.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing mutation and index-state tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryAddPersistsToSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	entry, err := repo.Add(AddEntry{Depth: 2, Title: "SQLite add", Body: "body text", Source: "full source text", Origin: "note.md"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	reloaded, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || reloaded.Title != "SQLite add" {
		t.Fatalf("reloaded = %+v, want title SQLite add", reloaded)
	}
}

func TestRepositoryMarksIndexDirtyWhenIndexWriteFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	repo.indexEntriesImpl = func([]Entry) error { return fmt.Errorf("boom") }

	_, err := repo.Add(AddEntry{Depth: 1, Title: "Broken index", Body: "body text"})
	if err == nil {
		t.Fatal("Add() error = nil, want non-nil")
	}

	state, stateErr := repo.metaValue("index_state")
	if stateErr != nil {
		t.Fatalf("metaValue(index_state) error = %v", stateErr)
	}
	if state != "dirty" {
		t.Fatalf("index_state = %q, want dirty", state)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepository(AddPersistsToSQLite|MarksIndexDirtyWhenIndexWriteFails)' -v`

Expected: FAIL because add/update/delete still rely on snapshot mutation and there is no explicit SQLite-backed index state.

- [ ] **Step 3: Add SQLite meta helpers and vector hooks**

Update `internal/store/sqlite_metadata.go`:

```go
func (r *Repository) metaValue(key string) (string, error) {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return "", err
	}
	var value string
	if err := r.metaDB.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func (r *Repository) setMetaValue(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
```

Update `internal/store/repository.go`:

```go
type Repository struct {
	// existing fields
	indexEntriesImpl       func([]Entry) error
	deleteIndexedEntriesImpl func(...int) error
}

func NewRepository(path, indexPath string, provider vector.Provider) *Repository {
	repo := &Repository{/* existing setup */}
	repo.indexEntriesImpl = repo.indexEntriesLocked
	repo.deleteIndexedEntriesImpl = repo.deleteIndexedEntriesLocked
	return repo
}
```

- [ ] **Step 4: Move `AddMany`, `Update`, and `Delete` metadata writes into SQL transactions**

Use transaction shape like:

```go
func (r *Repository) AddMany(requests []AddEntry) ([]Entry, error) {
	entries := make([]Entry, 0, len(requests))
	err := r.withExclusiveLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		tx, err := r.metaDB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := r.setMetaValue(tx, "index_state", "dirty"); err != nil {
			return err
		}
		for _, req := range requests {
			entry, err := r.insertEntryTx(tx, req)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := r.indexEntriesImpl(entries); err != nil {
			return err
		}
		readyTx, err := r.metaDB.Begin()
		if err != nil {
			return err
		}
		defer readyTx.Rollback()
		if err := r.setMetaValue(readyTx, "index_state", "ready"); err != nil {
			return err
		}
		return readyTx.Commit()
	})
	return entries, err
}
```

Apply the same model to `Update` and `Delete`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/store -run 'TestRepository(AddPersistsToSQLite|MarksIndexDirtyWhenIndexWriteFails|UpdateReindexesEntry|ExternalizesAndDeduplicatesSourceBlobs)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/repository.go internal/store/sqlite_metadata.go internal/store/sqlite_queries.go internal/store/repository_test.go
git commit -m "refactor: move repository mutations to sqlite"
```

### Task 4: Migrate legacy JSON stores into SQLite on startup

**Files:**
- Create: `internal/store/sqlite_migration.go`
- Modify: `internal/store/repository.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write the failing migration test**

Add to `internal/store/repository_test.go`:

```go
func TestRepositoryMigratesLegacyStoreJSONToSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	legacy := Snapshot{
		Version: 1,
		NextID:  2,
		Entries: []Entry{{
			ID:        1,
			Depth:     1,
			Title:     "Legacy",
			Body:      "legacy body",
			Source:    "legacy full source",
			Origin:    "legacy.md",
			CreatedAt: time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		}},
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(store.json) error = %v", err)
	}

	repo := NewRepository(storePath, filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	entry, ok, err := repo.Get(1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || entry.Source != "legacy full source" {
		t.Fatalf("entry = %+v, want migrated legacy source", entry)
	}

	if _, err := os.Stat(filepath.Join(root, "store.db")); err != nil {
		t.Fatalf("Stat(store.db) error = %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("Stat(store.json) error = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestRepositoryMigratesLegacyStoreJSONToSQLite -v`

Expected: FAIL because startup does not yet import `store.json` into SQLite.

- [ ] **Step 3: Add the migration helper**

Create `internal/store/sqlite_migration.go`:

```go
package store

import (
	"encoding/json"
	"fmt"
	"os"
)

func (r *Repository) migrateLegacyJSONToSQLite() error {
	if _, err := os.Stat(r.metaPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(r.path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode legacy store: %w", err)
	}
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	tx, err := r.metaDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := r.setMetaValue(tx, "index_state", "dirty"); err != nil {
		return err
	}
	seenSources := map[string]struct{}{}
	for _, entry := range snapshot.Entries {
		normalized, err := r.normalizeLoadedEntry(entry, seenSources)
		if err != nil {
			return err
		}
		if err := r.insertEntryRecordTx(tx, normalized); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.RebuildIndex()
}
```

- [ ] **Step 4: Call migration from repository initialization**

Update `internal/store/repository.go` initialization flow:

```go
func (r *Repository) Init() error {
	return r.withExclusiveLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		if err := r.migrateLegacyJSONToSQLite(); err != nil {
			return err
		}
		return r.ensureIndexCurrentLocked()
	})
}
```

- [ ] **Step 5: Run the migration test to verify it passes**

Run: `rtk go test ./internal/store -run TestRepositoryMigratesLegacyStoreJSONToSQLite -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/sqlite_migration.go internal/store/repository.go internal/store/repository_test.go
git commit -m "feat: migrate legacy json metadata to sqlite"
```

### Task 5: Add repair behavior, fail-fast search while dirty, and performance checks

**Files:**
- Modify: `internal/store/repository.go`
- Create: `internal/store/repository_benchmark_test.go`
- Modify: `internal/store/repository_test.go`

- [ ] **Step 1: Write failing dirty-search and repair tests**

Add to `internal/store/repository_test.go`:

```go
func TestRepositorySearchFailsFastWhenIndexDirty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	repo.indexEntriesImpl = func([]Entry) error { return fmt.Errorf("boom") }
	if _, err := repo.Add(AddEntry{Depth: 1, Title: "Broken", Body: "body"}); err == nil {
		t.Fatal("Add() error = nil, want non-nil")
	}

	_, err := repo.SearchDetailed(Query{Text: "body", Limit: 5})
	if err == nil || !strings.Contains(err.Error(), "index needs repair") {
		t.Fatalf("SearchDetailed() error = %v, want index needs repair", err)
	}
}

func TestRepositoryRebuildIndexClearsDirtyState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	repo.indexEntriesImpl = func([]Entry) error { return fmt.Errorf("boom") }
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Broken", Body: "body"})
	repo.indexEntriesImpl = repo.indexEntriesLocked

	if err := repo.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
	state, err := repo.metaValue("index_state")
	if err != nil {
		t.Fatalf("metaValue(index_state) error = %v", err)
	}
	if state != "ready" {
		t.Fatalf("index_state = %q, want ready", state)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/store -run 'TestRepository(SearchFailsFastWhenIndexDirty|RebuildIndexClearsDirtyState)' -v`

Expected: FAIL because search does not yet consult SQLite-backed `index_state` and repair does not yet reset it.

- [ ] **Step 3: Add the dirty-state guard and repair reset**

Update `internal/store/repository.go`:

```go
func (r *Repository) ensureIndexReadyLocked() error {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	state, err := r.metaValue("index_state")
	if err != nil {
		return err
	}
	if state != "ready" {
		return fmt.Errorf("index needs repair")
	}
	return nil
}

func (r *Repository) SearchDetailed(q Query) ([]SearchResult, error) {
	if err := r.ensureIndexReadyLocked(); err != nil {
		return nil, err
	}
	// existing search logic
}

func (r *Repository) RebuildIndex() error {
	return r.withExclusiveLock(func() error {
		entries, err := r.ListMetadata(Query{Limit: 0})
		if err != nil {
			return err
		}
		if err := r.resetVectorCollectionLocked(entries); err != nil {
			return err
		}
		tx, err := r.metaDB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := r.setMetaValue(tx, "index_state", "ready"); err != nil {
			return err
		}
		return tx.Commit()
	})
}
```

- [ ] **Step 4: Add lightweight performance benchmarks**

Create `internal/store/repository_benchmark_test.go`:

```go
package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/codysnider/tagmem/internal/vector"
)

func BenchmarkRepositoryAddManySQLite(b *testing.B) {
	root := b.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	batch := make([]AddEntry, 0, 100)
	for i := 0; i < 100; i++ {
		batch = append(batch, AddEntry{Depth: 1, Title: fmt.Sprintf("Entry %d", i), Body: "Body text for benchmark", Source: "Shared full source text for benchmark"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.AddMany(batch); err != nil {
			b.Fatal(err)
		}
	}
}
```

- [ ] **Step 5: Run correctness tests and the benchmark**

Run:

```bash
rtk go test ./internal/store ./internal/importer ./internal/cli ./internal/mcp ./cmd/tagmem
go test ./internal/store -run '^$' -bench BenchmarkRepositoryAddManySQLite -benchmem
```

Expected:

- all focused tests pass
- benchmark prints `BenchmarkRepositoryAddManySQLite` with `ns/op`, `B/op`, and `allocs/op`

- [ ] **Step 6: Commit**

```bash
git add internal/store/repository.go internal/store/repository_test.go internal/store/repository_benchmark_test.go
git commit -m "feat: add sqlite-backed repair and perf checks"
```

## Self-Review

### Spec coverage

- SQLite metadata store: covered in Tasks 1-4
- source blobs remain externalized: covered in Tasks 2-4
- repository facade preserved: covered in Tasks 2-5
- `index_state` dirty/ready behavior: covered in Tasks 3 and 5
- migration safety: covered in Task 4
- performance checks: covered in Task 5

### Placeholder scan

- No `TBD`, `TODO`, or deferred implementation placeholders remain.
- Each task lists exact files and explicit commands.

### Type consistency

- `source_ref`, `index_state`, `ListMetadata`, and `store.db` are used consistently across tasks.
- The repository remains the single public facade throughout the plan.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-purego-sqlite-metadata-store.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
