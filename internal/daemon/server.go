package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Server struct {
	socketPath string
	backend    *Backend
}

func NewServer(socketPath string, backend *Backend) *Server {
	return &Server{socketPath: socketPath, backend: backend}
}

func (s *Server) Run(ctx context.Context) error {
	if s.backend == nil {
		return fmt.Errorf("daemon backend is required")
	}

	listener, err := s.listen()
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(s.socketPath)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept daemon connection: %w", err)
		}

		go s.handleConnection(ctx, conn)
	}
}

func (s *Server) listen() (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create daemon socket directory: %w", err)
	}

	if err := removeSocketFile(s.socketPath); err != nil {
		return nil, err
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on daemon socket: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(s.socketPath)
		return nil, fmt.Errorf("set daemon socket permissions: %w", err)
	}
	return listener, nil
}

func removeSocketFile(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat daemon socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("daemon socket path %q exists and is not a socket", socketPath)
	}
	inUse, err := socketInUse(socketPath)
	if err != nil {
		return err
	}
	if inUse {
		return fmt.Errorf("daemon socket already in use")
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
}

func socketInUse(socketPath string) (bool, error) {
	conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) || errors.Is(opErr.Err, syscall.ENOENT) {
			return false, nil
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return false, nil
	}

	return false, fmt.Errorf("probe daemon socket: %w", err)
}

func (s *Server) handleConnection(parent context.Context, conn net.Conn) {
	defer conn.Close()

	request, err := ReadRequest(conn)
	if err != nil {
		_ = WriteResponse(conn, Response{Success: false, Error: err.Error()})
		return
	}

	requestCtx, cancel := context.WithCancel(parent)
	defer cancel()
	stopWatching := watchConnectionContext(conn, cancel)
	defer stopWatching()

	response := s.backend.Handle(requestCtx, request)
	_ = WriteResponse(conn, response)
}

func watchConnectionContext(conn net.Conn, cancel context.CancelFunc) func() {
	stopped := make(chan struct{})
	go func() {
		buffer := make([]byte, 1)
		_, err := conn.Read(buffer)
		select {
		case <-stopped:
			return
		default:
		}
		if err != nil {
			cancel()
			return
		}
		cancel()
	}()

	return func() {
		close(stopped)
		_ = conn.SetReadDeadline(time.Unix(0, 1))
	}
}
