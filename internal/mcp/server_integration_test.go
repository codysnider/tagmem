package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type benchmarkFixtures struct {
	Entries []struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Depth  int      `json:"depth"`
		Tags   []string `json:"tags"`
		Source string   `json:"source"`
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

func TestMCPMemoryFlowWithBenchmarkFixtures(t *testing.T) {
	t.Parallel()

	fixtures := loadFixtures(t)
	server := newTestServer(t)

	var list struct {
		Tools []map[string]any `json:"tools"`
	}
	callToolRPC(t, server, "tools/list", map[string]any{}, &list)
	assertToolPresent(t, list.Tools, "tiered_memory_add_entry")
	assertToolPresent(t, list.Tools, "tiered_memory_search")
	assertToolPresent(t, list.Tools, "tiered_memory_delete_entry")

	addedIDs := make([]int, 0, len(fixtures.Entries))
	for _, entry := range fixtures.Entries {
		var added struct {
			ID    int      `json:"id"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
		}
		callToolRPC(t, server, "tools/call", map[string]any{
			"name": "tiered_memory_add_entry",
			"arguments": map[string]any{
				"depth":  entry.Depth,
				"title":  entry.Title,
				"body":   entry.Body,
				"tags":   entry.Tags,
				"source": entry.Source,
			},
		}, &added)
		addedIDs = append(addedIDs, added.ID)
	}

	var status map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_status", "arguments": map[string]any{}}, &status)
	if int(status["total_entries"].(float64)) != len(fixtures.Entries) {
		t.Fatalf("total_entries = %v, want %d", status["total_entries"], len(fixtures.Entries))
	}

	for _, query := range fixtures.Queries {
		var results []struct {
			Title string `json:"title"`
		}
		callToolRPC(t, server, "tools/call", map[string]any{
			"name": "tiered_memory_search",
			"arguments": map[string]any{
				"query": query.Query,
				"limit": 5,
			},
		}, &results)
		if len(results) == 0 {
			t.Fatalf("search %q returned no results", query.Query)
		}
		if results[0].Title != query.ExpectTitle {
			t.Fatalf("search %q returned %q first, want %q", query.Query, results[0].Title, query.ExpectTitle)
		}
	}

	var show struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_show_entry", "arguments": map[string]any{"id": addedIDs[0]}}, &show)
	if show.Title != fixtures.Entries[0].Title {
		t.Fatalf("show title = %q, want %q", show.Title, fixtures.Entries[0].Title)
	}

	var duplicates []struct {
		Entry struct {
			Title string `json:"title"`
		} `json:"entry"`
		Similarity float64 `json:"similarity"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name": "tiered_memory_check_duplicate",
		"arguments": map[string]any{
			"content":   fixtures.Entries[0].Body,
			"threshold": 0.5,
		},
	}, &duplicates)
	if len(duplicates) == 0 || duplicates[0].Entry.Title != fixtures.Entries[0].Title {
		t.Fatalf("duplicate check did not return expected title")
	}

	var filtered []struct {
		Title string `json:"title"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_list_entries",
		"arguments": map[string]any{"tag": "locomo", "limit": 10},
	}, &filtered)
	if len(filtered) != 1 || filtered[0].Title != "Support group memory" {
		t.Fatalf("tag filter result = %+v, want support group memory", filtered)
	}

	var deleted map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_delete_entry", "arguments": map[string]any{"id": addedIDs[2]}}, &deleted)
	if deleted["deleted"] != true {
		t.Fatalf("delete result = %v, want true", deleted)
	}

	var remaining map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_status", "arguments": map[string]any{}}, &remaining)
	if int(remaining["total_entries"].(float64)) != len(fixtures.Entries)-1 {
		t.Fatalf("remaining total_entries = %v, want %d", remaining["total_entries"], len(fixtures.Entries)-1)
	}
}

func TestMCPKnowledgeGraphAndDiaryFlow(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	var fact struct {
		Subject   string `json:"subject"`
		Predicate string `json:"predicate"`
		Object    string `json:"object"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name": "tiered_memory_kg_add",
		"arguments": map[string]any{
			"subject":    "caroline",
			"predicate":  "attended",
			"object":     "lgbtq support group",
			"valid_from": "2023-05-07",
		},
	}, &fact)
	if fact.Subject != "caroline" {
		t.Fatalf("kg add subject = %q, want caroline", fact.Subject)
	}

	var query struct {
		Count float64 `json:"count"`
		Facts []struct {
			Object string `json:"object"`
		} `json:"facts"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_kg_query",
		"arguments": map[string]any{"entity": "caroline"},
	}, &query)
	if int(query.Count) != 1 || query.Facts[0].Object != "lgbtq support group" {
		t.Fatalf("kg query = %+v, want one matching fact", query)
	}

	var diaryWrite struct {
		Agent string `json:"agent"`
		Topic string `json:"topic"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name": "tiered_memory_diary_write",
		"arguments": map[string]any{
			"agent_name": "researcher",
			"entry":      "Validated benchmark-derived MCP integration flow.",
			"topic":      "integration",
		},
	}, &diaryWrite)
	if diaryWrite.Agent != "researcher" {
		t.Fatalf("diary write agent = %q, want researcher", diaryWrite.Agent)
	}

	var diaryRead struct {
		Showing float64 `json:"showing"`
		Entries []struct {
			Topic   string `json:"topic"`
			Content string `json:"content"`
		} `json:"entries"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_diary_read",
		"arguments": map[string]any{"agent_name": "researcher", "last_n": 5},
	}, &diaryRead)
	if int(diaryRead.Showing) != 1 || diaryRead.Entries[0].Topic != "integration" {
		t.Fatalf("diary read = %+v, want one integration entry", diaryRead)
	}
}

func TestMCPStartupSequence(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	var initResult map[string]any
	callToolRPC(t, server, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}, &initResult)
	if initResult["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v, want 2024-11-05", initResult["protocolVersion"])
	}

	resp := server.handle(context.Background(), request{JSONRPC: "2.0", Method: "notifications/initialized", Params: json.RawMessage(`{}`)})
	if resp != nil {
		t.Fatalf("initialized notification returned response, want nil")
	}

	var tools struct {
		Tools []map[string]any `json:"tools"`
	}
	callToolRPC(t, server, "tools/list", map[string]any{}, &tools)
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	var resources struct {
		Resources []map[string]any `json:"resources"`
	}
	callToolRPC(t, server, "resources/list", map[string]any{}, &resources)
	if len(resources.Resources) != 0 {
		t.Fatalf("expected no resources, got %d", len(resources.Resources))
	}

	var prompts struct {
		Prompts []map[string]any `json:"prompts"`
	}
	callToolRPC(t, server, "prompts/list", map[string]any{}, &prompts)
	if len(prompts.Prompts) != 0 {
		t.Fatalf("expected no prompts, got %d", len(prompts.Prompts))
	}
}

func TestMCPGraphAndDuplicateEdgeCases(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)
	fixtures := loadGraphFixtures(t)
	for _, entry := range fixtures.Entries {
		var added struct {
			ID int `json:"id"`
		}
		callToolRPC(t, server, "tools/call", map[string]any{
			"name": "tiered_memory_add_entry",
			"arguments": map[string]any{
				"depth": entry.Depth,
				"title": entry.Title,
				"body":  entry.Body,
				"tags":  entry.Tags,
			},
		}, &added)
	}

	var traverse []map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_graph_traverse",
		"arguments": map[string]any{"start_tag": "auth", "max_hops": 2},
	}, &traverse)
	if len(traverse) == 0 {
		t.Fatal("expected graph traverse results")
	}
	assertTraverseEdge(t, traverse, "auth", "security")
	assertTraverseEdge(t, traverse, "auth", "finance")

	var bridges []map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_find_bridges",
		"arguments": map[string]any{"depth_a": 1, "depth_b": 2},
	}, &bridges)
	assertBridgeTag(t, bridges, "auth")

	var stats map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_graph_stats", "arguments": map[string]any{}}, &stats)
	if int(stats["tags"].(float64)) < 4 {
		t.Fatalf("graph tags = %v, want at least 4", stats["tags"])
	}

	var duplicates []struct {
		Entry struct {
			Title string `json:"title"`
		} `json:"entry"`
	}
	callToolRPC(t, server, "tools/call", map[string]any{
		"name":      "tiered_memory_check_duplicate",
		"arguments": map[string]any{"content": "This sentence is unrelated to all stored memories.", "threshold": 0.95},
	}, &duplicates)
	if len(duplicates) != 0 {
		t.Fatalf("expected no duplicate matches, got %d", len(duplicates))
	}

	var deleted map[string]any
	callToolRPC(t, server, "tools/call", map[string]any{"name": "tiered_memory_delete_entry", "arguments": map[string]any{"id": 9999}}, &deleted)
	if deleted["deleted"] != false {
		t.Fatalf("delete missing entry = %v, want false", deleted)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	paths := xdg.Paths{
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
		CacheDir:  filepath.Join(root, "cache"),
		IndexDir:  filepath.Join(root, "data", "vector"),
		ModelDir:  filepath.Join(root, "data", "models"),
		DiaryDir:  filepath.Join(root, "data", "diaries"),
		StorePath: filepath.Join(root, "data", "store.json"),
		KGPath:    filepath.Join(root, "data", "knowledge.json"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("paths.Ensure() error = %v", err)
	}
	repo := store.NewRepository(paths.StorePath, filepath.Join(paths.IndexDir, "test"), vector.EmbeddedHashProvider())
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}
	return New(nil, nil, nil, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, vector.EmbeddedHashProvider())
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

func assertToolPresent(t *testing.T, tools []map[string]any, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool["name"] == name {
			return
		}
	}
	t.Fatalf("tool %q not found", name)
}

func callToolRPC(t *testing.T, server *Server, method string, params map[string]any, out any) {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	resp := server.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: payload})
	if resp == nil {
		t.Fatal("response is nil")
	}
	data, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Marshal(result) error = %v", err)
	}
	var envelope struct {
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Structured) > 0 {
		if envelope.IsError {
			t.Fatalf("tool %s returned error envelope: %s", method, string(data))
		}
		if out != nil {
			if err := json.Unmarshal(envelope.Structured, out); err != nil {
				t.Fatalf("Unmarshal(structuredContent) error = %v\n%s", err, string(envelope.Structured))
			}
		}
		return
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("Unmarshal(result) error = %v\n%s", err, string(data))
		}
	}
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
