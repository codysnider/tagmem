package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (r *Repository) migrateLegacyStoreToSQLiteLocked(dbAlreadyExists bool) error {
	if dbAlreadyExists {
		return nil
	}
	if _, err := os.Stat(r.path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy store: %w", err)
	}

	snapshot, err := r.loadSnapshotFromStoreLocked()
	if err != nil {
		return err
	}
	stamp, err := currentFileStamp(r.path)
	if err != nil {
		return fmt.Errorf("stat legacy store: %w", err)
	}
	if err := r.syncSnapshotToSQLiteLocked(snapshot, 1, 0, formatFileStamp(stamp)); err != nil {
		return err
	}
	if err := r.rebuildIndexFromSnapshotLocked(snapshot); err != nil {
		return err
	}
	if err := r.setMirrorStateLocked(1, 1, formatFileStamp(stamp), snapshot.NextID); err != nil {
		return err
	}
	return r.setIndexStateLocked("ready")
}

func (r *Repository) rebuildJSONMirrorFromSQLiteLocked() error {
	if _, err := os.Stat(r.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat store: %w", err)
	}

	snapshot, err := sqliteLoadSnapshot(r.metaDB)
	if err != nil {
		return err
	}

	return r.saveStoreLocked(snapshot)
}

func (r *Repository) syncSnapshotToSQLiteLocked(snapshot Snapshot, metadataRevision, jsonMirrorRevision int, mirrorStamp string) error {
	if r.metaDB == nil {
		return fmt.Errorf("metadata db is not open")
	}

	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite snapshot sync: %w", err)
	}
	defer tx.Rollback()

	if err := setMetaValue(tx, metaKeyIndexState, "dirty"); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM entry_tags`); err != nil {
		return fmt.Errorf("clear sqlite entry tags: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM entries`); err != nil {
		return fmt.Errorf("clear sqlite entries: %w", err)
	}
	for _, entry := range snapshot.Entries {
		if err := sqliteUpsertEntry(tx, entry); err != nil {
			return err
		}
		if err := sqliteReplaceEntryTags(tx, entry.ID, entry.Tags); err != nil {
			return err
		}
	}
	if err := setMetaValue(tx, metaKeyMetadataRevision, strconv.Itoa(metadataRevision)); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyJSONMirrorRev, strconv.Itoa(jsonMirrorRevision)); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyJSONMirrorStamp, mirrorStamp); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(snapshot.NextID)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite snapshot sync: %w", err)
	}

	if mirrorStamp != "" {
		parts := strings.SplitN(mirrorStamp, ":", 2)
		if len(parts) == 2 {
			size, sizeErr := strconv.ParseInt(parts[0], 10, 64)
			modTime, modErr := strconv.ParseInt(parts[1], 10, 64)
			if sizeErr == nil && modErr == nil {
				r.cacheSnapshotWithStampLocked(snapshot, fileStamp{size: size, modTime: modTime})
				return nil
			}
		}
	}
	r.cacheSnapshotLocked(snapshot)
	return nil
}

func (r *Repository) rebuildIndexFromSnapshotLocked(snapshot Snapshot) error {
	if err := r.openIndexLocked(); err != nil {
		return err
	}
	if err := r.db.DeleteCollection(collectionName); err != nil {
		return fmt.Errorf("reset vector collection: %w", err)
	}

	collection, err := r.db.CreateCollection(collectionName, nil, r.provider.Func)
	if err != nil {
		return fmt.Errorf("recreate vector collection: %w", err)
	}
	r.collection = collection

	documents, err := r.makeDocumentsWithEmbeddings(snapshot.Entries)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return nil
	}
	if err := r.collection.AddDocuments(context.Background(), documents, 1); err != nil {
		return fmt.Errorf("rebuild vector index: %w", err)
	}
	return nil
}

func (r *Repository) rebuildIndexFromSQLiteLocked() error {
	if err := r.openIndexLocked(); err != nil {
		return err
	}
	if err := r.db.DeleteCollection(collectionName); err != nil {
		return fmt.Errorf("reset vector collection: %w", err)
	}

	collection, err := r.db.CreateCollection(collectionName, nil, r.provider.Func)
	if err != nil {
		return fmt.Errorf("recreate vector collection: %w", err)
	}
	r.collection = collection

	rows, err := r.metaDB.Query(`
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM entries
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("query sqlite index rebuild entries: %w", err)
	}
	defer rows.Close()

	batch := make([]Entry, 0, 128)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		documents, err := r.makeDocumentsWithEmbeddings(batch)
		if err != nil {
			return err
		}
		if len(documents) > 0 {
			if err := r.collection.AddDocuments(context.Background(), documents, 1); err != nil {
				return fmt.Errorf("rebuild vector index: %w", err)
			}
		}
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return fmt.Errorf("scan sqlite index rebuild entry: %w", err)
		}
		batch = append(batch, entry)
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite index rebuild entries: %w", err)
	}
	return flush()
}

func (r *Repository) syncStoreToSQLiteLocked(metadataRevision, jsonMirrorRevision int, mirrorStamp string) error {
	if r.metaDB == nil {
		return fmt.Errorf("metadata db is not open")
	}

	file, err := os.Open(r.path)
	if err != nil {
		return fmt.Errorf("read store: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	start, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode store: %w", err)
	}
	if delim, ok := start.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("decode store: expected object")
	}

	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite snapshot sync: %w", err)
	}
	defer tx.Rollback()

	if err := setMetaValue(tx, metaKeyIndexState, "dirty"); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM entry_tags`); err != nil {
		return fmt.Errorf("clear sqlite entry tags: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM entries`); err != nil {
		return fmt.Errorf("clear sqlite entries: %w", err)
	}

	nextID := 0
	maxID := 0
	seenSources := map[string]struct{}{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode store field: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("decode store: expected field name")
		}
		switch key {
		case "version":
			var version int
			if err := decoder.Decode(&version); err != nil {
				return fmt.Errorf("decode store version: %w", err)
			}
		case "next_id":
			if err := decoder.Decode(&nextID); err != nil {
				return fmt.Errorf("decode store next_id: %w", err)
			}
		case "entries":
			arrayStart, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode store entries: %w", err)
			}
			if delim, ok := arrayStart.(json.Delim); !ok || delim != '[' {
				return fmt.Errorf("decode store entries: expected array")
			}
			for decoder.More() {
				var entry Entry
				if err := decoder.Decode(&entry); err != nil {
					return fmt.Errorf("decode store entry: %w", err)
				}
				normalized, err := r.normalizeLoadedEntry(entry, seenSources)
				if err != nil {
					return err
				}
				if normalized.ID > maxID {
					maxID = normalized.ID
				}
				if err := sqliteUpsertEntry(tx, normalized); err != nil {
					return err
				}
				if err := sqliteReplaceEntryTags(tx, normalized.ID, normalized.Tags); err != nil {
					return err
				}
			}
			arrayEnd, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode store entries end: %w", err)
			}
			if delim, ok := arrayEnd.(json.Delim); !ok || delim != ']' {
				return fmt.Errorf("decode store entries: expected array end")
			}
		default:
			var discard any
			if err := decoder.Decode(&discard); err != nil {
				return fmt.Errorf("decode store field %q: %w", key, err)
			}
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode store end: %w", err)
	}
	if delim, ok := end.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("decode store: expected object end")
	}

	if nextID < 1 {
		nextID = maxID + 1
		if nextID < 1 {
			nextID = 1
		}
	}
	if err := setMetaValue(tx, metaKeyMetadataRevision, strconv.Itoa(metadataRevision)); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyJSONMirrorRev, strconv.Itoa(jsonMirrorRevision)); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyJSONMirrorStamp, mirrorStamp); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(nextID)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite snapshot sync: %w", err)
	}
	return nil
}
