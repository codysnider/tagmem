package daemon

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/codysnider/tagmem/internal/bench"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/importer"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type Backend struct {
	repo     *store.Repository
	kg       *kg.Store
	diary    *diary.Store
	paths    xdg.Paths
	provider vector.Provider

	corpusMu    sync.RWMutex
	corpusCache map[string]corpusCacheEntry
}

type corpusCacheEntry struct {
	corpus        *bench.InterfaceCorpus
	documentCount int
}

func NewBackend(repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *Backend {
	return &Backend{repo: repo, kg: kgStore, diary: diaryStore, paths: paths, provider: provider, corpusCache: make(map[string]corpusCacheEntry)}
}

func (b *Backend) Close() error {
	if b == nil {
		return nil
	}

	b.corpusMu.Lock()
	entries := make([]corpusCacheEntry, 0, len(b.corpusCache))
	for key, entry := range b.corpusCache {
		entries = append(entries, entry)
		delete(b.corpusCache, key)
	}
	b.corpusMu.Unlock()

	var firstErr error
	for _, entry := range entries {
		if entry.corpus == nil {
			continue
		}
		if err := entry.corpus.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (b *Backend) Handle(ctx context.Context, request Request) Response {
	payload, err := b.dispatch(ctx, request.Command, request.Payload)
	if err != nil {
		return Response{ID: request.ID, Success: false, Error: err.Error()}
	}
	return Response{ID: request.ID, Success: true, Payload: payload}
}

func (b *Backend) dispatch(ctx context.Context, command string, payload map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(command) {
	case "status":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		depths, err := b.repo.DepthCounts()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"total_entries": len(entries),
			"depths":        depths,
			"tags":          taggraph.TagCounts(entries),
			"store_path":    b.paths.StorePath,
			"index_path":    b.provider.IndexPath(b.paths.IndexDir),
			"embedding":     b.provider.Description,
		}, nil
	case "paths":
		return map[string]any{
			"data":   b.paths.DataDir,
			"config": b.paths.ConfigDir,
			"cache":  b.paths.CacheDir,
			"socket": b.paths.SocketPath,
			"store":  b.paths.StorePath,
			"index":  b.provider.IndexPath(b.paths.IndexDir),
			"diary":  b.paths.DiaryDir,
			"kg":     b.paths.KGPath,
		}, nil
	case "list_entries":
		query, err := queryFromPayload(payload, 25, false)
		if err != nil {
			return nil, err
		}
		entries, err := b.repo.List(query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil
	case "search":
		query, err := queryFromPayload(payload, 5, true)
		if err != nil {
			return nil, err
		}
		results, err := b.repo.SearchDetailed(query)
		if err != nil {
			return nil, err
		}
		entries := make([]store.Entry, 0, len(results))
		for _, result := range results {
			entries = append(entries, result.Entry)
		}
		return map[string]any{"entries": entries, "results": results}, nil
	case "show_entry":
		id, err := requiredInt(payload, "id")
		if err != nil {
			return nil, err
		}
		entry, ok, err := b.repo.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("entry %d not found", id)
		}
		return map[string]any{"entry": entry}, nil
	case "check_duplicate":
		matches, err := b.repo.DuplicateCheck(stringValue(payload, "content"), floatValue(payload, "threshold"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"matches": matches}, nil
	case "add_entry":
		depth := 1
		if value, ok, err := optionalInt(payload, "depth"); err != nil {
			return nil, err
		} else if ok && value > 0 {
			depth = value
		}
		title, err := requiredString(payload, "title")
		if err != nil {
			return nil, err
		}
		body, err := requiredString(payload, "body")
		if err != nil {
			return nil, err
		}
		createdAt, err := optionalTime(payload, "created_at")
		if err != nil {
			return nil, err
		}
		updatedAt, err := optionalTime(payload, "updated_at")
		if err != nil {
			return nil, err
		}
		entry, err := b.repo.Add(store.AddEntry{
			Depth:     depth,
			Title:     title,
			Body:      body,
			Tags:      stringSlice(payload, "tags"),
			Source:    stringValue(payload, "source"),
			Origin:    stringValue(payload, "origin"),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"entry": entry}, nil
	case "delete_entry":
		id, err := requiredInt(payload, "id")
		if err != nil {
			return nil, err
		}
		deleted, err := b.repo.Delete(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": deleted, "id": id}, nil
	case "list_depths":
		depths, err := b.repo.DepthCounts()
		if err != nil {
			return nil, err
		}
		return map[string]any{"depths": depths}, nil
	case "list_tags":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"tags": taggraph.TagCounts(entries)}, nil
	case "get_tag_map":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"tag_map": taggraph.DepthTagMap(entries)}, nil
	case "graph_traverse":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"edges": taggraph.Traverse(entries, stringValue(payload, "start_tag"), intValue(payload, "max_hops", 0))}, nil
	case "find_bridges":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		var depthA, depthB *int
		if value, ok, err := optionalInt(payload, "depth_a"); err != nil {
			return nil, err
		} else if ok {
			depthA = &value
		}
		if value, ok, err := optionalInt(payload, "depth_b"); err != nil {
			return nil, err
		} else if ok {
			depthB = &value
		}
		return map[string]any{"bridges": taggraph.FindBridges(entries, depthA, depthB)}, nil
	case "graph_stats":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return taggraph.Stats(entries), nil
	case "kg_query":
		direction := stringValue(payload, "direction")
		if direction == "" {
			direction = "both"
		}
		facts, err := b.kg.Query(stringValue(payload, "entity"), stringValue(payload, "as_of"), direction)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": stringValue(payload, "entity"), "as_of": stringValue(payload, "as_of"), "facts": facts, "count": len(facts)}, nil
	case "kg_add":
		fact, err := b.kg.Add(stringValue(payload, "subject"), stringValue(payload, "predicate"), stringValue(payload, "object"), stringValue(payload, "valid_from"), stringValue(payload, "source_entry"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"fact": fact}, nil
	case "kg_invalidate":
		if err := b.kg.Invalidate(stringValue(payload, "subject"), stringValue(payload, "predicate"), stringValue(payload, "object"), stringValue(payload, "ended")); err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	case "kg_timeline":
		timeline, err := b.kg.Timeline(stringValue(payload, "entity"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": stringValue(payload, "entity"), "timeline": timeline, "count": len(timeline)}, nil
	case "kg_stats":
		stats, err := b.kg.Stats()
		if err != nil {
			return nil, err
		}
		return stats, nil
	case "diary_write":
		entry, err := b.diary.Write(stringValue(payload, "agent_name"), stringValue(payload, "entry"), stringValue(payload, "topic"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"entry": entry}, nil
	case "diary_read":
		entries, err := b.diary.Read(stringValue(payload, "agent_name"), intValue(payload, "last_n", 10))
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent": stringValue(payload, "agent_name"), "entries": entries, "showing": len(entries)}, nil
	case "doctor":
		return map[string]any{"report": b.provider.Doctor(ctx)}, nil
	case "ensure_corpus":
		return b.ensureCorpus(payload)
	case "search_corpus":
		return b.searchCorpus(payload)
	case "rebuild_index":
		if err := b.repo.RebuildIndex(); err != nil {
			return nil, err
		}
		return map[string]any{"rebuilt": true}, nil
	default:
		return nil, fmt.Errorf("unknown daemon command %q", command)
	}
}

func (b *Backend) ensureCorpus(payload map[string]any) (map[string]any, error) {
	var request EnsureCorpusPayload
	if err := DecodePayload(payload, &request); err != nil {
		return nil, err
	}
	request.Key = strings.TrimSpace(request.Key)
	if request.Key == "" {
		return nil, fmt.Errorf("key is required")
	}

	b.corpusMu.RLock()
	entry, ok := b.corpusCache[request.Key]
	b.corpusMu.RUnlock()
	if ok {
		return map[string]any{
			"key":            request.Key,
			"cache_status":   "hit",
			"document_count": entry.documentCount,
		}, nil
	}

	documents := make([]bench.InterfaceDocument, 0, len(request.Documents))
	for _, document := range request.Documents {
		id := strings.TrimSpace(document.ID)
		content := strings.TrimSpace(document.Content)
		if id == "" || content == "" {
			continue
		}
		mode := importer.Mode(strings.TrimSpace(document.Mode))
		if mode == "" {
			mode = importer.ModeConversations
		}
		depth := document.Depth
		if depth <= 0 {
			depth = 1
		}
		documents = append(documents, bench.InterfaceDocument{
			ID:        id,
			Content:   content,
			Mode:      mode,
			Extract:   strings.TrimSpace(document.Extract),
			Depth:     depth,
			CreatedAt: daemonCorpusTime(document.CreatedAt),
			UpdatedAt: daemonCorpusTime(document.UpdatedAt),
		})
	}

	corpus, err := bench.NewInterfaceCorpusBuilder(b.provider).NewCorpus(documents)
	if err != nil {
		return nil, fmt.Errorf("build corpus: %w", err)
	}

	b.corpusMu.Lock()
	defer b.corpusMu.Unlock()
	if entry, ok := b.corpusCache[request.Key]; ok {
		_ = corpus.Close()
		return map[string]any{
			"key":            request.Key,
			"cache_status":   "hit",
			"document_count": entry.documentCount,
		}, nil
	}
	b.corpusCache[request.Key] = corpusCacheEntry{corpus: corpus, documentCount: len(documents)}
	return map[string]any{
		"key":            request.Key,
		"cache_status":   "miss",
		"document_count": len(documents),
	}, nil
}

func (b *Backend) searchCorpus(payload map[string]any) (map[string]any, error) {
	var request SearchCorpusPayload
	if err := DecodePayload(payload, &request); err != nil {
		return nil, err
	}
	request.Key = strings.TrimSpace(request.Key)
	request.Query = strings.TrimSpace(request.Query)
	if request.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if request.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if request.Limit <= 0 {
		request.Limit = 5
	}

	b.corpusMu.RLock()
	entry, ok := b.corpusCache[request.Key]
	b.corpusMu.RUnlock()
	if !ok || entry.corpus == nil {
		return nil, fmt.Errorf("corpus %q not found", request.Key)
	}

	originIDs, err := entry.corpus.Search(request.Query, request.Limit)
	if err != nil {
		return nil, fmt.Errorf("search corpus: %w", err)
	}
	return map[string]any{
		"key":        request.Key,
		"origin_ids": originIDs,
	}, nil
}

func daemonCorpusTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func queryFromPayload(payload map[string]any, defaultLimit int, requireText bool) (store.Query, error) {
	query := store.Query{Limit: defaultLimit, Tag: stringValue(payload, "tag")}
	if payload == nil {
		if requireText {
			return store.Query{}, fmt.Errorf("query is required")
		}
		return query, nil
	}
	if depth, ok, err := optionalInt(payload, "depth"); err != nil {
		return store.Query{}, err
	} else if ok {
		query.Depth = &depth
	}
	if limit, ok, err := optionalInt(payload, "limit"); err != nil {
		return store.Query{}, err
	} else if ok && limit > 0 {
		query.Limit = limit
	}
	if requireText {
		text, err := requiredString(payload, "query")
		if err != nil {
			return store.Query{}, err
		}
		query.Text = text
	}
	return query, nil
}

func requiredString(payload map[string]any, key string) (string, error) {
	value := stringValue(payload, key)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func stringValue(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stringSlice(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		values = append(values, text)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func requiredInt(payload map[string]any, key string) (int, error) {
	value, ok, err := optionalInt(payload, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalInt(payload map[string]any, key string) (int, bool, error) {
	if payload == nil {
		return 0, false, nil
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int32:
		return int(value), true, nil
	case int64:
		if value < minGoInt64 || value > maxGoInt64 {
			return 0, false, fmt.Errorf("%s must be within Go int range", key)
		}
		return int(value), true, nil
	case float64:
		if math.Trunc(value) != value {
			return 0, false, fmt.Errorf("%s must be an integer", key)
		}
		if value < minSafeFloat64Int || value > maxSafeFloat64Int {
			return 0, false, fmt.Errorf("%s must be within Go int range", key)
		}
		return int(value), true, nil
	case float32:
		if math.Trunc(float64(value)) != float64(value) {
			return 0, false, fmt.Errorf("%s must be an integer", key)
		}
		if float64(value) < minSafeFloat32Int || float64(value) > maxSafeFloat32Int {
			return 0, false, fmt.Errorf("%s must be within Go int range", key)
		}
		return int(value), true, nil
	default:
		return 0, false, fmt.Errorf("%s must be an integer", key)
	}
}

func intValue(payload map[string]any, key string, fallback int) int {
	value, ok, err := optionalInt(payload, key)
	if err != nil || !ok {
		return fallback
	}
	return value
}

func floatValue(payload map[string]any, key string) float64 {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	default:
		return 0
	}
}

func optionalTime(payload map[string]any, key string) (*time.Time, error) {
	value := stringValue(payload, key)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339: %w", key, err)
	}
	return &parsed, nil
}

const maxGoInt = int(^uint(0) >> 1)
const minGoInt = -maxGoInt - 1

var maxGoInt64 = int64(maxGoInt)
var minGoInt64 = int64(minGoInt)

var maxSafeFloat64Int = math.Min(float64(maxGoInt64), float64(1<<53))
var minSafeFloat64Int = math.Max(float64(minGoInt64), -float64(1<<53))
var maxSafeFloat32Int = math.Min(float64(maxGoInt64), float64(1<<24))
var minSafeFloat32Int = math.Max(float64(minGoInt64), -float64(1<<24))
