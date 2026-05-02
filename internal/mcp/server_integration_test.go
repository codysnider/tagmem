package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/xdg"
)

type benchmarkFixtures struct {
	Entries []struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Depth  int      `json:"depth"`
		Tags   []string `json:"tags"`
		Source string   `json:"source"`
		Origin string   `json:"origin"`
	} `json:"entries"`
	Queries []struct {
		Query       string `json:"query"`
		ExpectTitle string `json:"expect_title"`
	} `json:"queries"`
}

type graphFixtures struct {
	Entries []struct {
		Title string   `json:"title"`
		Body  string   `json:"body"`
		Depth int      `json:"depth"`
		Tags  []string `json:"tags"`
	} `json:"entries"`
}

func TestMCPStartupSequence(t *testing.T) {
	t.Parallel()
	_, session := newTestSession(t)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	resources, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources.Resources) != 0 {
		t.Fatalf("expected no resources, got %d", len(resources.Resources))
	}
	prompts, err := session.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrompts() error = %v", err)
	}
	if len(prompts.Prompts) != 0 {
		t.Fatalf("expected no prompts, got %d", len(prompts.Prompts))
	}
}

func TestMCPUsesDaemonBackend(t *testing.T) {
	t.Parallel()
	_, writerSession, shared := newDaemonBackedTestSessions(t)
	readerServer := newDaemonBackedServer(t, shared)
	readerSession := connectTestSession(t, readerServer, "test-client-reader")

	callTool(t, writerSession, "tagmem_add_entry", map[string]any{
		"depth": 1,
		"title": "daemon-backed entry",
		"body":  "stored through daemon backend",
		"tags":  []string{"daemon", "graph"},
	}, nil)

	var status struct {
		TotalEntries float64 `json:"total_entries"`
	}
	callTool(t, readerSession, "tagmem_status", map[string]any{}, &status)
	if int(status.TotalEntries) != 3 {
		t.Fatalf("status.TotalEntries = %v, want 3", status.TotalEntries)
	}

	var readEntries struct {
		Entries []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"entries"`
	}
	callTool(t, readerSession, "tagmem_list_entries", map[string]any{"limit": 10}, &readEntries)
	if len(readEntries.Entries) != 3 {
		t.Fatalf("len(readEntries.Entries) = %d, want 3", len(readEntries.Entries))
	}
	foundSharedEntry := false
	for _, entry := range readEntries.Entries {
		if entry.Title == "daemon-backed entry" && entry.Body == "stored through daemon backend" {
			foundSharedEntry = true
			break
		}
	}
	if !foundSharedEntry {
		t.Fatalf("reader session entries = %+v, want daemon-backed entry written by writer session", readEntries.Entries)
	}

	var graph struct {
		Tags float64 `json:"tags"`
	}
	callTool(t, readerSession, "tagmem_graph_stats", map[string]any{}, &graph)
	if int(graph.Tags) < 2 {
		t.Fatalf("graph.Tags = %v, want at least 2", graph.Tags)
	}

	var addedFact struct {
		Fact struct {
			Subject string `json:"subject"`
			Object  string `json:"object"`
		} `json:"fact"`
	}
	callTool(t, writerSession, "tagmem_kg_add", map[string]any{"subject": "daemon-user", "predicate": "uses", "object": "daemon backend", "source_entry": "entry:1"}, &addedFact)
	if addedFact.Fact.Subject != "daemon-user" || addedFact.Fact.Object != "daemon backend" {
		t.Fatalf("addedFact = %+v", addedFact.Fact)
	}

	var queriedFacts struct {
		Count float64 `json:"count"`
		Facts []struct {
			Object string `json:"object"`
		} `json:"facts"`
	}
	callTool(t, readerSession, "tagmem_kg_query", map[string]any{"entity": "daemon-user", "direction": "outgoing"}, &queriedFacts)
	if int(queriedFacts.Count) != 2 {
		t.Fatalf("queriedFacts.Count = %v, want 2", queriedFacts.Count)
	}

	callTool(t, writerSession, "tagmem_diary_write", map[string]any{"agent_name": "daemon-agent", "entry": "daemon-backed diary write", "topic": "daemon"}, nil)
	var diaryRead struct {
		Showing float64 `json:"showing"`
		Entries []struct {
			Topic string `json:"topic"`
		} `json:"entries"`
	}
	callTool(t, readerSession, "tagmem_diary_read", map[string]any{"agent_name": "daemon-agent", "last_n": 5}, &diaryRead)
	if int(diaryRead.Showing) != 2 || diaryRead.Entries[0].Topic != "daemon" {
		t.Fatalf("diaryRead = %+v", diaryRead)
	}
}

func TestMCPUsesDaemonBackendForDuplicateCheck(t *testing.T) {
	t.Parallel()
	_, session := newDaemonBackedTestSession(t)

	callTool(t, session, "tagmem_add_entry", map[string]any{
		"depth": 1,
		"title": "duplicate source",
		"body":  "daemon duplicate body",
	}, nil)

	var matches struct {
		Matches []struct {
			Entry struct {
				Title string `json:"title"`
			} `json:"entry"`
		} `json:"matches"`
	}
	callTool(t, session, "tagmem_check_duplicate", map[string]any{"content": "daemon duplicate body", "threshold": 0.1}, &matches)
	if len(matches.Matches) == 0 {
		t.Fatal("matches.Matches = empty, want duplicate matches")
	}
	if matches.Matches[0].Entry.Title != "duplicate source" {
		t.Fatalf("matches.Matches[0].Entry.Title = %q, want %q", matches.Matches[0].Entry.Title, "duplicate source")
	}
}

func TestMCPUsesDaemonBackendForCrossClientSemanticSearch(t *testing.T) {
	t.Parallel()
	_, writerSession, shared := newDaemonBackedTestSessions(t)
	readerServer := newDaemonBackedServer(t, shared)
	readerSession := connectTestSession(t, readerServer, "test-client-search-reader")

	callTool(t, writerSession, "tagmem_add_entry", map[string]any{
		"depth": 1,
		"title": "nebula orchard ledger",
		"body":  "Nebula orchard ledger handshake sequence for daemon-backed semantic search.",
		"tags":  []string{"semantic", "daemon"},
	}, nil)

	var results struct {
		Entries []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"entries"`
	}
	callTool(t, readerSession, "tagmem_search", map[string]any{"query": "nebula orchard ledger handshake", "limit": 5}, &results)
	if len(results.Entries) == 0 {
		t.Fatal("semantic search entries = empty, want cross-client daemon-backed result")
	}
	if results.Entries[0].Title != "nebula orchard ledger" {
		t.Fatalf("semantic search first title = %q, want %q", results.Entries[0].Title, "nebula orchard ledger")
	}
	if results.Entries[0].Body != "Nebula orchard ledger handshake sequence for daemon-backed semantic search." {
		t.Fatalf("semantic search first body = %q", results.Entries[0].Body)
	}
}

func TestMCPMemoryFlowWithBenchmarkFixtures(t *testing.T) {
	t.Parallel()
	fixtures := loadFixtures(t)
	_, session := newTestSession(t)

	for _, entry := range fixtures.Entries {
		var added struct {
			Entry struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
			} `json:"entry"`
		}
		callTool(t, session, "tagmem_add_entry", map[string]any{"depth": entry.Depth, "title": entry.Title, "body": entry.Body, "tags": entry.Tags, "source": entry.Source, "origin": entry.Origin}, &added)
		if added.Entry.Title != entry.Title {
			t.Fatalf("added title = %q, want %q", added.Entry.Title, entry.Title)
		}
	}

	var status map[string]any
	callTool(t, session, "tagmem_status", map[string]any{}, &status)
	if int(status["total_entries"].(float64)) != len(fixtures.Entries) {
		t.Fatalf("total_entries = %v, want %d", status["total_entries"], len(fixtures.Entries))
	}

	for _, query := range fixtures.Queries {
		var results struct {
			Entries []struct {
				Title string `json:"title"`
			} `json:"entries"`
		}
		callTool(t, session, "tagmem_search", map[string]any{"query": query.Query, "limit": 5}, &results)
		if len(results.Entries) == 0 || results.Entries[0].Title != query.ExpectTitle {
			t.Fatalf("search %q first=%v want %q", query.Query, results, query.ExpectTitle)
		}
	}
}

func TestMCPKnowledgeGraphLifecycleAndDiaryFlow(t *testing.T) {
	t.Parallel()
	_, session := newTestSession(t)

	var firstFact struct {
		Fact struct {
			Subject   string `json:"subject"`
			Predicate string `json:"predicate"`
			Object    string `json:"object"`
			Source    string `json:"source"`
		} `json:"fact"`
	}
	callTool(t, session, "tagmem_kg_add", map[string]any{"subject": "caroline", "predicate": "attended", "object": "lgbtq support group", "valid_from": "2023-05-07", "source_entry": "entry:1"}, &firstFact)
	if firstFact.Fact.Subject != "caroline" || firstFact.Fact.Predicate != "attended" || firstFact.Fact.Source != "entry:1" {
		t.Fatalf("firstFact = %+v", firstFact.Fact)
	}
	callTool(t, session, "tagmem_kg_add", map[string]any{"subject": "caroline", "predicate": "lives_in", "object": "new york", "valid_from": "2024-01-01", "source_entry": "entry:2"}, nil)
	callTool(t, session, "tagmem_kg_invalidate", map[string]any{"subject": "caroline", "predicate": "lives_in", "object": "new york", "ended": "2025-12-31"}, nil)
	callTool(t, session, "tagmem_kg_add", map[string]any{"subject": "caroline", "predicate": "lives_in", "object": "san francisco", "valid_from": "2026-01-01", "source_entry": "entry:3"}, nil)

	var currentQuery struct {
		Count float64 `json:"count"`
		Facts []struct {
			Predicate string `json:"predicate"`
			Object    string `json:"object"`
		} `json:"facts"`
	}
	callTool(t, session, "tagmem_kg_query", map[string]any{"entity": "caroline", "direction": "outgoing"}, &currentQuery)
	if int(currentQuery.Count) != 2 {
		t.Fatalf("currentQuery.Count = %v, want 2", currentQuery.Count)
	}
	assertFact(t, currentQuery.Facts, "attended", "lgbtq support group")
	assertFact(t, currentQuery.Facts, "lives_in", "san francisco")

	var historicalQuery struct {
		Count float64 `json:"count"`
		Facts []struct {
			Predicate string `json:"predicate"`
			Object    string `json:"object"`
		} `json:"facts"`
	}
	callTool(t, session, "tagmem_kg_query", map[string]any{"entity": "caroline", "direction": "outgoing", "as_of": "2025-06-01"}, &historicalQuery)
	if int(historicalQuery.Count) != 2 {
		t.Fatalf("historicalQuery.Count = %v, want 2", historicalQuery.Count)
	}
	assertFact(t, historicalQuery.Facts, "attended", "lgbtq support group")
	assertFact(t, historicalQuery.Facts, "lives_in", "new york")

	var incomingQuery struct {
		Count float64 `json:"count"`
		Facts []struct {
			Subject string `json:"subject"`
			Object  string `json:"object"`
		} `json:"facts"`
	}
	callTool(t, session, "tagmem_kg_query", map[string]any{"entity": "san francisco", "direction": "incoming"}, &incomingQuery)
	if int(incomingQuery.Count) != 1 || incomingQuery.Facts[0].Subject != "caroline" || incomingQuery.Facts[0].Object != "san francisco" {
		t.Fatalf("incomingQuery = %+v", incomingQuery)
	}

	var timeline struct {
		Count    float64 `json:"count"`
		Timeline []struct {
			Predicate string `json:"predicate"`
			Object    string `json:"object"`
			ValidFrom string `json:"valid_from"`
			ValidTo   string `json:"valid_to"`
		} `json:"timeline"`
	}
	callTool(t, session, "tagmem_kg_timeline", map[string]any{"entity": "caroline"}, &timeline)
	if int(timeline.Count) != 3 {
		t.Fatalf("timeline.Count = %v, want 3", timeline.Count)
	}
	if timeline.Timeline[0].Object != "lgbtq support group" || timeline.Timeline[1].Object != "new york" || timeline.Timeline[2].Object != "san francisco" {
		t.Fatalf("timeline = %+v, want chronological fact order", timeline.Timeline)
	}
	if timeline.Timeline[1].ValidTo != "2025-12-31" {
		t.Fatalf("timeline[1].ValidTo = %q, want 2025-12-31", timeline.Timeline[1].ValidTo)
	}

	var stats map[string]any
	callTool(t, session, "tagmem_kg_stats", map[string]any{}, &stats)
	if int(stats["facts"].(float64)) != 3 || int(stats["current"].(float64)) != 2 || int(stats["expired"].(float64)) != 1 {
		t.Fatalf("stats = %+v, want facts=3 current=2 expired=1", stats)
	}

	var diaryRead struct {
		Showing float64 `json:"showing"`
		Entries []struct {
			Topic string `json:"topic"`
		} `json:"entries"`
	}
	callTool(t, session, "tagmem_diary_write", map[string]any{"agent_name": "researcher", "entry": "Validated benchmark-derived MCP integration flow.", "topic": "integration"}, nil)
	callTool(t, session, "tagmem_diary_read", map[string]any{"agent_name": "researcher", "last_n": 5}, &diaryRead)
	if int(diaryRead.Showing) != 1 || diaryRead.Entries[0].Topic != "integration" {
		t.Fatalf("diary read = %+v", diaryRead)
	}
}

func TestMCPSearchReturnsComputedSignals(t *testing.T) {
	t.Parallel()
	_, session := newTestSession(t)
	sharedTags := []string{"staging", "database", "config"}
	callTool(t, session, "tagmem_add_entry", map[string]any{"depth": 1, "title": "Legacy staging database", "body": "Staging uses mysql.internal.example.com.", "tags": sharedTags, "origin": "docs/legacy.md"}, nil)
	callTool(t, session, "tagmem_add_entry", map[string]any{"depth": 1, "title": "Staging database", "body": "Staging uses postgres.internal.example.com.", "tags": sharedTags, "origin": "manual"}, nil)
	callTool(t, session, "tagmem_add_entry", map[string]any{"depth": 1, "title": "Staging database confirmation", "body": "Staging uses postgres.internal.example.com.", "tags": sharedTags, "origin": "notes/runbook.md"}, nil)

	var results struct {
		Entries []struct {
			Title string `json:"title"`
		} `json:"entries"`
		Results []struct {
			Entry struct {
				Body string `json:"body"`
			} `json:"entry"`
			SupportCount  int `json:"support_count"`
			SourceKinds   int `json:"source_kinds"`
			ConflictCount int `json:"conflict_count"`
		} `json:"results"`
	}
	callTool(t, session, "tagmem_search", map[string]any{"query": "What database does staging use?", "limit": 5}, &results)
	if len(results.Entries) == 0 || len(results.Results) == 0 {
		t.Fatalf("search result payload missing entries or results: %+v", results)
	}
	if results.Results[0].Entry.Body != "Staging uses postgres.internal.example.com." {
		t.Fatalf("results[0].entry.body = %q, want postgres match", results.Results[0].Entry.Body)
	}
	if results.Results[0].SupportCount != 2 {
		t.Fatalf("results[0].support_count = %d, want 2", results.Results[0].SupportCount)
	}
	if results.Results[0].SourceKinds != 2 {
		t.Fatalf("results[0].source_kinds = %d, want 2", results.Results[0].SourceKinds)
	}
	if results.Results[0].ConflictCount != 1 {
		t.Fatalf("results[0].conflict_count = %d, want 1", results.Results[0].ConflictCount)
	}
}

func TestMCPFactRubricAssessesPromotion(t *testing.T) {
	t.Parallel()
	_, session := newTestSession(t)

	var canonical struct {
		Assessment struct {
			StoreAsFact bool   `json:"store_as_fact"`
			KeepAsEntry bool   `json:"keep_as_entry"`
			Predicate   string `json:"predicate"`
		} `json:"assessment"`
	}
	callTool(t, session, "tagmem_fact_rubric", map[string]any{"text": "Staging uses postgres.internal.example.com."}, &canonical)
	if !canonical.Assessment.StoreAsFact || canonical.Assessment.KeepAsEntry || canonical.Assessment.Predicate != "uses" {
		t.Fatalf("canonical assessment = %+v", canonical.Assessment)
	}

	var soft struct {
		Assessment struct {
			StoreAsFact bool `json:"store_as_fact"`
			KeepAsEntry bool `json:"keep_as_entry"`
		} `json:"assessment"`
	}
	callTool(t, session, "tagmem_fact_rubric", map[string]any{"text": "We discussed maybe moving staging to postgres next quarter."}, &soft)
	if soft.Assessment.StoreAsFact || !soft.Assessment.KeepAsEntry {
		t.Fatalf("soft assessment = %+v", soft.Assessment)
	}
}

func TestMCPGraphAndDuplicateEdgeCases(t *testing.T) {
	t.Parallel()
	fixtures := loadGraphFixtures(t)
	_, session := newTestSession(t)
	for _, entry := range fixtures.Entries {
		callTool(t, session, "tagmem_add_entry", map[string]any{"depth": entry.Depth, "title": entry.Title, "body": entry.Body, "tags": entry.Tags}, nil)
	}
	var traverse struct {
		Edges []map[string]any `json:"edges"`
	}
	callTool(t, session, "tagmem_graph_traverse", map[string]any{"start_tag": "auth", "max_hops": 2}, &traverse)
	assertTraverseEdge(t, traverse.Edges, "auth", "security")
	assertTraverseEdge(t, traverse.Edges, "auth", "finance")

	var bridges struct {
		Bridges []map[string]any `json:"bridges"`
	}
	callTool(t, session, "tagmem_find_bridges", map[string]any{"depth_a": 1, "depth_b": 2}, &bridges)
	assertBridgeTag(t, bridges.Bridges, "auth")

	var stats map[string]any
	callTool(t, session, "tagmem_graph_stats", map[string]any{}, &stats)
	if int(stats["tags"].(float64)) < 4 {
		t.Fatalf("graph tags = %v, want at least 4", stats["tags"])
	}
}

func newTestSession(t *testing.T) (*Server, *sdk.ClientSession) {
	t.Helper()
	root := t.TempDir()
	paths := xdg.Paths{ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"), CacheDir: filepath.Join(root, "cache"), IndexDir: filepath.Join(root, "data", "vector"), ModelDir: filepath.Join(root, "data", "models"), DiaryDir: filepath.Join(root, "data", "diaries"), StorePath: filepath.Join(root, "data", "store.json"), KGPath: filepath.Join(root, "data", "knowledge.json")}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}
	repo := store.NewRepository(paths.StorePath, filepath.Join(paths.IndexDir, "test"), fakeembed.Provider())
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}
	server := New(nil, nil, nil, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, fakeembed.Provider())
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	t1, t2 := sdk.NewInMemoryTransports()
	if _, err := server.server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return server, session
}

func newDaemonBackedTestSession(t *testing.T) (*Server, *sdk.ClientSession) {
	t.Helper()
	server, writerSession, _ := newDaemonBackedTestSessions(t)
	return server, writerSession
}

type daemonBackedSessionShared struct {
	SocketPath string
}

func newDaemonBackedTestSessions(t *testing.T) (*Server, *sdk.ClientSession, daemonBackedSessionShared) {
	t.Helper()

	daemonProvider := fakeembed.Provider()
	daemonRoot := t.TempDir()
	daemonPaths := xdg.Paths{ConfigDir: filepath.Join(daemonRoot, "config"), DataDir: filepath.Join(daemonRoot, "data"), CacheDir: filepath.Join(daemonRoot, "cache"), IndexDir: filepath.Join(daemonRoot, "data", "vector"), ModelDir: filepath.Join(daemonRoot, "data", "models"), DiaryDir: filepath.Join(daemonRoot, "data", "diaries"), StorePath: filepath.Join(daemonRoot, "data", "store.json"), KGPath: filepath.Join(daemonRoot, "data", "knowledge.json"), SocketPath: filepath.Join(daemonRoot, "runtime", "tagmem.sock")}
	if err := daemonPaths.Ensure(); err != nil {
		t.Fatalf("daemonPaths.Ensure() error = %v", err)
	}
	daemonRepo := store.NewRepository(daemonPaths.StorePath, daemonProvider.IndexPath(daemonPaths.IndexDir), daemonProvider)
	if err := daemonRepo.Init(); err != nil {
		t.Fatalf("daemonRepo.Init() error = %v", err)
	}
	if _, err := daemonRepo.Add(store.AddEntry{Depth: 1, Title: "daemon seed", Body: "entry already owned by daemon"}); err != nil {
		t.Fatalf("daemonRepo.Add() error = %v", err)
	}
	if _, err := daemonRepo.Add(store.AddEntry{Depth: 1, Title: "daemon graph seed", Body: "graph seed body", Tags: []string{"daemon", "graph"}}); err != nil {
		t.Fatalf("daemonRepo.Add() graph seed error = %v", err)
	}
	daemonKG := kg.New(daemonPaths.KGPath)
	if _, err := daemonKG.Add("daemon-user", "uses", "seed daemon", "", "entry:0"); err != nil {
		t.Fatalf("daemonKG.Add() error = %v", err)
	}
	daemonDiary := diary.New(daemonPaths.DiaryDir)
	if _, err := daemonDiary.Write("daemon-agent", "existing daemon diary entry", "daemon"); err != nil {
		t.Fatalf("daemonDiary.Write() error = %v", err)
	}
	daemonBackend := daemon.NewBackend(daemonRepo, daemonKG, daemonDiary, daemonPaths, daemonProvider)
	daemonServer := daemon.NewServer(daemonPaths.SocketPath, daemonBackend)
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	daemonErr := make(chan error, 1)
	go func() {
		daemonErr <- daemonServer.Run(daemonCtx)
	}()
	waitForDaemonSocket(t, daemonPaths.SocketPath)
	t.Cleanup(func() {
		cancelDaemon()
		if err := <-daemonErr; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("daemonServer.Run() error = %v", err)
		}
	})

	mcpProvider := fakeembed.Provider()
	mcpRoot := t.TempDir()
	mcpPaths := xdg.Paths{ConfigDir: filepath.Join(mcpRoot, "config"), DataDir: filepath.Join(mcpRoot, "data"), CacheDir: filepath.Join(mcpRoot, "cache"), IndexDir: filepath.Join(mcpRoot, "data", "vector"), ModelDir: filepath.Join(mcpRoot, "data", "models"), DiaryDir: filepath.Join(mcpRoot, "data", "diaries"), StorePath: filepath.Join(mcpRoot, "data", "store.json"), KGPath: filepath.Join(mcpRoot, "data", "knowledge.json"), SocketPath: daemonPaths.SocketPath}
	if err := mcpPaths.Ensure(); err != nil {
		t.Fatalf("mcpPaths.Ensure() error = %v", err)
	}
	mcpRepo := store.NewRepository(mcpPaths.StorePath, mcpProvider.IndexPath(mcpPaths.IndexDir), mcpProvider)
	server := NewWithDaemonSocket(nil, nil, nil, daemonPaths.SocketPath, mcpRepo, kg.New(mcpPaths.KGPath), diary.New(mcpPaths.DiaryDir), mcpPaths, mcpProvider)
	writerSession := connectTestSession(t, server, "test-client-writer")
	return server, writerSession, daemonBackedSessionShared{SocketPath: daemonPaths.SocketPath}
}

func newDaemonBackedServer(t *testing.T, shared daemonBackedSessionShared) *Server {
	t.Helper()

	mcpProvider := fakeembed.Provider()
	mcpRoot := t.TempDir()
	mcpPaths := xdg.Paths{ConfigDir: filepath.Join(mcpRoot, "config"), DataDir: filepath.Join(mcpRoot, "data"), CacheDir: filepath.Join(mcpRoot, "cache"), IndexDir: filepath.Join(mcpRoot, "data", "vector"), ModelDir: filepath.Join(mcpRoot, "data", "models"), DiaryDir: filepath.Join(mcpRoot, "data", "diaries"), StorePath: filepath.Join(mcpRoot, "data", "store.json"), KGPath: filepath.Join(mcpRoot, "data", "knowledge.json"), SocketPath: shared.SocketPath}
	if err := mcpPaths.Ensure(); err != nil {
		t.Fatalf("mcpPaths.Ensure() error = %v", err)
	}
	mcpRepo := store.NewRepository(mcpPaths.StorePath, mcpProvider.IndexPath(mcpPaths.IndexDir), mcpProvider)
	return NewWithDaemonSocket(nil, nil, nil, shared.SocketPath, mcpRepo, kg.New(mcpPaths.KGPath), diary.New(mcpPaths.DiaryDir), mcpPaths, mcpProvider)
}

func connectTestSession(t *testing.T, server *Server, clientName string) *sdk.ClientSession {
	t.Helper()

	client := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: "0.0.1"}, nil)
	t1, t2 := sdk.NewInMemoryTransports()
	if _, err := server.server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func waitForDaemonSocket(t *testing.T, socketPath string) {
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

func callTool(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	if res.IsError {
		payload, _ := json.Marshal(res.Content)
		t.Fatalf("CallTool(%s) returned error content: %s", name, string(payload))
	}
	if out == nil {
		return
	}
	if res.StructuredContent == nil {
		payload, _ := json.Marshal(res.Content)
		t.Fatalf("CallTool(%s) missing structured content: %s", name, string(payload))
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("Marshal structured content for %s error = %v", name, err)
	}
	if err := json.Unmarshal(structured, out); err != nil {
		t.Fatalf("Unmarshal structured content for %s error = %v", name, err)
	}
}

func loadFixtures(t *testing.T) benchmarkFixtures {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "benchmark_fixtures.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixtures benchmarkFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return fixtures
}

func loadGraphFixtures(t *testing.T) graphFixtures {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "graph_fixtures.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixtures graphFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return fixtures
}

func assertTraverseEdge(t *testing.T, edges []map[string]any, from, to string) {
	t.Helper()
	for _, edge := range edges {
		if edge["from"] == from && edge["to"] == to {
			return
		}
	}
	t.Fatalf("expected traverse edge %s -> %s not found", from, to)
}

func assertBridgeTag(t *testing.T, bridges []map[string]any, tag string) {
	t.Helper()
	for _, bridge := range bridges {
		if bridge["tag"] == tag {
			return
		}
	}
	t.Fatalf("expected bridge tag %q not found", tag)
}

func assertFact(t *testing.T, facts []struct {
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}, predicate, object string) {
	t.Helper()
	for _, fact := range facts {
		if fact.Predicate == predicate && fact.Object == object {
			return
		}
	}
	t.Fatalf("expected fact %s -> %s not found in %+v", predicate, object, facts)
}
