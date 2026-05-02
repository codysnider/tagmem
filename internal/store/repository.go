package store

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	chromem "github.com/philippgille/chromem-go"

	"github.com/codysnider/tagmem/internal/retrieval"
	"github.com/codysnider/tagmem/internal/tagging"
	"github.com/codysnider/tagmem/internal/vector"
)

const currentVersion = 1
const collectionName = "entries"
const semanticTailSlack = 0.12

type Entry struct {
	ID        int       `json:"id"`
	Depth     int       `json:"depth"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      []string  `json:"tags,omitempty"`
	Source    string    `json:"source,omitempty"`
	SourceRef string    `json:"source_ref,omitempty"`
	Origin    string    `json:"origin,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Snapshot struct {
	Version int     `json:"version"`
	NextID  int     `json:"next_id"`
	Entries []Entry `json:"entries"`
}

type AddEntry struct {
	Depth     int
	Title     string
	Body      string
	Tags      []string
	Source    string
	Origin    string
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

type Query struct {
	Depth *int
	Text  string
	Tag   string
	Limit int
}

type SearchResult struct {
	Entry         Entry `json:"entry"`
	SupportCount  int   `json:"support_count,omitempty"`
	SourceKinds   int   `json:"source_kinds,omitempty"`
	ConflictCount int   `json:"conflict_count,omitempty"`
}

type DuplicateMatch struct {
	Entry      Entry   `json:"entry"`
	Similarity float64 `json:"similarity"`
}

type searchScoredResult struct {
	entry        Entry
	rawText      string
	distance     float64
	overlap      float64
	similarity   float32
	updatedAtUTC time.Time
	features     retrieval.ClaimFeatures
	supportCount int
	sourceKinds  int
	conflicts    int
}

type DepthSummary struct {
	Depth int
	Count int
}

type Repository struct {
	path                     string
	metaPath                 string
	indexPath                string
	sourceDir                string
	provider                 vector.Provider
	now                      func() time.Time
	indexEntriesImpl         func([]Entry) error
	deleteIndexedEntriesImpl func(...int) error
	metaDB                   *sql.DB
	db                       *chromem.DB
	collection               *chromem.Collection
	opMu                     sync.Mutex
	mu                       sync.RWMutex
	snapshot                 Snapshot
	loaded                   bool
	storeStamp               fileStamp
	queryCache               *queryEmbeddingCache
	loadSnapshotFromStoreFn  func() (Snapshot, error)
}

type fileStamp struct {
	size    int64
	modTime int64
}

func NewRepository(path, indexPath string, provider vector.Provider) *Repository {
	repo := &Repository{
		path:       path,
		metaPath:   filepath.Join(filepath.Dir(path), "store.db"),
		indexPath:  indexPath,
		sourceDir:  filepath.Join(filepath.Dir(path), "sources"),
		provider:   provider,
		now:        time.Now,
		queryCache: newQueryEmbeddingCache(256),
	}
	repo.indexEntriesImpl = repo.indexEntriesLocked
	repo.deleteIndexedEntriesImpl = repo.deleteIndexedEntriesLocked
	repo.loadSnapshotFromStoreFn = repo.loadSnapshotFromStoreLocked
	return repo
}

func (r *Repository) Init() error {
	return r.withExclusiveLock(func() error {
		dbAlreadyExists := false
		if _, err := os.Stat(r.metaPath); err == nil {
			dbAlreadyExists = true
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat metadata store: %w", err)
		}
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		if dbAlreadyExists {
			if err := r.rebuildJSONMirrorFromSQLiteLocked(); err != nil {
				return err
			}
		}
		snapshot, err := r.loadLocked()
		if err != nil {
			return err
		}
		return r.ensureIndexLocked(snapshot)
	})
}

func (r *Repository) RebuildIndex() error {
	return r.withExclusiveLock(func() error {
		snapshot, err := r.loadLocked()
		if err != nil {
			return err
		}
		if err := r.rebuildIndexFromSnapshotLocked(snapshot); err != nil {
			return err
		}
		if err := r.setIndexStateLocked("ready"); err != nil {
			return err
		}
		return r.saveLocked(snapshot)
	})
}

func (r *Repository) Add(req AddEntry) (Entry, error) {
	entries, err := r.AddMany([]AddEntry{req})
	if err != nil {
		return Entry{}, err
	}
	return entries[0], nil
}

func (r *Repository) AddMany(requests []AddEntry) ([]Entry, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	entries := make([]Entry, 0, len(requests))
	err := r.withExclusiveLock(func() error {
		snapshot, err := r.loadLocked()
		if err != nil {
			return err
		}
		writeMirror, err := r.shouldWriteJSONMirrorAfterMutationLocked()
		if err != nil {
			return err
		}
		now := r.now().UTC()
		for _, req := range requests {
			if req.Depth < 0 {
				return fmt.Errorf("depth must be >= 0")
			}
			title := strings.TrimSpace(req.Title)
			body := strings.TrimSpace(req.Body)
			if title == "" {
				return fmt.Errorf("title is required")
			}
			if body == "" {
				return fmt.Errorf("body is required")
			}
			createdAt := now
			updatedAt := now
			if req.CreatedAt != nil {
				createdAt = req.CreatedAt.UTC()
			}
			if req.UpdatedAt != nil {
				updatedAt = req.UpdatedAt.UTC()
			} else if req.CreatedAt != nil {
				updatedAt = createdAt
			}
			fullSource := sourceText(body, req.Source)
			sourceRef, err := r.ensureSourceBlob(fullSource)
			if err != nil {
				return err
			}
			storedEntry := Entry{
				ID:        snapshot.NextID,
				Depth:     req.Depth,
				Title:     title,
				Body:      body,
				Tags:      r.tagsForEntry(title, body, req.Tags, req.Source, req.Origin),
				SourceRef: sourceRef,
				Origin:    strings.TrimSpace(req.Origin),
				CreatedAt: createdAt,
				UpdatedAt: updatedAt,
			}
			snapshot.NextID++
			snapshot.Entries = append(snapshot.Entries, storedEntry)
			entry := storedEntry
			entry.Source = fullSource
			entries = append(entries, entry)
		}
		if err := r.applyMetadataMutationLocked(snapshot.NextID, func(tx *sql.Tx) error {
			for _, entry := range entries {
				if err := sqliteUpsertEntry(tx, entry); err != nil {
					return err
				}
				if err := sqliteReplaceEntryTags(tx, entry.ID, entry.Tags); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		r.cacheSnapshotLocked(snapshot)
		if err := r.indexEntriesImpl(entries); err != nil {
			return err
		}
		if err := r.setIndexStateLocked("ready"); err != nil {
			return err
		}
		if writeMirror {
			return r.saveStoreLocked(snapshot)
		}
		return r.markJSONMirrorMissingLocked(snapshot.NextID)
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Repository) Get(id int) (Entry, bool, error) {
	entry := Entry{}
	found := false
	err := r.withSharedLock(func() error {
		if err := r.ensureSQLiteReadModelLocked(); err != nil {
			return err
		}
		var err error
		entry, found, err = sqliteGetEntry(r.metaDB, id)
		return err
	})
	if err != nil || !found {
		return entry, found, err
	}
	hydrated, err := r.hydrateEntrySource(entry)
	if err != nil {
		return Entry{}, false, err
	}
	return hydrated, true, nil
}

func (r *Repository) Delete(id int) (bool, error) {
	deleted := false
	err := r.withExclusiveLock(func() error {
		snapshot, err := r.loadLocked()
		if err != nil {
			return err
		}
		writeMirror, err := r.shouldWriteJSONMirrorAfterMutationLocked()
		if err != nil {
			return err
		}
		entries := make([]Entry, 0, len(snapshot.Entries))
		for _, entry := range snapshot.Entries {
			if entry.ID == id {
				deleted = true
				continue
			}
			entries = append(entries, entry)
		}
		if !deleted {
			return nil
		}
		snapshot.Entries = entries
		if err := r.applyMetadataMutationLocked(snapshot.NextID, func(tx *sql.Tx) error {
			return sqliteDeleteEntry(tx, id)
		}); err != nil {
			return err
		}
		r.cacheSnapshotLocked(snapshot)
		if err := r.deleteIndexedEntriesImpl(id); err != nil {
			return err
		}
		if err := r.setIndexStateLocked("ready"); err != nil {
			return err
		}
		if writeMirror {
			return r.saveStoreLocked(snapshot)
		}
		return r.markJSONMirrorMissingLocked(snapshot.NextID)
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

func (r *Repository) Update(id int, req AddEntry) (Entry, bool, error) {
	updated := Entry{}
	found := false
	err := r.withExclusiveLock(func() error {
		if req.Depth < 0 {
			return fmt.Errorf("depth must be >= 0")
		}
		title := strings.TrimSpace(req.Title)
		body := strings.TrimSpace(req.Body)
		if title == "" {
			return fmt.Errorf("title is required")
		}
		if body == "" {
			return fmt.Errorf("body is required")
		}
		snapshot, err := r.loadLocked()
		if err != nil {
			return err
		}
		writeMirror, err := r.shouldWriteJSONMirrorAfterMutationLocked()
		if err != nil {
			return err
		}
		now := r.now().UTC()
		for i := range snapshot.Entries {
			if snapshot.Entries[i].ID != id {
				continue
			}
			found = true
			fullSource := sourceText(body, req.Source)
			sourceRef, err := r.ensureSourceBlob(fullSource)
			if err != nil {
				return err
			}
			snapshot.Entries[i].Depth = req.Depth
			snapshot.Entries[i].Title = title
			snapshot.Entries[i].Body = body
			snapshot.Entries[i].Tags = r.tagsForEntry(title, body, req.Tags, req.Source, req.Origin)
			snapshot.Entries[i].Source = ""
			snapshot.Entries[i].SourceRef = sourceRef
			snapshot.Entries[i].Origin = strings.TrimSpace(req.Origin)
			snapshot.Entries[i].UpdatedAt = now
			updated = snapshot.Entries[i]
			updated.Source = fullSource
			break
		}
		if !found {
			return nil
		}
		if err := r.applyMetadataMutationLocked(snapshot.NextID, func(tx *sql.Tx) error {
			if err := sqliteUpsertEntry(tx, updated); err != nil {
				return err
			}
			return sqliteReplaceEntryTags(tx, updated.ID, updated.Tags)
		}); err != nil {
			return err
		}
		r.cacheSnapshotLocked(snapshot)
		if err := r.deleteIndexedEntriesImpl(updated.ID); err != nil {
			return err
		}
		if err := r.indexEntriesImpl([]Entry{updated}); err != nil {
			return err
		}
		if err := r.setIndexStateLocked("ready"); err != nil {
			return err
		}
		if writeMirror {
			return r.saveStoreLocked(snapshot)
		}
		return r.markJSONMirrorMissingLocked(snapshot.NextID)
	})
	if err != nil {
		return Entry{}, false, err
	}
	return updated, found, nil
}

func (r *Repository) List(q Query) ([]Entry, error) {
	entries, err := r.listEntries(q)
	if err != nil {
		return nil, err
	}
	return r.hydrateEntrySources(entries)
}

func (r *Repository) ListMetadata(q Query) ([]Entry, error) {
	return r.listEntries(q)
}

func (r *Repository) listEntries(q Query) ([]Entry, error) {
	entries := []Entry{}
	err := r.withSharedLock(func() error {
		if err := r.ensureSQLiteReadModelLocked(); err != nil {
			return err
		}
		var err error
		entries, err = sqliteListEntries(r.metaDB, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Repository) Search(q Query) ([]Entry, error) {
	results, err := r.SearchDetailed(q)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(results))
	for _, result := range results {
		entries = append(entries, result.Entry)
	}
	return entries, nil
}

func (r *Repository) SearchDetailed(q Query) ([]SearchResult, error) {
	text := strings.TrimSpace(q.Text)
	queryKeywords := retrieval.ExtractKeywords(text)
	if text == "" || len(queryKeywords) == 0 {
		if err := r.withSharedLock(func() error {
			if err := r.ensureSQLiteReadModelLocked(); err != nil {
				return err
			}
			return r.ensureIndexStateReadyLocked()
		}); err != nil {
			return nil, err
		}
		entries, err := r.List(q)
		if err != nil {
			return nil, err
		}
		return wrapSearchEntries(entries), nil
	}
	var searchResults []SearchResult
	err := r.withSharedLock(func() error {
		if err := r.ensureSQLiteReadModelLocked(); err != nil {
			return err
		}
		if err := r.ensureIndexStateReadyLocked(); err != nil {
			return err
		}
		if err := r.openIndexLocked(); err != nil {
			return err
		}

		limit := q.Limit
		if limit <= 0 {
			limit = 25
		}
		count := r.collection.Count()
		if count == 0 {
			searchResults = nil
			return nil
		}
		candidateLimit := limit * 4
		if candidateLimit < limit {
			candidateLimit = limit
		}
		if candidateLimit > count {
			candidateLimit = count
		}

		where := map[string]string(nil)
		if q.Depth != nil {
			where = map[string]string{"depth": strconv.Itoa(*q.Depth)}
		}

		queryEmbedding, err := r.queryEmbedding(text)
		if err != nil {
			return fmt.Errorf("embed query: %w", err)
		}
		results, err := r.collection.QueryEmbedding(context.Background(), queryEmbedding, candidateLimit, where, nil)
		if err != nil {
			return fmt.Errorf("query vector index: %w", err)
		}

		candidateIDs := make([]int, 0, len(results))
		for _, result := range results {
			id, err := strconv.Atoi(result.ID)
			if err != nil {
				continue
			}
			candidateIDs = append(candidateIDs, id)
		}

		candidateEntries, err := sqliteListEntriesByIDs(r.metaDB, candidateIDs)
		if err != nil {
			return err
		}
		entriesByID := make(map[int]Entry, len(candidateEntries))
		for _, entry := range candidateEntries {
			entriesByID[entry.ID] = entry
		}

		queryFeatures := retrieval.ExtractClaimFeatures(text)
		scored := make([]searchScoredResult, 0, len(results))
		for _, result := range results {
			id, err := strconv.Atoi(result.ID)
			if err != nil {
				continue
			}
			entry, ok := entriesByID[id]
			if !ok {
				continue
			}
			if q.Tag != "" && !hasTag(entry.Tags, q.Tag) {
				continue
			}

			overlap := retrieval.KeywordOverlap(queryKeywords, entrySearchText(entry))
			fusedDistance := retrieval.FuseSimilarity(result.Similarity, overlap)
			if q.Depth == nil {
				fusedDistance *= depthPenalty(entry.Depth)
			}

			rawText := entry.Title + "\n\n" + entry.Body
			features := retrieval.ExtractClaimFeatures(rawText)
			fusedDistance *= claimDistancePenalty(queryFeatures, features, strings.ToLower(rawText))
			scored = append(scored, searchScoredResult{
				entry:        entry,
				rawText:      strings.ToLower(rawText),
				distance:     fusedDistance,
				overlap:      overlap,
				similarity:   result.Similarity,
				updatedAtUTC: entry.UpdatedAt.UTC(),
				features:     features,
			})
		}

		if len(scored) == 0 {
			fallbackEntries, err := sqliteSearchEntries(r.metaDB, q)
			if err != nil {
				return err
			}
			fallbackResults, err := r.hydrateSearchResults(wrapSearchEntries(fallbackEntries))
			if err != nil {
				return err
			}
			searchResults = fallbackResults
			return nil
		}

		type supportInfo struct {
			supportCount int
			sourceKinds  int
			conflicts    int
		}
		support := make(map[int]supportInfo, len(scored))
		for i := range scored {
			entryI := scored[i].entry
			sources := map[string]struct{}{sourceKind(entryI.Origin): {}}
			supportCount := 1
			conflicts := 0
			for j := range scored {
				if i == j {
					continue
				}
				entryJ := scored[j].entry
				if corroborates(scored[i], scored[j]) {
					supportCount++
					sources[sourceKind(entryJ.Origin)] = struct{}{}
				}
				if contradicts(scored[i], scored[j]) {
					conflicts++
				}
			}
			support[entryI.ID] = supportInfo{supportCount: supportCount, sourceKinds: len(sources), conflicts: conflicts}
		}
		now := r.now().UTC()
		for i := range scored {
			info := support[scored[i].entry.ID]
			scored[i].supportCount = info.supportCount
			scored[i].sourceKinds = info.sourceKinds
			scored[i].conflicts = info.conflicts
			scored[i].distance *= retrieval.RecencyPenalty(scored[i].entry.UpdatedAt.UTC(), now)
			scored[i].distance *= retrieval.ReinforcementPenalty(info.supportCount, info.sourceKinds)
			scored[i].distance *= retrieval.ContradictionPenalty(info.conflicts)
		}

		sort.Slice(scored, func(i, j int) bool {
			if scored[i].distance == scored[j].distance {
				if scored[i].updatedAtUTC.Equal(scored[j].updatedAtUTC) {
					return scored[i].entry.ID > scored[j].entry.ID
				}
				return scored[i].updatedAtUTC.After(scored[j].updatedAtUTC)
			}
			return scored[i].distance < scored[j].distance
		})

		bestDistance := scored[0].distance
		filtered := make([]searchScoredResult, 0, len(scored))
		for i, result := range scored {
			if i == 0 || result.overlap > 0 || result.distance <= bestDistance+semanticTailSlack {
				filtered = append(filtered, result)
			}
		}
		if len(filtered) == 0 {
			filtered = scored[:1]
		}

		searchResults = make([]SearchResult, 0, len(filtered))
		for _, result := range filtered {
			searchResults = append(searchResults, SearchResult{
				Entry:         result.entry,
				SupportCount:  result.supportCount,
				SourceKinds:   result.sourceKinds,
				ConflictCount: result.conflicts,
			})
		}
		searchResults, err = r.hydrateSearchResults(searchResults)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return limitSearchResults(searchResults, q.Limit), nil
}

func sourceKind(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "unknown"
	}
	if source == "clipboard" || source == "manual" {
		return source
	}
	parts := strings.Split(filepath.ToSlash(source), "/")
	if len(parts) == 0 {
		return source
	}
	return parts[0]
}

func corroborates(a, b searchScoredResult) bool {
	if a.entry.ID == b.entry.ID {
		return false
	}
	if len(sharedTags(a.entry.Tags, b.entry.Tags)) == 0 {
		return false
	}
	if retrieval.EntityOverlap(a.features, b.features) == 0 && retrieval.KeywordSetOverlap(a.features, b.features) < 0.5 {
		return false
	}
	if len(a.features.Values) > 0 && len(b.features.Values) > 0 && retrieval.ValueOverlap(a.features, b.features) == 0 {
		return false
	}
	return retrieval.KeywordSetOverlap(a.features, b.features) >= 0.5 || retrieval.ValueOverlap(a.features, b.features) >= 0.5
}

func contradicts(a, b searchScoredResult) bool {
	if a.entry.ID == b.entry.ID {
		return false
	}
	shared := sharedTags(a.entry.Tags, b.entry.Tags)
	if len(shared) == 0 {
		return false
	}
	if retrieval.EntityOverlap(a.features, b.features) == 0 && retrieval.KeywordSetOverlap(a.features, b.features) < 0.5 {
		return false
	}
	if len(a.features.Values) == 0 || len(b.features.Values) == 0 {
		return false
	}
	if retrieval.ValueOverlap(a.features, b.features) > 0 {
		return false
	}
	return true
}

func claimDistancePenalty(query, candidate retrieval.ClaimFeatures, candidateText string) float64 {
	penalty := 1.0
	if query.Environment != "" {
		if candidate.Environment == query.Environment {
			penalty *= 0.88
		} else if candidate.Environment != "" {
			penalty *= 1.10
		}
	}
	if query.Speaker != "" {
		if candidate.Speaker == query.Speaker {
			penalty *= 0.88
		} else if candidate.Speaker != "" {
			penalty *= 1.12
		}
	}
	if entity := retrieval.EntityOverlap(query, candidate); entity > 0 {
		penalty *= 1.0 - 0.12*entity
	}
	if values := retrieval.ValueOverlap(query, candidate); values > 0 {
		penalty *= 1.0 - 0.15*values
	}
	if kinds := retrieval.KeywordSetOverlap(retrieval.ClaimFeatures{Keywords: query.ValueKinds}, retrieval.ClaimFeatures{Keywords: candidate.ValueKinds}); kinds > 0 {
		penalty *= 1.0 - 0.08*kinds
	} else if len(query.ValueKinds) > 0 && len(candidate.ValueKinds) > 0 {
		penalty *= 1.08
	}
	if query.ExactWanted {
		penalty *= 1.0 - 0.10*candidate.Precision
		if candidate.Approximate {
			penalty *= 1.08
		}
	}
	if keywords := retrieval.KeywordSetOverlap(query, candidate); keywords > 0 {
		penalty *= 1.0 - 0.06*keywords
	}
	penalty *= intentPenalty(query, candidate, candidateText)
	if penalty < 0.70 {
		return 0.70
	}
	return penalty
}

func intentPenalty(query, candidate retrieval.ClaimFeatures, text string) float64 {
	penalty := 1.0
	switch query.Intent {
	case "suggestion":
		if strings.Contains(text, "you suggested") || strings.Contains(text, "you recommended") {
			penalty *= 0.72
		}
		if strings.Contains(text, "i suggested") || strings.Contains(text, "i recommended") {
			penalty *= 1.18
		}
		if strings.Contains(text, "we discussed") || strings.Contains(text, "follow-up") || strings.Contains(text, "notes") {
			penalty *= 1.14
		}
		if candidate.Assertion == "suggestion" {
			penalty *= 0.82
		}
	case "preference":
		if strings.Contains(text, "prefer") || strings.Contains(text, "favorite") || strings.Contains(text, "likes") || strings.Contains(text, "enjoys") {
			penalty *= 0.80
		}
		if strings.Contains(text, "sometimes") || strings.Contains(text, "also") {
			penalty *= 1.08
		}
		if candidate.Assertion == "preference" {
			penalty *= 0.84
		}
	case "current-state":
		if strings.Contains(text, "current") || strings.Contains(text, "is ") || strings.Contains(text, "defaults to") {
			penalty *= 0.86
		}
		if strings.Contains(text, "used to") || strings.Contains(text, "previously") || strings.Contains(text, "formerly") {
			penalty *= 1.18
		}
		switch candidate.State {
		case "asserted":
			penalty *= 0.82
		case "historical":
			penalty *= 1.18
		case "planned":
			penalty *= 1.10
		}
	case "temporal-event":
		if strings.Contains(text, "discussed") || strings.Contains(text, "planned") {
			penalty *= 1.10
		}
		if candidate.Assertion == "event" {
			penalty *= 0.84
		}
		if candidate.State == "planned" {
			penalty *= 1.12
		}
	case "value-lookup":
		if strings.Contains(text, "is ") || strings.Contains(text, "uses ") || strings.Contains(text, "runs ") || strings.Contains(text, "defaults to") {
			penalty *= 0.90
		}
		if candidate.State == "asserted" {
			penalty *= 0.90
		}
		if candidate.State == "historical" {
			penalty *= 1.10
		}
	}
	if query.Assertion != "" && candidate.Assertion != "" {
		if query.Assertion == candidate.Assertion {
			penalty *= 0.90
		} else {
			penalty *= 1.04
		}
	}
	return penalty
}

func sharedTags(a, b []string) []string {
	set := map[string]struct{}{}
	for _, tag := range a {
		set[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	out := []string{}
	for _, tag := range b {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if _, ok := set[tag]; ok {
			out = append(out, tag)
		}
	}
	return out
}

func (r *Repository) queryEmbedding(text string) ([]float32, error) {
	r.mu.RLock()
	if vector, ok := r.queryCache.get(text); ok {
		r.mu.RUnlock()
		return vector, nil
	}
	r.mu.RUnlock()
	vector, err := r.provider.Func(context.Background(), text)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.queryCache.put(text, vector)
	r.mu.Unlock()
	return vector, nil
}

func (r *Repository) DepthCounts() ([]DepthSummary, error) {
	summaries := []DepthSummary{}
	err := r.withSharedLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		var err error
		summaries, err = sqliteDepthCounts(r.metaDB)
		return err
	})
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

func (r *Repository) DuplicateCheck(content string, threshold float64) ([]DuplicateMatch, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}
	if threshold <= 0 {
		threshold = 0.9
	}
	var matches []DuplicateMatch
	err := r.withSharedLock(func() error {
		if err := r.ensureMetadataStoreLocked(); err != nil {
			return err
		}
		if err := r.ensureIndexStateReadyLocked(); err != nil {
			return err
		}
		if err := r.openIndexLocked(); err != nil {
			return err
		}
		entryCount, err := sqliteEntryCount(r.metaDB)
		if err != nil {
			return err
		}
		count := r.collection.Count()
		if count != entryCount {
			if entryCount < count {
				count = entryCount
			}
		}
		if count == 0 {
			matches = nil
			return nil
		}
		limit := 5
		if count < limit {
			limit = count
		}
		results, err := r.collection.Query(context.Background(), content, limit, nil, nil)
		if err != nil {
			return err
		}
		matchedIDs := make([]int, 0, len(results))
		similaritiesByID := make(map[int]float64, len(results))
		for _, result := range results {
			id, err := strconv.Atoi(result.ID)
			if err != nil {
				continue
			}
			similarity := float64(result.Similarity)
			if similarity < threshold {
				continue
			}
			matchedIDs = append(matchedIDs, id)
			similaritiesByID[id] = similarity
		}
		entries, err := sqliteListEntriesByIDs(r.metaDB, matchedIDs)
		if err != nil {
			return err
		}
		matches = make([]DuplicateMatch, 0, len(entries))
		for _, entry := range entries {
			similarity, ok := similaritiesByID[entry.ID]
			if !ok {
				continue
			}
			matches = append(matches, DuplicateMatch{Entry: entry, Similarity: similarity})
		}
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].Similarity > matches[j].Similarity
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range matches {
		hydrated, err := r.hydrateEntrySource(matches[i].Entry)
		if err != nil {
			return nil, err
		}
		matches[i].Entry = hydrated
	}
	return matches, nil
}

func (r *Repository) loadLocked() (Snapshot, error) {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return Snapshot{}, err
	}

	stamp, err := currentFileStamp(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			snapshot, loadErr := sqliteLoadSnapshot(r.metaDB)
			if loadErr != nil {
				return Snapshot{}, loadErr
			}
			metadataRevision, loadErr := r.metaRevisionLocked(metaKeyMetadataRevision)
			if loadErr != nil {
				return Snapshot{}, loadErr
			}
			jsonMirrorRevision, loadErr := r.metaRevisionLocked(metaKeyJSONMirrorRev)
			if loadErr != nil {
				return Snapshot{}, loadErr
			}
			if metadataRevision == 0 && jsonMirrorRevision == 0 && len(snapshot.Entries) == 0 && snapshot.NextID == 1 {
				if err := r.saveStoreLocked(snapshot); err != nil {
					return Snapshot{}, err
				}
				return snapshot, nil
			}
			r.cacheSnapshotLocked(snapshot)
			return snapshot, nil
		}
		return Snapshot{}, fmt.Errorf("stat store: %w", err)
	}

	r.mu.RLock()
	if r.loaded && r.storeStamp == stamp {
		snapshot := r.snapshot
		r.mu.RUnlock()
		return snapshot, nil
	}
	r.mu.RUnlock()

	metadataRevision, err := r.metaRevisionLocked(metaKeyMetadataRevision)
	if err != nil {
		return Snapshot{}, err
	}
	jsonMirrorRevision, err := r.metaRevisionLocked(metaKeyJSONMirrorRev)
	if err != nil {
		return Snapshot{}, err
	}
	if metadataRevision != jsonMirrorRevision {
		snapshot, err := sqliteLoadSnapshot(r.metaDB)
		if err != nil {
			return Snapshot{}, err
		}
		r.cacheSnapshotWithStampLocked(snapshot, stamp)
		return snapshot, nil
	}

	snapshot, err := sqliteLoadSnapshot(r.metaDB)
	if err != nil {
		return Snapshot{}, err
	}
	r.cacheSnapshotWithStampLocked(snapshot, stamp)
	return snapshot, nil
}

func (r *Repository) saveLocked(snapshot Snapshot) error {
	if err := r.saveStoreLocked(snapshot); err != nil {
		return err
	}
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	if err := sqliteSyncSnapshot(r.metaDB, r.snapshot); err != nil {
		return err
	}
	return nil
}

func (r *Repository) saveStoreLocked(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	prepared, err := r.prepareSnapshotForSave(snapshot)
	if err != nil {
		return err
	}

	prepared.Version = currentVersion
	data, err := json.MarshalIndent(prepared, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}

	tmpPath := r.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp store: %w", err)
	}

	if err := os.Rename(tmpPath, r.path); err != nil {
		return fmt.Errorf("replace store: %w", err)
	}

	stamp, err := currentFileStamp(r.path)
	if err != nil {
		return fmt.Errorf("stat saved store: %w", err)
	}

	r.mu.Lock()
	r.snapshot = prepared
	r.loaded = true
	r.storeStamp = stamp
	r.mu.Unlock()

	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	metadataRevision, err := r.metaRevisionLocked(metaKeyMetadataRevision)
	if err != nil {
		return err
	}
	if err := r.setMirrorStateLocked(metadataRevision, metadataRevision, formatFileStamp(stamp), prepared.NextID); err != nil {
		return err
	}

	return nil
}

func (r *Repository) applyMetadataMutationLocked(nextID int, mutate func(*sql.Tx) error) error {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	if nextID < 1 {
		nextID = 1
	}
	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite metadata mutation: %w", err)
	}
	defer tx.Rollback()
	metadataRevision, err := metaRevisionTx(tx, metaKeyMetadataRevision)
	if err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyIndexState, "dirty"); err != nil {
		return err
	}
	if err := mutate(tx); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(nextID)); err != nil {
		return err
	}
	if err := setMetaValue(tx, metaKeyMetadataRevision, strconv.Itoa(metadataRevision+1)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite metadata mutation: %w", err)
	}
	return nil
}

func (r *Repository) setIndexStateLocked(state string) error {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite meta update: %w", err)
	}
	defer tx.Rollback()
	if err := setMetaValue(tx, metaKeyIndexState, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite meta update: %w", err)
	}
	return nil
}

func (r *Repository) ensureIndexLocked(snapshot Snapshot) error {
	if err := r.ensureIndexStateReadyLocked(); err != nil {
		return err
	}

	if err := r.openIndexLocked(); err != nil {
		return err
	}

	if r.collection.Count() == len(snapshot.Entries) {
		return nil
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

func (r *Repository) ensureIndexStateReadyLocked() error {
	indexState, err := r.metaValueUnlocked(metaKeyIndexState)
	if err != nil {
		return err
	}
	if indexState == "ready" {
		return nil
	}
	r.db = nil
	r.collection = nil
	return fmt.Errorf("vector index needs repair: state is %s", indexState)
}

func (r *Repository) openIndexLocked() error {
	if r.collection != nil {
		return nil
	}

	db, err := chromem.NewPersistentDB(r.indexPath, true)
	if err != nil {
		return fmt.Errorf("open vector index: %w", err)
	}

	collection, err := db.GetOrCreateCollection(collectionName, nil, r.provider.Func)
	if err != nil {
		return fmt.Errorf("open vector collection: %w", err)
	}

	r.db = db
	r.collection = collection
	return nil
}

func (r *Repository) indexEntriesLocked(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := r.openIndexLocked(); err != nil {
		return err
	}
	documents, err := r.makeDocumentsWithEmbeddings(entries)
	if err != nil {
		return err
	}
	if err := r.collection.AddDocuments(context.Background(), documents, 1); err != nil {
		return fmt.Errorf("index entries: %w", err)
	}

	return nil
}

func (r *Repository) reindexEntriesLocked(entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := r.deleteIndexedEntriesLocked(entryIDs(entries...)...); err != nil {
		return err
	}
	return r.indexEntriesLocked(entries)
}

func (r *Repository) deleteIndexedEntriesLocked(ids ...int) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.openIndexLocked(); err != nil {
		return err
	}
	stringIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		stringIDs = append(stringIDs, strconv.Itoa(id))
	}
	if err := r.collection.Delete(context.Background(), nil, nil, stringIDs...); err != nil {
		return fmt.Errorf("delete indexed entries: %w", err)
	}
	return nil
}

func (r *Repository) withSharedLock(run func() error) error {
	return r.withLock(syscall.LOCK_SH, run)
}

func (r *Repository) withExclusiveLock(run func() error) error {
	return r.withLock(syscall.LOCK_EX, run)
}

func (r *Repository) withLock(how int, run func() error) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}
	file, err := os.OpenFile(r.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open store lock: %w", err)
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		return fmt.Errorf("lock store: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}()

	return run()
}

func (r *Repository) ensureMetadataStoreLocked() error {
	if r.metaDB != nil {
		return nil
	}
	if r.metaPath == "" {
		r.metaPath = r.metadataPath()
	}
	_, err := os.Stat(r.metaPath)
	dbAlreadyExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat metadata store: %w", err)
	}
	db, err := openMetadataDB(r.metaPath)
	if err != nil {
		return fmt.Errorf("open metadata store: %w", err)
	}
	r.metaDB = db
	if err := r.migrateLegacyStoreToSQLiteLocked(dbAlreadyExists); err != nil {
		return err
	}
	if err := r.ensureNextIDMetadataLocked(); err != nil {
		return err
	}
	return nil
}

func (r *Repository) ensureSQLiteReadModelLocked() error {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return err
	}
	return nil
}

func (r *Repository) loadSnapshotFromStoreLocked() (Snapshot, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read store: %w", err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode store: %w", err)
	}

	if snapshot.Version == 0 {
		snapshot.Version = currentVersion
	}
	if snapshot.NextID == 0 {
		snapshot.NextID = len(snapshot.Entries) + 1
	}
	if snapshot.Entries == nil {
		snapshot.Entries = []Entry{}
	}
	migratedSources := map[string]struct{}{}
	for i := range snapshot.Entries {
		entry, err := r.normalizeLoadedEntry(snapshot.Entries[i], migratedSources)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Entries[i] = entry
	}
	return snapshot, nil
}

func (r *Repository) cacheSnapshotLocked(snapshot Snapshot) {
	r.mu.Lock()
	r.snapshot = snapshot
	r.loaded = true
	r.mu.Unlock()
}

func (r *Repository) cacheSnapshotWithStampLocked(snapshot Snapshot, stamp fileStamp) {
	r.mu.Lock()
	previousStamp := r.storeStamp
	r.snapshot = snapshot
	r.loaded = true
	r.storeStamp = stamp
	if previousStamp != stamp {
		r.db = nil
		r.collection = nil
	}
	r.mu.Unlock()
}

func (r *Repository) metaRevisionLocked(key string) (int, error) {
	value, err := r.metaValueUnlocked(key)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", key, err)
	}
	revision, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return revision, nil
}

func (r *Repository) metaValueUnlocked(key string) (string, error) {
	if err := r.ensureMetadataStoreLocked(); err != nil {
		return "", err
	}
	value, err := metaValueDB(r.metaDB, key)
	if err != nil {
		return "", fmt.Errorf("query meta value %q: %w", key, err)
	}
	return value, nil
}

func metaRevisionTx(tx *sql.Tx, key string) (int, error) {
	value, err := metaValueTx(tx, key)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", key, err)
	}
	revision, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return revision, nil
}

func (r *Repository) setMirrorStateLocked(metadataRevision, jsonMirrorRevision int, mirrorStamp string, nextID int) error {
	tx, err := r.metaDB.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite mirror state update: %w", err)
	}
	defer tx.Rollback()
	if nextID < 1 {
		nextID = 1
	}
	if err := setMetaValue(tx, metaKeyNextID, strconv.Itoa(nextID)); err != nil {
		return err
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite mirror state update: %w", err)
	}
	return nil
}

func (r *Repository) shouldWriteJSONMirrorAfterMutationLocked() (bool, error) {
	if _, err := os.Stat(r.path); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat store: %w", err)
	}
	return false, nil
}

func (r *Repository) markJSONMirrorMissingLocked(nextID int) error {
	metadataRevision, err := r.metaRevisionLocked(metaKeyMetadataRevision)
	if err != nil {
		return err
	}
	jsonMirrorRevision, err := r.metaRevisionLocked(metaKeyJSONMirrorRev)
	if err != nil {
		return err
	}
	return r.setMirrorStateLocked(metadataRevision, jsonMirrorRevision, "", nextID)
}

func currentFileStamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

func formatFileStamp(stamp fileStamp) string {
	return strconv.FormatInt(stamp.size, 10) + ":" + strconv.FormatInt(stamp.modTime, 10)
}

func entryIDs(entries ...Entry) []int {
	ids := make([]int, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func (r *Repository) makeDocumentsWithEmbeddings(entries []Entry) ([]chromem.Document, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	documents := make([]chromem.Document, 0, len(entries))
	if r.provider.Batch == nil {
		for _, entry := range entries {
			documents = append(documents, makeDocument(entry))
		}
		return documents, nil
	}
	texts := make([]string, 0, len(entries))
	for _, entry := range entries {
		texts = append(texts, entry.Title+"\n\n"+entry.Body)
	}
	vectors, err := r.provider.Batch(context.Background(), texts)
	if err != nil {
		return nil, fmt.Errorf("embed documents: %w", err)
	}
	for i, entry := range entries {
		document := makeDocument(entry)
		document.Embedding = vectors[i]
		documents = append(documents, document)
	}
	return documents, nil
}

func makeDocument(entry Entry) chromem.Document {
	return chromem.Document{
		ID: strconv.Itoa(entry.ID),
		Metadata: map[string]string{
			"depth": strconv.Itoa(entry.Depth),
		},
		Content: entry.Title + "\n\n" + entry.Body,
	}
}

func (r *Repository) searchFallback(snapshot Snapshot, q Query) []Entry {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(q.Text)))
	if len(tokens) == 0 {
		entries, _ := r.List(q)
		return entries
	}

	type scoredEntry struct {
		entry Entry
		score int
	}

	results := make([]scoredEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if q.Depth != nil && entry.Depth != *q.Depth {
			continue
		}

		score := scoreEntry(entry, tokens)
		if score == 0 {
			continue
		}

		results = append(results, scoredEntry{entry: entry, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			if results[i].entry.UpdatedAt.Equal(results[j].entry.UpdatedAt) {
				return results[i].entry.ID > results[j].entry.ID
			}
			return results[i].entry.UpdatedAt.After(results[j].entry.UpdatedAt)
		}
		return results[i].score > results[j].score
	})

	entries := make([]Entry, 0, len(results))
	for _, result := range results {
		entries = append(entries, result.entry)
	}

	return limitEntries(entries, q.Limit)
}

func normalizeTags(tags []string) []string {
	cleaned := make([]string, 0, len(tags))
	seen := map[string]struct{}{}

	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		cleaned = append(cleaned, tag)
	}

	sort.Strings(cleaned)
	return cleaned
}

func scoreEntry(entry Entry, tokens []string) int {
	title := strings.ToLower(entry.Title)
	body := strings.ToLower(entry.Body)
	origin := strings.ToLower(entry.Origin)
	tags := strings.Join(entry.Tags, " ")

	total := 0
	for _, token := range tokens {
		if strings.Contains(title, token) {
			total += 5
		}
		if strings.Contains(body, token) {
			total += 2
		}
		if strings.Contains(tags, token) {
			total += 3
		}
		if strings.Contains(origin, token) {
			total += 1
		}
	}

	return total
}

func entrySearchText(entry Entry) string {
	parts := []string{entry.Title, entry.Body, strings.Join(entry.Tags, " "), entry.Origin}
	return strings.Join(parts, " ")
}

func (r *Repository) tagsForEntry(title, body string, explicitTags []string, source, origin string) []string {
	if len(explicitTags) > 0 {
		return normalizeTags(explicitTags)
	}
	content := sourceText(body, source)
	if content == "" {
		content = body
	}
	return tagging.BuildTags(originOrTitle(origin, title), content, tagging.ModeFiles, &r.provider)
}

func sourceText(body, source string) string {
	source = strings.TrimSpace(source)
	if source != "" {
		return source
	}
	return strings.TrimSpace(body)
}

func originOrTitle(origin, title string) string {
	origin = strings.TrimSpace(origin)
	if origin != "" {
		return origin
	}
	return strings.TrimSpace(title)
}

func (r *Repository) prepareSnapshotForSave(snapshot Snapshot) (Snapshot, error) {
	prepared := snapshot
	prepared.Entries = make([]Entry, 0, len(snapshot.Entries))
	seenSources := map[string]struct{}{}
	for _, entry := range snapshot.Entries {
		normalized, err := r.normalizeLoadedEntry(entry, seenSources)
		if err != nil {
			return Snapshot{}, err
		}
		prepared.Entries = append(prepared.Entries, normalized)
	}
	return prepared, nil
}

func (r *Repository) normalizeLoadedEntry(entry Entry, seenSources map[string]struct{}) (Entry, error) {
	entry.Source = strings.TrimSpace(entry.Source)
	entry.SourceRef = strings.TrimSpace(entry.SourceRef)
	entry.Origin = strings.TrimSpace(entry.Origin)
	if entry.Origin == "" && looksLikeLegacyOrigin(entry.Source) {
		entry.Origin = entry.Source
		entry.Source = strings.TrimSpace(entry.Body)
		entry.SourceRef = ""
	}
	if entry.SourceRef == "" {
		inlineSource := entry.Source
		if inlineSource == "" {
			inlineSource = strings.TrimSpace(entry.Body)
		}
		if inlineSource != "" {
			ref, err := r.ensureSourceBlobWithSeen(inlineSource, seenSources)
			if err != nil {
				return Entry{}, err
			}
			entry.SourceRef = ref
		}
	}
	if entry.SourceRef != "" {
		entry.Source = ""
	}
	return entry, nil
}

func (r *Repository) hydrateEntrySource(entry Entry) (Entry, error) {
	if strings.TrimSpace(entry.Source) != "" {
		return entry, nil
	}
	if entry.SourceRef == "" {
		entry.Source = strings.TrimSpace(entry.Body)
		return entry, nil
	}
	source, err := r.readSourceBlob(entry.SourceRef)
	if err != nil {
		return Entry{}, err
	}
	entry.Source = source
	return entry, nil
}

func (r *Repository) hydrateEntrySources(entries []Entry) ([]Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	hydrated := make([]Entry, 0, len(entries))
	cache := map[string]string{}
	for _, entry := range entries {
		copyEntry := entry
		if strings.TrimSpace(copyEntry.Source) == "" && copyEntry.SourceRef != "" {
			if source, ok := cache[copyEntry.SourceRef]; ok {
				copyEntry.Source = source
			} else {
				source, err := r.readSourceBlob(copyEntry.SourceRef)
				if err != nil {
					return nil, err
				}
				cache[copyEntry.SourceRef] = source
				copyEntry.Source = source
			}
		}
		if copyEntry.Source == "" {
			copyEntry.Source = strings.TrimSpace(copyEntry.Body)
		}
		hydrated = append(hydrated, copyEntry)
	}
	return hydrated, nil
}

func (r *Repository) hydrateSearchResults(results []SearchResult) ([]SearchResult, error) {
	if len(results) == 0 {
		return results, nil
	}
	entries := make([]Entry, 0, len(results))
	for _, result := range results {
		entries = append(entries, result.Entry)
	}
	hydratedEntries, err := r.hydrateEntrySources(entries)
	if err != nil {
		return nil, err
	}
	hydrated := make([]SearchResult, 0, len(results))
	for i, result := range results {
		result.Entry = hydratedEntries[i]
		hydrated = append(hydrated, result)
	}
	return hydrated, nil
}

func (r *Repository) ensureSourceBlob(source string) (string, error) {
	return r.ensureSourceBlobWithSeen(source, nil)
}

func (r *Repository) ensureSourceBlobWithSeen(source string, seen map[string]struct{}) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	ref := sourceRef(source)
	if seen != nil {
		if _, ok := seen[ref]; ok {
			return ref, nil
		}
		seen[ref] = struct{}{}
	}
	path := r.sourceBlobPath(ref)
	if _, err := os.Stat(path); err == nil {
		return ref, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat source blob: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create source blob dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ref+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp source blob: %w", err)
	}
	tmpPath := tmpFile.Name()
	gz := gzip.NewWriter(tmpFile)
	_, writeErr := io.WriteString(gz, source)
	closeErr := gz.Close()
	fileCloseErr := tmpFile.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write source blob: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close source blob gzip: %w", closeErr)
	}
	if fileCloseErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close source blob file: %w", fileCloseErr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replace source blob: %w", err)
	}
	return ref, nil
}

func (r *Repository) readSourceBlob(ref string) (string, error) {
	path := r.sourceBlobPath(ref)
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source blob: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open source blob gzip: %w", err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return "", fmt.Errorf("read source blob: %w", err)
	}
	return string(data), nil
}

func (r *Repository) sourceBlobPath(ref string) string {
	prefix := ref
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	return filepath.Join(r.sourceDir, prefix, ref+".txt.gz")
}

func sourceRef(source string) string {
	hash := sha256.Sum256([]byte(source))
	return hex.EncodeToString(hash[:])
}

func looksLikeLegacyOrigin(value string) bool {
	if value == "" {
		return false
	}
	if value == "manual" || value == "clipboard" {
		return true
	}
	value = filepath.ToSlash(value)
	return !strings.ContainsAny(value, "\n\r") && (strings.Contains(value, "/") || strings.Contains(value, "."))
}

func hasTag(tags []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, tag := range tags {
		if strings.ToLower(strings.TrimSpace(tag)) == target {
			return true
		}
	}
	return false
}

func depthPenalty(depth int) float64 {
	if depth <= 0 {
		return 1.0
	}
	if depth > 5 {
		depth = 5
	}
	return 1.0 + (float64(depth) * 0.03)
}

func limitEntries(entries []Entry, limit int) []Entry {
	if limit <= 0 || limit >= len(entries) {
		return entries
	}

	return entries[:limit]
}

func wrapSearchEntries(entries []Entry) []SearchResult {
	results := make([]SearchResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, SearchResult{Entry: entry})
	}
	return results
}

func limitSearchResults(results []SearchResult, limit int) []SearchResult {
	if limit <= 0 || limit >= len(results) {
		return results
	}

	return results[:limit]
}
