package daemon

import (
	"context"
	"fmt"
	"net"
	"time"
)

func Call(ctx context.Context, socketPath string, request Request) (Response, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("dial daemon socket: %w", err)
	}
	defer conn.Close()

	stopContextWatch := watchContext(ctx, conn)
	defer stopContextWatch()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return Response{}, fmt.Errorf("set daemon connection deadline: %w", err)
		}
	}

	if err := WriteRequest(conn, request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, err
	}

	response, err := ReadResponse(conn)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, err
	}
	if response.ID == "" {
		return Response{}, fmt.Errorf("daemon response missing id")
	}
	if response.ID != request.ID {
		return Response{}, fmt.Errorf("daemon response id mismatch: got %q want %q", response.ID, request.ID)
	}

	return response, nil
}

func watchContext(ctx context.Context, conn net.Conn) func() {
	if ctx.Done() == nil {
		return func() {}
	}

	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Unix(0, 1))
		case <-stopped:
		}
	}()

	return func() {
		close(stopped)
	}
}
