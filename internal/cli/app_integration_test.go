package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppIngestStatusAndContextFlow(t *testing.T) {
	t.Parallel()

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
		"TAGMEM_EMBED_PROVIDER=embedded-hash",
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
