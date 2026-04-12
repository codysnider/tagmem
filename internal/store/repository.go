package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	path       string
	indexPath  string
	provider   vector.Provider
	now        func() time.Time
	db         *chromem.DB
	collection *chromem.Collection
	mu         sync.RWMutex
	snapshot   Snapshot
	loaded     bool
	queryCache map[string][]float32
}

func NewRepository(path, indexPath string, provider vector.Provider) *Repository {
	return &Repository{
		path:       path,
		indexPath:  indexPath,
		provider:   provider,
		now:        time.Now,
		queryCache: map[string][]float32{},
	}
}

func (r *Repository) Init() error {
	if _, err := os.Stat(r.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat store: %w", err)
	}

	snapshot := Snapshot{
		Version: currentVersion,
		NextID:  1,
		Entries: []Entry{},
	}

	if err := r.save(snapshot); err != nil {
		return err
	}

	return r.ensureIndex(snapshot)
}

func (r *Repository) RebuildIndex() error {
	snapshot, err := r.load()
	if err != nil {
		return err
	}
	if err := r.openIndex(); err != nil {
		return err
	}
	if err := r.db.DeleteCollection(collectionName); err != nil {
		return fmt.Errorf("reset vector collection: %w", err)
	}
	r.collection = nil
	return r.ensureIndex(snapshot)
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
	snapshot, err := r.load()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(requests))
	now := r.now().UTC()
	for _, req := range requests {
		if req.Depth < 0 {
			return nil, fmt.Errorf("depth must be >= 0")
		}
		title := strings.TrimSpace(req.Title)
		body := strings.TrimSpace(req.Body)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}
		if body == "" {
			return nil, fmt.Errorf("body is required")
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
		entry := Entry{
			ID:        snapshot.NextID,
			Depth:     req.Depth,
			Title:     title,
			Body:      body,
			Tags:      r.tagsForEntry(title, body, req.Tags, req.Source, req.Origin),
			Source:    sourceText(body, req.Source),
			Origin:    strings.TrimSpace(req.Origin),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		snapshot.NextID++
		snapshot.Entries = append(snapshot.Entries, entry)
		entries = append(entries, entry)
	}
	if err := r.save(snapshot); err != nil {
		return nil, err
	}
	if err := r.indexEntries(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Repository) Get(id int) (Entry, bool, error) {
	snapshot, err := r.load()
	if err != nil {
		return Entry{}, false, err
	}

	for _, entry := range snapshot.Entries {
		if entry.ID == id {
			return entry, true, nil
		}
	}

	return Entry{}, false, nil
}

func (r *Repository) Delete(id int) (bool, error) {
	snapshot, err := r.load()
	if err != nil {
		return false, err
	}
	entries := make([]Entry, 0, len(snapshot.Entries))
	deleted := false
	for _, entry := range snapshot.Entries {
		if entry.ID == id {
			deleted = true
			continue
		}
		entries = append(entries, entry)
	}
	if !deleted {
		return false, nil
	}
	snapshot.Entries = entries
	if err := r.save(snapshot); err != nil {
		return false, err
	}
	if err := r.ensureIndex(snapshot); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) Update(id int, req AddEntry) (Entry, bool, error) {
	if req.Depth < 0 {
		return Entry{}, false, fmt.Errorf("depth must be >= 0")
	}
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" {
		return Entry{}, false, fmt.Errorf("title is required")
	}
	if body == "" {
		return Entry{}, false, fmt.Errorf("body is required")
	}
	snapshot, err := r.load()
	if err != nil {
		return Entry{}, false, err
	}
	updated := Entry{}
	found := false
	now := r.now().UTC()
	for i := range snapshot.Entries {
		if snapshot.Entries[i].ID != id {
			continue
		}
		found = true
		snapshot.Entries[i].Depth = req.Depth
		snapshot.Entries[i].Title = title
		snapshot.Entries[i].Body = body
		snapshot.Entries[i].Tags = r.tagsForEntry(title, body, req.Tags, req.Source, req.Origin)
		snapshot.Entries[i].Source = sourceText(body, req.Source)
		snapshot.Entries[i].Origin = strings.TrimSpace(req.Origin)
		snapshot.Entries[i].UpdatedAt = now
		updated = snapshot.Entries[i]
		break
	}
	if !found {
		return Entry{}, false, nil
	}
	if err := r.save(snapshot); err != nil {
		return Entry{}, false, err
	}
	if err := r.ensureIndex(snapshot); err != nil {
		return Entry{}, false, err
	}
	return updated, true, nil
}

func (r *Repository) List(q Query) ([]Entry, error) {
	snapshot, err := r.load()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if q.Depth != nil && entry.Depth != *q.Depth {
			continue
		}
		if q.Tag != "" && !hasTag(entry.Tags, q.Tag) {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	return limitEntries(entries, q.Limit), nil
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
	if text == "" {
		entries, err := r.List(q)
		if err != nil {
			return nil, err
		}
		return wrapSearchEntries(entries), nil
	}
	queryKeywords := retrieval.ExtractKeywords(text)
	if len(queryKeywords) == 0 {
		entries, err := r.List(q)
		if err != nil {
			return nil, err
		}
		return wrapSearchEntries(entries), nil
	}

	snapshot, err := r.load()
	if err != nil {
		return nil, err
	}
	if err := r.ensureIndex(snapshot); err != nil {
		return nil, err
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 25
	}
	count := r.collection.Count()
	if count == 0 {
		return nil, nil
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
		return nil, fmt.Errorf("embed query: %w", err)
	}
	results, err := r.collection.QueryEmbedding(context.Background(), queryEmbedding, candidateLimit, where, nil)
	if err != nil {
		return nil, fmt.Errorf("query vector index: %w", err)
	}

	entriesByID := make(map[string]Entry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entriesByID[strconv.Itoa(entry.ID)] = entry
	}

	queryFeatures := retrieval.ExtractClaimFeatures(text)
	scored := make([]searchScoredResult, 0, len(results))
	for _, result := range results {
		entry, ok := entriesByID[result.ID]
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
		return wrapSearchEntries(r.searchFallback(snapshot, q)), nil
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

	searchResults := make([]SearchResult, 0, len(filtered))
	for _, result := range filtered {
		searchResults = append(searchResults, SearchResult{
			Entry:         result.entry,
			SupportCount:  result.supportCount,
			SourceKinds:   result.sourceKinds,
			ConflictCount: result.conflicts,
		})
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
	if vector, ok := r.queryCache[text]; ok {
		r.mu.RUnlock()
		return vector, nil
	}
	r.mu.RUnlock()
	vector, err := r.provider.Func(context.Background(), text)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.queryCache[text] = vector
	r.mu.Unlock()
	return vector, nil
}

func (r *Repository) DepthCounts() ([]DepthSummary, error) {
	snapshot, err := r.load()
	if err != nil {
		return nil, err
	}

	counts := map[int]int{}
	for _, entry := range snapshot.Entries {
		counts[entry.Depth]++
	}

	summaries := make([]DepthSummary, 0, len(counts))
	for depth, count := range counts {
		summaries = append(summaries, DepthSummary{Depth: depth, Count: count})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Depth < summaries[j].Depth
	})

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
	snapshot, err := r.load()
	if err != nil {
		return nil, err
	}
	if err := r.ensureIndex(snapshot); err != nil {
		return nil, err
	}
	count := r.collection.Count()
	if count == 0 {
		return nil, nil
	}
	limit := 5
	if count < limit {
		limit = count
	}
	results, err := r.collection.Query(context.Background(), content, limit, nil, nil)
	if err != nil {
		return nil, err
	}
	entriesByID := make(map[string]Entry, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entriesByID[strconv.Itoa(entry.ID)] = entry
	}
	matches := make([]DuplicateMatch, 0, len(results))
	for _, result := range results {
		entry, ok := entriesByID[result.ID]
		if !ok {
			continue
		}
		similarity := float64(result.Similarity)
		if similarity < threshold {
			continue
		}
		matches = append(matches, DuplicateMatch{Entry: entry, Similarity: similarity})
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})
	return matches, nil
}

func (r *Repository) load() (Snapshot, error) {
	r.mu.RLock()
	if r.loaded {
		snapshot := r.snapshot
		r.mu.RUnlock()
		return snapshot, nil
	}
	r.mu.RUnlock()

	if err := r.Init(); err != nil {
		return Snapshot{}, err
	}

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
	for i := range snapshot.Entries {
		snapshot.Entries[i] = normalizeLoadedEntry(snapshot.Entries[i])
	}

	r.mu.Lock()
	r.snapshot = snapshot
	r.loaded = true
	r.mu.Unlock()

	return snapshot, nil
}

func (r *Repository) save(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}

	snapshot.Version = currentVersion
	data, err := json.MarshalIndent(snapshot, "", "  ")
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

	r.mu.Lock()
	r.snapshot = snapshot
	r.loaded = true
	r.mu.Unlock()

	return nil
}

func (r *Repository) ensureIndex(snapshot Snapshot) error {
	if err := r.openIndex(); err != nil {
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

func (r *Repository) openIndex() error {
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

func (r *Repository) indexEntry(entry Entry) error {
	return r.indexEntries([]Entry{entry})
}

func (r *Repository) indexEntries(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := r.openIndex(); err != nil {
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

func normalizeLoadedEntry(entry Entry) Entry {
	entry.Source = strings.TrimSpace(entry.Source)
	entry.Origin = strings.TrimSpace(entry.Origin)
	if entry.Origin == "" && looksLikeLegacyOrigin(entry.Source) {
		entry.Origin = entry.Source
		entry.Source = strings.TrimSpace(entry.Body)
	}
	if entry.Source == "" {
		entry.Source = strings.TrimSpace(entry.Body)
	}
	return entry
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
