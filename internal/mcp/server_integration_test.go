package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
		callTool(t, session, "tagmem_add_entry", map[string]any{"depth": entry.Depth, "title": entry.Title, "body": entry.Body, "tags": entry.Tags, "source": entry.Source}, &added)
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

func TestMCPKnowledgeGraphAndDiaryFlow(t *testing.T) {
	t.Parallel()
	_, session := newTestSession(t)

	var fact struct {
		Fact struct {
			Subject string `json:"subject"`
			Object  string `json:"object"`
		} `json:"fact"`
	}
	callTool(t, session, "tagmem_kg_add", map[string]any{"subject": "caroline", "predicate": "attended", "object": "lgbtq support group", "valid_from": "2023-05-07"}, &fact)
	if fact.Fact.Subject != "caroline" {
		t.Fatalf("fact subject = %q, want caroline", fact.Fact.Subject)
	}

	var query struct {
		Count float64 `json:"count"`
		Facts []struct {
			Object string `json:"object"`
		} `json:"facts"`
	}
	callTool(t, session, "tagmem_kg_query", map[string]any{"entity": "caroline"}, &query)
	if int(query.Count) != 1 || query.Facts[0].Object != "lgbtq support group" {
		t.Fatalf("kg query = %+v", query)
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
	repo := store.NewRepository(paths.StorePath, filepath.Join(paths.IndexDir, "test"), vector.EmbeddedHashProvider())
	if err := repo.Init(); err != nil {
		t.Fatalf("repo.Init() error = %v", err)
	}
	server := New(nil, nil, nil, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, vector.EmbeddedHashProvider())
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
