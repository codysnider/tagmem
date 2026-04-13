//go:build linux && tagmem_onnx

package vector_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func TestEmbeddedProviderSearchesMultilingualDocuments(t *testing.T) {
	paths := newTestPaths(t)
	provider, err := vector.EmbeddedProvider(paths, "bge-small-en-v1.5", "cpu")
	if err != nil {
		t.Fatalf("EmbeddedProvider() error = %v", err)
	}

	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	docs := []struct {
		origin string
		title  string
		body   string
	}{
		{
			origin: "ru-workshop.txt",
			title:  "Синий чемодан",
			body:   "В мастерской на Садовой улице хранится синий чемодан с зимними письмами и фотографиями моря.",
		},
		{
			origin: "ru-greenhouse.txt",
			title:  "Зелёный ящик",
			body:   "В оранжерее у северных ворот стоит зелёный ящик с весенними семенами и журналом полива.",
		},
		{
			origin: "zh-library.txt",
			title:  "木窗借书卡",
			body:   "在海边的小图书馆里，林把借书卡放在木窗旁边，并在目录里记下晒过太阳的页码。",
		},
		{
			origin: "zh-observatory.txt",
			title:  "铜灯潮汐图",
			body:   "在旧天文台旁边，明把铜灯和潮汐地图锁进石柜里，等夜潮退去再来查看。",
		},
	}

	for _, doc := range docs {
		if _, err := repo.Add(store.AddEntry{
			Depth:  0,
			Title:  doc.title,
			Body:   doc.body,
			Source: doc.body,
			Origin: doc.origin,
		}); err != nil {
			t.Fatalf("Add(%s) error = %v", doc.origin, err)
		}
	}

	tests := []struct {
		name         string
		query        string
		expectOrigin string
		expectBody   string
	}{
		{
			name:         "russian inquiry finds russian workshop doc",
			query:        "В какой мастерской лежат зимние письма и фотографии моря?",
			expectOrigin: "ru-workshop.txt",
			expectBody:   docs[0].body,
		},
		{
			name:         "english inquiry finds russian workshop doc",
			query:        "Which workshop keeps the winter letters and sea photographs?",
			expectOrigin: "ru-workshop.txt",
			expectBody:   docs[0].body,
		},
		{
			name:         "russian inquiry finds greenhouse doc",
			query:        "Где стоит зелёный ящик с журналом полива?",
			expectOrigin: "ru-greenhouse.txt",
			expectBody:   docs[1].body,
		},
		{
			name:         "literal chinese query finds library doc",
			query:        "借书卡放在木窗旁边",
			expectOrigin: "zh-library.txt",
			expectBody:   docs[2].body,
		},
		{
			name:         "english inquiry finds chinese library doc",
			query:        "Who stores the cards by the wooden window in the seaside library?",
			expectOrigin: "zh-library.txt",
			expectBody:   docs[2].body,
		},
		{
			name:         "literal chinese query finds observatory doc",
			query:        "谁把铜灯和潮汐地图锁进石柜里？",
			expectOrigin: "zh-observatory.txt",
			expectBody:   docs[3].body,
		},
		{
			name:         "english inquiry finds observatory doc",
			query:        "Who locks the brass lantern and tide map in the stone cabinet?",
			expectOrigin: "zh-observatory.txt",
			expectBody:   docs[3].body,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := repo.SearchDetailed(store.Query{Text: tc.query, Limit: len(docs)})
			if err != nil {
				t.Fatalf("SearchDetailed(%q) error = %v", tc.query, err)
			}
			if len(results) == 0 {
				t.Fatalf("SearchDetailed(%q) returned no results", tc.query)
			}
			if results[0].Entry.Origin != tc.expectOrigin {
				t.Fatalf("SearchDetailed(%q) top origin = %q, want %q", tc.query, results[0].Entry.Origin, tc.expectOrigin)
			}
			if strings.TrimSpace(results[0].Entry.Source) != tc.expectBody {
				t.Fatalf("SearchDetailed(%q) top source did not match expected body", tc.query)
			}
		})
	}
}

func newTestPaths(t *testing.T) xdg.Paths {
	t.Helper()
	root := t.TempDir()
	paths := xdg.Paths{
		AppName:      "tagmem",
		ConfigDir:    filepath.Join(root, "config"),
		DataDir:      filepath.Join(root, "data"),
		CacheDir:     filepath.Join(root, "cache"),
		IndexDir:     filepath.Join(root, "data", "vector"),
		ModelDir:     filepath.Join(root, "data", "models"),
		DiaryDir:     filepath.Join(root, "data", "diaries"),
		StorePath:    filepath.Join(root, "data", "store.json"),
		ConfigPath:   filepath.Join(root, "config", "config.json"),
		KGPath:       filepath.Join(root, "data", "knowledge.json"),
		IdentityPath: filepath.Join(root, "config", "identity.txt"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	return paths
}
