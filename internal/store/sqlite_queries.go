package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const sqliteVariableBatchSize = 900

type sqliteEntryRow struct {
	ID        int
	Depth     int
	Title     string
	Body      string
	SourceRef string
	Origin    string
	CreatedAt string
	UpdatedAt string
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (Entry, error) {
	var raw sqliteEntryRow
	if err := row.Scan(
		&raw.ID,
		&raw.Depth,
		&raw.Title,
		&raw.Body,
		&raw.SourceRef,
		&raw.Origin,
		&raw.CreatedAt,
		&raw.UpdatedAt,
	); err != nil {
		return Entry{}, err
	}

	createdAt, err := time.Parse(time.RFC3339Nano, raw.CreatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("parse created_at for entry %d: %w", raw.ID, err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, raw.UpdatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("parse updated_at for entry %d: %w", raw.ID, err)
	}

	return Entry{
		ID:        raw.ID,
		Depth:     raw.Depth,
		Title:     raw.Title,
		Body:      raw.Body,
		SourceRef: strings.TrimSpace(raw.SourceRef),
		Origin:    strings.TrimSpace(raw.Origin),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func loadTags(db *sql.DB, entryIDs []int) (map[int][]string, error) {
	tagsByEntryID := make(map[int][]string, len(entryIDs))
	if len(entryIDs) == 0 {
		return tagsByEntryID, nil
	}

	for _, entryID := range entryIDs {
		tagsByEntryID[entryID] = []string{}
	}

	for start := 0; start < len(entryIDs); start += sqliteVariableBatchSize {
		end := start + sqliteVariableBatchSize
		if end > len(entryIDs) {
			end = len(entryIDs)
		}
		batch := entryIDs[start:end]
		placeholders := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch))
		for _, entryID := range batch {
			placeholders = append(placeholders, "?")
			args = append(args, entryID)
		}

		query := fmt.Sprintf(
			`SELECT entry_id, tag FROM entry_tags WHERE entry_id IN (%s) ORDER BY entry_id, tag`,
			strings.Join(placeholders, ", "),
		)
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query entry tags: %w", err)
		}
		for rows.Next() {
			var entryID int
			var tag string
			if err := rows.Scan(&entryID, &tag); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan entry tag: %w", err)
			}
			tagsByEntryID[entryID] = append(tagsByEntryID[entryID], tag)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate entry tags: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close entry tag rows: %w", err)
		}
	}

	return tagsByEntryID, nil
}

func sqliteEntryCount(db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sqlite entries: %w", err)
	}
	return count, nil
}

func sqliteDepthCounts(db *sql.DB) ([]DepthSummary, error) {
	rows, err := db.Query(`
		SELECT depth, COUNT(*)
		FROM entries
		GROUP BY depth
		ORDER BY depth
	`)
	if err != nil {
		return nil, fmt.Errorf("query sqlite depth counts: %w", err)
	}
	defer rows.Close()

	summaries := []DepthSummary{}
	for rows.Next() {
		var summary DepthSummary
		if err := rows.Scan(&summary.Depth, &summary.Count); err != nil {
			return nil, fmt.Errorf("scan sqlite depth count: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite depth counts: %w", err)
	}

	return summaries, nil
}

func sqliteGetEntry(db *sql.DB, id int) (Entry, bool, error) {
	entry, err := scanEntry(db.QueryRow(`
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM entries
		WHERE id = ?
	`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("query sqlite entry %d: %w", id, err)
	}

	tagsByEntryID, err := loadTags(db, []int{id})
	if err != nil {
		return Entry{}, false, err
	}
	entry.Tags = tagsByEntryID[id]
	return entry, true, nil
}

func sqliteEntryFilter(q Query) ([]string, []any) {
	clauses := []string{"1 = 1"}
	args := []any{}

	if q.Depth != nil {
		clauses = append(clauses, "depth = ?")
		args = append(args, *q.Depth)
	}
	if strings.TrimSpace(q.Tag) != "" {
		clauses = append(clauses, `EXISTS (
			SELECT 1 FROM entry_tags
			WHERE entry_tags.entry_id = entries.id AND entry_tags.tag = ?
		)`)
		args = append(args, strings.ToLower(strings.TrimSpace(q.Tag)))
	}

	return clauses, args
}

func sqliteQueryEntries(db *sql.DB, query string, args ...any) ([]Entry, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []Entry{}
	entryIDs := []int{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		entryIDs = append(entryIDs, entry.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tagsByEntryID, err := loadTags(db, entryIDs)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Tags = tagsByEntryID[entries[i].ID]
	}

	return entries, nil
}

func hydrateEntriesWithTags(db *sql.DB, entries []Entry) ([]Entry, error) {
	entryIDs := make([]int, 0, len(entries))
	for _, entry := range entries {
		entryIDs = append(entryIDs, entry.ID)
	}
	tagsByEntryID, err := loadTags(db, entryIDs)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Tags = tagsByEntryID[entries[i].ID]
	}
	return entries, nil
}

func sqliteListEntries(db *sql.DB, q Query) ([]Entry, error) {
	clauses, args := sqliteEntryFilter(q)

	query := `
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM entries
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY updated_at DESC, id DESC`
	if q.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}

	entries, err := sqliteQueryEntries(db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite entries: %w", err)
	}
	return entries, nil
}

func sqliteListEntriesByIDs(db *sql.DB, ids []int) ([]Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	entriesByID := make(map[int]Entry, len(ids))
	foundIDs := make([]int, 0, len(ids))
	seenIDs := make(map[int]struct{}, len(ids))
	for start := 0; start < len(ids); start += sqliteVariableBatchSize {
		end := start + sqliteVariableBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch))
		for _, id := range batch {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}

		query := fmt.Sprintf(`
			SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
			FROM entries
			WHERE id IN (%s)
		`, strings.Join(placeholders, ", "))

		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query sqlite entries by ids: %w", err)
		}
		for rows.Next() {
			entry, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan sqlite entry by ids: %w", err)
			}
			entriesByID[entry.ID] = entry
			if _, ok := seenIDs[entry.ID]; !ok {
				seenIDs[entry.ID] = struct{}{}
				foundIDs = append(foundIDs, entry.ID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate sqlite entries by ids: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close sqlite entry rows: %w", err)
		}
	}

	tagsByEntryID, err := loadTags(db, foundIDs)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(foundIDs))
	for _, id := range ids {
		entry, ok := entriesByID[id]
		if !ok {
			continue
		}
		entry.Tags = tagsByEntryID[id]
		entries = append(entries, entry)
	}

	return entries, nil
}

func sqliteSearchEntries(db *sql.DB, q Query) ([]Entry, error) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(q.Text)))
	if len(tokens) == 0 {
		return sqliteListEntries(db, q)
	}

	clauses, filterArgs := sqliteEntryFilter(q)
	scoreParts := make([]string, 0, len(tokens)*4)
	scoreArgs := make([]any, 0, len(tokens)*4)
	for _, token := range tokens {
		scoreParts = append(scoreParts,
			`CASE WHEN instr(lower(title), ?) > 0 THEN 5 ELSE 0 END`,
			`CASE WHEN instr(lower(body), ?) > 0 THEN 2 ELSE 0 END`,
			`CASE WHEN EXISTS (SELECT 1 FROM entry_tags WHERE entry_tags.entry_id = entries.id AND instr(tag, ?) > 0) THEN 3 ELSE 0 END`,
			`CASE WHEN instr(lower(origin), ?) > 0 THEN 1 ELSE 0 END`,
		)
		scoreArgs = append(scoreArgs, token, token, token, token)
	}

	args := append(scoreArgs, filterArgs...)
	query := `
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM (
			SELECT
				id,
				depth,
				title,
				body,
				source_ref,
				origin,
				created_at,
				updated_at,
				(` + strings.Join(scoreParts, " + ") + `) AS score
			FROM entries
			WHERE ` + strings.Join(clauses, " AND ") + `
		)
		WHERE score > 0
		ORDER BY score DESC, updated_at DESC, id DESC`
	if q.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, q.Limit)
	}

	entries, err := sqliteQueryEntries(db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite search fallback entries: %w", err)
	}
	return entries, nil
}

func sqliteSyncSnapshot(db *sql.DB, snapshot Snapshot) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite metadata sync: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM entry_tags`); err != nil {
		return fmt.Errorf("clear sqlite entry tags: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM entries`); err != nil {
		return fmt.Errorf("clear sqlite entries: %w", err)
	}

	for _, entry := range snapshot.Entries {
		if _, err := tx.Exec(
			`INSERT INTO entries (id, depth, title, body, source_ref, origin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.ID,
			entry.Depth,
			entry.Title,
			entry.Body,
			strings.TrimSpace(entry.SourceRef),
			strings.TrimSpace(entry.Origin),
			entry.CreatedAt.UTC().Format(time.RFC3339Nano),
			entry.UpdatedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("insert sqlite entry %d: %w", entry.ID, err)
		}
		for _, tag := range entry.Tags {
			if _, err := tx.Exec(
				`INSERT INTO entry_tags (entry_id, tag) VALUES (?, ?)`,
				entry.ID,
				strings.ToLower(strings.TrimSpace(tag)),
			); err != nil {
				return fmt.Errorf("insert sqlite entry tag for %d: %w", entry.ID, err)
			}
		}
	}
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(snapshot.NextID)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite metadata sync: %w", err)
	}
	return nil
}

func sqliteUpsertEntry(tx *sql.Tx, entry Entry) error {
	if _, err := tx.Exec(`
		INSERT INTO entries (id, depth, title, body, source_ref, origin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			depth = excluded.depth,
			title = excluded.title,
			body = excluded.body,
			source_ref = excluded.source_ref,
			origin = excluded.origin,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at
	`, entry.ID, entry.Depth, entry.Title, entry.Body, strings.TrimSpace(entry.SourceRef), strings.TrimSpace(entry.Origin), entry.CreatedAt.UTC().Format(time.RFC3339Nano), entry.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upsert sqlite entry %d: %w", entry.ID, err)
	}
	return nil
}

func sqliteReplaceEntryTags(tx *sql.Tx, entryID int, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM entry_tags WHERE entry_id = ?`, entryID); err != nil {
		return fmt.Errorf("clear sqlite entry tags for %d: %w", entryID, err)
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO entry_tags (entry_id, tag) VALUES (?, ?)`, entryID, strings.ToLower(strings.TrimSpace(tag))); err != nil {
			return fmt.Errorf("insert sqlite entry tag for %d: %w", entryID, err)
		}
	}
	return nil
}

func sqliteDeleteEntry(tx *sql.Tx, id int) error {
	if _, err := tx.Exec(`DELETE FROM entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete sqlite entry %d: %w", id, err)
	}
	return nil
}

func sqliteLoadSnapshot(db *sql.DB) (Snapshot, error) {
	rows, err := db.Query(`
		SELECT id, depth, title, body, source_ref, origin, created_at, updated_at
		FROM entries
		ORDER BY id
	`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("query sqlite snapshot entries: %w", err)
	}
	defer rows.Close()

	entries := []Entry{}
	entryIDs := []int{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return Snapshot{}, fmt.Errorf("scan sqlite snapshot entry: %w", err)
		}
		entries = append(entries, entry)
		entryIDs = append(entryIDs, entry.ID)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate sqlite snapshot entries: %w", err)
	}

	tagsByEntryID, err := loadTags(db, entryIDs)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range entries {
		entries[i].Tags = tagsByEntryID[entries[i].ID]
	}

	nextID, err := sqliteNextID(db)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Version: currentVersion,
		NextID:  nextID,
		Entries: entries,
	}, nil
}

func sqliteNextID(db *sql.DB) (int, error) {
	value, err := metaValueDB(db, metaKeyNextID)
	if err == nil {
		nextID, convErr := strconv.Atoi(value)
		if convErr != nil {
			return 0, fmt.Errorf("parse sqlite next_id: %w", convErr)
		}
		if nextID < 1 {
			return 1, nil
		}
		return nextID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("query sqlite next_id: %w", err)
	}
	return sqliteFallbackNextID(db)
}

func sqliteFallbackNextID(db *sql.DB) (int, error) {
	var maxID sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(id) FROM entries`).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("query sqlite max id: %w", err)
	}
	if !maxID.Valid || maxID.Int64 < 1 {
		return 1, nil
	}
	return int(maxID.Int64) + 1, nil
}
