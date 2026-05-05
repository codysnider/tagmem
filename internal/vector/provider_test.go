package vector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/codysnider/tagmem/internal/xdg"
)

func testPaths() xdg.Paths {
	return xdg.Paths{ModelDir: "/tmp/tiered-memory-test-models"}
}

func TestProviderFromEnvDefaultsToEmbedded(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.Name != ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderEmbedded)
	}
}

func TestEmbeddedProfileInternalPhasesRecordRequiredPhaseSet(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROFILE", "1")

	got, restoreSink := captureEmbeddedProfilePhases()
	defer restoreSink()

	profiler := newEmbeddedProfiler(nil)
	stopTotal := profiler.begin("embed_total")
	vectors, err := runEmbeddedBatchProfiled(
		[]string{"hello", "world"},
		profiler,
		embeddedBatchProfileOps[tokenizedBatchFixture, preparedBatchFixture, sessionFixture, outputFixture]{
			Tokenize: func(texts []string) (tokenizedBatchFixture, error) {
				return tokenizedBatchFixture{texts: texts}, nil
			},
			TensorPrepare: func(tokenized tokenizedBatchFixture) (preparedBatchFixture, error) {
				return preparedBatchFixture{count: len(tokenized.texts)}, nil
			},
			SessionCheckout: func() (sessionFixture, error) {
				return sessionFixture{id: "session"}, nil
			},
			ONNXRun: func(session sessionFixture, prepared preparedBatchFixture) (outputFixture, error) {
				return outputFixture{sessionID: session.id, count: prepared.count}, nil
			},
			PoolNormalize: func(output outputFixture, tokenized tokenizedBatchFixture) ([][]float32, error) {
				if output.sessionID != "session" {
					t.Fatalf("output.sessionID = %q, want session", output.sessionID)
				}
				if output.count != len(tokenized.texts) {
					t.Fatalf("output.count = %d, want %d", output.count, len(tokenized.texts))
				}
				return [][]float32{{1, 2, 3}, {4, 5, 6}}, nil
			},
		},
	)
	stopTotal()
	if err != nil {
		t.Fatalf("runEmbeddedBatchProfiled() error = %v", err)
	}
	if !reflect.DeepEqual(vectors, [][]float32{{1, 2, 3}, {4, 5, 6}}) {
		t.Fatalf("vectors = %v, want %v", vectors, [][]float32{{1, 2, 3}, {4, 5, 6}})
	}

	if !reflect.DeepEqual(*got, requiredEmbeddedProfilePhases()) {
		t.Fatalf("recorded phases = %v, want %v", *got, requiredEmbeddedProfilePhases())
	}
	if len(*got) != len(requiredEmbeddedProfilePhases()) {
		t.Fatalf("recorded phase count = %d, want %d", len(*got), len(requiredEmbeddedProfilePhases()))
	}
}

func TestEmbeddedProfileStaysSilentWhenDisabled(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROFILE", "")

	got, restoreSink := captureEmbeddedProfilePhases()
	defer restoreSink()

	profiler := newEmbeddedProfiler(nil)
	stopTotal := profiler.begin("embed_total")
	if _, err := runEmbeddedBatchProfiled(
		[]string{"hello"},
		profiler,
		embeddedBatchProfileOps[tokenizedBatchFixture, preparedBatchFixture, sessionFixture, outputFixture]{
			Tokenize: func(texts []string) (tokenizedBatchFixture, error) {
				return tokenizedBatchFixture{texts: texts}, nil
			},
			TensorPrepare: func(tokenized tokenizedBatchFixture) (preparedBatchFixture, error) {
				return preparedBatchFixture{count: len(tokenized.texts)}, nil
			},
			SessionCheckout: func() (sessionFixture, error) {
				return sessionFixture{id: "session"}, nil
			},
			ONNXRun: func(session sessionFixture, prepared preparedBatchFixture) (outputFixture, error) {
				return outputFixture{sessionID: session.id, count: prepared.count}, nil
			},
			PoolNormalize: func(output outputFixture, tokenized tokenizedBatchFixture) ([][]float32, error) {
				return [][]float32{{1, 2, 3}}, nil
			},
		},
	); err != nil {
		t.Fatalf("runEmbeddedBatchProfiled() error = %v", err)
	}
	stopTotal()

	if len(*got) != 0 {
		t.Fatalf("recorded phases = %v, want none", *got)
	}
}

func TestEmbeddedProfileTensorPrepareRecordedOnError(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROFILE", "1")

	got, restoreSink := captureEmbeddedProfilePhases()
	defer restoreSink()

	profiler := newEmbeddedProfiler(nil)
	stopTotal := profiler.begin("embed_total")
	_, err := runEmbeddedBatchProfiled(
		[]string{"hello"},
		profiler,
		embeddedBatchProfileOps[tokenizedBatchFixture, preparedBatchFixture, sessionFixture, outputFixture]{
			Tokenize: func(texts []string) (tokenizedBatchFixture, error) {
				return tokenizedBatchFixture{texts: texts}, nil
			},
			TensorPrepare: func(tokenized tokenizedBatchFixture) (preparedBatchFixture, error) {
				return preparedBatchFixture{}, errors.New("tensor prepare failed")
			},
			SessionCheckout: func() (sessionFixture, error) {
				t.Fatal("SessionCheckout should not run after tensor_prepare error")
				return sessionFixture{}, nil
			},
			ONNXRun: func(sessionFixture, preparedBatchFixture) (outputFixture, error) {
				t.Fatal("ONNXRun should not run after tensor_prepare error")
				return outputFixture{}, nil
			},
			PoolNormalize: func(outputFixture, tokenizedBatchFixture) ([][]float32, error) {
				t.Fatal("PoolNormalize should not run after tensor_prepare error")
				return nil, nil
			},
		},
	)
	stopTotal()
	if err == nil {
		t.Fatal("runEmbeddedBatchProfiled() error = nil, want non-nil")
	}

	if !reflect.DeepEqual(*got, []string{"tokenize", "tensor_prepare", "embed_total"}) {
		t.Fatalf("recorded phases = %v, want %v", *got, []string{"tokenize", "tensor_prepare", "embed_total"})
	}
}

func requiredEmbeddedProfilePhases() []string {
	return []string{"tokenize", "tensor_prepare", "session_checkout", "onnx_run", "pool_normalize", "embed_total"}
}

type tokenizedBatchFixture struct{ texts []string }

type preparedBatchFixture struct{ count int }

type sessionFixture struct{ id string }

type outputFixture struct {
	sessionID string
	count     int
}

func captureEmbeddedProfilePhases() (*[]string, func()) {
	var got []string
	originalSink := embeddedProfileSink
	embeddedProfileSink = func(name string, _ time.Duration) {
		got = append(got, name)
	}
	return &got, func() {
		embeddedProfileSink = originalSink
	}
}

func TestProviderFromEnvReadsOllamaConfig(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "openai")
	t.Setenv("TAGMEM_OPENAI_MODEL", "bge-m3")
	t.Setenv("TAGMEM_OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("TAGMEM_OPENAI_API_KEY", "secret")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.Name != ProviderOpenAI {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderOpenAI)
	}
	if provider.IndexKey != "openai-bge-m3" {
		t.Fatalf("provider.IndexKey = %q, want %q", provider.IndexKey, "openai-bge-m3")
	}
	if provider.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("provider.BaseURL = %q, want %q", provider.BaseURL, "http://localhost:11434/v1")
	}
}

func TestProviderFromEnvRejectsUnknownProvider(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "mystery")

	_, err := ProviderFromEnv(testPaths())
	if err == nil {
		t.Fatal("ProviderFromEnv() error = nil, want non-nil")
	}
}

func TestProviderFromEnvRejectsRemovedHashProviders(t *testing.T) {
	for _, providerName := range []string{"hash", "embedded-hash"} {
		t.Run(providerName, func(t *testing.T) {
			t.Setenv("TAGMEM_EMBED_PROVIDER", providerName)

			_, err := ProviderFromEnv(testPaths())
			if err == nil {
				t.Fatal("ProviderFromEnv() error = nil, want non-nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
				t.Fatalf("ProviderFromEnv() error = %q, want unsupported provider error", err)
			}
		})
	}
}

func TestProviderFromEnvFallsBackToOllamaHost(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "openai")
	t.Setenv("TAGMEM_OPENAI_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "http://localhost:11434")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("provider.BaseURL = %q, want %q", provider.BaseURL, "http://localhost:11434/v1")
	}
}

func TestEmbeddedProviderFailsWhenEmbeddedRuntimeUnsupported(t *testing.T) {
	originalLoadLocalBERTEmbedder := loadLocalBERTEmbedderFunc
	loadLocalBERTEmbedderFunc = func(modelDir string, spec localModelSpec, accel string, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
		if state != nil {
			state.executionDevice = "unsupported"
		}
		return nil, errors.New("embedded runtime unsupported in test")
	}
	t.Cleanup(func() {
		loadLocalBERTEmbedderFunc = originalLoadLocalBERTEmbedder
	})

	provider, err := EmbeddedProvider(testPaths(), defaultEmbeddedModel, "auto")
	if err != nil {
		t.Fatalf("EmbeddedProvider() error = %v, want nil", err)
	}
	if provider.Name != ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderEmbedded)
	}

	_, err = provider.Func(t.Context(), "test text")
	if err == nil {
		t.Fatal("provider.Func() error = nil, want non-nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("provider.Func() error = %q, want unsupported-runtime error", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Fatalf("provider.Func() error = %q, want no hash fallback reference", err)
	}
}

func TestDiagnoseDoctorErrorForMissingEmbeddings(t *testing.T) {
	t.Parallel()

	diagnosis, hint := diagnoseDoctorError(Provider{Name: ProviderOpenAI}, "no embeddings found in the response")
	if diagnosis == "" {
		t.Fatal("diagnosis = empty, want value")
	}
	if !strings.Contains(strings.ToLower(hint), "dedicated embeddings model") {
		t.Fatalf("hint = %q, want embeddings guidance", hint)
	}
}

func TestEmbeddingDocsAndScriptsDoNotReferenceRemovedHashFallback(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	readme, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(README.md) error = %v", err)
	}
	if strings.Contains(string(readme), "embedded-hash") {
		t.Fatal("README.md still documents embedded-hash as a supported provider")
	}

	for _, relativePath := range []string{
		"scripts/install.sh",
		"scripts/cmd/release-image/run.sh",
		"scripts/cmd/release-image-arm64-remote/run.sh",
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, relativePath))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relativePath, err)
		}
		if strings.Contains(string(data), "embedded hash fallback") {
			t.Fatalf("%s still contains removed embedded hash fallback string logic", relativePath)
		}
	}
}

func TestEmbeddingCacheReturnsClonedVectorsForHits(t *testing.T) {
	cache := newEmbeddingCache(1)
	cache.put("hello", []float32{1, 2, 3})

	got, ok := cache.get("hello")
	if !ok {
		t.Fatal("cache.get() ok = false, want true")
	}
	if !reflect.DeepEqual(got, []float32{1, 2, 3}) {
		t.Fatalf("cache.get() = %v, want %v", got, []float32{1, 2, 3})
	}

	got[0] = 99

	again, ok := cache.get("hello")
	if !ok {
		t.Fatal("second cache.get() ok = false, want true")
	}
	if !reflect.DeepEqual(again, []float32{1, 2, 3}) {
		t.Fatalf("second cache.get() = %v, want %v", again, []float32{1, 2, 3})
	}
}

func TestEmbeddingCacheEvictsOldestEntriesWhenOverCapacity(t *testing.T) {
	cache := newEmbeddingCache(2)
	cache.put("first", []float32{1})
	cache.put("second", []float32{2})
	cache.put("third", []float32{3})

	if _, ok := cache.get("first"); ok {
		t.Fatal("cache.get(first) ok = true, want false")
	}
	if got, ok := cache.get("second"); !ok || !reflect.DeepEqual(got, []float32{2}) {
		t.Fatalf("cache.get(second) = (%v, %t), want (%v, true)", got, ok, []float32{2})
	}
	if got, ok := cache.get("third"); !ok || !reflect.DeepEqual(got, []float32{3}) {
		t.Fatalf("cache.get(third) = (%v, %t), want (%v, true)", got, ok, []float32{3})
	}
}

func TestEmbeddingCachePutDefensivelyCopiesCallerOwnedSlice(t *testing.T) {
	cache := newEmbeddingCache(1)
	original := []float32{1, 2, 3}

	cache.put("hello", original)
	original[0] = 99

	got, ok := cache.get("hello")
	if !ok {
		t.Fatal("cache.get() ok = false, want true")
	}
	if !reflect.DeepEqual(got, []float32{1, 2, 3}) {
		t.Fatalf("cache.get() = %v, want %v", got, []float32{1, 2, 3})
	}
}

func TestEmbeddingCacheZeroCapacityDisablesCaching(t *testing.T) {
	cache := newEmbeddingCache(0)
	cache.put("hello", []float32{1, 2, 3})

	if _, ok := cache.get("hello"); ok {
		t.Fatal("cache.get() ok = true, want false")
	}
}

func TestMiniLMEmbedCacheProviderFuncReusesCache(t *testing.T) {
	var calls [][]string
	provider, _, probe := stubEmbeddedProviderForCacheTest(t, nil, func(_ *miniLMEmbedder, texts []string, _ embeddedProfiler) ([][]float32, error) {
		calls = append(calls, append([]string(nil), texts...))
		if !reflect.DeepEqual(texts, []string{"hello"}) {
			t.Fatalf("texts = %v, want %v", texts, []string{"hello"})
		}
		return [][]float32{{1, 2, 3}}, nil
	})

	first, err := provider.Func(context.Background(), "hello")
	if err != nil {
		t.Fatalf("provider.Func(first) error = %v", err)
	}
	second, err := provider.Func(context.Background(), "hello")
	if err != nil {
		t.Fatalf("provider.Func(second) error = %v", err)
	}

	if probe.loadCount != 1 {
		t.Fatalf("load count = %d, want 1", probe.loadCount)
	}
	if len(calls) != 1 {
		t.Fatalf("underlying batch call count = %d, want 1", len(calls))
	}
	if !reflect.DeepEqual(first, []float32{1, 2, 3}) {
		t.Fatalf("first = %v, want %v", first, []float32{1, 2, 3})
	}
	if !reflect.DeepEqual(second, []float32{1, 2, 3}) {
		t.Fatalf("second = %v, want %v", second, []float32{1, 2, 3})
	}

	first[0] = 99
	if reflect.DeepEqual(first, second) {
		t.Fatal("provider.Func cache hit returned caller-owned slice; want defensive copy")
	}
	if !reflect.DeepEqual(second, []float32{1, 2, 3}) {
		t.Fatalf("second after mutation = %v, want %v", second, []float32{1, 2, 3})
	}
}

func TestMiniLMEmbedBatchCacheProviderFuncThenBatchReusesCache(t *testing.T) {
	var calls [][]string
	provider, embedder, probe := stubEmbeddedProviderForCacheTest(t, nil, func(_ *miniLMEmbedder, texts []string, _ embeddedProfiler) ([][]float32, error) {
		calls = append(calls, append([]string(nil), texts...))
		switch {
		case reflect.DeepEqual(texts, []string{"shared"}):
			return [][]float32{{3, 3, 3}}, nil
		case reflect.DeepEqual(texts, []string{"miss"}):
			return [][]float32{{4, 4, 4}}, nil
		default:
			t.Fatalf("texts = %v, want %v or %v", texts, []string{"shared"}, []string{"miss"})
			return nil, nil
		}
	})

	first, err := provider.Func(context.Background(), "shared")
	if err != nil {
		t.Fatalf("provider.Func() error = %v", err)
	}
	got, err := provider.Batch(context.Background(), []string{"shared", "miss", "shared"})
	if err != nil {
		t.Fatalf("provider.Batch() error = %v", err)
	}

	if probe.loadCount != 1 {
		t.Fatalf("load count = %d, want 1", probe.loadCount)
	}
	if !reflect.DeepEqual(calls, [][]string{{"shared"}, {"miss"}}) {
		t.Fatalf("underlying batch calls = %v, want %v", calls, [][]string{{"shared"}, {"miss"}})
	}
	if !reflect.DeepEqual(first, []float32{3, 3, 3}) {
		t.Fatalf("provider.Func() = %v, want %v", first, []float32{3, 3, 3})
	}
	want := [][]float32{{3, 3, 3}, {4, 4, 4}, {3, 3, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider.Batch() = %v, want %v", got, want)
	}
	cache, ok := getMiniLMEmbedderCacheForTest(embedder)
	if !ok {
		t.Fatal("getMiniLMEmbedderCacheForTest() ok = false, want true")
	}
	miss, ok := cache.get("miss")
	if !ok || !reflect.DeepEqual(miss, []float32{4, 4, 4}) {
		t.Fatalf("cache.get(miss) = (%v, %t), want (%v, true)", miss, ok, []float32{4, 4, 4})
	}
}

func TestMiniLMEmbedBatchCacheProviderComputesMissesOnceAndPreservesOrder(t *testing.T) {
	var calls [][]string
	provider, embedder, probe := stubEmbeddedProviderForCacheTest(t, map[string][]float32{"cached": {9, 9, 9}}, func(_ *miniLMEmbedder, texts []string, _ embeddedProfiler) ([][]float32, error) {
		calls = append(calls, append([]string(nil), texts...))
		if len(texts) != 2 {
			t.Fatalf("len(texts) = %d, want 2", len(texts))
		}
		vectors := make([][]float32, 0, len(texts))
		for _, text := range texts {
			switch text {
			case "miss-one":
				vectors = append(vectors, []float32{1, 1, 1})
			case "miss-two":
				vectors = append(vectors, []float32{2, 2, 2})
			default:
				t.Fatalf("unexpected miss text %q", text)
			}
		}
		return vectors, nil
	})

	got, err := provider.Batch(context.Background(), []string{"cached", "miss-one", "cached", "miss-two", "miss-one"})
	if err != nil {
		t.Fatalf("provider.Batch() error = %v", err)
	}

	if probe.loadCount != 1 {
		t.Fatalf("load count = %d, want 1", probe.loadCount)
	}
	if len(calls) != 1 {
		t.Fatalf("underlying batch call count = %d, want 1", len(calls))
	}
	if !sameStringSet(calls[0], []string{"miss-one", "miss-two"}) {
		t.Fatalf("underlying miss batch = %v, want set %v", calls[0], []string{"miss-one", "miss-two"})
	}
	want := [][]float32{{9, 9, 9}, {1, 1, 1}, {9, 9, 9}, {2, 2, 2}, {1, 1, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider.Batch() = %v, want %v", got, want)
	}
	cache, ok := getMiniLMEmbedderCacheForTest(embedder)
	if !ok {
		t.Fatal("getMiniLMEmbedderCacheForTest() ok = false, want true")
	}
	missOne, ok := cache.get("miss-one")
	if !ok || !reflect.DeepEqual(missOne, []float32{1, 1, 1}) {
		t.Fatalf("cache.get(miss-one) = (%v, %t), want (%v, true)", missOne, ok, []float32{1, 1, 1})
	}
	missTwo, ok := cache.get("miss-two")
	if !ok || !reflect.DeepEqual(missTwo, []float32{2, 2, 2}) {
		t.Fatalf("cache.get(miss-two) = (%v, %t), want (%v, true)", missTwo, ok, []float32{2, 2, 2})
	}
}

func sameStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, item := range want {
		counts[item]++
	}
	for _, item := range got {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

type embeddedProviderCacheTestProbe struct {
	loadCount int
}

func stubEmbeddedProviderForCacheTest(t *testing.T, initial map[string][]float32, batchStub func(*miniLMEmbedder, []string, embeddedProfiler) ([][]float32, error)) (Provider, *miniLMEmbedder, *embeddedProviderCacheTestProbe) {
	t.Helper()

	if setMiniLMEmbedderCacheForTest == nil || getMiniLMEmbedderCacheForTest == nil {
		t.Skip("miniLM embedder cache test seams not available in this build")
	}

	embedder := &miniLMEmbedder{}
	if !setMiniLMEmbedderCacheForTest(embedder, newEmbeddingCache(8)) {
		t.Skip("miniLM embedder cache not available in this build")
	}
	cache, ok := getMiniLMEmbedderCacheForTest(embedder)
	if !ok {
		t.Fatal("getMiniLMEmbedderCacheForTest() ok = false, want true")
	}
	for text, vector := range initial {
		cache.put(text, vector)
	}

	probe := &embeddedProviderCacheTestProbe{}
	originalLoadLocalBERTEmbedder := loadLocalBERTEmbedderFunc
	loadLocalBERTEmbedderFunc = func(modelDir string, spec localModelSpec, accel string, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
		probe.loadCount++
		if state != nil {
			state.executionDevice = "cpu"
			state.runtimeLibrary = modelDir
		}
		return embedder, nil
	}
	originalBatchFunc := miniLMEmbedBatchInternalFunc
	miniLMEmbedBatchInternalFunc = batchStub
	t.Cleanup(func() {
		loadLocalBERTEmbedderFunc = originalLoadLocalBERTEmbedder
		miniLMEmbedBatchInternalFunc = originalBatchFunc
	})

	provider, err := EmbeddedProvider(testPaths(), defaultEmbeddedModel, "auto")
	if err != nil {
		t.Fatalf("EmbeddedProvider() error = %v", err)
	}
	return provider, embedder, probe
}
