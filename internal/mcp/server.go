package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

const protocolVersion = "2024-11-05"

type Server struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	repo     *store.Repository
	kg       *kg.Store
	diary    *diary.Store
	paths    xdg.Paths
	provider vector.Provider
}

func New(stdin io.Reader, stdout, stderr io.Writer, repo *store.Repository, kgStore *kg.Store, diaryStore *diary.Store, paths xdg.Paths, provider vector.Provider) *Server {
	return &Server{stdin: stdin, stdout: stdout, stderr: stderr, repo: repo, kg: kgStore, diary: diaryStore, paths: paths, provider: provider}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Run(ctx context.Context) error {
	reader := bufio.NewReader(s.stdin)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		body, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			if err := s.write(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
				return err
			}
			continue
		}

		resp := s.handle(ctx, req)
		if resp == nil {
			continue
		}
		if err := s.write(*resp); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req request) *response {
	id := decodeID(req.ID)
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return &response{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"subscribe": false, "listChanged": false},
				"prompts":   map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": "tagmem", "version": "0.1.0"},
		}}
	case "notifications/initialized":
		return nil
	case "resources/list":
		return &response{JSONRPC: "2.0", ID: id, Result: map[string]any{"resources": []map[string]any{}}}
	case "prompts/list":
		return &response{JSONRPC: "2.0", ID: id, Result: map[string]any{"prompts": []map[string]any{}}}
	case "ping":
		return &response{JSONRPC: "2.0", ID: id, Result: map[string]any{}}
	case "tools/list":
		return &response{JSONRPC: "2.0", ID: id, Result: map[string]any{"tools": toolDefinitions()}}
	case "tools/call":
		result, err := s.callTool(ctx, req.Params)
		if err != nil {
			return &response{JSONRPC: "2.0", ID: id, Result: toolErrorResult(err)}
		}
		return &response{JSONRPC: "2.0", ID: id, Result: toolSuccessResult(result)}
	default:
		if isNotification {
			return nil
		}
		return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: "method not found"}}
	}
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode tool call: %w", err)
	}

	args := params.Arguments
	if args == nil {
		args = map[string]interface{}{}
	}

	switch params.Name {
	case "tiered_memory_status":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		depths, err := s.repo.DepthCounts()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"total_entries": len(entries),
			"depths":        depths,
			"tags":          taggraph.TagCounts(entries),
			"store_path":    s.paths.StorePath,
			"index_path":    s.provider.IndexPath(s.paths.IndexDir),
			"embedding":     s.provider.Description,
		}, nil
	case "tiered_memory_paths":
		return map[string]any{
			"data":   s.paths.DataDir,
			"config": s.paths.ConfigDir,
			"cache":  s.paths.CacheDir,
			"store":  s.paths.StorePath,
			"index":  s.provider.IndexPath(s.paths.IndexDir),
		}, nil
	case "tiered_memory_list_depths":
		return s.repo.DepthCounts()
	case "tiered_memory_list_tags":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return taggraph.TagCounts(entries), nil
	case "tiered_memory_get_tag_map":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return taggraph.DepthTagMap(entries), nil
	case "tiered_memory_list_entries":
		query, err := parseQueryArgs(args, false)
		if err != nil {
			return nil, err
		}
		return s.repo.List(query)
	case "tiered_memory_search":
		query, err := parseQueryArgs(args, true)
		if err != nil {
			return nil, err
		}
		return s.repo.Search(query)
	case "tiered_memory_show_entry":
		id, err := intArg(args, "id", true)
		if err != nil {
			return nil, err
		}
		entry, ok, err := s.repo.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("entry %d not found", id)
		}
		return entry, nil
	case "tiered_memory_check_duplicate":
		content, err := stringArg(args, "content", true)
		if err != nil {
			return nil, err
		}
		threshold, err := floatArg(args, "threshold", false)
		if err != nil {
			return nil, err
		}
		if threshold == 0 {
			threshold = 0.9
		}
		return s.repo.DuplicateCheck(content, threshold)
	case "tiered_memory_add_entry":
		depth, err := intArg(args, "depth", false)
		if err != nil {
			return nil, err
		}
		if depth == 0 && args["depth"] == nil {
			depth = 1
		}
		title, err := stringArg(args, "title", true)
		if err != nil {
			return nil, err
		}
		body, err := stringArg(args, "body", true)
		if err != nil {
			return nil, err
		}
		tags := stringSliceArg(args, "tags")
		source, err := stringArg(args, "source", false)
		if err != nil {
			return nil, err
		}
		entry, err := s.repo.Add(store.AddEntry{Depth: depth, Title: title, Body: body, Tags: tags, Source: source})
		if err != nil {
			return nil, err
		}
		return entry, nil
	case "tiered_memory_delete_entry":
		id, err := intArg(args, "id", true)
		if err != nil {
			return nil, err
		}
		deleted, err := s.repo.Delete(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": deleted, "id": id}, nil
	case "tiered_memory_graph_traverse":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		startTag, err := stringArg(args, "start_tag", true)
		if err != nil {
			return nil, err
		}
		maxHops, err := intArg(args, "max_hops", false)
		if err != nil {
			return nil, err
		}
		return taggraph.Traverse(entries, startTag, maxHops), nil
	case "tiered_memory_find_bridges":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		var depthA, depthB *int
		if args["depth_a"] != nil {
			value, err := intArg(args, "depth_a", true)
			if err != nil {
				return nil, err
			}
			depthA = &value
		}
		if args["depth_b"] != nil {
			value, err := intArg(args, "depth_b", true)
			if err != nil {
				return nil, err
			}
			depthB = &value
		}
		return taggraph.FindBridges(entries, depthA, depthB), nil
	case "tiered_memory_graph_stats":
		entries, err := s.repo.List(store.Query{Limit: 0})
		if err != nil {
			return nil, err
		}
		return taggraph.Stats(entries), nil
	case "tiered_memory_kg_query":
		entity, err := stringArg(args, "entity", true)
		if err != nil {
			return nil, err
		}
		asOf, err := stringArg(args, "as_of", false)
		if err != nil {
			return nil, err
		}
		direction, err := stringArg(args, "direction", false)
		if err != nil {
			return nil, err
		}
		if direction == "" {
			direction = "both"
		}
		facts, err := s.kg.Query(entity, asOf, direction)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": entity, "as_of": asOf, "facts": facts, "count": len(facts)}, nil
	case "tiered_memory_kg_add":
		subject, err := stringArg(args, "subject", true)
		if err != nil {
			return nil, err
		}
		predicate, err := stringArg(args, "predicate", true)
		if err != nil {
			return nil, err
		}
		object, err := stringArg(args, "object", true)
		if err != nil {
			return nil, err
		}
		validFrom, err := stringArg(args, "valid_from", false)
		if err != nil {
			return nil, err
		}
		source, err := stringArg(args, "source_entry", false)
		if err != nil {
			return nil, err
		}
		fact, err := s.kg.Add(subject, predicate, object, validFrom, source)
		if err != nil {
			return nil, err
		}
		return fact, nil
	case "tiered_memory_kg_invalidate":
		subject, err := stringArg(args, "subject", true)
		if err != nil {
			return nil, err
		}
		predicate, err := stringArg(args, "predicate", true)
		if err != nil {
			return nil, err
		}
		object, err := stringArg(args, "object", true)
		if err != nil {
			return nil, err
		}
		ended, err := stringArg(args, "ended", false)
		if err != nil {
			return nil, err
		}
		if err := s.kg.Invalidate(subject, predicate, object, ended); err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	case "tiered_memory_kg_timeline":
		entity, err := stringArg(args, "entity", false)
		if err != nil {
			return nil, err
		}
		timeline, err := s.kg.Timeline(entity)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": entity, "timeline": timeline, "count": len(timeline)}, nil
	case "tiered_memory_kg_stats":
		return s.kg.Stats()
	case "tiered_memory_diary_write":
		agent, err := stringArg(args, "agent_name", true)
		if err != nil {
			return nil, err
		}
		entry, err := stringArg(args, "entry", true)
		if err != nil {
			return nil, err
		}
		topic, err := stringArg(args, "topic", false)
		if err != nil {
			return nil, err
		}
		return s.diary.Write(agent, entry, topic)
	case "tiered_memory_diary_read":
		agent, err := stringArg(args, "agent_name", true)
		if err != nil {
			return nil, err
		}
		lastN, err := intArg(args, "last_n", false)
		if err != nil {
			return nil, err
		}
		if lastN == 0 {
			lastN = 10
		}
		entries, err := s.diary.Read(agent, lastN)
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent": agent, "entries": entries, "showing": len(entries)}, nil
	case "tiered_memory_doctor":
		return s.provider.Doctor(ctx), nil
	default:
		return nil, fmt.Errorf("unknown tool %q", params.Name)
	}
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		tool("tiered_memory_status", "Overview of entries, depths, tags, and active embedding backend.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_paths", "Resolved data, config, cache, store, and index paths.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_list_depths", "Counts for all depths in the local memory store.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_list_tags", "Counts for all tags derived from entries.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_get_tag_map", "Depth to tag count map.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_list_entries", "List entries, optionally filtered by depth or tag.", map[string]any{"type": "object", "properties": map[string]any{"depth": intSchema(), "tag": stringSchema(), "limit": intSchema()}}),
		tool("tiered_memory_search", "Semantic search over entries with optional depth or tag filter.", map[string]any{"type": "object", "properties": map[string]any{"query": stringSchema(), "depth": intSchema(), "tag": stringSchema(), "limit": intSchema()}, "required": []string{"query"}}),
		tool("tiered_memory_show_entry", "Show one entry by numeric ID.", map[string]any{"type": "object", "properties": map[string]any{"id": intSchema()}, "required": []string{"id"}}),
		tool("tiered_memory_check_duplicate", "Check whether content already exists before storing it.", map[string]any{"type": "object", "properties": map[string]any{"content": stringSchema(), "threshold": numberSchema()}, "required": []string{"content"}}),
		tool("tiered_memory_add_entry", "Add a new memory entry.", map[string]any{"type": "object", "properties": map[string]any{"depth": intSchema(), "title": stringSchema(), "body": stringSchema(), "tags": map[string]any{"type": "array", "items": stringSchema()}, "source": stringSchema()}, "required": []string{"title", "body"}}),
		tool("tiered_memory_delete_entry", "Delete one entry by numeric ID.", map[string]any{"type": "object", "properties": map[string]any{"id": intSchema()}, "required": []string{"id"}}),
		tool("tiered_memory_kg_query", "Query the knowledge graph for an entity.", map[string]any{"type": "object", "properties": map[string]any{"entity": stringSchema(), "as_of": stringSchema(), "direction": stringSchema()}, "required": []string{"entity"}}),
		tool("tiered_memory_kg_add", "Add a fact to the knowledge graph.", map[string]any{"type": "object", "properties": map[string]any{"subject": stringSchema(), "predicate": stringSchema(), "object": stringSchema(), "valid_from": stringSchema(), "source_entry": stringSchema()}, "required": []string{"subject", "predicate", "object"}}),
		tool("tiered_memory_kg_invalidate", "Mark a fact as no longer true.", map[string]any{"type": "object", "properties": map[string]any{"subject": stringSchema(), "predicate": stringSchema(), "object": stringSchema(), "ended": stringSchema()}, "required": []string{"subject", "predicate", "object"}}),
		tool("tiered_memory_kg_timeline", "Chronological history of facts for an entity or all facts.", map[string]any{"type": "object", "properties": map[string]any{"entity": stringSchema()}}),
		tool("tiered_memory_kg_stats", "Knowledge graph overview and counts.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_graph_traverse", "Traverse related tags from a starting tag.", map[string]any{"type": "object", "properties": map[string]any{"start_tag": stringSchema(), "max_hops": intSchema()}, "required": []string{"start_tag"}}),
		tool("tiered_memory_find_bridges", "Find tags that bridge multiple depths or two specific depths.", map[string]any{"type": "object", "properties": map[string]any{"depth_a": intSchema(), "depth_b": intSchema()}}),
		tool("tiered_memory_graph_stats", "Tag graph overview.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("tiered_memory_diary_write", "Write a persistent diary entry for an agent or role.", map[string]any{"type": "object", "properties": map[string]any{"agent_name": stringSchema(), "entry": stringSchema(), "topic": stringSchema()}, "required": []string{"agent_name", "entry"}}),
		tool("tiered_memory_diary_read", "Read recent diary entries for an agent or role.", map[string]any{"type": "object", "properties": map[string]any{"agent_name": stringSchema(), "last_n": intSchema()}, "required": []string{"agent_name"}}),
		tool("tiered_memory_doctor", "Validate the active embedding backend and index configuration.", map[string]any{"type": "object", "properties": map[string]any{}}),
	}
}

func tool(name, description string, schema map[string]any) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": schema}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }
func intSchema() map[string]any    { return map[string]any{"type": "integer"} }
func numberSchema() map[string]any { return map[string]any{"type": "number"} }

func toolSuccessResult(result any) map[string]any {
	payload, _ := json.MarshalIndent(result, "", "  ")
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(payload)}},
		"structuredContent": result,
		"isError":           false,
	}
}

func toolErrorResult(err error) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}

func parseQueryArgs(args map[string]interface{}, requireQuery bool) (store.Query, error) {
	limit, err := intArg(args, "limit", false)
	if err != nil {
		return store.Query{}, err
	}
	if limit == 0 {
		if requireQuery {
			limit = 5
		} else {
			limit = 25
		}
	}
	query := store.Query{Limit: limit}
	if args["depth"] != nil {
		depth, err := intArg(args, "depth", true)
		if err != nil {
			return store.Query{}, err
		}
		query.Depth = &depth
	}
	if args["tag"] != nil {
		tag, err := stringArg(args, "tag", true)
		if err != nil {
			return store.Query{}, err
		}
		query.Tag = tag
	}
	if requireQuery {
		text, err := stringArg(args, "query", true)
		if err != nil {
			return store.Query{}, err
		}
		query.Text = text
	}
	return query, nil
}

func floatArg(args map[string]interface{}, name string, required bool) (float64, error) {
	value, ok := args[name]
	if !ok || value == nil {
		if required {
			return 0, fmt.Errorf("%s is required", name)
		}
		return 0, nil
	}
	switch v := value.(type) {
	case float64:
		return v, nil
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", name)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a number", name)
	}
}

func stringArg(args map[string]interface{}, name string, required bool) (string, error) {
	value, ok := args[name]
	if !ok || value == nil {
		if required {
			return "", fmt.Errorf("%s is required", name)
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return text, nil
}

func intArg(args map[string]interface{}, name string, required bool) (int, error) {
	value, ok := args[name]
	if !ok || value == nil {
		if required {
			return 0, fmt.Errorf("%s is required", name)
		}
		return 0, nil
	}
	switch v := value.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case string:
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
}

func stringSliceArg(args map[string]interface{}, name string) []string {
	value, ok := args[name]
	if !ok || value == nil {
		return nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, text)
	}
	return out
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	headers := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		headers[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
	}

	length, err := strconv.Atoi(headers["content-length"])
	if err != nil {
		return nil, fmt.Errorf("invalid content-length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Server) write(resp response) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func decodeID(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}
