# Remove Hash Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the embedded hash fallback from supported runtime behavior so embedded model selection always means the real ONNX-backed model path, with explicit hard failures otherwise.

**Architecture:** Keep only supported runtime providers in production code: embedded ONNX and OpenAI-compatible. Replace `EmbeddedHashProvider()` usage in tests with an explicit deterministic test double package, then simplify scripts and validation to rely on command failure instead of post-hoc fallback string detection.

**Tech Stack:** Go 1.25, existing ONNX-tagged embedded provider path, deterministic fake embedding provider for unit tests, Docker/release shell scripts, `rtk go test`, tagged ONNX integration tests.

---

## File Structure

### Files to create

- `internal/testutil/fakeembed/provider.go`
  - Deterministic test-only provider constructor used by unit tests and benchmarks

### Files to modify

- `internal/vector/provider.go`
  - Remove runtime selection of `embedded-hash` / `hash`
- `internal/vector/local_models.go`
  - Remove fallback-to-hash behavior from embedded provider initialization
- `internal/vector/provider_test.go`
  - Add coverage for rejecting hash provider and failing embedded initialization when unsupported
- `internal/store/repository_test.go`
- `internal/store/repository_benchmark_test.go`
- `internal/store/sqlite_metadata_test.go`
- `internal/importer/importer_integration_test.go`
- `internal/mcp/server_integration_test.go`
- `internal/bench/interface_test.go`
  - Replace hash-provider usage with explicit test double provider
- `scripts/install.sh`
  - Remove fallback-string grep logic and rely on command failure
- `scripts/cmd/release-image/run.sh`
- `scripts/cmd/release-image-arm64-remote/run.sh`
  - Remove fallback-string detection and rely on doctor failure / device checks

### Files expected to be deleted or emptied by the end

- `internal/vector/local_common.go`
  - If it contains only the hash provider after migration, delete it or reduce it to the remaining shared helpers only

## Task 1: Remove hash provider from runtime resolution and embedded fallback

**Files:**
- Modify: `internal/vector/provider.go`
- Modify: `internal/vector/local_models.go`
- Modify: `internal/vector/provider_test.go`

- [ ] **Step 1: Write the failing provider-resolution tests**

Add to `internal/vector/provider_test.go`:

```go
func TestProviderFromEnvRejectsHashProvider(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "hash")

	_, err := ProviderFromEnv(testPaths())
	if err == nil {
		t.Fatal("ProviderFromEnv() error = nil, want non-nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("error = %v, want unsupported provider message", err)
	}
}

func TestEmbeddedProviderFailsWhenEmbeddedRuntimeUnsupported(t *testing.T) {
	provider, err := EmbeddedProvider(testPaths(), "bge-small-en-v1.5", "auto")
	if err != nil {
		t.Fatalf("EmbeddedProvider() constructor error = %v, want lazy provider", err)
	}

	_, err = provider.Func(context.Background(), "health check")
	if err == nil {
		t.Fatal("provider.Func() error = nil, want non-nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Fatalf("error = %v, should not mention hash fallback", err)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `rtk go test ./internal/vector -run 'TestProviderFromEnvRejectsHashProvider|TestEmbeddedProviderFailsWhenEmbeddedRuntimeUnsupported' -v`

Expected: FAIL because `hash` is still accepted and embedded setup still degrades into the hash provider.

- [ ] **Step 3: Remove hash provider selection from runtime provider resolution**

Update `internal/vector/provider.go`:

```go
const (
	ProviderEmbedded = "embedded"
	ProviderOpenAI   = "openai"

	defaultOpenAIModel   = "nomic-embed-text"
	defaultEmbeddedModel = "bge-small-en-v1.5"
)

func ProviderFromEnv(paths xdg.Paths) (Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(envOrDefault("TAGMEM_EMBED_PROVIDER", "", ProviderEmbedded)))

	switch providerName {
	case "", ProviderEmbedded, "local", "builtin":
		model := strings.TrimSpace(envOrDefault("TAGMEM_EMBED_MODEL", "", defaultEmbeddedModel))
		accel := strings.TrimSpace(envOrDefault("TAGMEM_EMBED_ACCEL", "", "auto"))
		return EmbeddedProvider(paths, model, accel)
	case ProviderOpenAI, "openai-compatible", "compat", "ollama":
		model := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_MODEL", "", envOrDefault("OPENAI_MODEL", "", defaultOpenAIModel)))
		baseURL := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_BASE_URL", "", envOrDefault("OPENAI_BASE_URL", "", envOrDefault("OLLAMA_HOST", "", ""))))
		apiKey := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_API_KEY", "", envOrDefault("OPENAI_API_KEY", "", "")))
		return OpenAICompatibleProvider(model, baseURL, apiKey), nil
	default:
		return Provider{}, fmt.Errorf("unsupported embedding provider %q", providerName)
	}
}
```

- [ ] **Step 4: Remove embedded fallback-to-hash behavior**

Update `internal/vector/local_models.go`:

```go
func EmbeddedProvider(paths xdg.Paths, modelName, accel string) (Provider, error) {
	key := sanitizeLocalModel(modelName)
	spec, ok := localModelSpecs[key]
	if !ok {
		return Provider{}, fmt.Errorf("unsupported embedded model %q", modelName)
	}
	modelDir := filepath.Join(paths.ModelDir, spec.Name)
	state := &embeddedRuntimeState{executionDevice: "pending"}

	var (
		once        sync.Once
		embedder    *miniLMEmbedder
		embedderErr error
	)

	return Provider{
		Name:        ProviderEmbedded,
		IndexKey:    ProviderEmbedded + "-" + sanitizeKey(spec.Name),
		Description: spec.Description,
		Model:       spec.Name,
		Func: func(ctx context.Context, text string) ([]float32, error) {
			once.Do(func() {
				embedder, embedderErr = loadLocalBERTEmbedder(modelDir, spec, accel, state)
			})
			if embedderErr != nil {
				return nil, embedderErr
			}
			return embedder.EmbeddingFunc()(ctx, text)
		},
		Batch: func(ctx context.Context, texts []string) ([][]float32, error) {
			once.Do(func() {
				embedder, embedderErr = loadLocalBERTEmbedder(modelDir, spec, accel, state)
			})
			if embedderErr != nil {
				return nil, embedderErr
			}
			return embedder.EmbedBatch(ctx, texts)
		},
		Details: func() map[string]string {
			return map[string]string{"execution_device": state.executionDevice, "runtime_library": state.runtimeLibrary}
		},
	}, nil
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `rtk go test ./internal/vector -run 'TestProviderFromEnvRejectsHashProvider|TestEmbeddedProviderFailsWhenEmbeddedRuntimeUnsupported|TestProviderFromEnvRejectsUnknownProvider' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/vector/provider.go internal/vector/local_models.go internal/vector/provider_test.go
git commit -m "refactor: remove embedded hash runtime selection"
```

### Task 2: Replace EmbeddedHashProvider test usage with explicit test doubles

**Files:**
- Create: `internal/testutil/fakeembed/provider.go`
- Modify: `internal/store/repository_test.go`
- Modify: `internal/store/repository_benchmark_test.go`
- Modify: `internal/store/sqlite_metadata_test.go`
- Modify: `internal/importer/importer_integration_test.go`
- Modify: `internal/mcp/server_integration_test.go`
- Modify: `internal/bench/interface_test.go`

- [ ] **Step 1: Write a failing unit test that imports the new fake provider helper**

Add to `internal/store/sqlite_metadata_test.go`:

```go
func TestFakeEmbedProviderProducesUsableRepositoryProvider(t *testing.T) {
	provider := fakeembed.Provider()
	if provider.Name != vector.ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, vector.ProviderEmbedded)
	}
	if provider.Batch == nil {
		t.Fatal("provider.Batch = nil, want deterministic batch embedding func")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `rtk go test ./internal/store -run TestFakeEmbedProviderProducesUsableRepositoryProvider -v`

Expected: FAIL because `internal/testutil/fakeembed` does not exist yet.

- [ ] **Step 3: Create the explicit test double provider**

Create `internal/testutil/fakeembed/provider.go`:

```go
package fakeembed

import (
	"context"
	"hash/fnv"

	"github.com/codysnider/tagmem/internal/vector"
)

func Provider() vector.Provider {
	fn := func(_ context.Context, text string) ([]float32, error) {
		const dims = 32
		out := make([]float32, dims)
		h := fnv.New32a()
		_, _ = h.Write([]byte(text))
		seed := h.Sum32()
		for i := range out {
			value := float32(((seed + uint32(i*97)) % 1000)) / 1000
			out[i] = value
		}
		return out, nil
	}

	return vector.Provider{
		Name:        vector.ProviderEmbedded,
		IndexKey:    "test-fake-embed-v1",
		Description: "test fake embedding provider",
		Model:       "test-fake-embed-v1",
		Func:        fn,
		Batch: func(ctx context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, 0, len(texts))
			for _, text := range texts {
				vec, err := fn(ctx, text)
				if err != nil {
					return nil, err
				}
				out = append(out, vec)
			}
			return out, nil
		},
	}
}
```

- [ ] **Step 4: Replace EmbeddedHashProvider in the listed tests and benchmark**

Use this exact pattern in each file:

```go
import "github.com/codysnider/tagmem/internal/testutil/fakeembed"

repo := NewRepository(filepath.Join(root, "store.json"), filepath.Join(root, "vector"), fakeembed.Provider())
```

Apply to:

- `internal/store/repository_test.go`
- `internal/store/repository_benchmark_test.go`
- `internal/store/sqlite_metadata_test.go`
- `internal/importer/importer_integration_test.go`
- `internal/mcp/server_integration_test.go`
- `internal/bench/interface_test.go`

- [ ] **Step 5: Run affected test packages**

Run:

```bash
rtk go test ./internal/store ./internal/importer ./internal/mcp ./internal/bench
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/testutil/fakeembed/provider.go internal/store/repository_test.go internal/store/repository_benchmark_test.go internal/store/sqlite_metadata_test.go internal/importer/importer_integration_test.go internal/mcp/server_integration_test.go internal/bench/interface_test.go
git commit -m "test: replace hash fallback with explicit fake provider"
```

### Task 3: Remove fallback-string validation logic from scripts and installer

**Files:**
- Modify: `scripts/install.sh`
- Modify: `scripts/cmd/release-image/run.sh`
- Modify: `scripts/cmd/release-image-arm64-remote/run.sh`

- [ ] **Step 1: Write a failing shell-level assertion as a grep check**

Run:

```bash
grep -n "embedded hash fallback" scripts/install.sh scripts/cmd/release-image/run.sh scripts/cmd/release-image-arm64-remote/run.sh
```

Expected: Matches found, proving scripts still rely on fallback string detection.

- [ ] **Step 2: Remove fallback-string validators and rely on command failure**

Update `scripts/install.sh` by deleting:

```bash
validate_doctor_output() {
  local subject="$1" output="$2"
  if grep -q 'embedded hash fallback' <<<"$output"; then
    printf '%s\n' "$output" >&2
    printf '%s validation failed: embedded hash fallback is not supported for installer installs.\n' "$subject" >&2
    return 1
  fi
}
```

And remove the call site so success depends only on the doctor command succeeding.

Update `scripts/cmd/release-image/run.sh` by deleting:

```bash
validate_doctor_output() {
  local subject="$1"
  local output="$2"
  if grep -q 'embedded hash fallback' <<<"$output"; then
    printf '%s\n' "$output"
    log_error "$subject fell back to embedded hash embeddings"
    exit 1
  fi
}
```

Then simplify `validate_cpu_image` and `validate_gpu_image` so they treat any non-zero doctor run as failure and keep the CUDA device assertion for GPU.

Update `scripts/cmd/release-image-arm64-remote/run.sh` by replacing:

```bash
if grep -q "embedded hash fallback" <<<"$output"; then exit 1; fi;
```

with no extra fallback grep, relying on `doctor` success alone.

- [ ] **Step 3: Run syntax and grep verification**

Run:

```bash
bash -n scripts/install.sh scripts/cmd/release-image/run.sh scripts/cmd/release-image-arm64-remote/run.sh
grep -n "embedded hash fallback" scripts/install.sh scripts/cmd/release-image/run.sh scripts/cmd/release-image-arm64-remote/run.sh
```

Expected:

- `bash -n` succeeds with no output
- `grep` returns no matches

- [ ] **Step 4: Commit**

```bash
git add scripts/install.sh scripts/cmd/release-image/run.sh scripts/cmd/release-image-arm64-remote/run.sh
git commit -m "refactor: fail hard on embedded model validation"
```

### Task 4: Remove obsolete hash provider code and run final verification

**Files:**
- Modify or Delete: `internal/vector/local_common.go`
- Modify: `internal/vector/provider.go`
- Modify: `internal/vector/local_models.go`
- Modify: `internal/vector/provider_test.go`
- Modify: `internal/store/repository_benchmark_test.go`

- [ ] **Step 1: Write the failing cleanup assertions**

Run:

```bash
grep -R -n "EmbeddedHashProvider\|ProviderEmbeddedHash\|embedded-hash\|TAGMEM_EMBED_PROVIDER=hash" internal scripts
```

Expected: Matches found in runtime code or test/benchmark references.

- [ ] **Step 2: Remove the obsolete hash provider implementation**

If `internal/vector/local_common.go` only contains the hash provider after Task 2, delete it. If any shared helper remains, reduce it to only the shared helper:

```go
package vector

import (
	"context"

	chromem "github.com/philippgille/chromem-go"
)

func (e *miniLMEmbedder) EmbeddingFunc() chromem.EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		return e.Embed(text)
	}
}
```

Also ensure:

- `ProviderEmbeddedHash` is removed from `internal/vector/provider.go`
- no benchmark or test still calls `EmbeddedHashProvider()`

- [ ] **Step 3: Run final verification**

Run:

```bash
rtk go test ./internal/vector ./internal/store ./internal/importer ./internal/mcp ./internal/bench ./cmd/tagmem
bash -n scripts/install.sh scripts/cmd/release-image/run.sh scripts/cmd/release-image-arm64-remote/run.sh
grep -R -n "EmbeddedHashProvider\|ProviderEmbeddedHash\|embedded-hash\|TAGMEM_EMBED_PROVIDER=hash" internal scripts
```

Expected:

- tests pass
- shell syntax checks pass
- grep returns no runtime/test/script references to the removed fallback path

- [ ] **Step 4: Commit**

```bash
git add internal/vector/provider.go internal/vector/local_models.go internal/vector/provider_test.go internal/vector/local_common.go internal/store/repository_benchmark_test.go
git commit -m "refactor: remove embedded hash fallback"
```

## Self-Review

### Spec coverage

- runtime provider resolution no longer exposes hash: Task 1
- embedded fallback-to-hash removed: Task 1
- tests use explicit test doubles instead of product fallback: Task 2
- installer/release validation relies on hard failure: Task 3
- obsolete hash code and references removed: Task 4

### Placeholder scan

- No `TBD`, `TODO`, or deferred implementation placeholders remain.
- Each task includes exact files, concrete code, and explicit commands.

### Type consistency

- `ProviderEmbeddedHash`, `EmbeddedHashProvider`, `fakeembed.Provider`, and `index needs repair` semantics are used consistently.
- The plan keeps runtime behavior and test-only behavior clearly separated.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-30-remove-hash-fallback.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
