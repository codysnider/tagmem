package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/xdg"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestShouldUseDaemonMCPBackendRequiresServingSocket(t *testing.T) {
	t.Setenv("TAGMEM_MCP_USE_DAEMON", "")

	root := t.TempDir()
	socketPath := filepath.Join(root, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	_ = listener.Close()

	if shouldUseDaemonMCPBackend(socketPath) {
		t.Fatal("shouldUseDaemonMCPBackend() = true, want false for stale socket")
	}
}

func TestShouldUseDaemonMCPBackendUsesServingSocket(t *testing.T) {
	t.Setenv("TAGMEM_MCP_USE_DAEMON", "")
	paths, stop := startTestDaemon(t)
	defer stop()

	if !shouldUseDaemonMCPBackend(paths.SocketPath) {
		t.Fatal("shouldUseDaemonMCPBackend() = false, want true for live daemon")
	}
}

func TestShouldUseDaemonMCPBackendEnvStillRequiresLiveDaemon(t *testing.T) {
	t.Setenv("TAGMEM_MCP_USE_DAEMON", "1")

	root := t.TempDir()
	socketPath := filepath.Join(root, "missing.sock")
	if shouldUseDaemonMCPBackend(socketPath) {
		t.Fatal("shouldUseDaemonMCPBackend() = true, want false when env is set but daemon is unavailable")
	}
}

func TestAppMCPDaemonRequiredFailsWithoutFallback(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	xdgConfig := filepath.Join(root, ".config")
	xdgData := filepath.Join(root, ".local", "share")
	xdgCache := filepath.Join(root, ".cache")
	if err := os.MkdirAll(filepath.Join(xdgConfig, "tagmem"), 0o755); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}

	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + xdgConfig,
		"XDG_DATA_HOME=" + xdgData,
		"XDG_CACHE_HOME=" + xdgCache,
		"TAGMEM_DATA_ROOT=",
		"TAGMEM_CONFIG_ROOT=",
		"TAGMEM_CACHE_ROOT=",
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_MCP_USE_DAEMON=1",
	}

	stdinFile, err := os.CreateTemp(t.TempDir(), "stdin-*.json")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer stdinFile.Close()

	originalStdin := os.Stdin
	os.Stdin = stdinFile
	t.Cleanup(func() {
		os.Stdin = originalStdin
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := New(stdout, stderr)
	originalEnv := os.Environ()
	restoreEnv(t, originalEnv)
	for _, value := range env {
		parts := strings.SplitN(value, "=", 2)
		if err := os.Setenv(parts[0], parts[1]); err != nil {
			t.Fatalf("Setenv(%s) error = %v", parts[0], err)
		}
	}

	code := app.Run([]string{"mcp"})
	if code != 1 {
		t.Fatalf("app.Run(mcp) exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "daemon-backed MCP mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want daemon-required failure", stderr.String())
	}

	paths, err := xdg.Resolve("tagmem")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, err := os.Stat(paths.StorePath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want store to remain uninitialized", paths.StorePath, err)
	}
}

func TestAppMCPUsesLiveDaemonBackend(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	appDataDir := filepath.Join(root, "app-data")
	appConfigDir := filepath.Join(root, "app-config")
	appCacheDir := filepath.Join(root, "app-cache")
	daemonRoot := filepath.Join(root, "daemon")

	daemonPaths := xdg.Paths{
		ConfigDir:  filepath.Join(daemonRoot, "config"),
		DataDir:    filepath.Join(daemonRoot, "data"),
		CacheDir:   filepath.Join(daemonRoot, "cache"),
		SocketPath: filepath.Join(runtimeDir, "tagmem", "tagmem.sock"),
		IndexDir:   filepath.Join(daemonRoot, "data", "vector"),
		ModelDir:   filepath.Join(daemonRoot, "data", "models"),
		DiaryDir:   filepath.Join(daemonRoot, "data", "diaries"),
		StorePath:  filepath.Join(daemonRoot, "data", "store.json"),
		KGPath:     filepath.Join(daemonRoot, "data", "knowledge.json"),
	}
	if err := daemonPaths.Ensure(); err != nil {
		t.Fatalf("daemonPaths.Ensure() error = %v", err)
	}
	repo := store.NewRepository(daemonPaths.StorePath, fakeembed.Provider().IndexPath(daemonPaths.IndexDir), fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}
	if _, err := repo.Add(store.AddEntry{Depth: 1, Title: "daemon-backed entry", Body: "served through live daemon"}); err != nil {
		t.Fatalf("repo.Add() error = %v", err)
	}
	backend := daemon.NewBackend(repo, kg.New(daemonPaths.KGPath), diary.New(daemonPaths.DiaryDir), daemonPaths, fakeembed.Provider())
	server := daemon.NewServer(daemonPaths.SocketPath, backend)
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()
	waitForDaemonProbe(t, daemonPaths.SocketPath)
	defer func() {
		cancelServer()
		select {
		case <-serverErr:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon server did not stop")
		}
	}()

	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"TAGMEM_DATA_ROOT=" + appDataDir,
		"TAGMEM_CONFIG_ROOT=" + appConfigDir,
		"TAGMEM_CACHE_ROOT=" + appCacheDir,
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_MCP_USE_DAEMON=",
	}

	stdinReader, stdinWriter, stdoutReader, stdoutWriter := swapMCPStdio(t)
	defer stdinReader.Close()
	defer stdoutReader.Close()

	stderr := &bytes.Buffer{}
	app := New(io.Discard, stderr)
	originalEnv := os.Environ()
	restoreEnv(t, originalEnv)
	for _, value := range env {
		parts := strings.SplitN(value, "=", 2)
		if err := os.Setenv(parts[0], parts[1]); err != nil {
			t.Fatalf("Setenv(%s) error = %v", parts[0], err)
		}
	}

	appDone := make(chan int, 1)
	go func() {
		appDone <- app.Run([]string{"mcp"})
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	clientCtx, cancelClient := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClient()
	session, err := client.Connect(clientCtx, &sdk.IOTransport{Reader: stdoutReader, Writer: stdinWriter}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(clientCtx, &sdk.CallToolParams{Name: "tagmem_status"})
	if err != nil {
		t.Fatalf("CallTool(tagmem_status) error = %v", err)
	}
	if res.IsError {
		payload, _ := json.Marshal(res.Content)
		t.Fatalf("CallTool(tagmem_status) returned error content: %s", string(payload))
	}

	var status struct {
		TotalEntries float64 `json:"total_entries"`
		StorePath    string  `json:"store_path"`
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("Marshal structured content error = %v", err)
	}
	if err := json.Unmarshal(structured, &status); err != nil {
		t.Fatalf("Unmarshal structured content error = %v", err)
	}
	if status.StorePath != daemonPaths.StorePath {
		t.Fatalf("status.StorePath = %q, want daemon store %q", status.StorePath, daemonPaths.StorePath)
	}
	if status.TotalEntries != 1 {
		t.Fatalf("status.TotalEntries = %v, want 1 from daemon-backed store", status.TotalEntries)
	}

	appPaths, err := xdg.Resolve("tagmem")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if appPaths.StorePath == daemonPaths.StorePath {
		t.Fatalf("app store path = %q, want different local path from daemon store", appPaths.StorePath)
	}
	if _, err := os.Stat(appPaths.StorePath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want local store to remain unused", appPaths.StorePath, err)
	}

	_ = session.Close()
	_ = stdinWriter.Close()
	_ = stdoutWriter.Close()

	select {
	case code := <-appDone:
		if code != 0 {
			t.Fatalf("app.Run(mcp) exit = %d, want 0; stderr=%q", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app.Run(mcp) did not stop after MCP client disconnect")
	}
}

func swapMCPStdio(t *testing.T) (stdinReader *os.File, stdinWriter *os.File, stdoutReader *os.File, stdoutWriter *os.File) {
	t.Helper()

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdin) error = %v", err)
	}
	stdoutReader, stdoutWriter, err = os.Pipe()
	if err != nil {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		t.Fatalf("os.Pipe(stdout) error = %v", err)
	}

	originalStdin := os.Stdin
	originalStdout := os.Stdout
	os.Stdin = stdinReader
	os.Stdout = stdoutWriter
	t.Cleanup(func() {
		os.Stdin = originalStdin
		os.Stdout = originalStdout
	})

	return stdinReader, stdinWriter, stdoutReader, stdoutWriter
}

func startTestDaemon(t *testing.T) (xdg.Paths, func()) {
	t.Helper()

	provider := fakeembed.Provider()
	root := t.TempDir()
	paths := xdg.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
		SocketPath: filepath.Join(root, "runtime", "tagmem.sock"),
		IndexDir:   filepath.Join(root, "data", "vector"),
		ModelDir:   filepath.Join(root, "data", "models"),
		DiaryDir:   filepath.Join(root, "data", "diaries"),
		StorePath:  filepath.Join(root, "data", "store.json"),
		KGPath:     filepath.Join(root, "data", "knowledge.json"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}

	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}
	backend := daemon.NewBackend(repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	server := daemon.NewServer(paths.SocketPath, backend)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	waitForDaemonProbe(t, paths.SocketPath)

	return paths, func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon server did not stop")
		}
	}
}

func waitForDaemonProbe(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if probeDaemonMCPBackend(socketPath) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("daemon socket %q did not become probeable", socketPath)
}
