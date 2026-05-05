package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/xdg"
)

type testDaemonRequest struct {
	ID      string         `json:"id"`
	Command string         `json:"command"`
	Payload map[string]any `json:"payload,omitempty"`
}

type testDaemonResponse struct {
	ID      string         `json:"id"`
	Success bool           `json:"success"`
	Payload map[string]any `json:"payload,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type testEnsureCorpusPayload struct {
	Key       string                      `json:"key"`
	Documents []testCorpusDocumentPayload `json:"documents"`
}

type testSearchCorpusPayload struct {
	Key   string `json:"key"`
	Query string `json:"query"`
}

type testCorpusDocumentPayload struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Mode      string `json:"mode,omitempty"`
	Extract   string `json:"extract,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func TestLongMemEvalInterfaceUsesDaemonCorpusCache(t *testing.T) {
	runtimeDir := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "interface-cache")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(cacheRoot) error = %v", err)
	}

	originalSearch := interfaceCorpusSearchDetailed
	t.Cleanup(func() {
		interfaceCorpusSearchDetailed = originalSearch
	})
	interfaceCorpusSearchDetailed = func(_ *store.Repository, _ store.Query) ([]store.SearchResult, error) {
		return nil, fmt.Errorf("direct interface corpus search path should not run when daemon cache is available")
	}

	paths, err := xdg.Resolve("tagmem")
	if err != nil {
		t.Fatalf("xdg.Resolve() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SocketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	type corpusRequest struct {
		command string
		key     string
		query   string
	}
	var (
		mu       sync.Mutex
		requests []corpusRequest
	)
	entries := []LongMemEvalEntry{{
		Question:         "Which session mentioned the red bicycle repair?",
		QuestionType:     "fact",
		AnswerSessionIDs: []string{"session-alpha"},
		HaystackSessions: [][]Turn{
			{{Role: "user", Content: "Can you remind me about the red bicycle repair at the downtown shop?"}, {Role: "assistant", Content: "The red bicycle repair was finished yesterday at the downtown shop."}},
			{{Role: "user", Content: "   "}, {Role: "assistant", Content: "   "}},
			{{Role: "user", Content: "Where is the blue kayak storage locker?"}, {Role: "assistant", Content: "The blue kayak storage locker is next to the marina office."}},
		},
		HaystackSessionIDs: []string{"session-alpha", "session-empty", "session-beta"},
		HaystackDates:      []string{"2024-01-02", "2024-01-05", "2024-01-03"},
	}}
	alphaTime := time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)
	betaTime := time.Date(2024, time.January, 3, 0, 0, 0, 0, time.UTC)
	expectedDocuments := []testCorpusDocumentPayload{
		{
			ID:        "session-alpha",
			Content:   "> User: Can you remind me about the red bicycle repair at the downtown shop?\nAssistant: The red bicycle repair was finished yesterday at the downtown shop.",
			Mode:      "conversations",
			Extract:   "exchange",
			Depth:     1,
			CreatedAt: alphaTime.Format(time.RFC3339),
			UpdatedAt: alphaTime.Format(time.RFC3339),
		},
		{
			ID:        "session-beta",
			Content:   "> User: Where is the blue kayak storage locker?\nAssistant: The blue kayak storage locker is next to the marina office.",
			Mode:      "conversations",
			Extract:   "exchange",
			Depth:     1,
			CreatedAt: betaTime.Format(time.RFC3339),
			UpdatedAt: betaTime.Format(time.RFC3339),
		},
	}

	listener, err := net.Listen("unix", paths.SocketPath)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var request testDaemonRequest
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					return
				}

				response := testDaemonResponse{ID: request.ID, Success: true, Payload: map[string]any{}}
				switch request.Command {
				case "ensure_corpus":
					var payload testEnsureCorpusPayload
					if err := decodeTestPayload(request.Payload, &payload); err != nil {
						response.Success = false
						response.Error = err.Error()
						break
					}
					if len(payload.Documents) != len(expectedDocuments) {
						response.Success = false
						response.Error = fmt.Sprintf("len(payload.Documents) = %d, want %d", len(payload.Documents), len(expectedDocuments))
						break
					}
					for i, document := range payload.Documents {
						expected := expectedDocuments[i]
						if document != expected {
							response.Success = false
							response.Error = fmt.Sprintf("payload.Documents[%d] = %+v, want %+v", i, document, expected)
							break
						}
					}
					if !response.Success {
						break
					}
					mu.Lock()
					requests = append(requests, corpusRequest{command: request.Command, key: payload.Key})
					mu.Unlock()
					response.Payload["key"] = payload.Key
					response.Payload["cache_status"] = "hit"
				case "search_corpus":
					var payload testSearchCorpusPayload
					if err := decodeTestPayload(request.Payload, &payload); err != nil {
						response.Success = false
						response.Error = err.Error()
						break
					}
					mu.Lock()
					requests = append(requests, corpusRequest{command: request.Command, key: payload.Key, query: payload.Query})
					mu.Unlock()
					response.Payload["origin_ids"] = []string{"session-alpha"}
				default:
					response.Success = false
					response.Error = "unexpected command"
				}

				_ = json.NewEncoder(conn).Encode(response)
			}(conn)
		}
	}()

	dataFile := filepath.Join(t.TempDir(), "longmemeval.json")
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(dataFile, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	for run := 0; run < 2; run++ {
		result, err := RunLongMemEvalInterfaceWithOptions(context.Background(), dataFile, 0, fakeembed.Provider(), LongMemEvalInterfaceOptions{CorpusCacheDir: cacheRoot})
		if err != nil {
			t.Fatalf("RunLongMemEvalInterfaceWithOptions() run %d error = %v", run+1, err)
		}
		if len(result.Items) != 1 || len(result.Items[0].TopResults) == 0 || result.Items[0].TopResults[0] != "session-alpha" {
			t.Fatalf("run %d result.Items = %+v, want session-alpha top result", run+1, result.Items)
		}
	}
	cacheEntries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatalf("ReadDir(cacheRoot) error = %v", err)
	}
	if len(cacheEntries) != 0 {
		names := make([]string, 0, len(cacheEntries))
		for _, entry := range cacheEntries {
			names = append(names, entry.Name())
		}
		t.Fatalf("cacheRoot entries = %v, want no direct per-question corpus files when daemon path is used", names)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("len(requests) = %d, want 4 daemon corpus requests across repeated runs", len(requests))
	}
	for i, command := range []string{"ensure_corpus", "search_corpus", "ensure_corpus", "search_corpus"} {
		if requests[i].command != command {
			t.Fatalf("requests[%d].command = %q, want %q", i, requests[i].command, command)
		}
	}
	if requests[0].key == "" {
		t.Fatal("requests[0].key = empty, want stable corpus key")
	}
	for i := 1; i < len(requests); i++ {
		if requests[i].key != requests[0].key {
			t.Fatalf("requests[%d].key = %q, want %q", i, requests[i].key, requests[0].key)
		}
	}
}

func decodeTestPayload(payload map[string]any, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
