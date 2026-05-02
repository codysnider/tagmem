package bench

import (
	"path/filepath"
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
