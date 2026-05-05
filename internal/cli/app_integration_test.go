package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func TestAppIngestStatusAndContextFlow(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	projectDir := copyCLIProject(t, root)
	xdgConfig := filepath.Join(root, ".config")
	xdgData := filepath.Join(root, ".local", "share")
	xdgCache := filepath.Join(root, ".cache")
	if err := os.MkdirAll(filepath.Join(xdgConfig, "tagmem"), 0o755); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}
	identity := []byte("You are a local-first memory system for benchmark-derived test fixtures.\n")
	if err := os.WriteFile(filepath.Join(xdgConfig, "tagmem", "identity.txt"), identity, 0o644); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
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
	}

	stdout, stderr, code := runApp(t, env, "ingest", "--mode", "files", "--depth", "1", filepath.Join(projectDir, "notes"))
	if code != 0 {
		t.Fatalf("ingest files exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "entries added:   2") {
		t.Fatalf("ingest files stdout = %q, want entries added line", stdout)
	}

	stdout, stderr, code = runApp(t, env, "ingest", "--mode", "conversations", "--depth", "2", filepath.Join(projectDir, "chats"))
	if code != 0 {
		t.Fatalf("ingest conversations exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "files processed: 1") {
		t.Fatalf("ingest conversations stdout = %q, want processed count", stdout)
	}

	stdout, stderr, code = runApp(t, env, "status")
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "entries:") || !strings.Contains(stdout, "top tags:") {
		t.Fatalf("status stdout = %q, want summary sections", stdout)
	}

	stdout, stderr, code = runApp(t, env, "context", "--limit", "5")
	if code != 0 {
		t.Fatalf("context exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "## Identity") || !strings.Contains(stdout, "Business Administration") {
		t.Fatalf("context stdout = %q, want identity and ingested content", stdout)
	}

	stdout, stderr, code = runApp(t, env, "search", "Business Administration")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "degree.md") {
		t.Fatalf("search stdout = %q, want degree.md result", stdout)
	}
}

func TestAppSearchExplainShowsComputedSignals(t *testing.T) {
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
	}
	tags := "staging,database,config"

	_, stderr, code := runApp(t, append(append([]string{}, env...), "TAGMEM_IMPORT_UPDATED_AT=2026-04-07T10:00:00Z"), "add", "--depth", "1", "--title", "Legacy staging database", "--body", "Staging uses mysql.internal.example.com.", "--tags", tags, "--origin", "docs/legacy.md")
	if code != 0 {
		t.Fatalf("add legacy exit=%d stderr=%s", code, stderr)
	}
	_, stderr, code = runApp(t, append(append([]string{}, env...), "TAGMEM_IMPORT_UPDATED_AT=2026-04-07T11:00:00Z"), "add", "--depth", "1", "--title", "Staging database", "--body", "Staging uses postgres.internal.example.com.", "--tags", tags, "--origin", "manual")
	if code != 0 {
		t.Fatalf("add current exit=%d stderr=%s", code, stderr)
	}
	_, stderr, code = runApp(t, append(append([]string{}, env...), "TAGMEM_IMPORT_UPDATED_AT=2026-04-07T12:00:00Z"), "add", "--depth", "1", "--title", "Staging database confirmation", "--body", "Staging uses postgres.internal.example.com.", "--tags", tags, "--origin", "notes/runbook.md")
	if code != 0 {
		t.Fatalf("add confirmation exit=%d stderr=%s", code, stderr)
	}

	stdout, stderr, code := runApp(t, env, "search", "--explain", "What database does staging use?")
	if code != 0 {
		t.Fatalf("search --explain exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "support=2") || !strings.Contains(stdout, "sources=2") || !strings.Contains(stdout, "conflicts=1") {
		t.Fatalf("search --explain stdout = %q, want computed signal fields", stdout)
	}
	if !strings.Contains(stdout, "Staging database confirmation") {
		t.Fatalf("search --explain stdout = %q, want top staging result", stdout)
	}
}

func TestAppAddProfilingOffDoesNotEmitProfileBlock(t *testing.T) {
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
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Profile off", "--body", "No profile output expected")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if stdout != "added entry 1 at depth 1\n" {
		t.Fatalf("stdout = %q, want unchanged add output", stdout)
	}
	if strings.Contains(stderr, "[profile]") {
		t.Fatalf("stderr = %q, want no profile block when TAGMEM_PROFILE is off", stderr)
	}
}

func TestAppAddProfilingOnEmitsProfileBlock(t *testing.T) {
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
		"TAGMEM_PROFILE=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Profile on", "--body", "Profile output expected")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if stdout != "added entry 1 at depth 1\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}
	if !strings.Contains(stderr, "[profile] command=add") {
		t.Fatalf("stderr = %q, want add profile block", stderr)
	}
}

func TestAppAddProfilingShowsPhaseBreakdown(t *testing.T) {
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
		"TAGMEM_PROFILE=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Profile phases", "--body", "Expect repository phase timings")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if stdout != "added entry 1 at depth 1\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}
	for _, phase := range []string{
		"resolve_paths=",
		"resolve_provider=",
		"repo_init=",
		"add_total=",
		"sqlite_mutation=",
		"vector_mutation=",
	} {
		if !strings.Contains(stderr, phase) {
			t.Fatalf("stderr = %q, want phase %q", stderr, phase)
		}
	}
}

func TestAppSearchProfilingOnEmitsProfileBlock(t *testing.T) {
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
		"TAGMEM_PROFILE=1",
	}

	seedStdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Search profile", "--body", "Search output expected")
	if code != 0 {
		t.Fatalf("seed add exit=%d stderr=%s", code, stderr)
	}
	if seedStdout != "added entry 1 at depth 1\n" {
		t.Fatalf("seed stdout = %q, want preserved add output", seedStdout)
	}

	stdout, stderr, code := runApp(t, env, "search", "Search")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "[1]  depth=1  Search profile") {
		t.Fatalf("stdout = %q, want preserved search result output", stdout)
	}
	if !strings.Contains(stderr, "[profile] command=search") {
		t.Fatalf("stderr = %q, want search profile block", stderr)
	}
}

func TestAppSearchProfilingOffDoesNotEmitProfileBlock(t *testing.T) {
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
	}

	seedStdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Search profile off", "--body", "Search should stay quiet")
	if code != 0 {
		t.Fatalf("seed add exit=%d stderr=%s", code, stderr)
	}
	if seedStdout != "added entry 1 at depth 1\n" {
		t.Fatalf("seed stdout = %q, want preserved add output", seedStdout)
	}

	stdout, stderr, code := runApp(t, env, "search", "Search")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "[1]  depth=1  Search profile off") {
		t.Fatalf("stdout = %q, want preserved search result output", stdout)
	}
	if strings.Contains(stderr, "[profile]") {
		t.Fatalf("stderr = %q, want no profile block when TAGMEM_PROFILE is off", stderr)
	}
}

func TestAppSearchProfilingShowsPhaseBreakdown(t *testing.T) {
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
		"TAGMEM_PROFILE=1",
	}

	seedStdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Search profile", "--body", "Search phase output expected")
	if code != 0 {
		t.Fatalf("seed add exit=%d stderr=%s", code, stderr)
	}
	if seedStdout != "added entry 1 at depth 1\n" {
		t.Fatalf("seed stdout = %q, want preserved add output", seedStdout)
	}

	stdout, stderr, code := runApp(t, env, "search", "Search")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "[1]  depth=1  Search profile") {
		t.Fatalf("stdout = %q, want preserved search result output", stdout)
	}
	for _, phase := range []string{
		"repo_init=",
		"search_total=",
		"query_embedding=",
		"vector_query=",
		"sqlite_candidate_fetch=",
		"rerank=",
		"source_hydration=",
	} {
		if !strings.Contains(stderr, phase) {
			t.Fatalf("stderr = %q, want phase %q", stderr, phase)
		}
	}
}

func TestAppAddUsesLiveDaemonWhenEnabled(t *testing.T) {
	useFakeProvider(t)

	paths, stop := startTestDaemon(t)
	defer stop()

	root := t.TempDir()
	localDataDir := filepath.Dir(paths.SocketPath)
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + localDataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "local-config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "local-cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "2", "--title", "Daemon add", "--body", "Routed through daemon")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if stdout != "added entry 1 at depth 2\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}

	daemonRepo := store.NewRepository(paths.StorePath, fakeembed.Provider().IndexPath(paths.IndexDir), fakeembed.Provider())
	if err := daemonRepo.Init(); err != nil {
		t.Fatalf("daemonRepo.Init() error = %v", err)
	}
	daemonEntries, err := daemonRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("daemonRepo.List() error = %v", err)
	}
	if len(daemonEntries) != 1 {
		t.Fatalf("len(daemonEntries) = %d, want 1", len(daemonEntries))
	}
	if daemonEntries[0].Title != "Daemon add" {
		t.Fatalf("daemonEntries[0].Title = %q, want %q", daemonEntries[0].Title, "Daemon add")
	}

	localRepo := store.NewRepository(filepath.Join(localDataDir, "store.json"), fakeembed.Provider().IndexPath(filepath.Join(localDataDir, "vector")), fakeembed.Provider())
	if err := localRepo.Init(); err != nil {
		t.Fatalf("localRepo.Init() error = %v", err)
	}
	localEntries, err := localRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("localRepo.List() error = %v", err)
	}
	if len(localEntries) != 0 {
		t.Fatalf("len(localEntries) = %d, want 0 when add is routed to daemon", len(localEntries))
	}
}

func TestAppAddUsesLiveDaemonWhenEnabledWithoutResolvingLocalProvider(t *testing.T) {
	paths, stop := startTestDaemon(t)
	defer stop()
	useFailingProvider(t)

	root := t.TempDir()
	localDataDir := filepath.Dir(paths.SocketPath)
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + localDataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "local-config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "local-cache"),
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "2", "--title", "Daemon add no provider", "--body", "Routed through daemon")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != "added entry 1 at depth 2\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}
}

func TestAppAddUsesLiveDaemonWhenEnabledPreservesImportTimestamps(t *testing.T) {
	useFakeProvider(t)

	paths, stop := startTestDaemon(t)
	defer stop()

	root := t.TempDir()
	localDataDir := filepath.Dir(paths.SocketPath)
	createdAt := "2020-01-02T03:04:05Z"
	updatedAt := "2020-01-02T03:05:06Z"
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + localDataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "local-config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "local-cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
		"TAGMEM_IMPORT_CREATED_AT=" + createdAt,
		"TAGMEM_IMPORT_UPDATED_AT=" + updatedAt,
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "2", "--title", "Daemon add timestamps", "--body", "Routed through daemon")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != "added entry 1 at depth 2\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}

	daemonRepo := store.NewRepository(paths.StorePath, fakeembed.Provider().IndexPath(paths.IndexDir), fakeembed.Provider())
	if err := daemonRepo.Init(); err != nil {
		t.Fatalf("daemonRepo.Init() error = %v", err)
	}
	daemonEntries, err := daemonRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("daemonRepo.List() error = %v", err)
	}
	if len(daemonEntries) != 1 {
		t.Fatalf("len(daemonEntries) = %d, want 1", len(daemonEntries))
	}
	if got := daemonEntries[0].CreatedAt.UTC().Format(time.RFC3339); got != createdAt {
		t.Fatalf("CreatedAt = %q, want %q", got, createdAt)
	}
	if got := daemonEntries[0].UpdatedAt.UTC().Format(time.RFC3339); got != updatedAt {
		t.Fatalf("UpdatedAt = %q, want %q", got, updatedAt)
	}
}

func TestAppAddFallsBackToDirectWhenDisabled(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "2", "--title", "Direct add unset", "--body", "Uses direct path")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != "added entry 1 at depth 2\n" {
		t.Fatalf("stdout = %q, want preserved add output", stdout)
	}

	localRepo := store.NewRepository(filepath.Join(dataDir, "store.json"), fakeembed.Provider().IndexPath(filepath.Join(dataDir, "vector")), fakeembed.Provider())
	if err := localRepo.Init(); err != nil {
		t.Fatalf("localRepo.Init() error = %v", err)
	}
	localEntries, err := localRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("localRepo.List() error = %v", err)
	}
	if len(localEntries) != 1 {
		t.Fatalf("len(localEntries) = %d, want 1 when add uses direct path", len(localEntries))
	}
	if localEntries[0].Title != "Direct add unset" {
		t.Fatalf("localEntries[0].Title = %q, want %q", localEntries[0].Title, "Direct add unset")
	}
}

func TestAppAddFailsWhenDaemonRequiredButUnavailable(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--depth", "2", "--title", "Direct add unavailable", "--body", "Should fail without daemon")
	if code == 0 {
		t.Fatalf("add exit=%d stderr=%s stdout=%s, want non-zero exit", code, stderr, stdout)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no success output", stdout)
	}
	if !strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want daemon-required failure", stderr)
	}

	localRepo := store.NewRepository(filepath.Join(dataDir, "store.json"), fakeembed.Provider().IndexPath(filepath.Join(dataDir, "vector")), fakeembed.Provider())
	if err := localRepo.Init(); err != nil {
		t.Fatalf("localRepo.Init() error = %v", err)
	}
	localEntries, err := localRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("localRepo.List() error = %v", err)
	}
	if len(localEntries) != 0 {
		t.Fatalf("len(localEntries) = %d, want 0 when daemon is required but unavailable", len(localEntries))
	}
}

func TestAppAddHelpDoesNotRequireDaemon(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--help")
	if code == 0 {
		t.Fatalf("add --help exit=%d stdout=%s stderr=%s, want non-zero from flag help", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no stdout for flag help", stdout)
	}
	if !strings.Contains(stderr, "Usage of add:") {
		t.Fatalf("stderr = %q, want add help output", stderr)
	}
	if strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want help output instead of daemon failure", stderr)
	}
}

func TestAppAddUsageErrorDoesNotRequireDaemon(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "add", "--badflag")
	if code == 0 {
		t.Fatalf("add --badflag exit=%d stdout=%s stderr=%s, want non-zero", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no stdout for usage error", stdout)
	}
	if !strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf("stderr = %q, want flag parse error", stderr)
	}
	if strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want usage error instead of daemon failure", stderr)
	}
}

func TestAppSearchUsesLiveDaemonWhenEnabled(t *testing.T) {
	useFakeProvider(t)

	paths, stop := startTestDaemon(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := daemon.Call(ctx, paths.SocketPath, daemon.Request{
		ID:      "test-search-seed",
		Command: "add_entry",
		Payload: map[string]any{
			"depth": 1,
			"title": "Daemon search",
			"body":  "Live daemon search result",
		},
	})
	if err != nil {
		t.Fatalf("daemon.Call(add_entry) error = %v", err)
	}
	if !response.Success {
		t.Fatalf("daemon add_entry success = false, error = %q", response.Error)
	}
	var seeded struct {
		Entry store.Entry `json:"entry"`
	}
	if err := daemon.DecodePayload(response.Payload, &seeded); err != nil {
		t.Fatalf("daemon.DecodePayload() error = %v", err)
	}

	root := t.TempDir()
	localDataDir := filepath.Dir(paths.SocketPath)
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + localDataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "local-config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "local-cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "search", "daemon")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := formatSearchResultLine(store.SearchResult{Entry: seeded.Entry}, false) + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}

	localRepo := store.NewRepository(filepath.Join(localDataDir, "store.json"), fakeembed.Provider().IndexPath(filepath.Join(localDataDir, "vector")), fakeembed.Provider())
	if err := localRepo.Init(); err != nil {
		t.Fatalf("localRepo.Init() error = %v", err)
	}
	localEntries, err := localRepo.List(store.Query{Limit: 10})
	if err != nil {
		t.Fatalf("localRepo.List() error = %v", err)
	}
	if len(localEntries) != 0 {
		t.Fatalf("len(localEntries) = %d, want 0 when search is routed to daemon", len(localEntries))
	}
}

func TestAppSearchUsesLiveDaemonWhenEnabledWithoutResolvingLocalProvider(t *testing.T) {
	paths, stop := startTestDaemon(t)
	defer stop()
	useFailingProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := daemon.Call(ctx, paths.SocketPath, daemon.Request{
		ID:      "test-search-no-provider-seed",
		Command: "add_entry",
		Payload: map[string]any{
			"depth": 1,
			"title": "Daemon search no provider",
			"body":  "Live daemon search result",
		},
	})
	if err != nil {
		t.Fatalf("daemon.Call(add_entry) error = %v", err)
	}
	if !response.Success {
		t.Fatalf("daemon add_entry success = false, error = %q", response.Error)
	}

	root := t.TempDir()
	localDataDir := filepath.Dir(paths.SocketPath)
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + localDataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "local-config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "local-cache"),
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "search", "provider")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Daemon search no provider") {
		t.Fatalf("stdout = %q, want daemon search result", stdout)
	}
}

func TestAddViaDaemonAllowsColdStartTimeoutBudget(t *testing.T) {
	original := daemonCallFunc
	defer func() {
		daemonCallFunc = original
	}()

	daemonCallFunc = func(ctx context.Context, socketPath string, request daemon.Request) (daemon.Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("ctx.Deadline() ok = false, want timeout")
		}
		remaining := time.Until(deadline)
		if remaining < 5*time.Second {
			t.Fatalf("remaining timeout = %s, want at least %s", remaining, 5*time.Second)
		}
		return daemon.Response{
			ID:      request.ID,
			Success: true,
			Payload: map[string]any{"entry": store.Entry{ID: 1, Depth: 2}},
		}, nil
	}

	entry, err := addViaDaemon("/tmp/tagmem.sock", store.AddEntry{Depth: 2, Title: "cold add", Body: "body"})
	if err != nil {
		t.Fatalf("addViaDaemon() error = %v", err)
	}
	if entry.ID != 1 || entry.Depth != 2 {
		t.Fatalf("entry = %#v, want id=1 depth=2", entry)
	}
}

func TestSearchViaDaemonAllowsColdStartTimeoutBudget(t *testing.T) {
	original := daemonCallFunc
	defer func() {
		daemonCallFunc = original
	}()

	daemonCallFunc = func(ctx context.Context, socketPath string, request daemon.Request) (daemon.Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("ctx.Deadline() ok = false, want timeout")
		}
		remaining := time.Until(deadline)
		if remaining < 5*time.Second {
			t.Fatalf("remaining timeout = %s, want at least %s", remaining, 5*time.Second)
		}
		return daemon.Response{
			ID:      request.ID,
			Success: true,
			Payload: map[string]any{"results": []store.SearchResult{{Entry: store.Entry{ID: 7, Depth: 1, Title: "match"}}}},
		}, nil
	}

	results, explain, err := searchViaDaemon("/tmp/tagmem.sock", []string{"query"}, io.Discard)
	if err != nil {
		t.Fatalf("searchViaDaemon() error = %v", err)
	}
	if explain {
		t.Fatal("explain = true, want false")
	}
	if len(results) != 1 || results[0].Entry.ID != 7 {
		t.Fatalf("results = %#v, want one result with id 7", results)
	}
}

func TestAppSearchUsesDirectPathWhenDaemonEnvIsUnset(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=",
	}

	seedStdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Direct search unset", "--body", "Uses direct search path")
	if code != 0 {
		t.Fatalf("seed add exit=%d stderr=%s stdout=%s", code, stderr, seedStdout)
	}

	stdout, stderr, code := runApp(t, env, "search", "unset")
	if code != 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Direct search unset") {
		t.Fatalf("stdout = %q, want direct search result", stdout)
	}
}

func TestAppSearchFailsWhenDaemonRequiredButUnavailable(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	seedStdout, stderr, code := runApp(t, env, "add", "--depth", "1", "--title", "Direct search unavailable", "--body", "Falls back to direct search path")
	if code == 0 {
		t.Fatalf("seed add exit=%d stderr=%s stdout=%s, want non-zero exit", code, stderr, seedStdout)
	}
	if seedStdout != "" {
		t.Fatalf("seed stdout = %q, want no success output", seedStdout)
	}
	if !strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("seed stderr = %q, want daemon-required failure", stderr)
	}

	stdout, stderr, code := runApp(t, env, "search", "unavailable")
	if code == 0 {
		t.Fatalf("search exit=%d stderr=%s stdout=%s, want non-zero exit", code, stderr, stdout)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no search output", stdout)
	}
	if !strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want daemon-required failure", stderr)
	}
}

func TestAppSearchHelpDoesNotRequireDaemon(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "search", "--help")
	if code == 0 {
		t.Fatalf("search --help exit=%d stdout=%s stderr=%s, want non-zero from flag help", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no stdout for flag help", stdout)
	}
	if !strings.Contains(stderr, "Usage of search:") {
		t.Fatalf("stderr = %q, want search help output", stderr)
	}
	if strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want help output instead of daemon failure", stderr)
	}
}

func TestAppSearchUsageErrorDoesNotRequireDaemon(t *testing.T) {
	useFakeProvider(t)

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	env := []string{
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"XDG_RUNTIME_DIR=",
		"TAGMEM_DATA_ROOT=" + dataDir,
		"TAGMEM_CONFIG_ROOT=" + filepath.Join(root, "config"),
		"TAGMEM_CACHE_ROOT=" + filepath.Join(root, "cache"),
		"TAGMEM_EMBED_PROVIDER=embedded",
		"TAGMEM_USE_DAEMON=1",
	}

	stdout, stderr, code := runApp(t, env, "search")
	if code == 0 {
		t.Fatalf("search exit=%d stdout=%s stderr=%s, want non-zero", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no stdout for usage error", stdout)
	}
	if !strings.Contains(stderr, "usage: tagmem search") {
		t.Fatalf("stderr = %q, want search usage output", stderr)
	}
	if strings.Contains(stderr, "daemon-backed CLI mode requires a reachable daemon") {
		t.Fatalf("stderr = %q, want usage output instead of daemon failure", stderr)
	}
}

func TestAppStatusProfilingOnDoesNotEmitProfileBlock(t *testing.T) {
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
		"TAGMEM_PROFILE=1",
	}

	stdout, stderr, code := runApp(t, env, "status")
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "entries:") {
		t.Fatalf("stdout = %q, want normal status output", stdout)
	}
	if strings.Contains(stderr, "[profile]") {
		t.Fatalf("stderr = %q, want no profile block for non-Task-1 commands", stderr)
	}
}

func runApp(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
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
	code := app.Run(args)
	return stdout.String(), stderr.String(), code
}

func useFakeProvider(t *testing.T) {
	t.Helper()
	original := resolveProviderFunc
	resolveProviderFunc = func(xdg.Paths) (vector.Provider, error) {
		return fakeembed.Provider(), nil
	}
	t.Cleanup(func() {
		resolveProviderFunc = original
	})
}

func useFailingProvider(t *testing.T) {
	t.Helper()
	original := resolveProviderFunc
	resolveProviderFunc = func(xdg.Paths) (vector.Provider, error) {
		return vector.Provider{}, errors.New("provider should not be resolved")
	}
	t.Cleanup(func() {
		resolveProviderFunc = original
	})
}

func restoreEnv(t *testing.T, env []string) {
	t.Helper()
	t.Cleanup(func() {
		os.Clearenv()
		for _, value := range env {
			parts := strings.SplitN(value, "=", 2)
			_ = os.Setenv(parts[0], parts[1])
		}
	})
}

func copyCLIProject(t *testing.T, root string) string {
	t.Helper()
	projectDir := filepath.Join(root, "project")
	copyFileCLI(t, filepath.Join("..", "importer", "testdata", "project", ".gitignore"), filepath.Join(projectDir, ".gitignore"))
	copyFileCLI(t, filepath.Join("..", "importer", "testdata", "project", "notes", "degree.md"), filepath.Join(projectDir, "notes", "degree.md"))
	copyFileCLI(t, filepath.Join("..", "importer", "testdata", "project", "notes", "support.md"), filepath.Join(projectDir, "notes", "support.md"))
	copyFileCLI(t, filepath.Join("..", "importer", "testdata", "project", "chats", "session.md"), filepath.Join(projectDir, "chats", "session.md"))
	copyFileCLI(t, filepath.Join("..", "importer", "testdata", "project", "ignored", "secret.md"), filepath.Join(projectDir, "ignored", "secret.md"))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = projectDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init error = %v", err)
	}
	return projectDir
}

func copyFileCLI(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", dst, err)
	}
}
