package kg

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAddTrimsValuesAndDefaultsValidFrom(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	store.now = func() time.Time {
		return time.Date(2026, 4, 11, 15, 0, 0, 0, time.UTC)
	}

	fact, err := store.Add("  repo  ", "  default_branch  ", "  main  ", "", "  entry:1  ")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if fact.Subject != "repo" || fact.Predicate != "default_branch" || fact.Object != "main" {
		t.Fatalf("fact = %+v, want trimmed subject/predicate/object", fact)
	}
	if fact.ValidFrom != "2026-04-11" {
		t.Fatalf("fact.ValidFrom = %q, want 2026-04-11", fact.ValidFrom)
	}
	if fact.Source != "entry:1" {
		t.Fatalf("fact.Source = %q, want entry:1", fact.Source)
	}
	loaded, err := store.Query("repo", "", "outgoing")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != fact.ID {
		t.Fatalf("loaded = %+v, want persisted fact", loaded)
	}
}

func TestStoreQueryReturnsCurrentFactsByDefaultAndHistoricalFactsWithAsOf(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Add("caroline", "lives_in", "new york", "2024-01-01", "entry:1"); err != nil {
		t.Fatalf("Add(old) error = %v", err)
	}
	if err := store.Invalidate("caroline", "lives_in", "new york", "2025-12-31"); err != nil {
		t.Fatalf("Invalidate(old) error = %v", err)
	}
	if _, err := store.Add("caroline", "lives_in", "san francisco", "2026-01-01", "entry:2"); err != nil {
		t.Fatalf("Add(current) error = %v", err)
	}

	current, err := store.Query("caroline", "", "outgoing")
	if err != nil {
		t.Fatalf("Query(current) error = %v", err)
	}
	if len(current) != 1 || current[0].Object != "san francisco" {
		t.Fatalf("current = %+v, want current fact only", current)
	}

	historical, err := store.Query("caroline", "2025-06-01", "outgoing")
	if err != nil {
		t.Fatalf("Query(historical) error = %v", err)
	}
	if len(historical) != 1 || historical[0].Object != "new york" {
		t.Fatalf("historical = %+v, want historical fact", historical)
	}

	incoming, err := store.Query("new york", "2025-06-01", "incoming")
	if err != nil {
		t.Fatalf("Query(incoming) error = %v", err)
	}
	if len(incoming) != 1 || incoming[0].Subject != "caroline" {
		t.Fatalf("incoming = %+v, want incoming historical fact", incoming)
	}

	both, err := store.Query("san francisco", "", "both")
	if err != nil {
		t.Fatalf("Query(both) error = %v", err)
	}
	if len(both) != 1 || both[0].Subject != "caroline" {
		t.Fatalf("both = %+v, want current incoming fact", both)
	}
}

func TestStoreInvalidateOnlyTouchesMatchingOpenFacts(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	store.now = func() time.Time {
		return time.Date(2026, 4, 11, 15, 0, 0, 0, time.UTC)
	}
	if _, err := store.Add("repo", "default_branch", "main", "2024-01-01", "entry:1"); err != nil {
		t.Fatalf("Add(main) error = %v", err)
	}
	if _, err := store.Add("repo", "default_branch", "develop", "2023-01-01", "entry:0"); err != nil {
		t.Fatalf("Add(develop) error = %v", err)
	}
	if err := store.Invalidate(" repo ", " default_branch ", " main ", ""); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}

	timeline, err := store.Timeline("repo")
	if err != nil {
		t.Fatalf("Timeline() error = %v", err)
	}
	if len(timeline) != 2 {
		t.Fatalf("len(timeline) = %d, want 2", len(timeline))
	}
	if timeline[1].Object != "main" || timeline[1].ValidTo != "2026-04-11" {
		t.Fatalf("timeline[1] = %+v, want invalidated main branch fact", timeline[1])
	}
	current, err := store.Query("repo", "", "outgoing")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(current) != 1 || current[0].Object != "develop" {
		t.Fatalf("current = %+v, want still-open develop fact", current)
	}
}

func TestStoreTimelineAndStatsReflectCurrentAndExpiredFacts(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, err := store.Add("caroline", "attended", "support group", "2023-05-07", "entry:1"); err != nil {
		t.Fatalf("Add(attended) error = %v", err)
	}
	if _, err := store.Add("caroline", "lives_in", "new york", "2024-01-01", "entry:2"); err != nil {
		t.Fatalf("Add(new york) error = %v", err)
	}
	if err := store.Invalidate("caroline", "lives_in", "new york", "2025-12-31"); err != nil {
		t.Fatalf("Invalidate(new york) error = %v", err)
	}
	if _, err := store.Add("caroline", "lives_in", "san francisco", "2026-01-01", "entry:3"); err != nil {
		t.Fatalf("Add(san francisco) error = %v", err)
	}

	timeline, err := store.Timeline("caroline")
	if err != nil {
		t.Fatalf("Timeline(caroline) error = %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("len(timeline) = %d, want 3", len(timeline))
	}
	if timeline[0].Predicate != "attended" || timeline[1].Object != "new york" || timeline[2].Object != "san francisco" {
		t.Fatalf("timeline = %+v, want chronological order", timeline)
	}

	all, err := store.Timeline("")
	if err != nil {
		t.Fatalf("Timeline(all) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats["facts"].(int) != 3 || stats["current"].(int) != 2 || stats["expired"].(int) != 1 {
		t.Fatalf("stats = %+v, want facts=3 current=2 expired=1", stats)
	}
	predicates := stats["predicates"].(map[string]int)
	if predicates["lives_in"] != 2 || predicates["attended"] != 1 {
		t.Fatalf("predicates = %+v, want lives_in=2 attended=1", predicates)
	}
	if stats["entities"].(int) != 4 {
		t.Fatalf("stats[entities] = %v, want 4", stats["entities"])
	}
}

func TestFactValidAtBoundaries(t *testing.T) {
	t.Parallel()

	fact := Fact{Subject: "repo", Predicate: "default_branch", Object: "main", ValidFrom: "2024-01-01", ValidTo: "2025-12-31"}
	if factValidAt(fact, "") {
		t.Fatal("expected expired fact to be excluded from current queries")
	}
	if !factValidAt(fact, "2024-01-01") {
		t.Fatal("expected fact to be valid on valid_from boundary")
	}
	if !factValidAt(fact, "2025-12-31") {
		t.Fatal("expected fact to be valid on valid_to boundary")
	}
	if factValidAt(fact, "2026-01-01") {
		t.Fatal("expected fact to be invalid after valid_to")
	}
	if factValidAt(fact, "2023-12-31") {
		t.Fatal("expected fact to be invalid before valid_from")
	}
}

func TestMatchesDirectionTrimsAndFilters(t *testing.T) {
	t.Parallel()

	fact := Fact{Subject: "repo", Predicate: "default_branch", Object: "main"}
	if !matchesDirection(fact, "repo", " outgoing ") {
		t.Fatal("expected outgoing direction to match subject")
	}
	if !matchesDirection(fact, "main", " incoming ") {
		t.Fatal("expected incoming direction to match object")
	}
	if matchesDirection(fact, "repo", "incoming") {
		t.Fatal("expected incoming direction to exclude subject-only match")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "knowledge.json"))
}
