package daemon

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/codysnider/tagmem/internal/store"
)

func TestBackendRejectsNonIntegerRequiredNumericPayload(t *testing.T) {
	_, _, backend := newTestBackend(t)

	response := backend.Handle(context.Background(), Request{
		ID:      "req-1",
		Command: "show_entry",
		Payload: map[string]any{"id": 1.5},
	})

	if response.Success {
		t.Fatal("response.Success = true, want false")
	}
	if response.Error != "id must be an integer" {
		t.Fatalf("response.Error = %q, want %q", response.Error, "id must be an integer")
	}
}

func TestBackendRejectsNonIntegerOptionalNumericPayload(t *testing.T) {
	_, _, backend := newTestBackend(t)

	response := backend.Handle(context.Background(), Request{
		ID:      "req-2",
		Command: "list_entries",
		Payload: map[string]any{"limit": 2.5},
	})

	if response.Success {
		t.Fatal("response.Success = true, want false")
	}
	if response.Error != "limit must be an integer" {
		t.Fatalf("response.Error = %q, want %q", response.Error, "limit must be an integer")
	}
}

func TestBackendRejectsOutOfRangeIntegerNumericPayload(t *testing.T) {
	_, _, backend := newTestBackend(t)

	response := backend.Handle(context.Background(), Request{
		ID:      "req-3",
		Command: "show_entry",
		Payload: map[string]any{"id": float64(maxSafeTestInt()) + 2},
	})

	if response.Success {
		t.Fatal("response.Success = true, want false")
	}
	if response.Error != "id must be within Go int range" {
		t.Fatalf("response.Error = %q, want %q", response.Error, "id must be within Go int range")
	}
}

func maxSafeTestInt() int64 {
	if strconvIntSize == 32 {
		return math.MaxInt32
	}
	return math.MaxInt64
}

func TestBackendEnsureCorpusPreservesDocumentTimestamps(t *testing.T) {
	_, _, backend := newTestBackend(t)

	response := backend.Handle(context.Background(), Request{
		ID:      "req-corpus-timestamps",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "timestamped-corpus",
			"documents": []map[string]any{{
				"id":         "session-alpha",
				"content":    "> User: remind me about the blue backpack\nAssistant: the blue backpack is in the hall closet",
				"mode":       "conversations",
				"extract":    "exchange",
				"depth":      1,
				"created_at": "2024-01-02T00:00:00Z",
				"updated_at": "2024-01-03T00:00:00Z",
			}},
		},
	})
	if !response.Success {
		t.Fatalf("ensure_corpus success = false, error = %q", response.Error)
	}

	backend.corpusMu.RLock()
	entry := backend.corpusCache["timestamped-corpus"]
	backend.corpusMu.RUnlock()
	repo := testInterfaceCorpusRepo(entry.corpus)
	if entry.corpus == nil || repo == nil {
		t.Fatal("cached corpus repo = nil, want populated corpus")
	}

	entries, err := repo.ListMetadata(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("ListMetadata() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("ListMetadata() returned no entries, want timestamped corpus entries")
	}
	createdAt := entries[0].CreatedAt.UTC().Format(time.RFC3339)
	updatedAt := entries[0].UpdatedAt.UTC().Format(time.RFC3339)
	if createdAt != "2024-01-02T00:00:00Z" {
		t.Fatalf("entries[0].CreatedAt = %q, want %q", createdAt, "2024-01-02T00:00:00Z")
	}
	if updatedAt != "2024-01-03T00:00:00Z" {
		t.Fatalf("entries[0].UpdatedAt = %q, want %q", updatedAt, "2024-01-03T00:00:00Z")
	}
}

func testInterfaceCorpusRepo(corpus any) *store.Repository {
	if corpus == nil {
		return nil
	}
	value := reflect.ValueOf(corpus)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return nil
	}
	field := value.Elem().FieldByName("repo")
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	return *(**store.Repository)(unsafe.Pointer(field.UnsafeAddr()))
}

const strconvIntSize = 32 << (^uint(0) >> 63)
