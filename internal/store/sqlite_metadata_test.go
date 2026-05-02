package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	_ "modernc.org/sqlite"

	"github.com/codysnider/tagmem/internal/vector"
)

func TestFakeEmbedProviderProducesUsableRepositoryProvider(t *testing.T) {
	t.Parallel()

	provider := fakeembed.Provider()
	if provider.Name != vector.ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, vector.ProviderEmbedded)
	}
	if provider.Batch == nil {
		t.Fatal("provider.Batch is nil, want batch embedding function")
	}
}

func TestRepositoryInitCreatesSQLiteMetadataStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())

	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	dbPath := filepath.Join(root, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var schemaVersion string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&schemaVersion); err != nil {
		t.Fatalf("QueryRow(schema_version) error = %v", err)
	}
	if schemaVersion != "1" {
		t.Fatalf("schema_version = %q, want %q", schemaVersion, "1")
	}

	var indexState string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'index_state'`).Scan(&indexState); err != nil {
		t.Fatalf("QueryRow(index_state) error = %v", err)
	}
	if indexState != "ready" {
		t.Fatalf("index_state = %q, want %q", indexState, "ready")
	}
}
