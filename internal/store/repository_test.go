package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/vector"
)

func TestRepositoryAddAndGet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	repo.now = func() time.Time {
		return time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	}

	entry, err := repo.Add(AddEntry{
		Depth:  0,
		Title:  "Identity",
		Body:   "Always load this first.",
		Tags:   []string{" Core ", "core", "identity"},
		Source: "Original scratchpad note: Always load this first.",
		Origin: "manual",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if entry.ID != 1 {
		t.Fatalf("entry.ID = %d, want 1", entry.ID)
	}
	if len(entry.Tags) != 2 {
		t.Fatalf("len(entry.Tags) = %d, want 2", len(entry.Tags))
	}

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry")
	}
	if stored.Title != entry.Title {
		t.Fatalf("stored.Title = %q, want %q", stored.Title, entry.Title)
	}
	if stored.Source != "Original scratchpad note: Always load this first." {
		t.Fatalf("stored.Source = %q, want verbatim source", stored.Source)
	}
	if stored.Origin != "manual" {
		t.Fatalf("stored.Origin = %q, want manual", stored.Origin)
	}
}

func TestRepositoryAddDefaultsSourceAndDerivesTags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Auth migration",
		Body:  "We migrated authentication to bearer tokens for the API gateway.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if entry.Source != entry.Body {
		t.Fatalf("entry.Source = %q, want body fallback %q", entry.Source, entry.Body)
	}
	if len(entry.Tags) == 0 {
		t.Fatal("expected derived tags for untagged entry")
	}
}

func TestRepositoryIgnoresExternalJSONEditWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth note",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, filepath.Join(root, "vector"))

	mutateStoreJSONEntry(t, storePath, entry.ID, func(stored *Entry) {
		stored.Body = "External JSON drift should not replace SQLite body."
		stored.Source = "external json source drift"
		stored.SourceRef = ""
	})

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry")
	}
	if stored.Body != entry.Body {
		t.Fatalf("stored.Body = %q, want %q", stored.Body, entry.Body)
	}
	if stored.Source != entry.Source {
		t.Fatalf("stored.Source = %q, want %q", stored.Source, entry.Source)
	}
}

func TestRepositorySearchIgnoresExternalJSONEditWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth note",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
		Tags:   []string{"auth"},
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, filepath.Join(root, "vector"))

	mutateStoreJSONEntry(t, storePath, entry.ID, func(stored *Entry) {
		stored.Body = "External JSON drift should not replace SQLite body."
		stored.Source = "external json source drift"
		stored.SourceRef = ""
	})

	results, err := repo.SearchDetailed(Query{Text: "token authentication", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Entry.ID != entry.ID {
		t.Fatalf("results[0].Entry.ID = %d, want %d", results[0].Entry.ID, entry.ID)
	}
	if results[0].Entry.Body != entry.Body {
		t.Fatalf("results[0].Entry.Body = %q, want %q", results[0].Entry.Body, entry.Body)
	}
	if results[0].Entry.Source != entry.Source {
		t.Fatalf("results[0].Entry.Source = %q, want %q", results[0].Entry.Source, entry.Source)
	}
}

func TestRepositoryPhaseHookEmitsRepresentativeAddAndSearchPhases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	var addPhases []string
	repo.SetPhaseHook(func(name string, duration time.Duration) {
		if duration < 0 {
			t.Fatalf("phase %q duration = %s, want non-negative", name, duration)
		}
		addPhases = append(addPhases, name)
	})

	entry, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Phase coverage",
		Body:  "Repository profiling should expose representative phases.",
		Tags:  []string{"profile"},
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	for _, phase := range []string{"sqlite_mutation", "vector_mutation"} {
		if !containsString(addPhases, phase) {
			t.Fatalf("add phases = %v, want %q", addPhases, phase)
		}
	}

	var searchPhases []string
	repo.SetPhaseHook(func(name string, duration time.Duration) {
		if duration < 0 {
			t.Fatalf("phase %q duration = %s, want non-negative", name, duration)
		}
		searchPhases = append(searchPhases, name)
	})

	results, err := repo.SearchDetailed(Query{Text: "Phase coverage", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchDetailed() returned no results, want seeded entry")
	}
	if results[0].Entry.ID != entry.ID {
		t.Fatalf("results[0].Entry.ID = %d, want %d", results[0].Entry.ID, entry.ID)
	}
	for _, phase := range []string{"query_embedding", "vector_query", "sqlite_candidate_fetch", "rerank", "source_hydration"} {
		if !containsString(searchPhases, phase) {
			t.Fatalf("search phases = %v, want %q", searchPhases, phase)
		}
	}
	if containsString(searchPhases, "sqlite_mutation") {
		t.Fatalf("search phases = %v, did not expect add mutation phase", searchPhases)
	}
	if containsString(searchPhases, "vector_mutation") {
		t.Fatalf("search phases = %v, did not expect add vector phase", searchPhases)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestRepositoryLoadLockedIgnoresExternalJSONEditWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth note",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, filepath.Join(root, "vector"))

	mutateStoreJSONEntry(t, storePath, entry.ID, func(stored *Entry) {
		stored.Body = "External JSON drift should not replace SQLite body."
		stored.Source = "external json source drift"
		stored.SourceRef = ""
	})

	err = repo.withSharedLock(func() error {
		snapshot, err := repo.loadLocked()
		if err != nil {
			return err
		}
		if len(snapshot.Entries) != 1 {
			t.Fatalf("len(snapshot.Entries) = %d, want 1", len(snapshot.Entries))
		}
		if snapshot.Entries[0].Body != entry.Body {
			t.Fatalf("snapshot.Entries[0].Body = %q, want %q", snapshot.Entries[0].Body, entry.Body)
		}
		if snapshot.Entries[0].SourceRef == "" {
			t.Fatal("snapshot.Entries[0].SourceRef = empty, want SQLite-backed source ref")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("loadLocked() error = %v", err)
	}
}

func TestRepositoryInitRebuildsMissingJSONMirrorFromSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth note",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if entry.ID != 1 {
		t.Fatalf("entry.ID = %d, want 1", entry.ID)
	}

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", storePath, err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want rebuilt mirror", storePath, err)
	}
	rebuilt := readSnapshotFile(t, storePath)
	if len(rebuilt.Entries) != 1 {
		t.Fatalf("len(rebuilt.Entries) = %d, want 1", len(rebuilt.Entries))
	}
	if rebuilt.Entries[0].ID != entry.ID {
		t.Fatalf("rebuilt.Entries[0].ID = %d, want %d", rebuilt.Entries[0].ID, entry.ID)
	}
	if rebuilt.Entries[0].Body != entry.Body {
		t.Fatalf("rebuilt.Entries[0].Body = %q, want %q", rebuilt.Entries[0].Body, entry.Body)
	}
	if rebuilt.Entries[0].SourceRef == "" {
		t.Fatal("rebuilt.Entries[0].SourceRef = empty, want SQLite-backed source ref")
	}

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry after mirror rebuild")
	}
	if stored.ID != entry.ID {
		t.Fatalf("stored.ID = %d, want %d", stored.ID, entry.ID)
	}
	if stored.Body != entry.Body {
		t.Fatalf("stored.Body = %q, want %q", stored.Body, entry.Body)
	}
}

func TestRepositoryRebuildIndexRebuildsMissingJSONMirrorFromSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Repairable entry",
		Body:   "repairtoken entry body",
		Source: "source note: repairtoken entry body",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", storePath, err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())
	updated, found, err := repo.Update(entry.ID, AddEntry{
		Depth:  entry.Depth,
		Title:  "Rebuilt from SQLite",
		Body:   "repairtoken entry body updated while mirror missing",
		Source: "source note: repairtoken entry body updated while mirror missing",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !found {
		t.Fatal("Update() did not find entry while mirror missing")
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want mirror to stay missing before RebuildIndex", storePath, err)
	}

	if err := repo.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want rebuilt mirror", storePath, err)
	}
	rebuilt := readSnapshotFile(t, storePath)
	if len(rebuilt.Entries) != 1 {
		t.Fatalf("len(rebuilt.Entries) = %d, want 1", len(rebuilt.Entries))
	}
	if rebuilt.Entries[0].Title != updated.Title {
		t.Fatalf("rebuilt.Entries[0].Title = %q, want %q", rebuilt.Entries[0].Title, updated.Title)
	}
	if rebuilt.Entries[0].Body != updated.Body {
		t.Fatalf("rebuilt.Entries[0].Body = %q, want %q", rebuilt.Entries[0].Body, updated.Body)
	}
	if rebuilt.Entries[0].SourceRef == "" {
		t.Fatal("rebuilt.Entries[0].SourceRef = empty, want SQLite-backed source ref")
	}

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry after RebuildIndex mirror rebuild")
	}
	if stored.Title != updated.Title {
		t.Fatalf("stored.Title = %q, want %q", stored.Title, updated.Title)
	}
	if stored.Body != updated.Body {
		t.Fatalf("stored.Body = %q, want %q", stored.Body, updated.Body)
	}
	if stored.Source != updated.Source {
		t.Fatalf("stored.Source = %q, want %q", stored.Source, updated.Source)
	}
}

func TestRepositoryMutationsDoNotRewriteJSONMirror(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(t *testing.T, repo *Repository, entry Entry) Entry
		check  func(t *testing.T, repo *Repository, original Entry, mutated Entry)
	}{
		{
			name: "add",
			mutate: func(t *testing.T, repo *Repository, entry Entry) Entry {
				t.Helper()
				added, err := repo.Add(AddEntry{Depth: 2, Title: "Added while mirror missing", Body: "mutation body"})
				if err != nil {
					t.Fatalf("repo.Add() error = %v", err)
				}
				return added
			},
			check: func(t *testing.T, repo *Repository, original Entry, mutated Entry) {
				t.Helper()
				loaded, ok, err := repo.Get(mutated.ID)
				if err != nil {
					t.Fatalf("repo.Get(added) error = %v", err)
				}
				if !ok {
					t.Fatal("repo.Get(added) did not find entry")
				}
				if loaded.Title != mutated.Title {
					t.Fatalf("loaded.Title = %q, want %q", loaded.Title, mutated.Title)
				}
			},
		},
		{
			name: "update",
			mutate: func(t *testing.T, repo *Repository, entry Entry) Entry {
				t.Helper()
				updated, found, err := repo.Update(entry.ID, AddEntry{Depth: entry.Depth, Title: "Updated while mirror missing", Body: "updated mutation body"})
				if err != nil {
					t.Fatalf("repo.Update() error = %v", err)
				}
				if !found {
					t.Fatal("repo.Update() did not find entry")
				}
				return updated
			},
			check: func(t *testing.T, repo *Repository, original Entry, mutated Entry) {
				t.Helper()
				loaded, ok, err := repo.Get(original.ID)
				if err != nil {
					t.Fatalf("repo.Get(updated) error = %v", err)
				}
				if !ok {
					t.Fatal("repo.Get(updated) did not find entry")
				}
				if loaded.Title != mutated.Title {
					t.Fatalf("loaded.Title = %q, want %q", loaded.Title, mutated.Title)
				}
			},
		},
		{
			name: "delete",
			mutate: func(t *testing.T, repo *Repository, entry Entry) Entry {
				t.Helper()
				deleted, err := repo.Delete(entry.ID)
				if err != nil {
					t.Fatalf("repo.Delete() error = %v", err)
				}
				if !deleted {
					t.Fatal("repo.Delete() did not delete entry")
				}
				return entry
			},
			check: func(t *testing.T, repo *Repository, original Entry, mutated Entry) {
				t.Helper()
				_, ok, err := repo.Get(original.ID)
				if err != nil {
					t.Fatalf("repo.Get(deleted) error = %v", err)
				}
				if ok {
					t.Fatal("repo.Get(deleted) found removed entry")
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			storePath := filepath.Join(root, "store.json")
			indexPath := filepath.Join(root, "vector")
			repo := NewRepository(storePath, indexPath, fakeembed.Provider())

			entry, err := repo.Add(AddEntry{Depth: 1, Title: "Original entry", Body: "original body"})
			if err != nil {
				t.Fatalf("repo.Add(seed) error = %v", err)
			}

			poisonMirrorWithSentinelSnapshot(t, storePath)
			mirrorBeforeMutation := snapshotMirrorFileState(t, storePath)

			mutated := tc.mutate(t, repo, entry)

			assertMirrorFileStateUnchanged(t, storePath, mirrorBeforeMutation, tc.name)

			tc.check(t, repo, entry, mutated)
		})

		t.Run(tc.name+"_missing_mirror", func(t *testing.T) {
			root := t.TempDir()
			storePath := filepath.Join(root, "store.json")
			indexPath := filepath.Join(root, "vector")
			repo := NewRepository(storePath, indexPath, fakeembed.Provider())

			entry, err := repo.Add(AddEntry{Depth: 1, Title: "Original entry", Body: "original body"})
			if err != nil {
				t.Fatalf("repo.Add(seed) error = %v", err)
			}

			if err := os.Remove(storePath); err != nil {
				t.Fatalf("os.Remove(%q) error = %v", storePath, err)
			}

			repo = NewRepository(storePath, indexPath, fakeembed.Provider())
			mutated := tc.mutate(t, repo, entry)

			if _, err := os.Stat(storePath); !os.IsNotExist(err) {
				t.Fatalf("os.Stat(%q) error = %v, want missing mirror after %s", storePath, err, tc.name)
			}

			tc.check(t, repo, entry, mutated)

			repo = NewRepository(storePath, indexPath, fakeembed.Provider())
			if err := repo.Init(); err != nil {
				t.Fatalf("repo.Init() error = %v", err)
			}
			if _, err := os.Stat(storePath); err != nil {
				t.Fatalf("os.Stat(%q) after Init error = %v, want rebuilt mirror", storePath, err)
			}
			rebuilt := readSnapshotFile(t, storePath)
			switch tc.name {
			case "add":
				if len(rebuilt.Entries) != 2 {
					t.Fatalf("len(rebuilt.Entries) = %d, want 2 after add recovery", len(rebuilt.Entries))
				}
			case "update":
				if len(rebuilt.Entries) != 1 {
					t.Fatalf("len(rebuilt.Entries) = %d, want 1 after update recovery", len(rebuilt.Entries))
				}
				if rebuilt.Entries[0].Title != mutated.Title {
					t.Fatalf("rebuilt.Entries[0].Title = %q, want %q", rebuilt.Entries[0].Title, mutated.Title)
				}
			case "delete":
				if len(rebuilt.Entries) != 0 {
					t.Fatalf("len(rebuilt.Entries) = %d, want 0 after delete recovery", len(rebuilt.Entries))
				}
			}
			tc.check(t, repo, entry, mutated)
		})
	}
}

func TestRepositoryInitIgnoresDriftedJSONMirrorWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth note",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, indexPath)

	mutateStoreJSONEntry(t, storePath, entry.ID, func(stored *Entry) {
		stored.Body = "External JSON drift should not replace SQLite body during Init."
		stored.Source = "external json source drift during init"
		stored.SourceRef = ""
	})

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry after Init with drifted JSON mirror")
	}
	if stored.Body != entry.Body {
		t.Fatalf("stored.Body = %q, want %q", stored.Body, entry.Body)
	}
	if stored.Source != entry.Source {
		t.Fatalf("stored.Source = %q, want %q", stored.Source, entry.Source)
	}
}

func TestRepositoryQueryEmbeddingCacheIsBounded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	calls := map[string]int{}
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.Provider{
		Name:     "test",
		IndexKey: "test",
		Func: func(_ context.Context, text string) ([]float32, error) {
			calls[text]++
			return []float32{float32(len(text))}, nil
		},
	})
	repo.queryCache = newQueryEmbeddingCache(2)

	for _, query := range []string{"one", "two", "two", "three", "one"} {
		if _, err := repo.queryEmbedding(query); err != nil {
			t.Fatalf("queryEmbedding(%q) error = %v", query, err)
		}
	}

	if got := calls["one"]; got != 2 {
		t.Fatalf("provider calls for one = %d, want 2 after eviction", got)
	}
	if got := calls["two"]; got != 1 {
		t.Fatalf("provider calls for two = %d, want 1 from cache hit", got)
	}
	if got := calls["three"]; got != 1 {
		t.Fatalf("provider calls for three = %d, want 1", got)
	}

	vector, ok := repo.queryCache.get("three")
	if !ok {
		t.Fatal("expected three to remain in cache")
	}
	if len(vector) != 1 || vector[0] != 5 {
		t.Fatalf("cache.get(three) = %v, want [5]", vector)
	}

	vector, ok = repo.queryCache.get("one")
	if !ok {
		t.Fatal("expected one to be reinserted after eviction")
	}
	if len(vector) != 1 || vector[0] != 3 {
		t.Fatalf("cache.get(one) = %v, want [3]", vector)
	}

	if _, ok := repo.queryCache.get("two"); ok {
		t.Fatal("expected two to be evicted after one was recomputed")
	}
	if got := len(repo.queryCache.order); got != 2 {
		t.Fatalf("len(cache.order) = %d, want 2", got)
	}
	if got := len(repo.queryCache.values); got != 2 {
		t.Fatalf("len(cache.values) = %d, want 2", got)
	}
}

func mutateStoreJSONEntry(t *testing.T, storePath string, id int, mutate func(*Entry)) {
	t.Helper()

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", storePath, err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for i := range snapshot.Entries {
		if snapshot.Entries[i].ID != id {
			continue
		}
		mutate(&snapshot.Entries[i])
		updated, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			t.Fatalf("json.MarshalIndent() error = %v", err)
		}
		if err := os.WriteFile(storePath, updated, 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", storePath, err)
		}
		return
	}

	t.Fatalf("entry %d not found in %s", id, storePath)
}

func rebuildJSONMirrorForTest(t testing.TB, storePath, indexPath string) {
	t.Helper()

	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
}

func TestSQLiteListEntriesByIDsPreservesRequestedSubset(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	first, err := repo.Add(AddEntry{Depth: 1, Title: "First", Body: "body one", Tags: []string{"alpha"}})
	if err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	_, err = repo.Add(AddEntry{Depth: 1, Title: "Second", Body: "body two", Tags: []string{"beta"}})
	if err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	third, err := repo.Add(AddEntry{Depth: 1, Title: "Third", Body: "body three", Tags: []string{"gamma"}})
	if err != nil {
		t.Fatalf("Add(third) error = %v", err)
	}

	var entries []Entry
	err = repo.withSharedLock(func() error {
		if err := repo.ensureSQLiteReadModelLocked(); err != nil {
			return err
		}
		var err error
		entries, err = sqliteListEntriesByIDs(repo.metaDB, []int{third.ID, first.ID})
		return err
	})
	if err != nil {
		t.Fatalf("sqliteListEntriesByIDs() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].ID != third.ID || entries[0].Title != "Third" {
		t.Fatalf("entries[0] = %+v, want third entry", entries[0])
	}
	if entries[1].ID != first.ID || entries[1].Title != "First" {
		t.Fatalf("entries[1] = %+v, want first entry", entries[1])
	}
	if len(entries[0].Tags) != 1 || entries[0].Tags[0] != "gamma" {
		t.Fatalf("entries[0].Tags = %v, want [gamma]", entries[0].Tags)
	}
	if len(entries[1].Tags) != 1 || entries[1].Tags[0] != "alpha" {
		t.Fatalf("entries[1].Tags = %v, want [alpha]", entries[1].Tags)
	}
}

func TestSQLiteListEntriesByIDsHandlesLargeRequestedSubset(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Large subset entry", Body: "body"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	const entryCount = 40000
	ids := make([]int, 0, entryCount)
	for i := 0; i < entryCount; i++ {
		ids = append(ids, entry.ID)
	}

	var entries []Entry
	err = repo.withSharedLock(func() error {
		if err := repo.ensureSQLiteReadModelLocked(); err != nil {
			return err
		}
		var err error
		entries, err = sqliteListEntriesByIDs(repo.metaDB, ids)
		return err
	})
	if err != nil {
		t.Fatalf("sqliteListEntriesByIDs() error = %v", err)
	}

	if len(entries) != entryCount {
		t.Fatalf("len(entries) = %d, want %d", len(entries), entryCount)
	}
	if entries[0].ID != entry.ID {
		t.Fatalf("entries[0].ID = %d, want %d", entries[0].ID, entry.ID)
	}
	if entries[len(entries)-1].ID != entry.ID {
		t.Fatalf("entries[last].ID = %d, want %d", entries[len(entries)-1].ID, entry.ID)
	}
}

func TestRepositorySearchOrdersByScore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Auth decision", Body: "We moved auth to tiered sessions.", Tags: []string{"security"}})
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Roadmap", Body: "Auth migration is blocked on rollout planning.", Tags: []string{"planning"}})
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "Billing", Body: "Unrelated entry.", Tags: []string{"finance"}})

	results, err := repo.Search(Query{Text: "auth", Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Title != "Auth decision" {
		t.Fatalf("results[0].Title = %q, want %q", results[0].Title, "Auth decision")
	}
}

func TestRepositoryDepthCounts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	repo.now = func() time.Time { return time.Now().UTC() }

	_, _ = repo.Add(AddEntry{Depth: 2, Title: "A", Body: "one"})
	_, _ = repo.Add(AddEntry{Depth: 0, Title: "B", Body: "two"})
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "C", Body: "three"})

	summaries, err := repo.DepthCounts()
	if err != nil {
		t.Fatalf("DepthCounts() error = %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].Depth != 0 || summaries[0].Count != 1 {
		t.Fatalf("summaries[0] = %+v, want depth 0 count 1", summaries[0])
	}
	if summaries[1].Depth != 2 || summaries[1].Count != 2 {
		t.Fatalf("summaries[1] = %+v, want depth 2 count 2", summaries[1])
	}
}

func TestRepositoryDepthCountsUsesSQLiteMetadataWhenSnapshotEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	if _, err := repo.Add(AddEntry{Depth: 2, Title: "A", Body: "one"}); err != nil {
		t.Fatalf("Add(A) error = %v", err)
	}
	if _, err := repo.Add(AddEntry{Depth: 0, Title: "B", Body: "two"}); err != nil {
		t.Fatalf("Add(B) error = %v", err)
	}
	if _, err := repo.Add(AddEntry{Depth: 2, Title: "C", Body: "three"}); err != nil {
		t.Fatalf("Add(C) error = %v", err)
	}

	repo.mu.Lock()
	repo.snapshot = Snapshot{}
	repo.loaded = false
	repo.mu.Unlock()
	repo.path = root

	summaries, err := repo.DepthCounts()
	if err != nil {
		t.Fatalf("DepthCounts() error = %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].Depth != 0 || summaries[0].Count != 1 {
		t.Fatalf("summaries[0] = %+v, want depth 0 count 1", summaries[0])
	}
	if summaries[1].Depth != 2 || summaries[1].Count != 2 {
		t.Fatalf("summaries[1] = %+v, want depth 2 count 2", summaries[1])
	}
}

func TestRepositoryDuplicateCheckHydratesMatchesWithoutSnapshotMap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Auth migration",
		Body:   "We moved authentication to bearer tokens for the API gateway.",
		Source: "source note: We moved authentication to bearer tokens for the API gateway.",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	repo.mu.Lock()
	repo.snapshot = Snapshot{}
	repo.loaded = false
	repo.mu.Unlock()
	repo.path = root

	matches, err := repo.DuplicateCheck(entry.Body, 0.9)
	if err != nil {
		t.Fatalf("DuplicateCheck() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].Entry.ID != entry.ID {
		t.Fatalf("matches[0].Entry.ID = %d, want %d", matches[0].Entry.ID, entry.ID)
	}
	if matches[0].Entry.Source != "source note: We moved authentication to bearer tokens for the API gateway." {
		t.Fatalf("matches[0].Entry.Source = %q, want hydrated source", matches[0].Entry.Source)
	}
}

func TestRepositorySearchTrimsLowSignalTail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Auth migration", Body: "We moved authentication from cookies to bearer tokens and session checks for the API gateway.", Tags: []string{"auth", "security"}})
	_, _ = repo.Add(AddEntry{Depth: 2, Title: "Gardening note", Body: "Tomato seedlings need warmer soil and less standing water this week.", Tags: []string{"garden"}})

	results, err := repo.Search(Query{Text: "api token authentication", Limit: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Title != "Auth migration" {
		t.Fatalf("results[0].Title = %q, want %q", results[0].Title, "Auth migration")
	}
}

func TestRepositorySearchDetailedIncludesSupportAndConflicts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	sharedTags := []string{"staging", "database", "config"}
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Legacy staging database", Body: "Staging uses mysql.internal.example.com.", Tags: sharedTags, Origin: "docs/legacy.md"})
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Staging database", Body: "Staging uses postgres.internal.example.com.", Tags: sharedTags, Origin: "manual"})
	_, _ = repo.Add(AddEntry{Depth: 1, Title: "Staging database confirmation", Body: "Staging uses postgres.internal.example.com.", Tags: sharedTags, Origin: "notes/runbook.md"})

	results, err := repo.SearchDetailed(Query{Text: "What database does staging use?", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected detailed search results")
	}
	if results[0].SupportCount != 2 {
		t.Fatalf("results[0].SupportCount = %d, want 2", results[0].SupportCount)
	}
	if results[0].SourceKinds != 2 {
		t.Fatalf("results[0].SourceKinds = %d, want 2", results[0].SourceKinds)
	}
	if results[0].ConflictCount != 1 {
		t.Fatalf("results[0].ConflictCount = %d, want 1", results[0].ConflictCount)
	}
	if results[0].Entry.Body != "Staging uses postgres.internal.example.com." {
		t.Fatalf("results[0].Entry.Body = %q, want postgres match", results[0].Entry.Body)
	}
}

func TestRepositorySearchDetailedWorksWithoutResidentSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	matching, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Bearer token authentication",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
		Tags:   []string{"auth"},
	})
	if err != nil {
		t.Fatalf("Add(matching) error = %v", err)
	}
	if _, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Gardening",
		Body:  "Tomatoes need warmer soil and less standing water.",
		Tags:  []string{"garden"},
	}); err != nil {
		t.Fatalf("Add(unrelated) error = %v", err)
	}

	stamp, err := currentFileStamp(repo.path)
	if err != nil {
		t.Fatalf("currentFileStamp() error = %v", err)
	}

	repo.mu.Lock()
	repo.snapshot = Snapshot{
		Version: 1,
		NextID:  3,
		Entries: []Entry{
			{ID: 900, Depth: 1, Title: "Bogus auth", Body: "irrelevant body", UpdatedAt: time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)},
			{ID: 901, Depth: 1, Title: "Bogus garden", Body: "still irrelevant", UpdatedAt: time.Date(2026, 4, 7, 12, 1, 0, 0, time.UTC)},
		},
	}
	repo.loaded = true
	repo.storeStamp = stamp
	repo.mu.Unlock()

	results, err := repo.SearchDetailed(Query{Text: "token authentication", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Entry.ID != matching.ID {
		t.Fatalf("results[0].Entry.ID = %d, want %d", results[0].Entry.ID, matching.ID)
	}
	if results[0].Entry.Source != "source note: The API now uses token authentication for requests." {
		t.Fatalf("results[0].Entry.Source = %q, want hydrated source", results[0].Entry.Source)
	}
}

func TestRepositoryListMetadataColdPathDoesNotLoadSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	for i := 0; i < 3; i++ {
		if _, err := repo.Add(AddEntry{Depth: 1, Title: "Entry " + strconv.Itoa(i), Body: "body " + strconv.Itoa(i), Tags: []string{"alpha"}}); err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())

	entries, err := repo.ListMetadata(Query{Limit: 1})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if repo.loaded {
		t.Fatal("expected cold metadata read to avoid snapshot residency")
	}
	if len(repo.snapshot.Entries) != 0 {
		t.Fatalf("len(repo.snapshot.Entries) = %d, want 0", len(repo.snapshot.Entries))
	}
}

func TestRepositorySearchDetailedEmptyQueryUsesListFilteringAndHydration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	depth := 1

	_, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Filtered entry",
		Body:   "body one",
		Source: "hydrated source one",
		Tags:   []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("Add(filtered) error = %v", err)
	}
	_, err = repo.Add(AddEntry{
		Depth:  2,
		Title:  "Other entry",
		Body:   "body two",
		Source: "hydrated source two",
		Tags:   []string{"beta"},
	})
	if err != nil {
		t.Fatalf("Add(other) error = %v", err)
	}

	results, err := repo.SearchDetailed(Query{Text: "", Depth: &depth, Limit: 10})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Entry.Title != "Filtered entry" {
		t.Fatalf("results[0].Entry.Title = %q, want %q", results[0].Entry.Title, "Filtered entry")
	}
	if results[0].Entry.Source != "hydrated source one" {
		t.Fatalf("results[0].Entry.Source = %q, want hydrated source", results[0].Entry.Source)
	}
}

func TestRepositorySearchDetailedKeywordlessQueryUsesListFilteringAndHydration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	_, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Tagged entry",
		Body:   "body one",
		Source: "hydrated source alpha",
		Tags:   []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("Add(alpha) error = %v", err)
	}
	_, err = repo.Add(AddEntry{
		Depth:  1,
		Title:  "Untagged match",
		Body:   "body two",
		Source: "hydrated source beta",
		Tags:   []string{"beta"},
	})
	if err != nil {
		t.Fatalf("Add(beta) error = %v", err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	repo.mu.Lock()
	repo.snapshot = Snapshot{}
	repo.loaded = false
	repo.mu.Unlock()

	results, err := repo.SearchDetailed(Query{Text: "...", Tag: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Entry.Title != "Tagged entry" {
		t.Fatalf("results[0].Entry.Title = %q, want %q", results[0].Entry.Title, "Tagged entry")
	}
	if results[0].Entry.Source != "hydrated source alpha" {
		t.Fatalf("results[0].Entry.Source = %q, want hydrated source", results[0].Entry.Source)
	}
}

func TestRepositorySearchDetailedFallbackRespectsTagFilter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	for i := 0; i < 4; i++ {
		_, err := repo.Add(AddEntry{
			Depth: 1,
			Title: "Release retention note " + strconv.Itoa(i),
			Body:  "Release notes retention plan for beta systems and rollout validation.",
			Tags:  []string{"beta"},
		})
		if err != nil {
			t.Fatalf("Add(beta %d) error = %v", i, err)
		}
	}

	alpha, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Alpha retention fallback",
		Body:   "Retention guidance for alpha systems.",
		Source: "alpha source",
		Tags:   []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("Add(alpha) error = %v", err)
	}

	results, err := repo.SearchDetailed(Query{Text: "release notes retention", Tag: "alpha", Limit: 1})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Entry.ID != alpha.ID {
		t.Fatalf("results[0].Entry.ID = %d, want %d", results[0].Entry.ID, alpha.ID)
	}
	if results[0].Entry.Source != "alpha source" {
		t.Fatalf("results[0].Entry.Source = %q, want hydrated source", results[0].Entry.Source)
	}
	if len(results[0].Entry.Tags) != 1 || results[0].Entry.Tags[0] != "alpha" {
		t.Fatalf("results[0].Entry.Tags = %v, want [alpha]", results[0].Entry.Tags)
	}
}

func TestRepositorySearchDetailedColdPathDoesNotLoadSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	if _, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Bearer token authentication",
		Body:   "The API now uses token authentication for requests.",
		Source: "source note: The API now uses token authentication for requests.",
		Tags:   []string{"auth"},
	}); err != nil {
		t.Fatalf("Add(matching) error = %v", err)
	}
	if _, err := repo.Add(AddEntry{Depth: 1, Title: "Gardening", Body: "Tomatoes need warmer soil.", Tags: []string{"garden"}}); err != nil {
		t.Fatalf("Add(unrelated) error = %v", err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())

	results, err := repo.SearchDetailed(Query{Text: "token authentication", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if repo.loaded {
		t.Fatal("expected cold search path to avoid snapshot residency")
	}
	if len(repo.snapshot.Entries) != 0 {
		t.Fatalf("len(repo.snapshot.Entries) = %d, want 0", len(repo.snapshot.Entries))
	}
}

func TestRepositoryUpdateReindexesEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Profile", Body: "legacytoken old profile note"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	updated, found, err := repo.Update(entry.ID, AddEntry{Depth: 1, Title: "Profile", Body: "currenttoken new profile note"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !found {
		t.Fatal("Update() did not find entry")
	}
	if updated.Body != "currenttoken new profile note" {
		t.Fatalf("updated.Body = %q", updated.Body)
	}

	results, err := repo.Search(Query{Text: "currenttoken", Limit: 5})
	if err != nil {
		t.Fatalf("Search(new) error = %v", err)
	}
	if len(results) == 0 || results[0].ID != entry.ID {
		t.Fatalf("Search(new) = %+v, want updated entry %d", results, entry.ID)
	}

	err = repo.withSharedLock(func() error {
		snapshot, err := repo.loadLocked()
		if err != nil {
			return err
		}
		if err := repo.ensureIndexLocked(snapshot); err != nil {
			return err
		}
		doc, err := repo.collection.GetByID(context.Background(), strconv.Itoa(entry.ID))
		if err != nil {
			return err
		}
		if doc.Content != "Profile\n\ncurrenttoken new profile note" {
			t.Fatalf("doc.Content = %q", doc.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect indexed document: %v", err)
	}
}

func TestRepositoryAddPersistsToSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	added, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "SQLite persistence",
		Body:   "metadata should persist before reads",
		Source: "full source text",
		Origin: "manual",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	stored, ok, err := repo.Get(added.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find added entry")
	}
	if stored.Title != added.Title {
		t.Fatalf("stored.Title = %q, want %q", stored.Title, added.Title)
	}
	if stored.Source != "full source text" {
		t.Fatalf("stored.Source = %q, want hydrated source", stored.Source)
	}

	indexState, err := repo.metaValue("index_state")
	if err != nil {
		t.Fatalf("metaValue(index_state) error = %v", err)
	}
	if indexState != "ready" {
		t.Fatalf("index_state = %q, want %q", indexState, "ready")
	}
}

func TestRepositoryMigratesLegacyStoreJSONToSQLite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	createdAt := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)

	legacy := Snapshot{
		Version: 1,
		NextID:  2,
		Entries: []Entry{{
			ID:        1,
			Depth:     1,
			Title:     "Legacy entry",
			Body:      "migrate this into sqlite",
			Tags:      []string{"legacy", "migration"},
			Source:    "legacy inline source",
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
		}},
	}

	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	stored, ok, err := repo.Get(1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find migrated entry")
	}
	if stored.Title != "Legacy entry" {
		t.Fatalf("stored.Title = %q, want %q", stored.Title, "Legacy entry")
	}
	if stored.Source != "legacy inline source" {
		t.Fatalf("stored.Source = %q, want %q", stored.Source, "legacy inline source")
	}

	results, err := repo.Search(Query{Text: "migrate this into sqlite", Limit: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 || results[0].ID != 1 {
		t.Fatalf("Search() = %+v, want migrated entry 1", results)
	}

	if _, err := os.Stat(filepath.Join(root, "store.db")); err != nil {
		t.Fatalf("store.db stat error = %v", err)
	}
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("store.json stat error = %v", err)
	}
}

func TestRepositoryMarksIndexDirtyWhenIndexWriteFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	indexErr := errors.New("index write failed")
	repo.indexEntriesImpl = func(entries []Entry) error {
		if len(entries) != 1 {
			t.Fatalf("len(entries) = %d, want 1", len(entries))
		}
		return indexErr
	}

	_, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Dirty index state",
		Body:  "metadata commit should survive index failure",
	})
	if !errors.Is(err, indexErr) {
		t.Fatalf("Add() error = %v, want %v", err, indexErr)
	}

	indexState, err := repo.metaValue("index_state")
	if err != nil {
		t.Fatalf("metaValue(index_state) error = %v", err)
	}
	if indexState != "dirty" {
		t.Fatalf("index_state = %q, want %q", indexState, "dirty")
	}

	stored, ok, err := repo.Get(1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find committed metadata after index failure")
	}
	if stored.Title != "Dirty index state" {
		t.Fatalf("stored.Title = %q, want %q", stored.Title, "Dirty index state")
	}
}

func TestRepositorySearchFailsFastWhenIndexDirty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	indexErr := errors.New("index write failed")
	repo.indexEntriesImpl = func(entries []Entry) error {
		return indexErr
	}

	_, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Dirty search entry",
		Body:  "search should fail fast until repair",
	})
	if !errors.Is(err, indexErr) {
		t.Fatalf("Add() error = %v, want %v", err, indexErr)
	}

	results, err := repo.SearchDetailed(Query{Text: "repair", Limit: 5})
	if err == nil {
		t.Fatalf("SearchDetailed() = %+v, want dirty index error", results)
	}
	if !strings.Contains(err.Error(), "index needs repair") {
		t.Fatalf("SearchDetailed() error = %v, want index needs repair message", err)
	}
	if len(results) != 0 {
		t.Fatalf("SearchDetailed() returned %+v, want no results while dirty", results)
	}
}

func TestRepositoryRebuildIndexClearsDirtyState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	indexErr := errors.New("index write failed")
	repo.indexEntriesImpl = func(entries []Entry) error {
		return indexErr
	}

	_, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Repairable entry",
		Body:  "repairtoken entry body",
	})
	if !errors.Is(err, indexErr) {
		t.Fatalf("Add() error = %v, want %v", err, indexErr)
	}

	if err := repo.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}

	indexState, err := repo.metaValue(metaKeyIndexState)
	if err != nil {
		t.Fatalf("metaValue(index_state) error = %v", err)
	}
	if indexState != "ready" {
		t.Fatalf("index_state = %q, want %q", indexState, "ready")
	}

	results, err := repo.SearchDetailed(Query{Text: "repairtoken", Limit: 5})
	if err != nil {
		t.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 || results[0].Entry.Title != "Repairable entry" {
		t.Fatalf("SearchDetailed() = %+v, want rebuilt entry", results)
	}
}

func TestRepositoryDefersStoreMirrorUntilIndexReady(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	orderingErr := errors.New("store mirror updated before index ready")
	indexErr := errors.New("index write failed after sqlite commit")
	repo.indexEntriesImpl = func(entries []Entry) error {
		if len(entries) != 1 {
			return errors.New("expected one indexed entry")
		}

		stored, found, err := sqliteGetEntry(repo.metaDB, entries[0].ID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("sqlite metadata missing committed entry")
		}
		if stored.Title != entries[0].Title {
			return errors.New("sqlite metadata title mismatch")
		}

		var indexState string
		if err := repo.metaDB.QueryRow(`SELECT value FROM meta WHERE key = 'index_state'`).Scan(&indexState); err != nil {
			return err
		}
		if indexState != "dirty" {
			return errors.New("index_state should be dirty before vector write")
		}

		rawStore, err := os.ReadFile(storePath)
		if err != nil {
			return err
		}
		if strings.Contains(string(rawStore), entries[0].Title) {
			return orderingErr
		}

		return indexErr
	}

	_, err := repo.Add(AddEntry{
		Depth: 1,
		Title: "Deferred mirror entry",
		Body:  "sqlite should commit before the json mirror",
	})
	if !errors.Is(err, indexErr) {
		t.Fatalf("Add() error = %v, want %v", err, indexErr)
	}
}

func TestRepositoryReloadsAfterExternalWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repoA := NewRepository(storePath, indexPath, fakeembed.Provider())
	repoB := NewRepository(storePath, indexPath, fakeembed.Provider())

	entries, err := repoA.List(Query{Limit: 0})
	if err != nil {
		t.Fatalf("repoA.List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("repoA.List() returned %d entries, want 0", len(entries))
	}

	first, err := repoB.Add(AddEntry{Depth: 1, Title: "First", Body: "alpha external write"})
	if err != nil {
		t.Fatalf("repoB.Add() error = %v", err)
	}
	second, err := repoA.Add(AddEntry{Depth: 1, Title: "Second", Body: "beta local write"})
	if err != nil {
		t.Fatalf("repoA.Add() error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("second.ID = %d, want a new ID after external write", second.ID)
	}

	repoC := NewRepository(storePath, indexPath, fakeembed.Provider())
	all, err := repoC.List(Query{Limit: 0})
	if err != nil {
		t.Fatalf("repoC.List() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}
}

func TestRepositoryEnsureSQLiteReadModelLockedKeepsMirrorMetadataStable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())

	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Status", Body: "before external refresh", Tags: []string{"alpha"}})
	if err != nil {
		t.Fatalf("repo.Add() error = %v", err)
	}

	originalMirrorRevision, err := repo.metaValue(metaKeyJSONMirrorRev)
	if err != nil {
		t.Fatalf("repo.metaValue(json_mirror_revision) error = %v", err)
	}
	originalMirrorStamp, err := repo.metaValue(metaKeyJSONMirrorStamp)
	if err != nil {
		t.Fatalf("repo.metaValue(json_mirror_stamp) error = %v", err)
	}

	snapshot := readSnapshotFile(t, storePath)
	for i := range snapshot.Entries {
		if snapshot.Entries[i].ID != entry.ID {
			continue
		}
		snapshot.Entries[i].Title = "Status refreshed"
		snapshot.Entries[i].Body = "after external refresh"
		snapshot.Entries[i].UpdatedAt = snapshot.Entries[i].UpdatedAt.Add(time.Minute)
		snapshot.Entries[i].Source = "after external refresh"
		snapshot.Entries[i].SourceRef = ""
	}
	writeSnapshotFile(t, storePath, snapshot)

	reader := NewRepository(storePath, indexPath, fakeembed.Provider())
	err = reader.withSharedLock(func() error {
		return reader.ensureSQLiteReadModelLocked()
	})
	if err != nil {
		t.Fatalf("reader.ensureSQLiteReadModelLocked() error = %v", err)
	}

	mirrorRevision, err := reader.metaValue(metaKeyJSONMirrorRev)
	if err != nil {
		t.Fatalf("reader.metaValue(json_mirror_revision) error = %v", err)
	}
	if mirrorRevision != originalMirrorRevision {
		t.Fatalf("json_mirror_revision = %q, want %q", mirrorRevision, originalMirrorRevision)
	}
	mirrorStamp, err := reader.metaValue(metaKeyJSONMirrorStamp)
	if err != nil {
		t.Fatalf("reader.metaValue(json_mirror_stamp) error = %v", err)
	}
	if mirrorStamp != originalMirrorStamp {
		t.Fatalf("json_mirror_stamp = %q, want %q", mirrorStamp, originalMirrorStamp)
	}

	entries, err := reader.ListMetadata(Query{Limit: 5})
	if err != nil {
		t.Fatalf("reader.ListMetadata() error = %v", err)
	}
	mirrorRevision, err = reader.metaValue(metaKeyJSONMirrorRev)
	if err != nil {
		t.Fatalf("reader.metaValue(json_mirror_revision) after ListMetadata error = %v", err)
	}
	if mirrorRevision != originalMirrorRevision {
		t.Fatalf("json_mirror_revision after ListMetadata = %q, want %q", mirrorRevision, originalMirrorRevision)
	}
	mirrorStamp, err = reader.metaValue(metaKeyJSONMirrorStamp)
	if err != nil {
		t.Fatalf("reader.metaValue(json_mirror_stamp) after ListMetadata error = %v", err)
	}
	if mirrorStamp != originalMirrorStamp {
		t.Fatalf("json_mirror_stamp after ListMetadata = %q, want %q", mirrorStamp, originalMirrorStamp)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Title != entry.Title {
		t.Fatalf("entries[0].Title = %q, want %q", entries[0].Title, entry.Title)
	}
	if entries[0].Body != entry.Body {
		t.Fatalf("entries[0].Body = %q, want %q", entries[0].Body, entry.Body)
	}
	if entries[0].Source != "" {
		t.Fatalf("entries[0].Source = %q, want empty metadata source", entries[0].Source)
	}
}

func TestRepositorySearchReloadsAfterExternalUpdate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repoA := NewRepository(storePath, indexPath, fakeembed.Provider())
	repoB := NewRepository(storePath, indexPath, fakeembed.Provider())

	entry, err := repoA.Add(AddEntry{Depth: 1, Title: "Status", Body: "beforetoken old state"})
	if err != nil {
		t.Fatalf("repoA.Add() error = %v", err)
	}
	if _, err := repoA.Search(Query{Text: "beforetoken", Limit: 5}); err != nil {
		t.Fatalf("repoA.Search(before) error = %v", err)
	}

	_, found, err := repoB.Update(entry.ID, AddEntry{Depth: 1, Title: "Status", Body: "aftertoken new state"})
	if err != nil {
		t.Fatalf("repoB.Update() error = %v", err)
	}
	if !found {
		t.Fatal("repoB.Update() did not find entry")
	}

	results, err := repoA.Search(Query{Text: "aftertoken", Limit: 5})
	if err != nil {
		t.Fatalf("repoA.Search(after) error = %v", err)
	}
	if len(results) == 0 || results[0].ID != entry.ID {
		t.Fatalf("repoA.Search(after) = %+v, want updated entry %d", results, entry.ID)
	}
}

func TestRepositorySearchKeepsSQLiteResultsAfterExternalJSONOnlyEdit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	now := time.Date(2026, 4, 30, 13, 0, 0, 0, time.UTC)
	repo.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	control, err := repo.Add(AddEntry{Depth: 1, Title: "Control", Body: "beforetoken control"})
	if err != nil {
		t.Fatalf("repo.Add(control) error = %v", err)
	}

	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Status", Body: "beforetoken alpha beta gamma"})
	if err != nil {
		t.Fatalf("repo.Add() error = %v", err)
	}

	beforeResults, err := repo.Search(Query{Text: "beforetoken alpha beta gamma", Limit: 5})
	if err != nil {
		t.Fatalf("repo.Search(before) error = %v", err)
	}
	if len(beforeResults) == 0 || beforeResults[0].ID != entry.ID {
		t.Fatalf("repo.Search(before) = %+v, want entry %d", beforeResults, entry.ID)
	}

	rebuildJSONMirrorForTest(t, storePath, indexPath)

	snapshot := readSnapshotFile(t, storePath)
	if len(snapshot.Entries) != 2 {
		t.Fatalf("len(snapshot.Entries) = %d, want 2", len(snapshot.Entries))
	}
	for i := range snapshot.Entries {
		if snapshot.Entries[i].ID != entry.ID {
			continue
		}
		snapshot.Entries[i].Body = "aftertoken new state"
		snapshot.Entries[i].UpdatedAt = snapshot.Entries[i].UpdatedAt.Add(time.Minute)
		snapshot.Entries[i].Source = "aftertoken new state"
		snapshot.Entries[i].SourceRef = ""
	}
	writeSnapshotFile(t, storePath, snapshot)

	staleResults, err := repo.Search(Query{Text: "beforetoken alpha beta gamma", Limit: 5})
	if err != nil {
		t.Fatalf("repo.Search(stale) error = %v", err)
	}
	if len(staleResults) == 0 || staleResults[0].ID != entry.ID {
		t.Fatalf("repo.Search(stale) = %+v, want original entry %d to remain searchable", staleResults, entry.ID)
	}

	controlResults, err := repo.Search(Query{Text: "beforetoken control", Limit: 5})
	if err != nil {
		t.Fatalf("repo.Search(control) error = %v", err)
	}
	if len(controlResults) == 0 || controlResults[0].ID != control.ID {
		t.Fatalf("repo.Search(control) = %+v, want control entry %d to rank first", controlResults, control.ID)
	}
}

func TestRepositorySearchFailsWhenExternalUpdateLeavesDirtyIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repoA := NewRepository(storePath, indexPath, fakeembed.Provider())
	indexErr := errors.New("index write failed during update")

	entry, err := repoA.Add(AddEntry{Depth: 1, Title: "Status", Body: "beforetoken old state"})
	if err != nil {
		t.Fatalf("repoA.Add() error = %v", err)
	}

	beforeResults, err := repoA.Search(Query{Text: "beforetoken", Limit: 5})
	if err != nil {
		t.Fatalf("repoA.Search(before) error = %v", err)
	}
	if len(beforeResults) == 0 || beforeResults[0].ID != entry.ID {
		t.Fatalf("repoA.Search(before) = %+v, want entry %d", beforeResults, entry.ID)
	}

	repoB := NewRepository(storePath, indexPath, fakeembed.Provider())
	loaded, ok, err := repoB.Get(entry.ID)
	if err != nil {
		t.Fatalf("repoB.Get() error = %v", err)
	}
	if !ok || loaded.ID != entry.ID {
		t.Fatalf("repoB.Get() = (%+v, %v), want entry %d", loaded, ok, entry.ID)
	}

	repoB.indexEntriesImpl = func(entries []Entry) error {
		return indexErr
	}

	_, _, err = repoB.Update(entry.ID, AddEntry{Depth: 1, Title: "Status", Body: "aftertoken new state"})
	if !errors.Is(err, indexErr) {
		t.Fatalf("repoB.Update() error = %v, want %v", err, indexErr)
	}

	indexState, err := repoB.metaValue(metaKeyIndexState)
	if err != nil {
		t.Fatalf("repoB.metaValue(index_state) error = %v", err)
	}
	if indexState != "dirty" {
		t.Fatalf("index_state = %q, want dirty after failed external update", indexState)
	}

	results, err := repoA.Search(Query{Text: "beforetoken", Limit: 5})
	if err == nil {
		t.Fatalf("repoA.Search(stale) = %+v, want dirty index error", results)
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("repoA.Search(stale) error = %v, want dirty index error", err)
	}
	if len(results) != 0 {
		t.Fatalf("repoA.Search(stale) returned %+v, want no stale results", results)
	}
}

func TestRepositoryListMetadataIgnoresExternalAddWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	initial, err := repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("repo.ListMetadata(initial) error = %v", err)
	}
	if len(initial) != 0 {
		t.Fatalf("len(initial) = %d, want 0", len(initial))
	}

	createdAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	writeSnapshotFile(t, storePath, Snapshot{
		Version: currentVersion,
		NextID:  2,
		Entries: []Entry{{
			ID:        1,
			Depth:     1,
			Title:     "External add",
			Body:      "alpha external metadata",
			SourceRef: sourceRef("alpha external metadata"),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}},
	})

	entries, err := repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("repo.ListMetadata(after add) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0 because SQLite metadata stays authoritative", len(entries))
	}
}

func TestRepositoryGetIgnoresExternalUpdateWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	entry, err := repo.Add(AddEntry{Depth: 1, Title: "Status", Body: "before body", Source: "before source"})
	if err != nil {
		t.Fatalf("repo.Add() error = %v", err)
	}

	loaded, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("repo.Get(before) error = %v", err)
	}
	if !ok {
		t.Fatal("repo.Get(before) did not find entry")
	}
	if loaded.Source != "before source" {
		t.Fatalf("loaded.Source = %q, want %q", loaded.Source, "before source")
	}

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store) error = %v", err)
	}
	repo = NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	snapshot := readSnapshotFile(t, storePath)
	snapshot.Entries[0].Body = "after body"
	snapshot.Entries[0].SourceRef = sourceRef("after source")
	snapshot.Entries[0].UpdatedAt = snapshot.Entries[0].UpdatedAt.Add(time.Minute)
	writeSnapshotFile(t, storePath, snapshot)
	if _, err := repo.ensureSourceBlob("after source"); err != nil {
		t.Fatalf("repo.ensureSourceBlob() error = %v", err)
	}

	loaded, ok, err = repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("repo.Get(after) error = %v", err)
	}
	if !ok {
		t.Fatal("repo.Get(after) did not find entry")
	}
	if loaded.Body != entry.Body {
		t.Fatalf("loaded.Body = %q, want %q", loaded.Body, entry.Body)
	}
	if loaded.Source != entry.Source {
		t.Fatalf("loaded.Source = %q, want %q", loaded.Source, entry.Source)
	}
}

func TestRepositoryListMetadataIgnoresExternalDeleteWhenSQLiteExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())

	_, err := repo.Add(AddEntry{Depth: 1, Title: "Delete me", Body: "temporary entry"})
	if err != nil {
		t.Fatalf("repo.Add() error = %v", err)
	}

	entries, err := repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("repo.ListMetadata(before delete) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	snapshot := readSnapshotFile(t, storePath)
	snapshot.Entries = []Entry{}
	writeSnapshotFile(t, storePath, snapshot)

	entries, err = repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("repo.ListMetadata(after delete) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1 because SQLite metadata stays authoritative", len(entries))
	}
	if entries[0].Title != "Delete me" {
		t.Fatalf("entries[0].Title = %q, want %q", entries[0].Title, "Delete me")
	}
}

func TestRepositoryExternalizesAndDeduplicatesSourceBlobs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	sharedSource := "Original conversation transcript with a unique trailer phrase that should never stay inline in store json."

	first, err := repo.Add(AddEntry{Depth: 1, Title: "Session one", Body: "chunk one", Source: sharedSource, Origin: "session-1.md"})
	if err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	second, err := repo.Add(AddEntry{Depth: 1, Title: "Session two", Body: "chunk two", Source: sharedSource, Origin: "session-2.md"})
	if err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	if first.SourceRef == "" || second.SourceRef == "" {
		t.Fatalf("expected source refs, got first=%q second=%q", first.SourceRef, second.SourceRef)
	}
	if first.SourceRef != second.SourceRef {
		t.Fatalf("expected shared source ref, got first=%q second=%q", first.SourceRef, second.SourceRef)
	}

	metadataEntries, err := repo.ListMetadata(Query{Limit: 0})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(metadataEntries) != 2 {
		t.Fatalf("len(metadataEntries) = %d, want 2", len(metadataEntries))
	}
	for _, entry := range metadataEntries {
		if entry.Source != "" {
			t.Fatalf("ListMetadata() returned inline source for entry %d", entry.ID)
		}
		if entry.SourceRef == "" {
			t.Fatalf("ListMetadata() missing source ref for entry %d", entry.ID)
		}
	}

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store) error = %v", err)
	}

	repo = NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	rawStore, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	if !strings.Contains(string(rawStore), "source_ref") {
		t.Fatal("store.json should contain source_ref")
	}
	if strings.Contains(string(rawStore), "unique trailer phrase") {
		t.Fatal("store.json should not persist full shared source inline")
	}

	sourceFiles := 0
	err = filepath.WalkDir(filepath.Join(root, "sources"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			sourceFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(sources) error = %v", err)
	}
	if sourceFiles != 1 {
		t.Fatalf("sourceFiles = %d, want 1 deduplicated blob", sourceFiles)
	}

	storedFirst, ok, err := repo.Get(first.ID)
	if err != nil {
		t.Fatalf("Get(first) error = %v", err)
	}
	if !ok {
		t.Fatal("Get(first) did not find entry")
	}
	if storedFirst.Source != sharedSource {
		t.Fatalf("storedFirst.Source = %q, want shared source", storedFirst.Source)
	}
}

func TestRepositoryListMetadataOmitsHydratedSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	fullSource := "Original conversation transcript with a unique sentence that should be hydrated from the source blob."

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Hydration test",
		Body:   "short summary",
		Source: fullSource,
		Origin: "manual",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, indexPath)

	metadataPath := filepath.Join(root, "store.db")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(store.db) error = %v", err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())

	entries, err := repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	stored := entries[0]
	if stored.ID != entry.ID {
		t.Fatalf("stored.ID = %d, want %d", stored.ID, entry.ID)
	}
	if stored.Source != "" {
		t.Fatalf("stored.Source = %q, want empty source in metadata listing", stored.Source)
	}
	if stored.SourceRef == "" {
		t.Fatal("stored.SourceRef is empty, want populated source ref")
	}
	if stored.SourceRef != entry.SourceRef {
		t.Fatalf("stored.SourceRef = %q, want %q", stored.SourceRef, entry.SourceRef)
	}
}

func TestRepositoryGetHydratesSourceFromSQLiteMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	fullSource := "Original conversation transcript with a second unique sentence that should come back from hydrated SQLite metadata."

	entry, err := repo.Add(AddEntry{
		Depth:  1,
		Title:  "Hydration get test",
		Body:   "short summary",
		Source: fullSource,
		Origin: "manual",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	rebuildJSONMirrorForTest(t, storePath, indexPath)

	metadataPath := filepath.Join(root, "store.db")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(store.db) error = %v", err)
	}

	repo = NewRepository(storePath, indexPath, fakeembed.Provider())

	metadataEntries, err := repo.ListMetadata(Query{Limit: 10})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(metadataEntries) != 1 {
		t.Fatalf("len(metadataEntries) = %d, want 1", len(metadataEntries))
	}
	if metadataEntries[0].Source != "" {
		t.Fatalf("metadataEntries[0].Source = %q, want empty source before hydration", metadataEntries[0].Source)
	}

	stored, ok, err := repo.Get(entry.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() did not find entry")
	}
	if stored.Source != fullSource {
		t.Fatalf("stored.Source = %q, want %q", stored.Source, fullSource)
	}
	if stored.SourceRef == "" {
		t.Fatal("stored.SourceRef is empty, want populated source ref")
	}
}

func overwriteSnapshotWithEmptyEntries(t *testing.T, storePath string) {
	t.Helper()

	writeSnapshotFile(t, storePath, Snapshot{
		Version: currentVersion,
		NextID:  1,
		Entries: []Entry{},
	})
}

func readSnapshotFile(t testing.TB, storePath string) Snapshot {
	t.Helper()

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("Unmarshal(store) error = %v", err)
	}
	return snapshot
}

func writeSnapshotFile(t testing.TB, storePath string, snapshot Snapshot) {
	t.Helper()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(snapshot) error = %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(store) error = %v", err)
	}
}

type mirrorFileState struct {
	data []byte
}

func snapshotMirrorFileState(t testing.TB, storePath string) mirrorFileState {
	t.Helper()

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	return mirrorFileState{
		data: data,
	}
}

func mirrorFileStateChanged(storePath string, before mirrorFileState) error {
	afterData, err := os.ReadFile(storePath)
	if err != nil {
		return err
	}
	if !bytes.Equal(afterData, before.data) {
		return errors.New("mirror content changed")
	}
	return nil
}

func assertMirrorFileStateUnchanged(t testing.TB, storePath string, before mirrorFileState, mutation string) {
	t.Helper()

	if err := mirrorFileStateChanged(storePath, before); err != nil {
		t.Fatalf("store.json changed during %s mutation: %v", mutation, err)
	}
}

func poisonMirrorWithSentinelSnapshot(t testing.TB, storePath string) {
	t.Helper()

	writeSnapshotFile(t, storePath, Snapshot{
		Version: currentVersion,
		NextID:  777,
		Entries: []Entry{{
			ID:        404,
			Depth:     9,
			Title:     "Sentinel mirror drift",
			Body:      "This mirror content must remain untouched by hot mutations.",
			Source:    "sentinel source",
			CreatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
			UpdatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
		}},
	})
}

func TestAssertMirrorFileStateUnchangedDetectsChangedMirrorContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	writeSnapshotFile(t, storePath, Snapshot{Version: currentVersion, NextID: 1})

	before := snapshotMirrorFileState(t, storePath)
	poisonMirrorWithSentinelSnapshot(t, storePath)

	if err := mirrorFileStateChanged(storePath, before); err == nil {
		t.Fatal("mirrorFileStateChanged() error = nil, want rewrite detection for changed mirror content")
	}
}

func TestRepositoryLoadsLegacyInlineSourceAndPersistsRefOnMirrorRebuild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storePath := filepath.Join(root, "store.json")
	legacySource := "Legacy inline source with a unique phrase that should move into a blob on the next save."
	snapshot := Snapshot{
		Version: currentVersion,
		NextID:  2,
		Entries: []Entry{{
			ID:        1,
			Depth:     1,
			Title:     "Legacy",
			Body:      "legacy chunk",
			Source:    legacySource,
			Origin:    "legacy.md",
			CreatedAt: time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		}},
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(snapshot) error = %v", err)
	}
	if err := os.WriteFile(storePath, data, 0o600); err != nil {
		t.Fatalf("WriteFile(store) error = %v", err)
	}

	repo := NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	loaded, ok, err := repo.Get(1)
	if err != nil {
		t.Fatalf("Get(legacy) error = %v", err)
	}
	if !ok {
		t.Fatal("Get(legacy) did not find entry")
	}
	if loaded.Source != legacySource {
		t.Fatalf("loaded.Source = %q, want legacy source", loaded.Source)
	}
	if loaded.SourceRef == "" {
		t.Fatal("loaded entry should have a source ref after migration load")
	}

	if _, err := repo.Add(AddEntry{Depth: 1, Title: "Fresh", Body: "fresh body"}); err != nil {
		t.Fatalf("Add(fresh) error = %v", err)
	}

	if err := os.Remove(storePath); err != nil {
		t.Fatalf("Remove(store) error = %v", err)
	}

	repo = NewRepository(storePath, filepath.Join(root, "vector"), fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	rawStore, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile(store) error = %v", err)
	}
	if !strings.Contains(string(rawStore), "source_ref") {
		t.Fatal("migrated store.json should contain source_ref")
	}
	if strings.Contains(string(rawStore), "unique phrase that should move into a blob") {
		t.Fatal("migrated store.json should not keep legacy source inline")
	}
}
