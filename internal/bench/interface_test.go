package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
)

func TestInterfaceCorpusBuilderReusesPersistentCache(t *testing.T) {
	t.Parallel()

	cacheRoot := filepath.Join(t.TempDir(), "corpora")
	provider := fakeembed.Provider()
	documents := []InterfaceDocument{{ID: "session-1", Content: "> User: hello world and tell me about the local coffee shop we visited yesterday\nAssistant: we talked about the place with the red awning and the late night hours", Mode: "conversations", Extract: "exchange", Depth: 1}}

	builderCold := NewInterfaceCorpusBuilderWithOptions(provider, InterfaceCorpusBuilderOptions{CorpusCacheRoot: cacheRoot})
	corpusCold, err := builderCold.NewCorpus(documents)
	if err != nil {
		t.Fatalf("NewCorpus(cold) error = %v", err)
	}
	t.Cleanup(func() { _ = corpusCold.Close() })
	statsCold := builderCold.CacheStats()
	if statsCold.Hits != 0 || statsCold.Misses != 1 {
		t.Fatalf("cold stats = %+v, want hits=0 misses=1", statsCold)
	}

	entriesCold, err := corpusCold.repo.List(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("List(cold) error = %v", err)
	}
	if len(entriesCold) == 0 || entriesCold[0].Origin != "session-1" {
		t.Fatalf("entriesCold = %+v, want origin session-1", entriesCold)
	}

	builderWarm := NewInterfaceCorpusBuilderWithOptions(provider, InterfaceCorpusBuilderOptions{CorpusCacheRoot: cacheRoot})
	corpusWarm, err := builderWarm.NewCorpus(documents)
	if err != nil {
		t.Fatalf("NewCorpus(warm) error = %v", err)
	}
	t.Cleanup(func() { _ = corpusWarm.Close() })
	statsWarm := builderWarm.CacheStats()
	if statsWarm.Hits != 1 || statsWarm.Misses != 0 {
		t.Fatalf("warm stats = %+v, want hits=1 misses=0", statsWarm)
	}

	entriesWarm, err := corpusWarm.repo.List(store.Query{Limit: 0})
	if err != nil {
		t.Fatalf("List(warm) error = %v", err)
	}
	if len(entriesWarm) == 0 || entriesWarm[0].Origin != "session-1" {
		t.Fatalf("entriesWarm = %+v, want origin session-1", entriesWarm)
	}
}

func TestInterfaceCorpusSearchOverfetchesBeforeOriginDedupe(t *testing.T) {
	t.Parallel()

	corpus := &InterfaceCorpus{repo: &store.Repository{}, entryCount: 6}

	originalSearch := interfaceCorpusSearchDetailed
	t.Cleanup(func() {
		interfaceCorpusSearchDetailed = originalSearch
	})
	var capturedQuery store.Query
	interfaceCorpusSearchDetailed = func(repo *store.Repository, q store.Query) ([]store.SearchResult, error) {
		capturedQuery = q
		allResults := []store.SearchResult{
			{Entry: store.Entry{Origin: "session-alpha"}},
			{Entry: store.Entry{Origin: "session-alpha"}},
			{Entry: store.Entry{Origin: "session-beta"}},
			{Entry: store.Entry{Origin: "session-gamma"}},
		}
		if q.Limit <= 0 || q.Limit >= len(allResults) {
			return allResults, nil
		}
		return allResults[:q.Limit], nil
	}

	results, err := corpus.Search("red bicycle repair downtown", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if capturedQuery.Limit <= 2 {
		t.Fatalf("capturedQuery.Limit = %d, want overfetch beyond requested unique limit", capturedQuery.Limit)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 unique origins after dedupe", len(results))
	}
	if results[0] != "session-alpha" || results[1] != "session-beta" {
		t.Fatalf("results = %v, want [session-alpha session-beta]", results)
	}
}

func TestInterfaceCorpusDaemonSearchUsesEnsureThenSearch(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	daemon := &recordingInterfaceCorpusDaemon{cacheKey: "daemon-a", results: []string{"session-alpha"}}

	results, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3)
	if err != nil {
		t.Fatalf("SearchWithDaemon() error = %v", err)
	}
	if len(results) != 1 || results[0] != "session-alpha" {
		t.Fatalf("results = %v, want [session-alpha]", results)
	}
	if len(daemon.calls) != 2 {
		t.Fatalf("len(daemon.calls) = %d, want 2", len(daemon.calls))
	}
	if daemon.calls[0].command != "ensure_corpus" {
		t.Fatalf("daemon.calls[0].command = %q, want ensure_corpus", daemon.calls[0].command)
	}
	if daemon.calls[1].command != "search_corpus" {
		t.Fatalf("daemon.calls[1].command = %q, want search_corpus", daemon.calls[1].command)
	}
	if daemon.calls[0].key == "" {
		t.Fatal("ensure_corpus key = empty, want derived corpus key")
	}
	if daemon.calls[1].key != daemon.calls[0].key {
		t.Fatalf("search key = %q, want %q", daemon.calls[1].key, daemon.calls[0].key)
	}
	if len(daemon.calls[0].documents) != len(documents) {
		t.Fatalf("ensure_corpus documents = %d, want %d", len(daemon.calls[0].documents), len(documents))
	}
	if daemon.calls[1].query != "Friday lighthouse dinner reservation" {
		t.Fatalf("search query = %q, want Friday lighthouse dinner reservation", daemon.calls[1].query)
	}
	if daemon.calls[1].limit != 3 {
		t.Fatalf("search limit = %d, want 3", daemon.calls[1].limit)
	}
}

func TestInterfaceCorpusDaemonSearchReusesCachedCorpus(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	daemon := &recordingInterfaceCorpusDaemon{cacheKey: "daemon-a", results: []string{"session-alpha"}}

	firstResults, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3)
	if err != nil {
		t.Fatalf("first SearchWithDaemon() error = %v", err)
	}
	secondResults, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3)
	if err != nil {
		t.Fatalf("second SearchWithDaemon() error = %v", err)
	}
	if len(firstResults) != 1 || firstResults[0] != "session-alpha" {
		t.Fatalf("firstResults = %v, want [session-alpha]", firstResults)
	}
	if len(secondResults) != 1 || secondResults[0] != "session-alpha" {
		t.Fatalf("secondResults = %v, want [session-alpha]", secondResults)
	}
	if daemon.ensureCalls != 1 {
		t.Fatalf("daemon.ensureCalls = %d, want 1 cached ensure", daemon.ensureCalls)
	}
	if daemon.searchCalls != 2 {
		t.Fatalf("daemon.searchCalls = %d, want 2 searches", daemon.searchCalls)
	}
	if len(daemon.calls) != 3 {
		t.Fatalf("len(daemon.calls) = %d, want 3", len(daemon.calls))
	}
	if daemon.calls[0].command != "ensure_corpus" || daemon.calls[1].command != "search_corpus" || daemon.calls[2].command != "search_corpus" {
		t.Fatalf("daemon calls = %+v, want ensure then search then search", daemon.calls)
	}
	if daemon.calls[1].key != daemon.calls[0].key || daemon.calls[2].key != daemon.calls[0].key {
		t.Fatalf("daemon keys = %+v, want all calls to use same key", daemon.calls)
	}
}

func TestInterfaceCorpusDaemonSearchEnsuresCorpusOnceUnderConcurrency(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	daemon := &blockingRecordingInterfaceCorpusDaemon{recordingInterfaceCorpusDaemon: recordingInterfaceCorpusDaemon{cacheKey: "daemon-a", results: []string{"session-alpha"}}, ensureStarted: make(chan struct{}, 2), releaseEnsure: make(chan struct{})}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3)
			if err != nil {
				errCh <- err
				return
			}
			if len(results) != 1 || results[0] != "session-alpha" {
				errCh <- fmt.Errorf("results = %v, want [session-alpha]", results)
			}
		}()
	}

	<-daemon.ensureStarted
	close(daemon.releaseEnsure)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if daemon.ensureCalls != 1 {
		t.Fatalf("daemon.ensureCalls = %d, want 1 under concurrent search", daemon.ensureCalls)
	}
	if daemon.searchCalls != 2 {
		t.Fatalf("daemon.searchCalls = %d, want 2", daemon.searchCalls)
	}
}

func TestInterfaceCorpusDaemonSearchReensuresForNewDaemonLifecycle(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	firstDaemon := &recordingInterfaceCorpusDaemon{cacheKey: "daemon-a", results: []string{"session-alpha"}}
	secondDaemon := &recordingInterfaceCorpusDaemon{cacheKey: "daemon-b", results: []string{"session-alpha"}}

	if _, err := builder.SearchWithDaemon(context.Background(), firstDaemon, documents, "Friday lighthouse dinner reservation", 3); err != nil {
		t.Fatalf("first SearchWithDaemon() error = %v", err)
	}
	if _, err := builder.SearchWithDaemon(context.Background(), secondDaemon, documents, "Friday lighthouse dinner reservation", 3); err != nil {
		t.Fatalf("second SearchWithDaemon() error = %v", err)
	}
	if firstDaemon.ensureCalls != 1 {
		t.Fatalf("firstDaemon.ensureCalls = %d, want 1", firstDaemon.ensureCalls)
	}
	if secondDaemon.ensureCalls != 1 {
		t.Fatalf("secondDaemon.ensureCalls = %d, want 1 after daemon lifecycle change", secondDaemon.ensureCalls)
	}
	if secondDaemon.searchCalls != 1 {
		t.Fatalf("secondDaemon.searchCalls = %d, want 1", secondDaemon.searchCalls)
	}
}

func TestInterfaceCorpusDaemonSearchReensuresAfterDaemonRestart(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	daemon := &recordingInterfaceCorpusDaemon{cacheKey: "daemon-a", results: []string{"session-alpha"}}

	if _, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3); err != nil {
		t.Fatalf("first SearchWithDaemon() error = %v", err)
	}
	daemon.cacheKey = "daemon-b"
	if _, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3); err != nil {
		t.Fatalf("second SearchWithDaemon() error = %v", err)
	}
	if daemon.ensureCalls != 2 {
		t.Fatalf("daemon.ensureCalls = %d, want 2 after daemon restart", daemon.ensureCalls)
	}
	if daemon.searchCalls != 2 {
		t.Fatalf("daemon.searchCalls = %d, want 2", daemon.searchCalls)
	}
}

func TestInterfaceCorpusDaemonSearchRequiresLifecycleIdentity(t *testing.T) {
	t.Parallel()

	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())
	documents := []InterfaceDocument{{ID: "session-alpha", Content: "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night", Mode: "conversations", Extract: "exchange", Depth: 1}}
	daemon := &recordingInterfaceCorpusDaemon{results: []string{"session-alpha"}}

	_, err := builder.SearchWithDaemon(context.Background(), daemon, documents, "Friday lighthouse dinner reservation", 3)
	if err == nil {
		t.Fatal("SearchWithDaemon() error = nil, want missing daemon lifecycle identity error")
	}
	if daemon.ensureCalls != 0 {
		t.Fatalf("daemon.ensureCalls = %d, want 0 when lifecycle identity is missing", daemon.ensureCalls)
	}
	if daemon.searchCalls != 0 {
		t.Fatalf("daemon.searchCalls = %d, want 0 when lifecycle identity is missing", daemon.searchCalls)
	}
}

func TestLongMemEvalInterfaceBuildsCorpusPerQuestion(t *testing.T) {
	entries := testLongMemEvalSharedCorpusEntries()
	builder := NewInterfaceCorpusBuilder(fakeembed.Provider())

	firstResults, err := rankLongMemEvalEntryInterface(builder, entries[0])
	if err != nil {
		t.Fatalf("rankLongMemEvalEntryInterface(first) error = %v", err)
	}
	if len(firstResults) == 0 || firstResults[0] != "session-alpha" {
		t.Fatalf("firstResults = %v, want session-alpha first", firstResults)
	}

	secondResults, err := rankLongMemEvalEntryInterface(builder, entries[1])
	if err != nil {
		t.Fatalf("rankLongMemEvalEntryInterface(second) error = %v", err)
	}
	if len(secondResults) == 0 || secondResults[0] != "session-beta" {
		t.Fatalf("secondResults = %v, want session-beta first", secondResults)
	}
	for _, origin := range secondResults {
		if origin != "session-beta" {
			t.Fatalf("secondResults = %v, want only session-beta matches from per-question corpus", secondResults)
		}
	}

	stats := builder.CacheStats()
	if stats.Misses != 2 || stats.Hits != 0 {
		t.Fatalf("builder.CacheStats() = %+v, want misses=2 hits=0 for per-question corpora", stats)
	}
}

func TestRunLongMemEvalInterfaceWithOptionsSharedCorpusEndToEnd(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "longmemeval.json")
	entries := testLongMemEvalSharedCorpusEntries()

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(dataFile, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := RunLongMemEvalInterfaceWithOptions(context.Background(), dataFile, 0, fakeembed.Provider(), LongMemEvalInterfaceOptions{})
	if err != nil {
		t.Fatalf("RunLongMemEvalInterfaceWithOptions() error = %v", err)
	}
	if result.Questions != 2 {
		t.Fatalf("result.Questions = %d, want 2", result.Questions)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(result.Items) = %d, want 2", len(result.Items))
	}
	if len(result.Items[0].TopResults) == 0 || result.Items[0].TopResults[0] != "session-alpha" {
		t.Fatalf("first question TopResults = %v, want session-alpha first", result.Items[0].TopResults)
	}
	if len(result.Items[1].TopResults) == 0 || result.Items[1].TopResults[0] != "session-beta" {
		t.Fatalf("second question TopResults = %v, want session-beta first", result.Items[1].TopResults)
	}
}

func testLongMemEvalSharedCorpusEntries() []LongMemEvalEntry {
	return []LongMemEvalEntry{
		{
			Question:           "Which session mentioned the red bicycle repair?",
			QuestionType:       "fact",
			AnswerSessionIDs:   []string{"session-alpha"},
			HaystackSessions:   [][]Turn{{{Role: "user", Content: "Can you remind me about the red bicycle repair at the downtown shop?"}, {Role: "assistant", Content: "The red bicycle repair was finished yesterday at the downtown shop."}}, {{Role: "user", Content: "Where is the blue kayak storage locker?"}, {Role: "assistant", Content: "The blue kayak storage locker is next to the marina office."}}},
			HaystackSessionIDs: []string{"session-alpha", "session-beta"},
			HaystackDates:      []string{"2024-01-02", "2024-01-03"},
		},
		{
			Question:           "Which session mentioned the blue kayak storage locker?",
			QuestionType:       "fact",
			AnswerSessionIDs:   []string{"session-beta"},
			HaystackSessions:   [][]Turn{{{Role: "user", Content: "Where is the blue kayak storage locker?"}, {Role: "assistant", Content: "The blue kayak storage locker is next to the marina office."}}},
			HaystackSessionIDs: []string{"session-beta"},
			HaystackDates:      []string{"2024-01-03"},
		},
	}
}

type recordingInterfaceCorpusDaemon struct {
	mu          sync.Mutex
	cacheKey    string
	results     []string
	calls       []interfaceCorpusDaemonCall
	ensureCalls int
	searchCalls int
}

type interfaceCorpusDaemonCall struct {
	command   string
	key       string
	documents []InterfaceDocument
	query     string
	limit     int
}

type blockingRecordingInterfaceCorpusDaemon struct {
	recordingInterfaceCorpusDaemon
	ensureStarted chan struct{}
	releaseEnsure chan struct{}
}

func (d *recordingInterfaceCorpusDaemon) EnsureCorpus(_ context.Context, key string, documents []InterfaceDocument) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureCalls++
	d.calls = append(d.calls, interfaceCorpusDaemonCall{command: "ensure_corpus", key: key, documents: append([]InterfaceDocument(nil), documents...)})
	return nil
}

func (d *blockingRecordingInterfaceCorpusDaemon) EnsureCorpus(ctx context.Context, key string, documents []InterfaceDocument) error {
	d.recordingInterfaceCorpusDaemon.EnsureCorpus(ctx, key, documents)
	select {
	case d.ensureStarted <- struct{}{}:
	default:
	}
	<-d.releaseEnsure
	return nil
}

func (d *recordingInterfaceCorpusDaemon) SearchCorpus(_ context.Context, key, query string, limit int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.searchCalls++
	d.calls = append(d.calls, interfaceCorpusDaemonCall{command: "search_corpus", key: key, query: query, limit: limit})
	return append([]string(nil), d.results...), nil
}

func (d *recordingInterfaceCorpusDaemon) CorpusCacheKey() string {
	return d.cacheKey
}
