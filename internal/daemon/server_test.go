package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/xdg"
)

func TestDaemonStatusRoundTrip(t *testing.T) {
	providerDescription, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRequest()

	response, err := Call(requestCtx, paths.SocketPath, Request{ID: "status-1", Command: "status"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !response.Success {
		t.Fatalf("response.Success = false, error = %q", response.Error)
	}
	if response.ID != "status-1" {
		t.Fatalf("response.ID = %q, want %q", response.ID, "status-1")
	}
	if got := response.Payload["total_entries"]; got != float64(0) {
		t.Fatalf("response.Payload[total_entries] = %#v, want %v", got, 0)
	}
	if got := response.Payload["store_path"]; got != paths.StorePath {
		t.Fatalf("response.Payload[store_path] = %#v, want %q", got, paths.StorePath)
	}
	if got := response.Payload["embedding"]; got != providerDescription {
		t.Fatalf("response.Payload[embedding] = %#v, want %q", got, providerDescription)
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestServerRunFailsWhenSocketAlreadyServed(t *testing.T) {
	_, paths, backend := newTestBackend(t)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()

	firstServer := NewServer(paths.SocketPath, backend)
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- firstServer.Run(firstCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	secondServer := NewServer(paths.SocketPath, backend)
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- secondServer.Run(context.Background())
	}()

	select {
	case err := <-secondErr:
		if err == nil {
			t.Fatal("secondServer.Run() error = nil, want live socket error")
		}
		if err.Error() != "daemon socket already in use" {
			t.Fatalf("secondServer.Run() error = %q, want %q", err.Error(), "daemon socket already in use")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("secondServer.Run() did not fail promptly while socket was already live")
	}

	cancelFirst()
	if err := <-firstErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("firstServer.Run() error = %v", err)
	}
}

func TestServerRunRemovesSocketOnCleanShutdown(t *testing.T) {
	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)
	cancelServer()

	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
	if _, err := os.Stat(paths.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) error = %v, want not exists", paths.SocketPath, err)
	}
}

func TestServerRunRemovesStaleSocketBeforeListening(t *testing.T) {
	_, paths, backend := newTestBackend(t)

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("Socket() error = %v", err)
	}
	addr := &syscall.SockaddrUnix{Name: paths.SocketPath}
	if err := syscall.Bind(fd, addr); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("Bind() error = %v", err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(paths.SocketPath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v", paths.SocketPath, err)
	}

	server := NewServer(paths.SocketPath, backend)
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRequest()
	response, err := Call(requestCtx, paths.SocketPath, Request{ID: "status-stale", Command: "status"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !response.Success {
		t.Fatalf("response.Success = false, error = %q", response.Error)
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestDaemonMultiClientSharedStore(t *testing.T) {
	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	writerCtx, cancelWriter := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWriter()
	writerResponse, err := Call(writerCtx, paths.SocketPath, Request{
		ID:      "client-a-add",
		Command: "add_entry",
		Payload: map[string]any{"depth": 1, "title": "shared daemon entry", "body": "written by client A", "tags": []string{"shared", "daemon"}},
	})
	if err != nil {
		t.Fatalf("client A add_entry error = %v", err)
	}
	if !writerResponse.Success {
		t.Fatalf("client A add_entry success = false, error = %q", writerResponse.Error)
	}

	readerCtx, cancelReader := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReader()
	readerResponse, err := Call(readerCtx, paths.SocketPath, Request{
		ID:      "client-b-list",
		Command: "list_entries",
		Payload: map[string]any{"tag": "shared", "limit": 10},
	})
	if err != nil {
		t.Fatalf("client B list_entries error = %v", err)
	}
	if !readerResponse.Success {
		t.Fatalf("client B list_entries success = false, error = %q", readerResponse.Error)
	}

	entries, ok := readerResponse.Payload["entries"].([]any)
	if !ok {
		t.Fatalf("readerResponse.Payload[entries] type = %T, want array", readerResponse.Payload["entries"])
	}
	if len(entries) != 1 {
		t.Fatalf("len(client B entries) = %d, want 1", len(entries))
	}
	readEntry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entries[0] type = %T, want object", entries[0])
	}
	if got := readEntry["title"]; got != "shared daemon entry" {
		t.Fatalf("client B entry title = %#v, want %q", got, "shared daemon entry")
	}
	if got := readEntry["body"]; got != "written by client A" {
		t.Fatalf("client B entry body = %#v, want %q", got, "written by client A")
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestDaemonMultiClientConcurrentWritesSharedStore(t *testing.T) {
	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	const writerCount = 6
	errs := make(chan error, writerCount)
	ids := make(chan int, writerCount)
	start := make(chan struct{})
	ready := make(chan struct{}, writerCount)
	var writers sync.WaitGroup
	for i := 0; i < writerCount; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			ready <- struct{}{}
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			response, err := Call(ctx, paths.SocketPath, Request{
				ID:      fmt.Sprintf("writer-%d", i),
				Command: "add_entry",
				Payload: map[string]any{
					"depth": 1,
					"title": fmt.Sprintf("concurrent entry %d", i),
					"body":  fmt.Sprintf("concurrent shared payload %d", i),
					"tags":  []string{"concurrent", "shared"},
				},
			})
			if err != nil {
				errs <- err
				return
			}
			if !response.Success {
				errs <- fmt.Errorf("writer %d response error: %s", i, response.Error)
				return
			}
			entry, ok := response.Payload["entry"].(map[string]any)
			if !ok {
				errs <- fmt.Errorf("writer %d entry payload type = %T", i, response.Payload["entry"])
				return
			}
			id, ok := entry["id"].(float64)
			if !ok {
				errs <- fmt.Errorf("writer %d id payload type = %T", i, entry["id"])
				return
			}
			ids <- int(id)
		}(i)
	}
	for i := 0; i < writerCount; i++ {
		<-ready
	}
	close(start)
	writers.Wait()
	close(errs)
	close(ids)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent writer error = %v", err)
		}
	}

	seenIDs := make(map[int]struct{}, writerCount)
	for id := range ids {
		seenIDs[id] = struct{}{}
	}
	if len(seenIDs) != writerCount {
		t.Fatalf("unique written IDs = %d, want %d", len(seenIDs), writerCount)
	}

	readerCtx, cancelReader := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReader()
	readerResponse, err := Call(readerCtx, paths.SocketPath, Request{
		ID:      "reader-concurrent-list",
		Command: "list_entries",
		Payload: map[string]any{"tag": "concurrent", "limit": 20},
	})
	if err != nil {
		t.Fatalf("reader list_entries error = %v", err)
	}
	if !readerResponse.Success {
		t.Fatalf("reader list_entries success = false, error = %q", readerResponse.Error)
	}

	entries, ok := readerResponse.Payload["entries"].([]any)
	if !ok {
		t.Fatalf("readerResponse.Payload[entries] type = %T, want array", readerResponse.Payload["entries"])
	}
	if len(entries) != writerCount {
		t.Fatalf("len(concurrent reader entries) = %d, want %d", len(entries), writerCount)
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestDaemonEnsureCorpusCachesOnce(t *testing.T) {
	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelFirst()
	firstResponse, err := Call(firstCtx, paths.SocketPath, Request{
		ID:      "corpus-ensure-1",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "shared-corpus",
			"documents": []map[string]any{{
				"id":      "session-alpha",
				"content": "> User: where did we leave the blue backpack\nAssistant: we left the blue backpack in the hall closet",
				"mode":    "conversations",
				"extract": "exchange",
				"depth":   1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("first ensure_corpus error = %v", err)
	}
	if !firstResponse.Success {
		t.Fatalf("first ensure_corpus success = false, error = %q", firstResponse.Error)
	}
	if got := firstResponse.Payload["cache_status"]; got != "miss" {
		t.Fatalf("first ensure_corpus cache_status = %#v, want %q", got, "miss")
	}

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	secondResponse, err := Call(secondCtx, paths.SocketPath, Request{
		ID:      "corpus-ensure-2",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "shared-corpus",
			"documents": []map[string]any{{
				"id":      "session-beta",
				"content": "> User: where is the spare charger\nAssistant: the spare charger is in the desk drawer",
				"mode":    "conversations",
				"extract": "exchange",
				"depth":   1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("second ensure_corpus error = %v", err)
	}
	if !secondResponse.Success {
		t.Fatalf("second ensure_corpus success = false, error = %q", secondResponse.Error)
	}
	if got := secondResponse.Payload["cache_status"]; got != "hit" {
		t.Fatalf("second ensure_corpus cache_status = %#v, want %q", got, "hit")
	}

	searchCtx, cancelSearch := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSearch()
	searchResponse, err := Call(searchCtx, paths.SocketPath, Request{
		ID:      "corpus-search-after-hit",
		Command: "search_corpus",
		Payload: map[string]any{
			"key":   "shared-corpus",
			"query": "blue backpack hall closet",
			"limit": 1,
		},
	})
	if err != nil {
		t.Fatalf("search_corpus after cache hit error = %v", err)
	}
	if !searchResponse.Success {
		t.Fatalf("search_corpus after cache hit success = false, error = %q", searchResponse.Error)
	}
	results, ok := searchResponse.Payload["origin_ids"].([]any)
	if !ok {
		t.Fatalf("searchResponse.Payload[origin_ids] type = %T, want array", searchResponse.Payload["origin_ids"])
	}
	if len(results) != 1 || results[0] != "session-alpha" {
		t.Fatalf("searchResponse.Payload[origin_ids] = %#v, want [session-alpha]", results)
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestDaemonSearchCorpusReturnsResults(t *testing.T) {
	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelEnsure()
	ensureResponse, err := Call(ensureCtx, paths.SocketPath, Request{
		ID:      "corpus-build-searchable",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "searchable-corpus",
			"documents": []map[string]any{
				{
					"id":      "session-alpha",
					"content": "> User: remind me about the lighthouse dinner reservation\nAssistant: the lighthouse dinner reservation is booked for Friday night",
					"mode":    "conversations",
					"extract": "exchange",
					"depth":   1,
				},
				{
					"id":      "session-beta",
					"content": "> User: remind me about the garden tools\nAssistant: the garden tools are stored behind the shed",
					"mode":    "conversations",
					"extract": "exchange",
					"depth":   1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ensure_corpus error = %v", err)
	}
	if !ensureResponse.Success {
		t.Fatalf("ensure_corpus success = false, error = %q", ensureResponse.Error)
	}

	searchCtx, cancelSearch := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSearch()
	searchResponse, err := Call(searchCtx, paths.SocketPath, Request{
		ID:      "corpus-search-results",
		Command: "search_corpus",
		Payload: map[string]any{
			"key":   "searchable-corpus",
			"query": "Friday lighthouse dinner reservation",
			"limit": 2,
		},
	})
	if err != nil {
		t.Fatalf("search_corpus error = %v", err)
	}
	if !searchResponse.Success {
		t.Fatalf("search_corpus success = false, error = %q", searchResponse.Error)
	}
	results, ok := searchResponse.Payload["origin_ids"].([]any)
	if !ok {
		t.Fatalf("searchResponse.Payload[origin_ids] type = %T, want array", searchResponse.Payload["origin_ids"])
	}
	if len(results) == 0 {
		t.Fatal("searchResponse.Payload[origin_ids] = empty, want ranked results")
	}
	if results[0] != "session-alpha" {
		t.Fatalf("searchResponse.Payload[origin_ids][0] = %#v, want %q", results[0], "session-alpha")
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func TestDaemonEnsureCorpusCleanupOnShutdown(t *testing.T) {
	before := corpusTempDirs(t)

	_, paths, backend := newTestBackend(t)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelEnsure()
	ensureResponse, err := Call(ensureCtx, paths.SocketPath, Request{
		ID:      "corpus-cleanup-build",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "cleanup-corpus",
			"documents": []map[string]any{{
				"id":      "session-cleanup",
				"content": "> User: remind me about the travel adapter\nAssistant: the travel adapter is packed in the top drawer",
				"mode":    "conversations",
				"extract": "exchange",
				"depth":   1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("ensure_corpus error = %v", err)
	}
	if !ensureResponse.Success {
		t.Fatalf("ensure_corpus success = false, error = %q", ensureResponse.Error)
	}

	during := corpusTempDirs(t)
	created := addedCorpusTempDirs(before, during)
	if len(created) == 0 {
		t.Fatal("ensure_corpus did not create a temp-backed corpus directory")
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}

	after := corpusTempDirs(t)
	for _, dir := range created {
		if slices.Contains(after, dir) {
			t.Fatalf("temp-backed corpus directory still exists after shutdown: %q", dir)
		}
	}
}

func TestDaemonShutdownWaitsForActiveCorpusSearch(t *testing.T) {
	provider := fakeembed.Provider()
	searchStarted := make(chan struct{}, 1)
	allowSearch := make(chan struct{})
	originalFunc := provider.Func
	provider.Func = func(ctx context.Context, text string) ([]float32, error) {
		if text == "blocked shutdown query" {
			select {
			case searchStarted <- struct{}{}:
			default:
			}
			<-allowSearch
		}
		return originalFunc(ctx, text)
	}

	root := t.TempDir()
	paths := xdg.Paths{
		AppName:    "tagmem",
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
		SocketPath: filepath.Join(root, "runtime", "tagmem.sock"),
		IndexDir:   filepath.Join(root, "data", "vector"),
		ModelDir:   filepath.Join(root, "data", "models"),
		DiaryDir:   filepath.Join(root, "data", "diaries"),
		StorePath:  filepath.Join(root, "data", "store.json"),
		KGPath:     filepath.Join(root, "data", "knowledge.json"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}
	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}

	backend := NewBackend(repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelEnsure()
	ensureResponse, err := Call(ensureCtx, paths.SocketPath, Request{
		ID:      "corpus-active-shutdown-build",
		Command: "ensure_corpus",
		Payload: map[string]any{
			"key": "active-shutdown-corpus",
			"documents": []map[string]any{{
				"id":      "session-active",
				"content": "> User: remind me about the blue lantern\nAssistant: the blue lantern is stored beside the camping stove",
				"mode":    "conversations",
				"extract": "exchange",
				"depth":   1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("ensure_corpus error = %v", err)
	}
	if !ensureResponse.Success {
		t.Fatalf("ensure_corpus success = false, error = %q", ensureResponse.Error)
	}

	searchErr := make(chan error, 1)
	searchResponse := make(chan Response, 1)
	go func() {
		searchCtx, cancelSearch := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelSearch()
		response, err := Call(searchCtx, paths.SocketPath, Request{
			ID:      "corpus-active-shutdown-search",
			Command: "search_corpus",
			Payload: map[string]any{
				"key":   "active-shutdown-corpus",
				"query": "blocked shutdown query",
				"limit": 1,
			},
		})
		if err != nil {
			searchErr <- err
			return
		}
		searchResponse <- response
	}()

	select {
	case <-searchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("search_corpus did not enter blocked query state")
	}

	cancelServer()

	select {
	case err := <-serverErr:
		t.Fatalf("server.Run() returned before active search finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(allowSearch)

	select {
	case err := <-searchErr:
		t.Fatalf("search_corpus error = %v", err)
	case response := <-searchResponse:
		if !response.Success {
			t.Fatalf("search_corpus success = false, error = %q", response.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("search_corpus did not complete after allowing shutdown")
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("server.Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server.Run() did not finish after active search completed")
	}
}

func TestDaemonRequestCancellationPropagatesContext(t *testing.T) {
	provider := fakeembed.Provider()
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan error, 1)
	provider.Func = func(ctx context.Context, text string) ([]float32, error) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		requestCanceled <- ctx.Err()
		return nil, ctx.Err()
	}

	root := t.TempDir()
	paths := xdg.Paths{
		AppName:    "tagmem",
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
		SocketPath: filepath.Join(root, "runtime", "tagmem.sock"),
		IndexDir:   filepath.Join(root, "data", "vector"),
		ModelDir:   filepath.Join(root, "data", "models"),
		DiaryDir:   filepath.Join(root, "data", "diaries"),
		StorePath:  filepath.Join(root, "data", "store.json"),
		KGPath:     filepath.Join(root, "data", "knowledge.json"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}
	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}

	backend := NewBackend(repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	server := NewServer(paths.SocketPath, backend)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run(serverCtx)
	}()

	waitForSocket(t, paths.SocketPath)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRequest()

	callErr := make(chan error, 1)
	go func() {
		_, err := Call(requestCtx, paths.SocketPath, Request{ID: "doctor-cancel", Command: "doctor"})
		callErr <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("doctor request did not start")
	}

	select {
	case err := <-callErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Call() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call() did not return after client cancellation")
	}

	select {
	case err := <-requestCanceled:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("provider ctx error = %v, want canceled or deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server-side request context was not canceled")
	}

	cancelServer()
	if err := <-serverErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func newTestBackend(t *testing.T) (providerDescription string, paths xdg.Paths, backend *Backend) {
	t.Helper()

	provider := fakeembed.Provider()
	root := t.TempDir()
	paths = xdg.Paths{
		AppName:    "tagmem",
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
		SocketPath: filepath.Join(root, "runtime", "tagmem.sock"),
		IndexDir:   filepath.Join(root, "data", "vector"),
		ModelDir:   filepath.Join(root, "data", "models"),
		DiaryDir:   filepath.Join(root, "data", "diaries"),
		StorePath:  filepath.Join(root, "data", "store.json"),
		KGPath:     filepath.Join(root, "data", "knowledge.json"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}

	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}

	backend = NewBackend(repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	return provider.Description, paths, backend
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("socket %q did not become ready", socketPath)
}

func corpusTempDirs(t *testing.T) []string {
	t.Helper()

	seen := make(map[string]struct{})
	for _, root := range []string{"/dev/shm", os.TempDir()} {
		if root == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, "tagmem-bench-interface-*"))
		if err != nil {
			t.Fatalf("filepath.Glob(%q) error = %v", root, err)
		}
		for _, match := range matches {
			seen[match] = struct{}{}
		}
	}

	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return dirs
}

func addedCorpusTempDirs(before, after []string) []string {
	seen := make(map[string]struct{}, len(before))
	for _, dir := range before {
		seen[dir] = struct{}{}
	}
	added := make([]string, 0)
	for _, dir := range after {
		if _, ok := seen[dir]; ok {
			continue
		}
		added = append(added, dir)
	}
	return added
}
