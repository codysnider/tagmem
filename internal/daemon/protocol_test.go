package daemon

import (
	"bytes"
	"testing"
)

func TestProtocolRequestRoundTrip(t *testing.T) {
	original := Request{
		ID:      "req-123",
		Command: "doctor",
		Payload: map[string]any{"depth": 2},
	}

	var buffer bytes.Buffer
	if err := WriteRequest(&buffer, original); err != nil {
		t.Fatalf("WriteRequest() error = %v", err)
	}

	decoded, err := ReadRequest(&buffer)
	if err != nil {
		t.Fatalf("ReadRequest() error = %v", err)
	}

	if decoded.ID != original.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Command != original.Command {
		t.Fatalf("Command = %q, want %q", decoded.Command, original.Command)
	}
	if got := decoded.Payload["depth"]; got != float64(2) {
		t.Fatalf("Payload[depth] = %#v, want %v", got, 2)
	}
}

func TestProtocolResponseRoundTrip(t *testing.T) {
	original := Response{
		ID:      "req-456",
		Success: true,
		Payload: map[string]any{"status": "ok"},
	}

	var buffer bytes.Buffer
	if err := WriteResponse(&buffer, original); err != nil {
		t.Fatalf("WriteResponse() error = %v", err)
	}

	decoded, err := ReadResponse(&buffer)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}

	if decoded.ID != original.ID {
		t.Fatalf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if !decoded.Success {
		t.Fatal("Success = false, want true")
	}
	if got := decoded.Payload["status"]; got != "ok" {
		t.Fatalf("Payload[status] = %#v, want %q", got, "ok")
	}
}
