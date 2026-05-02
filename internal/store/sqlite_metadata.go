package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = "1"

const (
	metaKeySchemaVersion    = "schema_version"
	metaKeyIndexState       = "index_state"
	metaKeyMetadataRevision = "metadata_revision"
	metaKeyJSONMirrorRev    = "json_mirror_revision"
	metaKeyJSONMirrorStamp  = "json_mirror_stamp"
	metaKeyNextID           = "next_id"
)

func (r *Repository) metadataPath() string {
	return filepath.Join(filepath.Dir(r.path), "store.db")
}

func openMetadataDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create metadata dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	statements := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY,
			depth INTEGER NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			source_ref TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS entry_tags (
			entry_id INTEGER NOT NULL,
			tag TEXT NOT NULL,
			PRIMARY KEY (entry_id, tag),
			FOREIGN KEY (entry_id) REFERENCES entries(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_updated_at ON entries(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_depth_updated_at ON entries(depth, updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_origin ON entries(origin);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_source_ref ON entries(source_ref);`,
		`CREATE INDEX IF NOT EXISTS idx_entry_tags_tag_entry_id ON entry_tags(tag, entry_id);`,
		`INSERT INTO meta(key, value) VALUES ('schema_version', '1') ON CONFLICT(key) DO NOTHING;`,
		`INSERT INTO meta(key, value) VALUES ('index_state', 'ready') ON CONFLICT(key) DO NOTHING;`,
		`INSERT INTO meta(key, value) VALUES ('metadata_revision', '0') ON CONFLICT(key) DO NOTHING;`,
		`INSERT INTO meta(key, value) VALUES ('json_mirror_revision', '0') ON CONFLICT(key) DO NOTHING;`,
		`INSERT INTO meta(key, value) VALUES ('json_mirror_stamp', '') ON CONFLICT(key) DO NOTHING;`,
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize metadata db: %w", err)
		}
	}

	return db, nil
}

func (r *Repository) metaValue(key string) (string, error) {
	value := ""
	err := r.withSharedLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		var err error
		value, err = metaValueDB(r.metaDB, key)
		if err != nil {
			return fmt.Errorf("query meta value %q: %w", key, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return value, nil
}

func setMetaValue(tx *sql.Tx, key, value string) error {
	if _, err := tx.Exec(`
		INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value); err != nil {
		return fmt.Errorf("set meta value %q: %w", key, err)
	}
	return nil
}

func metaValueDB(db *sql.DB, key string) (string, error) {
	value := ""
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func (r *Repository) ensureNextIDMetadataLocked() error {
	value, err := metaValueDB(r.metaDB, metaKeyNextID)
	if err == nil {
		nextID, convErr := strconv.Atoi(value)
		if convErr != nil {
			return fmt.Errorf("parse next_id: %w", convErr)
		}
		if nextID >= 1 {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query next_id: %w", err)
	}

	nextID := 1
	if _, statErr := os.Stat(r.path); statErr == nil {
		snapshot, loadErr := r.loadSnapshotFromStoreLocked()
		if loadErr != nil {
			return loadErr
		}
		if snapshot.NextID > 0 {
			nextID = snapshot.NextID
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat store: %w", statErr)
	} else {
		fallbackNextID, fallbackErr := sqliteFallbackNextID(r.metaDB)
		if fallbackErr != nil {
			return fallbackErr
		}
		nextID = fallbackNextID
	}

	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin next_id initialization: %w", err)
	}
	defer tx.Rollback()
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(nextID)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit next_id initialization: %w", err)
	}
	return nil
}

func metaValueTx(tx *sql.Tx, key string) (string, error) {
	value := ""
	if err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}
