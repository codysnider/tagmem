# Embedded ONNX Profiling and Batching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in profiling for the embedded ONNX embedding path and use it to identify and apply a small batching optimization grounded in real phase timings.

**Architecture:** Instrument the embedded provider at meaningful phase boundaries behind `TAGMEM_EMBED_PROFILE=1`, validate the emitted phase timings on the supported Docker/ONNX path, then make a minimal batching/path optimization only after the profiling data identifies a dominant cost.

**Tech Stack:** Go 1.25, existing embedded ONNX provider in `internal/vector`, Docker/ONNX runtime path, `rtk go test`, real `go run -tags tagmem_onnx` command validation.

---

## File Structure

### Files to create

- `internal/vector/profile.go`
  - opt-in embedded-profile helper
  - phase recorder and output helper

### Files to modify

- `internal/vector/local_minilm.go`
- `internal/vector/provider_test.go`
- `internal/cli/app_integration_test.go` only if small command-validation assertions are useful

## Task 1: Add opt-in embedded ONNX phase profiling

**Files:**
- Create: `internal/vector/profile.go`
- Modify: `internal/vector/local_minilm.go`
- Modify: `internal/vector/provider_test.go`

- [ ] **Step 1: Write the failing profiling tests**

Add tests to `internal/vector/provider_test.go` proving that when profiling is enabled for the embedded path:

- phase names are recorded for the embedded provider path
- profiling is silent when disabled

Because the real ONNX path is platform-tagged, it is acceptable to structure these as helper-level tests on the new profiling component rather than full ONNX integration tests.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/vector -run 'TestEmbeddedProfile.*' -v`

Expected: FAIL because the profiling helper does not exist yet.

- [ ] **Step 3: Add the profiling helper**

Create `internal/vector/profile.go` with a small helper activated by `TAGMEM_EMBED_PROFILE=1`.

Required behavior:

- cheap no-op when profiling is disabled
- records named phase durations
- emits a compact summary to `stderr` or another clear sink only when enabled

Phase names to support:

- `tokenize`
- `tensor_prepare`
- `session_checkout`
- `onnx_run`
- `pool_normalize`
- `embed_total`

- [ ] **Step 4: Instrument the embedded provider path**

Update `internal/vector/local_minilm.go` so both single and batch embedding paths record the required phases around:

- tokenizer encode work
- tensor flatten/build
- session channel checkout
- `session.Run(...)`
- output pooling/normalization

Keep profiling opt-in only.

- [ ] **Step 5: Run the tests to verify pass**

Run: `rtk go test ./internal/vector -run 'TestEmbeddedProfile.*' -v`

Expected: PASS

## Task 2: Validate real ONNX phase timings on supported Docker path

**Files:**
- No production code changes required unless a tiny output glue fix is needed

- [ ] **Step 1: Run focused package tests**

Run:

```bash
rtk go test ./internal/vector ./internal/cli ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run real Docker/ONNX commands with embedded profiling enabled**

Run supported-path smoke commands, for example inside the existing Docker dev service:

```bash
TAGMEM_EMBED_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem add --title profile --body body
TAGMEM_EMBED_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem search profile
```

Expected:

- commands succeed on the supported Docker/ONNX path
- embedded profile output prints the named ONNX phases

- [ ] **Step 3: Identify the dominant phase from fresh evidence**

Summarize whether the hot cost is mainly in:

- tokenization
- tensor preparation
- session checkout
- ONNX runtime `Run(...)`
- pooling/normalization

## Task 3: Apply one minimal batching/path optimization based on the measured hot phase

**Files:**
- Modify: `internal/vector/local_minilm.go`
- Modify: `internal/vector/provider_test.go` or add focused tests if needed

- [ ] **Step 1: Write the failing/targeted test for the chosen optimization**

Only after Task 2 identifies the dominant phase, add the smallest meaningful regression or behavior-preservation test needed for the chosen optimization.

Examples:

- if session checkout dominates: test pool reuse behavior
- if token/tensor prep dominates: test batch packing helper behavior
- if ONNX run dominates: test micro-batch splitting behavior remains correct

- [ ] **Step 2: Run the test to verify failure or expose the current inefficiency**

Run the narrowest relevant test command.

- [ ] **Step 3: Implement the minimal optimization**

Allowed optimization examples:

- adjust micro-batch sizing logic
- reduce repeated work between `Embed` and `EmbedBatch`
- avoid unnecessary single-item batch scaffolding
- reduce session-pool overhead if measured

Do not change model semantics.

- [ ] **Step 4: Re-run tests and the supported Docker/ONNX smoke commands**

Run:

```bash
rtk go test ./internal/vector ./internal/cli ./cmd/tagmem
```

Then rerun the profiled Docker/ONNX add/search commands.

Expected:

- tests still pass
- phase timings improve in the targeted hotspot without breaking command behavior

## Self-Review

### Spec coverage

- opt-in ONNX path profiling: Task 1
- real Docker/ONNX phase validation: Task 2
- one evidence-driven batching/path optimization: Task 3

### Placeholder scan

- No placeholders remain.
- Task 3 intentionally depends on evidence from Task 2, but still requires a concrete targeted test and implementation before any optimization is made.

### Type consistency

- phase names and profiling activation remain consistent across helper, provider, and validation paths.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-04-embedded-onnx-profiling-batching.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
