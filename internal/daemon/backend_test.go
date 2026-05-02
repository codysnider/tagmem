package daemon

import (
	"context"
	"math"
	"testing"
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

const strconvIntSize = 32 << (^uint(0) >> 63)
