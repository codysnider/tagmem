package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/vector"
)

func TestRepositoryAddAndGet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
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
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())

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

func TestRepositorySearchOrdersByScore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
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
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
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

func TestRepositorySearchTrimsLowSignalTail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
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
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
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
