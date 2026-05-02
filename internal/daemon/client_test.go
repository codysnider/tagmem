package daemon

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCallRoundTripOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	requests := make(chan Request, 1)
	serve := serveOneRequest(t, socketPath, func(conn net.Conn) {
		request, err := ReadRequest(conn)
		if err != nil {
			t.Errorf("ReadRequest() error = %v", err)
			return
		}
		requests <- request

		if err := WriteResponse(conn, Response{
			ID:      request.ID,
			Success: true,
			Payload: map[string]any{"status": "ok"},
		}); err != nil {
			t.Errorf("WriteResponse() error = %v", err)
		}
	})
	defer serve()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	response, err := Call(ctx, socketPath, Request{
		ID:      "req-1",
		Command: "doctor",
		Payload: map[string]any{"depth": 2},
	})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	request := <-requests
	if request.ID != "req-1" {
		t.Fatalf("request.ID = %q, want %q", request.ID, "req-1")
	}
	if request.Command != "doctor" {
		t.Fatalf("request.Command = %q, want %q", request.Command, "doctor")
	}
	if got := request.Payload["depth"]; got != float64(2) {
		t.Fatalf("request.Payload[depth] = %#v, want %v", got, 2)
	}
	if response.ID != "req-1" {
		t.Fatalf("response.ID = %q, want %q", response.ID, "req-1")
	}
	if !response.Success {
		t.Fatal("response.Success = false, want true")
	}
	if got := response.Payload["status"]; got != "ok" {
		t.Fatalf("response.Payload[status] = %#v, want %q", got, "ok")
	}
}

func TestCallRequiresMatchingResponseID(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	serve := serveOneRequest(t, socketPath, func(conn net.Conn) {
		request, err := ReadRequest(conn)
		if err != nil {
			t.Errorf("ReadRequest() error = %v", err)
			return
		}

		if err := WriteResponse(conn, Response{
			ID:      request.ID + "-other",
			Success: true,
		}); err != nil {
			t.Errorf("WriteResponse() error = %v", err)
		}
	})
	defer serve()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := Call(ctx, socketPath, Request{ID: "req-2", Command: "doctor"})
	if err == nil {
		t.Fatal("Call() error = nil, want mismatch error")
	}
}

func TestCallRequiresResponseID(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	serve := serveOneRequest(t, socketPath, func(conn net.Conn) {
		if _, err := ReadRequest(conn); err != nil {
			t.Errorf("ReadRequest() error = %v", err)
			return
		}

		if err := WriteResponse(conn, Response{Success: true}); err != nil {
			t.Errorf("WriteResponse() error = %v", err)
		}
	})
	defer serve()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := Call(ctx, socketPath, Request{ID: "req-3", Command: "doctor"})
	if err == nil {
		t.Fatal("Call() error = nil, want missing id error")
	}
}

func TestCallHonorsContextCancellationWhileWaitingForResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	serverDone := make(chan struct{})
	serve := serveOneRequest(t, socketPath, func(conn net.Conn) {
		defer close(serverDone)
		if _, err := ReadRequest(conn); err != nil {
			t.Errorf("ReadRequest() error = %v", err)
			return
		}

		<-time.After(time.Second)
	})
	defer serve()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := Call(ctx, socketPath, Request{ID: "req-4", Command: "doctor"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call() error = %v, want %v", err, context.DeadlineExceeded)
	}

	select {
	case <-serverDone:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("server handler did not finish")
	}
}

func serveOneRequest(t *testing.T, socketPath string, handler func(net.Conn)) func() {
	t.Helper()

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		handler(conn)
	}()

	return func() {
		_ = listener.Close()
		<-done
	}
}
