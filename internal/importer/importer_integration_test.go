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
	assertNoOrigin(t, entries, "ignored/secret.md")

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
	assertHasOrigin(t, entries, "ignored/secret.md")
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
	if results[0].Origin != "session.md" {
		t.Fatalf("results[0].Origin = %q, want session.md", results[0].Origin)
	}
	if !strings.Contains(results[0].Source, "Business Administration") {
		t.Fatalf("results[0].Source should contain full source material")
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

func TestRunFilesModeSearchReturnsFullDocumentSource(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	libraryDir := filepath.Join("testdata", "library")
	result, err := Run(repo, Options{
		SourceDir:        libraryDir,
		Mode:             ModeFiles,
		Depth:            1,
		RespectGitignore: false,
		SkipExisting:     true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FilesProcessed != 5 {
		t.Fatalf("FilesProcessed = %d, want 5", result.FilesProcessed)
	}

	type storyCheck struct {
		origin  string
		queries []string
	}
	checks := []storyCheck{
		{origin: "moonlit-harbor.txt", queries: []string{
			"The first customers were always fishermen with damp boots, but Mira loved the quiet moment before they arrived, when the harbor smelled like rainwater and rope and new paper.",
			"Inside the box lay a key carved from driftwood and a glass marble full of swirling blue light.",
			"Some were warnings. Some were riddles. One simply read, \"The harbor trusts your patience more than your hurry.\"",
		}},
		{origin: "wren-and-the-clock-garden.txt", queries: []string{
			"At the edge of the village stood the clock garden, a fenced enclosure full of stone flowerbeds and rusting metal stems where timepieces had once been grown for the royal observatory.",
			"The village blacksmith forged new supports for the lens ceiling.",
			"Because this place grows memory into rhythm, and rhythm into time.",
		}},
		{origin: "pip-and-the-paper-bridge.txt", queries: []string{
			"Her mother, who repaired books in the library cellar, used to say there had once been a paper bridge stretched over the narrow gorge behind the archives, folded from map scraps, theater posters, receipts, and letters never delivered.",
			"It was now a civic inconvenience everyone secretly adored.",
			"\"It began,\" she would say, \"when people decided that scraps were not leftovers but instructions waiting for one another.\"",
		}},
		{origin: "elio-and-the-rain-museum.txt", queries: []string{
			"At the center of town stood the Rain Museum, a long glass building with slate eaves and a copper weathervane shaped like a heron.",
			"We are giving the sky directions.",
			"Rain is never only falling water. It is route, memory, invitation, and reply.",
		}},
		{origin: "nora-and-the-pocket-orchard.txt", queries: []string{
			"But if someone entered through the side gate carrying an empty pocket and a patient mood, the orchard expanded.",
			"Gathering honest wishes turned out to be harder than collecting ordinary ones.",
			"Empty pockets are invitations if you know how to notice them.",
		}},
	}

	for _, check := range checks {
		content, err := os.ReadFile(filepath.Join(libraryDir, check.origin))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", check.origin, err)
		}
		expectedSource := strings.TrimSpace(string(content))
		for _, query := range check.queries {
			results, err := repo.Search(store.Query{Text: query, Limit: 5})
			if err != nil {
				t.Fatalf("Search(%q) error = %v", query, err)
			}
			if len(results) == 0 {
				t.Fatalf("Search(%q) returned no results", query)
			}
			matched := false
			for _, result := range results {
				if result.Origin != check.origin {
					continue
				}
				matched = true
				if strings.TrimSpace(result.Source) != expectedSource {
					t.Fatalf("result.Source for %s did not match full document", check.origin)
				}
				if !strings.Contains(result.Source, query) {
					t.Fatalf("result.Source for %s missing query text %q", check.origin, query)
				}
				for _, expectedFragment := range check.queries {
					if !strings.Contains(result.Source, expectedFragment) {
						t.Fatalf("result.Source for %s missing expected fragment %q", check.origin, expectedFragment)
					}
				}
				break
			}
			if !matched {
				t.Fatalf("Search(%q) did not return origin %s", query, check.origin)
			}
		}
	}
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

func assertNoOrigin(t *testing.T, entries []store.Entry, origin string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Origin == origin {
			t.Fatalf("unexpected origin %q found", origin)
		}
	}
}

func assertHasOrigin(t *testing.T, entries []store.Entry, origin string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Origin == origin {
			return
		}
	}
	t.Fatalf("expected origin %q not found", origin)
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
