//go:build linux && tagmem_onnx

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func TestAppDaemonHotPathWithEmbeddedONNXProvider(t *testing.T) {
	root := t.TempDir()
	originalEnv := os.Environ()
	restoreEnv(t, originalEnv)
	daemonRoot := filepath.Join(root, "daemon")
	localRoot := filepath.Join(root, "local")
	runtimeRoot := filepath.Join(root, ".runtime")

	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=" + runtimeRoot,
		"TAGMEM_DATA_ROOT=" + filepath.Join(localRoot, "data"),
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(localRoot, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(localRoot, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_EMBED_ACCEL=cpu",
		"TAGMEM_USE_DAEMON=1",
	}
	for _, value := range env {
		parts := strings.SplitN(value, "=", 2)
		if err := os.Setenv(parts[0], parts[1]); err != nil {
			t.Fatalf("Setenv(%s) error = %v", parts[0], err)
		}
	}

	daemonPaths := xdg.Paths{
		AppName:    "tagmem",
		ConfigDir:  filepath.Join(daemonRoot, "config"),
		DataDir:    filepath.Join(daemonRoot, "data"),
		CacheDir:   filepath.Join(daemonRoot, "cache"),
		SocketPath: filepath.Join(runtimeRoot, "tagmem", "tagmem.sock"),
		IndexDir:   filepath.Join(daemonRoot, "data", "vector"),
		ModelDir:   filepath.Join(daemonRoot, "data", "models"),
		DiaryDir:   filepath.Join(daemonRoot, "data", "diaries"),
		StorePath:  filepath.Join(daemonRoot, "data", "store.json"),
		KGPath:     filepath.Join(daemonRoot, "data", "knowledge.json"),
	}
	if err := daemonPaths.Ensure(); err != nil {
		t.Fatalf("daemonPaths.Ensure() error = %v", err)
	}

	provider, err := vector.ProviderFromEnv(daemonPaths)
	if err != nil {
		t.Fatalf("vector.ProviderFromEnv() error = %v", err)
	}

	repo := store.NewRepository(daemonPaths.StorePath, provider.IndexPath(daemonPaths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}

	localPaths, err := xdg.Resolve("tagmem")
	if err != nil {
		t.Fatalf("xdg.Resolve(local) error = %v", err)
	}
	if err := localPaths.Ensure(); err != nil {
		t.Fatalf("localPaths.Ensure() error = %v", err)
	}
	localRepo := store.NewRepository(localPaths.StorePath, provider.IndexPath(localPaths.IndexDir), provider)
	if err := localRepo.Init(); err != nil {
		t.Fatalf("localRepo.Init() error = %v", err)
	}
	localEntry, err := localRepo.Add(store.AddEntry{Depth: 1, Title: "Local direct result", Body: "shared daemon query marker"})
	if err != nil {
		t.Fatalf("localRepo.Add() error = %v", err)
	}

	backend := daemon.NewBackend(repo, kg.New(daemonPaths.KGPath), diary.New(daemonPaths.DiaryDir), daemonPaths, provider)
	server := daemon.NewServer(daemonPaths.SocketPath, backend)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	waitForDaemonProbe(t, daemonPaths.SocketPath)
	defer func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("server.Run() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("daemon server did not stop")
		}
	}()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := New(stdout, stderr)
	if code := app.Run([]string{"add", "--depth", "1", "--title", "ONNX daemon add", "--body", "shared daemon query marker"}); code != 0 {
		t.Fatalf("add exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stdout.String() != "added entry 1 at depth 1\n" {
		t.Fatalf("add stdout = %q, want exact preserved success output", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("add stderr = %q, want empty stderr", stderr.String())
	}

	daemonEntries, err := repo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("repo.List() error = %v", err)
	}
	if len(daemonEntries) != 1 {
		t.Fatalf("len(daemonEntries) = %d, want 1", len(daemonEntries))
	}
	daemonEntry := daemonEntries[0]
	if daemonEntry.Title != "ONNX daemon add" {
		t.Fatalf("daemonEntry.Title = %q, want %q", daemonEntry.Title, "ONNX daemon add")
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run([]string{"search", "shared daemon query marker"}); code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	wantSearch := formatSearchResultLine(store.SearchResult{Entry: daemonEntry}, false) + "\n"
	if stdout.String() != wantSearch {
		t.Fatalf("search stdout = %q, want %q", stdout.String(), wantSearch)
	}
	if stderr.Len() != 0 {
		t.Fatalf("search stderr = %q, want empty stderr", stderr.String())
	}

	localEntries, err := localRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("localRepo.List() error = %v", err)
	}
	if len(localEntries) != 1 {
		t.Fatalf("len(localEntries) = %d, want 1 local-only entry", len(localEntries))
	}
	if localEntries[0].ID != localEntry.ID || localEntries[0].Title != localEntry.Title {
		t.Fatalf("localEntries[0] = %#v, want seeded local entry %#v", localEntries[0], localEntry)
	}
}
