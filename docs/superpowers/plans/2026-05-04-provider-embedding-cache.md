# Provider Embedding Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce repeated ONNX runs by adding a bounded exact-text embedding cache inside the embedded provider and making both single-text and batch embedding paths reuse cached vectors.

**Architecture:** Add a small bounded cache to the embedded provider/embedder, key it by exact input text, and make `EmbedBatch` only run ONNX for cache misses while merging cached and newly computed vectors back into original order.

**Tech Stack:** Go 1.25, existing embedded ONNX provider in `internal/vector`, exact-text in-memory cache, Docker/ONNX validation path, `rtk go test`.

---

## File Structure

### Files to create

- `internal/vector/cache.go`
  - bounded exact-text embedding cache helper

### Files to modify

- `internal/vector/local_models.go`
- `internal/vector/local_minilm.go`
- `internal/vector/provider_test.go`

## Task 1: Add a bounded embedding cache helper

**Files:**
- Create: `internal/vector/cache.go`
- Modify: `internal/vector/provider_test.go`

- [ ] **Step 1: Write the failing cache tests**

Add tests proving:

- cache returns cloned vectors for hits
- cache evicts oldest entries when over capacity
- cache handles zero capacity as disabled

Example shape:

```go
func TestEmbeddingCacheEvictsOldest(t *testing.T) {
	cache := newEmbeddingCache(2)
	cache.put("one", []float32{1})
	cache.put("two", []float32{2})
	cache.put("three", []float32{3})

	if _, ok := cache.get("one"); ok {
		t.Fatal("cache.get(one) = ok, want evicted")
	}
	if value, ok := cache.get("two"); !ok || len(value) != 1 || value[0] != 2 {
		t.Fatalf("cache.get(two) = (%v, %v), want retained value", value, ok)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/vector -run 'TestEmbeddingCache.*' -v`

Expected: FAIL because the cache helper does not exist yet.

- [ ] **Step 3: Implement the bounded exact-text cache**

Create `internal/vector/cache.go`:

```go
package vector

type embeddingCache struct {
	capacity int
	order    []string
	values   map[string][]float32
}

func newEmbeddingCache(capacity int) *embeddingCache {
	if capacity < 0 {
		capacity = 0
	}
	return &embeddingCache{
		capacity: capacity,
		order:    make([]string, 0, capacity),
		values:   make(map[string][]float32, capacity),
	}
}

func (c *embeddingCache) get(key string) ([]float32, bool) {
	if c == nil {
		return nil, false
	}
	value, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return cloneEmbedding(value), true
}

func (c *embeddingCache) put(key string, value []float32) {
	if c == nil || c.capacity == 0 {
		return
	}
	if _, ok := c.values[key]; ok {
		c.values[key] = cloneEmbedding(value)
		return
	}
	if len(c.order) >= c.capacity {
		oldest := c.order[0]
		delete(c.values, oldest)
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.values[key] = cloneEmbedding(value)
}

func cloneEmbedding(value []float32) []float32 {
	if value == nil {
		return nil
	}
	copyValue := make([]float32, len(value))
	copy(copyValue, value)
	return copyValue
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `rtk go test ./internal/vector -run 'TestEmbeddingCache.*' -v`

Expected: PASS

## Task 2: Wire the cache into the embedded provider/embedder

**Files:**
- Modify: `internal/vector/local_models.go`
- Modify: `internal/vector/local_minilm.go`
- Modify: `internal/vector/provider_test.go`

- [ ] **Step 1: Write failing cache-aware embed tests**

Add tests proving:

- repeated `Embed(text)` reuses cache instead of rerunning the underlying embed batch path
- `EmbedBatch` only runs the underlying ONNX path for misses and returns results in original order

Use a small test seam/counter around the internal batch execution path if needed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/vector -run 'TestMiniLMEmbed(Cache.*|BatchCache.*)' -v`

Expected: FAIL because the embedded provider/embedder does not use a cache yet.

- [ ] **Step 3: Add the cache to the embedder/provider**

Update `internal/vector/local_minilm.go` and `local_models.go`:

- add an `embeddingCache` field to `miniLMEmbedder`
- initialize it with a fixed capacity (for example 512 or 1024) in `loadLocalBERTEmbedderWithRuntime`
- update `Embed(text)` to:
  - return cached vector on hit
  - otherwise compute once and store
- update `EmbedBatch(texts)` to:
  - collect hits and misses
  - only call the internal ONNX path for misses
  - merge cached and newly computed vectors into original order

Preserve output vectors exactly.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `rtk go test ./internal/vector -run 'TestMiniLMEmbed(Cache.*|BatchCache.*)' -v`

Expected: PASS

## Task 3: Re-profile the supported Docker/ONNX path

**Files:**
- No production code changes required unless a tiny profiling glue fix is needed

- [ ] **Step 1: Run package verification**

Run:

```bash
rtk go test ./internal/vector ./internal/tagging ./internal/cli ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run the supported Docker/ONNX profile commands again**

Run supported path commands with:

```bash
TAGMEM_EMBED_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded TAGMEM_PROFILE=1 go run -tags tagmem_onnx ./cmd/tagmem add --title profile --body body
TAGMEM_EMBED_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded TAGMEM_PROFILE=1 go run -tags tagmem_onnx ./cmd/tagmem search profile
```

Expected:

- commands still succeed
- embedded phase output still appears
- repeated identical text paths should show fewer or no repeated ONNX runs where cache hits apply

- [ ] **Step 3: Record the before/after observations in the final handoff**

Include:

- whether identical repeated texts avoided ONNX reruns
- whether `add` command phase shape improved
- whether `search` command phase shape improved

## Self-Review

### Spec coverage

- bounded provider-local embedding cache: Tasks 1 and 2
- cache-aware single and batch embed paths: Task 2
- supported Docker/ONNX reprofiling: Task 3

### Placeholder scan

- No placeholders remain.

### Type consistency

- cache semantics remain exact-text and process-local throughout.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-04-provider-embedding-cache.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
