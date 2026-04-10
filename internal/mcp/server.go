package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type Server struct {
	repo     *store.Repository
	kg       *kg.Store
	diary    *diary.Store
	paths    xdg.Paths
	provider vector.Provider
	server   *sdk.Server
}

func New(_ any, _ any, _ any, repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *Server {
	s := &Server{repo: repo, kg: kgStore, diary: diaryStore, paths: paths, provider: provider}
	s.server = sdk.NewServer(&sdk.Implementation{Name: "tagmem", Version: "0.1.0"}, nil)
	s.registerTools()
	return s
}

func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &sdk.StdioTransport{})
}

func (s *Server) registerTools() {
	type empty struct{}

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_status", Description: "Overview of entries, depths, tags, and active embedding backend."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, nil, err
		}
		depths, err := s.repo.DepthCounts()
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"total_entries": len(entries),
			"depths":        depths,
			"tags":          taggraph.TagCounts(entries),
			"store_path":    s.paths.StorePath,
			"index_path":    s.provider.IndexPath(s.paths.IndexDir),
			"embedding":     s.provider.Description,
		}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_paths", Description: "Resolved data, config, cache, store, and index paths."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"data": s.paths.DataDir, "config": s.paths.ConfigDir, "cache": s.paths.CacheDir, "store": s.paths.StorePath, "index": s.provider.IndexPath(s.paths.IndexDir)}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_depths", Description: "Counts for all depths in the local memory store."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		depths, err := s.repo.DepthCounts()
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"depths": depths}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_tags", Description: "Counts for all tags derived from entries."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"tags": taggraph.TagCounts(entries)}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_get_tag_map", Description: "Depth to tag count map."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"tag_map": taggraph.DepthTagMap(entries)}, nil
	})

	type listArgs struct {
		Depth *int   `json:"depth,omitempty"`
		Tag   string `json:"tag,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_list_entries", Description: "List entries, optionally filtered by depth or tag."}, func(ctx context.Context, _ *sdk.CallToolRequest, in listArgs) (*sdk.CallToolResult, map[string]any, error) {
		q := store.Query{Limit: defaultLimit(in.Limit, 25), Tag: in.Tag}
		if in.Depth != nil {
			q.Depth = in.Depth
		}
		entries, err := s.repo.List(q)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entries": entries}, nil
	})

	type searchArgs struct {
		Query string `json:"query"`
		Depth *int   `json:"depth,omitempty"`
		Tag   string `json:"tag,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_search", Description: "Semantic search over entries with optional depth or tag filter."}, func(ctx context.Context, _ *sdk.CallToolRequest, in searchArgs) (*sdk.CallToolResult, map[string]any, error) {
		q := store.Query{Text: in.Query, Limit: defaultLimit(in.Limit, 5), Tag: in.Tag}
		if in.Depth != nil {
			q.Depth = in.Depth
		}
		entries, err := s.repo.Search(q)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entries": entries}, nil
	})

	type idArgs struct {
		ID int `json:"id"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_show_entry", Description: "Show one entry by numeric ID."}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, map[string]any, error) {
		entry, ok, err := s.repo.Get(in.ID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("entry %d not found", in.ID)
		}
		return nil, map[string]any{"entry": entry}, nil
	})

	type duplicateArgs struct {
		Content   string  `json:"content"`
		Threshold float64 `json:"threshold,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_check_duplicate", Description: "Check whether content already exists before storing it."}, func(ctx context.Context, _ *sdk.CallToolRequest, in duplicateArgs) (*sdk.CallToolResult, map[string]any, error) {
		matches, err := s.repo.DuplicateCheck(in.Content, in.Threshold)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"matches": matches}, nil
	})

	type addArgs struct {
		Depth  *int     `json:"depth,omitempty"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Tags   []string `json:"tags,omitempty"`
		Source string   `json:"source,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_add_entry", Description: "Add a new memory entry."}, func(ctx context.Context, _ *sdk.CallToolRequest, in addArgs) (*sdk.CallToolResult, map[string]any, error) {
		depth := 1
		if in.Depth != nil {
			depth = *in.Depth
		}
		if depth == 0 {
			depth = 1
		}
		entry, err := s.repo.Add(store.AddEntry{Depth: depth, Title: in.Title, Body: in.Body, Tags: in.Tags, Source: in.Source})
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entry": entry}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_delete_entry", Description: "Delete one entry by numeric ID."}, func(ctx context.Context, _ *sdk.CallToolRequest, in idArgs) (*sdk.CallToolResult, map[string]any, error) {
		deleted, err := s.repo.Delete(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": deleted, "id": in.ID}, nil
	})

	type entityArgs struct {
		Entity    string `json:"entity"`
		AsOf      string `json:"as_of,omitempty"`
		Direction string `json:"direction,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_query", Description: "Query the knowledge graph for an entity."}, func(ctx context.Context, _ *sdk.CallToolRequest, in entityArgs) (*sdk.CallToolResult, map[string]any, error) {
		direction := in.Direction
		if direction == "" {
			direction = "both"
		}
		facts, err := s.kg.Query(in.Entity, in.AsOf, direction)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entity": in.Entity, "as_of": in.AsOf, "facts": facts, "count": len(facts)}, nil
	})

	type kgAddArgs struct {
		Subject     string `json:"subject"`
		Predicate   string `json:"predicate"`
		Object      string `json:"object"`
		ValidFrom   string `json:"valid_from,omitempty"`
		SourceEntry string `json:"source_entry,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_add", Description: "Add a fact to the knowledge graph."}, func(ctx context.Context, _ *sdk.CallToolRequest, in kgAddArgs) (*sdk.CallToolResult, map[string]any, error) {
		fact, err := s.kg.Add(in.Subject, in.Predicate, in.Object, in.ValidFrom, in.SourceEntry)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"fact": fact}, nil
	})

	type kgInvalidateArgs struct {
		Subject   string `json:"subject"`
		Predicate string `json:"predicate"`
		Object    string `json:"object"`
		Ended     string `json:"ended,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_invalidate", Description: "Mark a fact as no longer true."}, func(ctx context.Context, _ *sdk.CallToolRequest, in kgInvalidateArgs) (*sdk.CallToolResult, map[string]any, error) {
		if err := s.kg.Invalidate(in.Subject, in.Predicate, in.Object, in.Ended); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"success": true}, nil
	})

	type timelineArgs struct {
		Entity string `json:"entity"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_timeline", Description: "Chronological history of facts for an entity or all facts."}, func(ctx context.Context, _ *sdk.CallToolRequest, in timelineArgs) (*sdk.CallToolResult, map[string]any, error) {
		timeline, err := s.kg.Timeline(in.Entity)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entity": in.Entity, "timeline": timeline, "count": len(timeline)}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_kg_stats", Description: "Knowledge graph overview and counts."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		stats, err := s.kg.Stats()
		if err != nil {
			return nil, nil, err
		}
		return nil, stats, nil
	})

	type traverseArgs struct {
		StartTag string `json:"start_tag"`
		MaxHops  int    `json:"max_hops,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_graph_traverse", Description: "Traverse related tags from a starting tag."}, func(ctx context.Context, _ *sdk.CallToolRequest, in traverseArgs) (*sdk.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"edges": taggraph.Traverse(mustEntries(s.repo), in.StartTag, in.MaxHops)}, nil
	})

	type bridgeArgs struct {
		DepthA *int `json:"depth_a,omitempty"`
		DepthB *int `json:"depth_b,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_find_bridges", Description: "Find tags that bridge multiple depths or two specific depths."}, func(ctx context.Context, _ *sdk.CallToolRequest, in bridgeArgs) (*sdk.CallToolResult, map[string]any, error) {
		var a, b *int
		if in.DepthA != nil {
			a = in.DepthA
		}
		if in.DepthB != nil {
			b = in.DepthB
		}
		return nil, map[string]any{"bridges": taggraph.FindBridges(mustEntries(s.repo), a, b)}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_graph_stats", Description: "Tag graph overview."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		return nil, taggraph.Stats(mustEntries(s.repo)), nil
	})

	type diaryWriteArgs struct {
		AgentName string `json:"agent_name"`
		Entry     string `json:"entry"`
		Topic     string `json:"topic,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_diary_write", Description: "Write a persistent diary entry for an agent or role."}, func(ctx context.Context, _ *sdk.CallToolRequest, in diaryWriteArgs) (*sdk.CallToolResult, map[string]any, error) {
		entry, err := s.diary.Write(in.AgentName, in.Entry, in.Topic)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"entry": entry}, nil
	})

	type diaryReadArgs struct {
		AgentName string `json:"agent_name"`
		LastN     int    `json:"last_n,omitempty"`
	}
	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_diary_read", Description: "Read recent diary entries for an agent or role."}, func(ctx context.Context, _ *sdk.CallToolRequest, in diaryReadArgs) (*sdk.CallToolResult, map[string]any, error) {
		lastN := in.LastN
		if lastN == 0 {
			lastN = 10
		}
		entries, err := s.diary.Read(in.AgentName, lastN)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"agent": in.AgentName, "entries": entries, "showing": len(entries)}, nil
	})

	sdk.AddTool(s.server, &sdk.Tool{Name: "tagmem_doctor", Description: "Validate the active embedding backend and index configuration."}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"report": s.provider.Doctor(ctx)}, nil
	})
}

func mustEntries(repo *store.Repository) []store.Entry {
	entries, _ := repo.List(store.Query{Limit: 0})
	return entries
}

func defaultLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
