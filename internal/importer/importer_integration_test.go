package importer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
)

func TestRunFilesModeRespectsGitignoreAndIncludeIgnored(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	projectDir := copyFixtureProject(t)

	result, err := Run(repo, Options{
		SourceDir:        projectDir,
		Mode:             ModeFiles,
		Depth:            1,
		RespectGitignore: true,
		SkipExisting:     true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FilesProcessed != 3 {
		t.Fatalf("FilesProcessed = %d, want 3", result.FilesProcessed)
	}
	if result.EntriesAdded != 3 {
		t.Fatalf("EntriesAdded = %d, want 3", result.EntriesAdded)
	}

	entries, err := repo.List(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertNoSource(t, entries, "ignored/secret.md")

	result, err = Run(repo, Options{
		SourceDir:        projectDir,
		Mode:             ModeFiles,
		Depth:            1,
		RespectGitignore: true,
		SkipExisting:     true,
		IncludeIgnored:   []string{"ignored/secret.md"},
	})
	if err != nil {
		t.Fatalf("Run(include ignored) error = %v", err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("FilesProcessed include-ignored = %d, want 1", result.FilesProcessed)
	}

	entries, err = repo.List(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertHasSource(t, entries, "ignored/secret.md")
	assertTagPresent(t, entries, "degree")
	assertTagPresent(t, entries, "business-administration")
	assertTagPresent(t, entries, "ignored")
}

func TestRunConversationModeCreatesSearchableChunks(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	result, err := Run(repo, Options{
		SourceDir:    filepath.Join(copyFixtureProject(t), "chats"),
		Mode:         ModeConversations,
		Extract:      "exchange",
		Depth:        2,
		SkipExisting: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("FilesProcessed = %d, want 1", result.FilesProcessed)
	}
	if result.EntriesAdded < 2 {
		t.Fatalf("EntriesAdded = %d, want at least 2", result.EntriesAdded)
	}

	results, err := repo.Search(store.Query{Text: "What degree did I graduate with?", Limit: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected conversation search result")
	}
	if results[0].Source != "session.md" {
		t.Fatalf("results[0].Source = %q, want session.md", results[0].Source)
	}
	assertTagPresent(t, results, "business-administration")
	assertTagPresent(t, results, "lgbtq")
}

func TestRunConversationModeGeneralExtractsTypedMemories(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	result, err := Run(repo, Options{
		SourceDir:    filepath.Join(copyFixtureProject(t), "chats"),
		Mode:         ModeConversations,
		Extract:      "general",
		Depth:        2,
		SkipExisting: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FilesProcessed != 1 {
		t.Fatalf("FilesProcessed = %d, want 1", result.FilesProcessed)
	}
	if result.EntriesAdded == 0 {
		t.Fatal("expected at least one extracted memory")
	}

	entries, err := repo.List(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertAnyBodyContains(t, entries, []string{"[decisions]", "[preferences]", "[milestones]", "[problems]", "[emotional]"})
}

func newTestRepo(t *testing.T) *store.Repository {
	t.Helper()
	root := t.TempDir()
	repo := store.NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), vector.EmbeddedHashProvider())
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	return repo
}

func copyFixtureProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "project")
	copyFile(t, filepath.Join(src, ".gitignore"), filepath.Join(root, ".gitignore"))
	copyFile(t, filepath.Join(src, "notes", "degree.md"), filepath.Join(root, "notes", "degree.md"))
	copyFile(t, filepath.Join(src, "notes", "support.md"), filepath.Join(root, "notes", "support.md"))
	copyFile(t, filepath.Join(src, "chats", "session.md"), filepath.Join(root, "chats", "session.md"))
	copyFile(t, filepath.Join(src, "ignored", "secret.md"), filepath.Join(root, "ignored", "secret.md"))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init error = %v", err)
	}
	return root
}

func copyFile(t *testing.T, src, dst string) {
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

func assertNoSource(t *testing.T, entries []store.Entry, source string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Source == source {
			t.Fatalf("unexpected source %q found", source)
		}
	}
}

func assertHasSource(t *testing.T, entries []store.Entry, source string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Source == source {
			return
		}
	}
	t.Fatalf("expected source %q not found", source)
}

func assertTagPresent(t *testing.T, entries []store.Entry, tag string) {
	t.Helper()
	for _, entry := range entries {
		for _, entryTag := range entry.Tags {
			if entryTag == tag {
				return
			}
		}
	}
	t.Fatalf("expected tag %q not found", tag)
}

func assertBodyContains(t *testing.T, entries []store.Entry, needle string) {
	t.Helper()
	for _, entry := range entries {
		if strings.Contains(entry.Body, needle) {
			return
		}
	}
	t.Fatalf("expected body containing %q not found", needle)
}

func assertAnyBodyContains(t *testing.T, entries []store.Entry, needles []string) {
	t.Helper()
	for _, needle := range needles {
		for _, entry := range entries {
			if strings.Contains(entry.Body, needle) {
				return
			}
		}
	}
	t.Fatalf("expected typed memory body prefix not found")
}
