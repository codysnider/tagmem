package store

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
)

func benchmarkAddEntries(count int) []AddEntry {
	batch := make([]AddEntry, 0, count)
	for i := 0; i < count; i++ {
		batch = append(batch, AddEntry{
			Depth: 1,
			Title: "Benchmark entry " + strconv.Itoa(i),
			Body:  "benchmark body " + strconv.Itoa(i),
			Tags:  []string{"bench", "entry-" + strconv.Itoa(i%10)},
		})
	}
	return batch
}

func benchmarkRepositoryWithEntries(b *testing.B, count int) *Repository {
	b.Helper()

	root := b.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
	batch := benchmarkAddEntries(count)
	if _, err := repo.AddMany(batch); err != nil {
		b.Fatalf("AddMany() error = %v", err)
	}
	return repo
}

func BenchmarkRepositoryAddManySQLite(b *testing.B) {
	batch := benchmarkAddEntries(100)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		root := b.TempDir()
		repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
		b.StartTimer()

		if _, err := repo.AddMany(batch); err != nil {
			b.Fatalf("AddMany() error = %v", err)
		}
	}
}

func BenchmarkRepositoryListMetadataSQLite(b *testing.B) {
	repo := benchmarkRepositoryWithEntries(b, 1000)
	query := Query{Limit: 100}

	entries, err := repo.ListMetadata(query)
	if err != nil {
		b.Fatalf("ListMetadata() error = %v", err)
	}
	if len(entries) == 0 {
		b.Fatal("expected non-empty metadata results")
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entries, err := repo.ListMetadata(query)
		if err != nil {
			b.Fatalf("ListMetadata() error = %v", err)
		}
		if len(entries) == 0 {
			b.Fatal("expected non-empty metadata results")
		}
	}
}

func BenchmarkRepositoryListMetadataSQLiteCold(b *testing.B) {
	root := b.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	if _, err := repo.AddMany(benchmarkAddEntries(1000)); err != nil {
		b.Fatalf("AddMany() error = %v", err)
	}
	query := Query{Limit: 100}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		repo := NewRepository(storePath, indexPath, fakeembed.Provider())
		entries, err := repo.ListMetadata(query)
		if err != nil {
			b.Fatalf("ListMetadata() error = %v", err)
		}
		if len(entries) == 0 {
			b.Fatal("expected non-empty metadata results")
		}
		if repo.loaded {
			b.Fatal("expected cold metadata benchmark path to avoid snapshot residency")
		}
	}
}

func BenchmarkRepositorySearchDetailedSQLite(b *testing.B) {
	repo := benchmarkRepositoryWithEntries(b, 1000)
	queries := make([]Query, 0, 512)
	for i := 0; i < 512; i++ {
		queries = append(queries, Query{Text: "benchmark body " + strconv.Itoa(i), Limit: 25})
	}

	results, err := repo.SearchDetailed(queries[0])
	if err != nil {
		b.Fatalf("SearchDetailed() error = %v", err)
	}
	if len(results) == 0 {
		b.Fatal("expected non-empty detailed search results")
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		results, err := repo.SearchDetailed(query)
		if err != nil {
			b.Fatalf("SearchDetailed() error = %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected non-empty detailed search results")
		}
	}
}

func BenchmarkRepositorySearchDetailedSQLiteCold(b *testing.B) {
	root := b.TempDir()
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := NewRepository(storePath, indexPath, fakeembed.Provider())
	if _, err := repo.AddMany(benchmarkAddEntries(1000)); err != nil {
		b.Fatalf("AddMany() error = %v", err)
	}
	queries := make([]Query, 0, 128)
	for i := 0; i < 128; i++ {
		queries = append(queries, Query{Text: "benchmark body " + strconv.Itoa(i), Limit: 25})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		repo := NewRepository(storePath, indexPath, fakeembed.Provider())
		results, err := repo.SearchDetailed(queries[i%len(queries)])
		if err != nil {
			b.Fatalf("SearchDetailed() error = %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected non-empty detailed search results")
		}
		if repo.loaded {
			b.Fatal("expected cold search benchmark path to avoid snapshot residency")
		}
	}
}

func BenchmarkRepositoryInitRebuildsMissingJSONMirror(b *testing.B) {
	batch := benchmarkAddEntries(1000)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		root := b.TempDir()
		storePath := filepath.Join(root, "store.json")
		indexPath := filepath.Join(root, "vector")
		repo := NewRepository(storePath, indexPath, fakeembed.Provider())
		if _, err := repo.AddMany(batch); err != nil {
			b.Fatalf("AddMany() error = %v", err)
		}
		if err := os.Remove(storePath); err != nil {
			b.Fatalf("os.Remove(%q) error = %v", storePath, err)
		}
		repo = NewRepository(storePath, indexPath, fakeembed.Provider())
		b.StartTimer()

		if err := repo.Init(); err != nil {
			b.Fatalf("Init() error = %v", err)
		}

		b.StopTimer()
		if _, err := os.Stat(storePath); err != nil {
			b.Fatalf("os.Stat(%q) error = %v, want rebuilt mirror", storePath, err)
		}
		entries, err := repo.ListMetadata(Query{Limit: 100})
		if err != nil {
			b.Fatalf("ListMetadata() error = %v", err)
		}
		if len(entries) == 0 {
			b.Fatal("expected rebuilt mirror metadata results")
		}
		b.StartTimer()
	}
}
