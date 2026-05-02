package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codysnider/tagmem/internal/buildinfo"
	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type Server struct {
	backend *toolBackend
	server  *sdk.Server
}

func New(_ any, _ any, _ any, repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *Server {
	s := &Server{backend: newDirectToolBackend(repo, kgStore, diaryStore, paths, provider)}
	s.server = sdk.NewServer(&sdk.Implementation{Name: "tagmem", Version: buildinfo.Version}, nil)
	s.registerTools()
	return s
}

func NewWithDaemonSocket(_ any, _ any, _ any, socketPath string, repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *Server {
	_ = repo
	_ = kgStore
	_ = diaryStore
	_ = paths
	_ = provider
	s := &Server{backend: newDaemonToolBackend(socketPath)}
	s.server = sdk.NewServer(&sdk.Implementation{Name: "tagmem", Version: buildinfo.Version}, nil)
	s.registerTools()
	return s
}

type toolCaller interface {
	Call(context.Context, string, map[string]any) (map[string]any, error)
}

type toolBackend struct {
	caller toolCaller
}

func (b *toolBackend) Call(ctx context.Context, name string, payload map[string]any) (map[string]any, error) {
	if b == nil || b.caller == nil {
		return nil, fmt.Errorf("mcp backend is required")
	}
	return b.caller.Call(ctx, name, payload)
}

type directToolBackend struct {
	repo     *store.Repository
	kg       *kg.Store
	diary    *diary.Store
	paths    xdg.Paths
	provider vector.Provider
}

func newDirectToolBackend(repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *toolBackend {
	return &toolBackend{caller: &directToolBackend{repo: repo, kg: kgStore, diary: diaryStore, paths: paths, provider: provider}}
}

func (b *directToolBackend) Call(ctx context.Context, name string, payload map[string]any) (map[string]any, error) {
	switch name {
	case "tagmem_status":
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
	case "tagmem_paths":
		return map[string]any{"data": b.paths.DataDir, "config": b.paths.ConfigDir, "cache": b.paths.CacheDir, "store": b.paths.StorePath, "index": b.provider.IndexPath(b.paths.IndexDir)}, nil
	case "tagmem_list_depths":
		depths, err := b.repo.DepthCounts()
		if err != nil {
			return nil, err
		}
		return map[string]any{"depths": depths}, nil
	case "tagmem_list_tags":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"tags": taggraph.TagCounts(entries)}, nil
	case "tagmem_get_tag_map":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"tag_map": taggraph.DepthTagMap(entries)}, nil
	case "tagmem_list_entries":
		query := queryFromPayload(payload, 25)
		entries, err := b.repo.List(query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil
	case "tagmem_search":
		query := queryFromPayload(payload, 5)
		results, err := b.repo.SearchDetailed(query)
		if err != nil {
			return nil, err
		}
		entries := make([]store.Entry, 0, len(results))
		for _, result := range results {
			entries = append(entries, result.Entry)
		}
		return map[string]any{"entries": entries, "results": results}, nil
	case "tagmem_show_entry":
		id, err := payloadInt(payload, "id")
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
	case "tagmem_check_duplicate":
		matches, err := b.repo.DuplicateCheck(payloadString(payload, "content"), payloadFloat(payload, "threshold"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"matches": matches}, nil
	case "tagmem_add_entry":
		depth := 1
		if value, ok := payloadOptionalInt(payload, "depth"); ok && value > 0 {
			depth = value
		}
		entry, err := b.repo.Add(store.AddEntry{Depth: depth, Title: payloadString(payload, "title"), Body: payloadString(payload, "body"), Tags: payloadStringSlice(payload, "tags"), Source: payloadString(payload, "source"), Origin: payloadString(payload, "origin")})
		if err != nil {
			return nil, err
		}
		return map[string]any{"entry": entry}, nil
	case "tagmem_delete_entry":
		id, err := payloadInt(payload, "id")
		if err != nil {
			return nil, err
		}
		deleted, err := b.repo.Delete(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": deleted, "id": id}, nil
	case "tagmem_graph_traverse":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return map[string]any{"edges": taggraph.Traverse(entries, payloadString(payload, "start_tag"), payloadDefaultInt(payload, "max_hops", 0))}, nil
	case "tagmem_find_bridges":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		var depthA, depthB *int
		if value, ok := payloadOptionalInt(payload, "depth_a"); ok {
			depthA = &value
		}
		if value, ok := payloadOptionalInt(payload, "depth_b"); ok {
			depthB = &value
		}
		return map[string]any{"bridges": taggraph.FindBridges(entries, depthA, depthB)}, nil
	case "tagmem_graph_stats":
		entries, err := b.repo.ListMetadata(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return taggraph.Stats(entries), nil
	case "tagmem_kg_query":
		direction := payloadString(payload, "direction")
		if direction == "" {
			direction = "both"
		}
		facts, err := b.kg.Query(payloadString(payload, "entity"), payloadString(payload, "as_of"), direction)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": payloadString(payload, "entity"), "as_of": payloadString(payload, "as_of"), "facts": facts, "count": len(facts)}, nil
	case "tagmem_kg_add":
		fact, err := b.kg.Add(payloadString(payload, "subject"), payloadString(payload, "predicate"), payloadString(payload, "object"), payloadString(payload, "valid_from"), payloadString(payload, "source_entry"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"fact": fact}, nil
	case "tagmem_kg_invalidate":
		if err := b.kg.Invalidate(payloadString(payload, "subject"), payloadString(payload, "predicate"), payloadString(payload, "object"), payloadString(payload, "ended")); err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	case "tagmem_kg_timeline":
		timeline, err := b.kg.Timeline(payloadString(payload, "entity"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": payloadString(payload, "entity"), "timeline": timeline, "count": len(timeline)}, nil
	case "tagmem_kg_stats":
		stats, err := b.kg.Stats()
		if err != nil {
			return nil, err
		}
		return stats, nil
	case "tagmem_diary_write":
		entry, err := b.diary.Write(payloadString(payload, "agent_name"), payloadString(payload, "entry"), payloadString(payload, "topic"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"entry": entry}, nil
	case "tagmem_diary_read":
		lastN := payloadDefaultInt(payload, "last_n", 10)
		entries, err := b.diary.Read(payloadString(payload, "agent_name"), lastN)
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent": payloadString(payload, "agent_name"), "entries": entries, "showing": len(entries)}, nil
	case "tagmem_doctor":
		return map[string]any{"report": b.provider.Doctor(ctx)}, nil
	default:
		return nil, fmt.Errorf("unsupported mcp backend tool %q", name)
	}
}

type daemonToolBackend struct {
	socketPath string
}

func newDaemonToolBackend(socketPath string) *toolBackend {
	return &toolBackend{caller: &daemonToolBackend{socketPath: socketPath}}
}

func (b *daemonToolBackend) Call(ctx context.Context, name string, payload map[string]any) (map[string]any, error) {
	command, ok := daemonCommandName(name)
	if !ok {
		return nil, fmt.Errorf("unsupported daemon-backed mcp tool %q", name)
	}
	response, err := daemon.Call(ctx, b.socketPath, daemon.Request{ID: daemonRequestID(name), Command: command, Payload: payload})
	if err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New(response.Error)
	}
	return normalizeDaemonPayload(name, response.Payload), nil
}

func daemonCommandName(name string) (string, bool) {
	switch name {
	case "tagmem_status":
		return "status", true
	case "tagmem_paths":
		return "paths", true
	case "tagmem_list_depths":
		return "list_depths", true
	case "tagmem_list_tags":
		return "list_tags", true
	case "tagmem_get_tag_map":
		return "get_tag_map", true
	case "tagmem_list_entries":
		return "list_entries", true
	case "tagmem_search":
		return "search", true
	case "tagmem_show_entry":
		return "show_entry", true
	case "tagmem_check_duplicate":
		return "check_duplicate", true
	case "tagmem_add_entry":
		return "add_entry", true
	case "tagmem_delete_entry":
		return "delete_entry", true
	case "tagmem_kg_query":
		return "kg_query", true
	case "tagmem_kg_add":
		return "kg_add", true
	case "tagmem_kg_invalidate":
		return "kg_invalidate", true
	case "tagmem_kg_timeline":
		return "kg_timeline", true
	case "tagmem_kg_stats":
		return "kg_stats", true
	case "tagmem_graph_traverse":
		return "graph_traverse", true
	case "tagmem_find_bridges":
		return "find_bridges", true
	case "tagmem_graph_stats":
		return "graph_stats", true
	case "tagmem_diary_write":
		return "diary_write", true
	case "tagmem_diary_read":
		return "diary_read", true
	case "tagmem_doctor":
		return "doctor", true
	default:
		return "", false
	}
}

func normalizeDaemonPayload(name string, payload map[string]any) map[string]any {
	if name != "tagmem_paths" || payload == nil {
		return payload
	}
	return map[string]any{
		"data":   payload["data"],
		"config": payload["config"],
		"cache":  payload["cache"],
		"store":  payload["store"],
		"index":  payload["index"],
	}
}

func daemonRequestID(name string) string {
	return fmt.Sprintf("mcp-%s", name)
}

func queryFromPayload(payload map[string]any, fallback int) store.Query {
	query := store.Query{Limit: payloadDefaultInt(payload, "limit", fallback), Tag: payloadString(payload, "tag"), Text: payloadString(payload, "query")}
	if value, ok := payloadOptionalInt(payload, "depth"); ok {
		query.Depth = &value
	}
	return query
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return value
}

func payloadStringSlice(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	items, _ := payload[key].([]string)
	if len(items) > 0 {
		return items
	}
	values, _ := payload[key].([]any)
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		if text != "" {
			result = append(result, text)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func payloadInt(payload map[string]any, key string) (int, error) {
	value, ok := payloadOptionalInt(payload, key)
	if !ok {
		return 0, fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func payloadOptionalInt(payload map[string]any, key string) (int, bool) {
	if payload == nil {
		return 0, false
	}
	switch value := payload[key].(type) {
	case int:
		return value, true
	case *int:
		if value == nil {
			return 0, false
		}
		return *value, true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func payloadDefaultInt(payload map[string]any, key string, fallback int) int {
	if value, ok := payloadOptionalInt(payload, key); ok && value > 0 {
		return value
	}
	return fallback
}

func payloadFloat(payload map[string]any, key string) float64 {
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

func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &sdk.StdioTransport{})
}

func (s *Server) registerTools() {
	type empty struct{}

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_status", Description: "Overview of entries, depths, tags, and active embedding backend."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_status", nil)
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_paths", Description: "Resolved data, config, cache, store, and index paths."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_paths", nil)
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_depths", Description: "Counts for all depths in the local memory store."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_list_depths", nil)
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_tags", Description: "Counts for all tags derived from entries."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_list_tags", nil)
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_get_tag_map", Description: "Depth to tag count map."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_get_tag_map", nil)
		return nil, payload, err
	})

	type listArgs struct {
		Depth *int   `json:"depth,omitempty"`
		Tag   string `json:"tag,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_entries", Description: "List entries, optionally filtered by depth or tag."}, func(ctx context.Context, _ *sdk.CallToolRequest, in listArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_list_entries", map[string]any{"depth": in.Depth, "tag": in.Tag, "limit": in.Limit})
		return nil, payload, err
	})

	type searchArgs struct {
		Query string `json:"query"`
		Depth *int   `json:"depth,omitempty"`
		Tag   string `json:"tag,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_search", Description: "Semantic search over entries with optional depth or tag filter."}, func(ctx context.Context, _ *sdk.CallToolRequest, in searchArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_search", map[string]any{"query": in.Query, "depth": in.Depth, "tag": in.Tag, "limit": in.Limit})
		return nil, payload, err
	})

	type factRubricArgs struct {
		Text string `json:"text"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_fact_rubric", Description: "Assess whether text should stay as an entry, become a knowledge graph fact, or both."}, func(ctx context.Context, _ *sdk.CallToolRequest, in factRubricArgs) (*sdk.CallToolResult, map[string]any, error) {
		assessment := kg.AssessFactPromotion(in.Text)
		return nil, map[string]any{"assessment": assessment}, nil
	})

	type idArgs struct {
		ID int `json:"id"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_show_entry", Description: "Show one entry by numeric ID."}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_show_entry", map[string]any{"id": in.ID})
		return nil, payload, err
	})

	type duplicateArgs struct {
		Content   string  `json:"content"`
		Threshold float64 `json:"threshold,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_check_duplicate", Description: "Check whether content already exists before storing it."}, func(ctx context.Context, _ *sdk.CallToolRequest, in duplicateArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_check_duplicate", map[string]any{"content": in.Content, "threshold": in.Threshold})
		return nil, payload, err
	})

	type addArgs struct {
		Depth  *int     `json:"depth,omitempty"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Tags   []string `json:"tags,omitempty"`
		Source string   `json:"source,omitempty"`
		Origin string   `json:"origin,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_add_entry", Description: "Add a new memory entry."}, func(ctx context.Context, _ *sdk.CallToolRequest, in addArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_add_entry", map[string]any{"depth": in.Depth, "title": in.Title, "body": in.Body, "tags": in.Tags, "source": in.Source, "origin": in.Origin})
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_delete_entry", Description: "Delete one entry by numeric ID."}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_delete_entry", map[string]any{"id": in.ID})
		return nil, payload, err
	})

	type entityArgs struct {
		Entity    string `json:"entity"`
		AsOf      string `json:"as_of,omitempty"`
		Direction string `json:"direction,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_query", Description: "Query the knowledge graph for an entity."}, func(ctx context.Context, _ *sdk.CallToolRequest, in entityArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_kg_query", map[string]any{"entity": in.Entity, "as_of": in.AsOf, "direction": in.Direction})
		return nil, payload, err
	})

	type kgAddArgs struct {
		Subject     string `json:"subject"`
		Predicate   string `json:"predicate"`
		Object      string `json:"object"`
		ValidFrom   string `json:"valid_from,omitempty"`
		SourceEntry string `json:"source_entry,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_add", Description: "Add a fact to the knowledge graph."}, func(ctx context.Context, _ *sdk.CallToolRequest, in kgAddArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_kg_add", map[string]any{"subject": in.Subject, "predicate": in.Predicate, "object": in.Object, "valid_from": in.ValidFrom, "source_entry": in.SourceEntry})
		return nil, payload, err
	})

	type kgInvalidateArgs struct {
		Subject   string `json:"subject"`
		Predicate string `json:"predicate"`
		Object    string `json:"object"`
		Ended     string `json:"ended,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_invalidate", Description: "Mark a fact as no longer true."}, func(ctx context.Context, _ *sdk.CallToolRequest, in kgInvalidateArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_kg_invalidate", map[string]any{"subject": in.Subject, "predicate": in.Predicate, "object": in.Object, "ended": in.Ended})
		return nil, payload, err
	})

	type timelineArgs struct {
		Entity string `json:"entity"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_timeline", Description: "Chronological history of facts for an entity or all facts."}, func(ctx context.Context, _ *sdk.CallToolRequest, in timelineArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_kg_timeline", map[string]any{"entity": in.Entity})
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_stats", Description: "Knowledge graph overview and counts."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_kg_stats", nil)
		return nil, payload, err
	})

	type traverseArgs struct {
		StartTag string `json:"start_tag"`
		MaxHops  int    `json:"max_hops,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_graph_traverse", Description: "Traverse related tags from a starting tag."}, func(ctx context.Context, _ *sdk.CallToolRequest, in traverseArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_graph_traverse", map[string]any{"start_tag": in.StartTag, "max_hops": in.MaxHops})
		return nil, payload, err
	})

	type bridgeArgs struct {
		DepthA *int `json:"depth_a,omitempty"`
		DepthB *int `json:"depth_b,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_find_bridges", Description: "Find tags that bridge multiple depths or two specific depths."}, func(ctx context.Context, _ *sdk.CallToolRequest, in bridgeArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_find_bridges", map[string]any{"depth_a": in.DepthA, "depth_b": in.DepthB})
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_graph_stats", Description: "Tag graph overview."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_graph_stats", nil)
		return nil, payload, err
	})

	type diaryWriteArgs struct {
		AgentName string `json:"agent_name"`
		Entry     string `json:"entry"`
		Topic     string `json:"topic,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_diary_write", Description: "Write a persistent diary entry for an agent or role."}, func(ctx context.Context, _ *sdk.CallToolRequest, in diaryWriteArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_diary_write", map[string]any{"agent_name": in.AgentName, "entry": in.Entry, "topic": in.Topic})
		return nil, payload, err
	})

	type diaryReadArgs struct {
		AgentName string `json:"agent_name"`
		LastN     int    `json:"last_n,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_diary_read", Description: "Read recent diary entries for an agent or role."}, func(ctx context.Context, _ *sdk.CallToolRequest, in diaryReadArgs) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_diary_read", map[string]any{"agent_name": in.AgentName, "last_n": in.LastN})
		return nil, payload, err
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_doctor", Description: "Validate the active embedding backend and index configuration."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		payload, err := s.backend.Call(ctx, "tagmem_doctor", nil)
		return nil, payload, err
	})
}
