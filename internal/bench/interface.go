package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codysnider/tagmem/internal/importer"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
)

const interfaceCorpusCacheVersion = "v1"

type InterfaceDocument struct {
	ID        string
	Content   string
	Mode      importer.Mode
	Extract   string
	Depth     int
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

type InterfaceCorpus struct {
	root       string
	repo       *store.Repository
	entryCount int
	cleanup    bool
}

type InterfaceCorpusBuilderOptions struct {
	CorpusCacheRoot string
}

type InterfaceCorpusCacheStats struct {
	Hits   int64
	Misses int64
}

type interfaceCorpusMeta struct {
	Version    string `json:"version"`
	EntryCount int    `json:"entry_count"`
}

type InterfaceCorpusBuilder struct {
	provider        vector.Provider
	mu              sync.RWMutex
	cache           map[uint64][]store.AddEntry
	corpusCacheRoot string
	hits            atomic.Int64
	misses          atomic.Int64
}

func NewInterfaceCorpusBuilder(provider vector.Provider) *InterfaceCorpusBuilder {
	return NewInterfaceCorpusBuilderWithOptions(provider, InterfaceCorpusBuilderOptions{})
}

func NewInterfaceCorpusBuilderWithOptions(provider vector.Provider, options InterfaceCorpusBuilderOptions) *InterfaceCorpusBuilder {
	return &InterfaceCorpusBuilder{provider: provider, cache: map[uint64][]store.AddEntry{}, corpusCacheRoot: strings.TrimSpace(options.CorpusCacheRoot)}
}

func newInterfaceCorpus(provider vector.Provider, documents []InterfaceDocument) (*InterfaceCorpus, error) {
	return NewInterfaceCorpusBuilder(provider).NewCorpus(documents)
}

func (b *InterfaceCorpusBuilder) NewCorpus(documents []InterfaceDocument) (*InterfaceCorpus, error) {
	if b.corpusCacheRoot != "" {
		if corpus, ok, err := b.openCachedCorpus(documents); err != nil {
			return nil, err
		} else if ok {
			b.hits.Add(1)
			return corpus, nil
		}
	}
	b.misses.Add(1)
	return b.buildCorpus(documents)
}

func (b *InterfaceCorpusBuilder) buildCorpus(documents []InterfaceDocument) (*InterfaceCorpus, error) {
	cleanup := true
	root := ""
	if b.corpusCacheRoot != "" {
		root = b.corpusRoot(documents)
		cleanup = false
		if removeErr := os.RemoveAll(root); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("reset benchmark cache corpus: %w", removeErr)
		}
		if mkdirErr := os.MkdirAll(root, 0o755); mkdirErr != nil {
			return nil, fmt.Errorf("create benchmark cache corpus: %w", mkdirErr)
		}
	} else {
		tmpRoot, err := os.MkdirTemp(benchmarkTempRoot(), "tagmem-bench-interface-*")
		if err != nil {
			return nil, fmt.Errorf("create temp benchmark repo: %w", err)
		}
		root = tmpRoot
	}

	repo := store.NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), b.provider)
	if err := repo.Init(); err != nil {
		if cleanup {
			os.RemoveAll(root)
		}
		return nil, fmt.Errorf("initialize benchmark repo: %w", err)
	}

	batch := make([]store.AddEntry, 0, len(documents))
	for _, document := range documents {
		entries := b.entriesForDocument(document)
		for i := range entries {
			entries[i].CreatedAt = document.CreatedAt
			entries[i].UpdatedAt = document.UpdatedAt
		}
		batch = append(batch, entries...)
	}
	if len(batch) == 0 {
		if err := b.writeCorpusMeta(root, 0); err != nil {
			if cleanup {
				os.RemoveAll(root)
			}
			return nil, err
		}
		return &InterfaceCorpus{root: root, repo: repo, cleanup: cleanup}, nil
	}
	if _, err := repo.AddMany(batch); err != nil {
		if cleanup {
			os.RemoveAll(root)
		}
		return nil, fmt.Errorf("populate benchmark repo: %w", err)
	}
	if err := b.writeCorpusMeta(root, len(batch)); err != nil {
		if cleanup {
			os.RemoveAll(root)
		}
		return nil, err
	}
	return &InterfaceCorpus{root: root, repo: repo, entryCount: len(batch), cleanup: cleanup}, nil
}

func (c *InterfaceCorpus) Close() error {
	if c == nil || c.root == "" || !c.cleanup {
		return nil
	}
	return os.RemoveAll(c.root)
}

func (b *InterfaceCorpusBuilder) CacheStats() InterfaceCorpusCacheStats {
	return InterfaceCorpusCacheStats{Hits: b.hits.Load(), Misses: b.misses.Load()}
}

func (c *InterfaceCorpus) Search(query string, limit int) ([]string, error) {
	if c == nil || c.repo == nil {
		return nil, nil
	}
	queryLimit := c.entryCount
	if queryLimit < limit {
		queryLimit = limit
	}
	results, err := c.repo.SearchDetailed(store.Query{Text: query, Limit: queryLimit})
	if err != nil {
		return nil, fmt.Errorf("query benchmark repo: %w", err)
	}

	ranked := make([]string, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		origin := strings.TrimSpace(result.Entry.Origin)
		if origin == "" {
			origin = strings.TrimSpace(result.Entry.Title)
		}
		if origin == "" {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		ranked = append(ranked, origin)
	}
	return ranked, nil
}

func rankInterfaceDocuments(provider vector.Provider, documents []InterfaceDocument, query string, limit int) ([]string, error) {
	corpus, err := newInterfaceCorpus(provider, documents)
	if err != nil {
		return nil, err
	}
	defer corpus.Close()
	return corpus.Search(query, limit)
}

func (b *InterfaceCorpusBuilder) openCachedCorpus(documents []InterfaceDocument) (*InterfaceCorpus, bool, error) {
	root := b.corpusRoot(documents)
	meta, ok, err := readInterfaceCorpusMeta(filepath.Join(root, "corpus-meta.json"))
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, false, nil
	}
	if !ok || meta.Version != interfaceCorpusCacheVersion {
		return nil, false, nil
	}
	repo := store.NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), b.provider)
	if err := repo.Init(); err != nil {
		_ = os.RemoveAll(root)
		return nil, false, nil
	}
	return &InterfaceCorpus{root: root, repo: repo, entryCount: meta.EntryCount, cleanup: false}, true, nil
}

func (b *InterfaceCorpusBuilder) writeCorpusMeta(root string, entryCount int) error {
	if b.corpusCacheRoot == "" {
		return nil
	}
	if err := writeJSON(filepath.Join(root, "corpus-meta.json"), interfaceCorpusMeta{Version: interfaceCorpusCacheVersion, EntryCount: entryCount}); err != nil {
		return fmt.Errorf("write benchmark corpus metadata: %w", err)
	}
	return nil
}

func (b *InterfaceCorpusBuilder) entriesForDocument(document InterfaceDocument) []store.AddEntry {
	key := interfaceDocumentKey(document)
	b.mu.RLock()
	if cached, ok := b.cache[key]; ok {
		entries := cloneAddEntries(cached)
		b.mu.RUnlock()
		return entries
	}
	b.mu.RUnlock()

	entries := importer.BuildEntriesFromContent(document.ID, document.Content, document.Mode, document.Extract, document.Depth, &b.provider)
	b.mu.Lock()
	b.cache[key] = cloneAddEntries(entries)
	b.mu.Unlock()
	return entries
}

func (b *InterfaceCorpusBuilder) corpusRoot(documents []InterfaceDocument) string {
	return filepath.Join(b.corpusCacheRoot, b.provider.IndexKey, interfaceCorpusKey(documents))
}

func benchmarkTempRoot() string {
	for _, root := range []string{"/dev/shm", os.TempDir()} {
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

func interfaceDocumentKey(document InterfaceDocument) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(document.ID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(document.Content))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(document.Mode))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(document.Extract))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(fmt.Sprintf("%d", document.Depth)))
	return hash.Sum64()
}

func interfaceCorpusKey(documents []InterfaceDocument) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(interfaceCorpusCacheVersion))
	for _, document := range documents {
		_, _ = hash.Write([]byte(document.ID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(document.Content))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(document.Mode))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(document.Extract))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strconv.Itoa(document.Depth)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(timeKey(document.CreatedAt)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(timeKey(document.UpdatedAt)))
		_, _ = hash.Write([]byte{1})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneAddEntries(entries []store.AddEntry) []store.AddEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]store.AddEntry, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		if len(entry.Tags) > 0 {
			copyEntry.Tags = append([]string(nil), entry.Tags...)
		}
		cloned = append(cloned, copyEntry)
	}
	return cloned
}

func readInterfaceCorpusMeta(path string) (interfaceCorpusMeta, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return interfaceCorpusMeta{}, false, nil
		}
		return interfaceCorpusMeta{}, false, err
	}
	var meta interfaceCorpusMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return interfaceCorpusMeta{}, false, err
	}
	return meta, true, nil
}

func timeKey(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func benchmarkTime(date string) *time.Time {
	date = strings.TrimSpace(date)
	if date == "" {
		return nil
	}
	for _, layout := range []string{"2006/01/02", time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, date)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}
