# Daemon CLI ONNX Coverage And Note Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add supported-runtime ONNX coverage for the daemon-backed CLI hot path and record the measured direct-vs-daemon timings durably in the repo.

**Architecture:** Add one Linux+`tagmem_onnx` CLI integration test that uses the real embedded provider and local daemon in an isolated temp XDG root. Add one concise markdown note under `docs/superpowers/` that captures the measured direct and daemon-backed timings plus the warm amortization conclusion.

**Tech Stack:** Go, ONNX runtime build tag, existing CLI/daemon integration helpers, markdown docs.

---

### Task 1: Add supported-path ONNX daemon CLI integration test

**Files:**
- Create: `internal/cli/app_onnx_integration_test.go`
- Test: `internal/cli/app_onnx_integration_test.go`

- [ ] **Step 1: Write the failing ONNX integration test**

Add a Linux+`tagmem_onnx` test that:

```go
//go:build linux && tagmem_onnx

func TestAppDaemonHotPathWithEmbeddedONNXProvider(t *testing.T) {
    // resolve real embedded provider
    // start real daemon server with isolated XDG paths
    // run CLI add with TAGMEM_USE_DAEMON=1
    // run CLI search with TAGMEM_USE_DAEMON=1
    // assert success and normal stdout formatting
}
```

- [ ] **Step 2: Run the ONNX test to verify it fails or is missing**

Run: `rtk go test -tags tagmem_onnx ./internal/cli -run TestAppDaemonHotPathWithEmbeddedONNXProvider -v`
Expected: FAIL before implementation.

- [ ] **Step 3: Implement the minimal real-runtime test helper flow**

Use isolated temp dirs, `xdg.Resolve`, `vector.ProviderFromEnv`, `store.NewRepository`, and the existing `runApp` helper pattern so the test exercises the real daemon-backed CLI path with the embedded ONNX provider.

- [ ] **Step 4: Run the ONNX test to verify it passes**

Run: `rtk go test -tags tagmem_onnx ./internal/cli -run TestAppDaemonHotPathWithEmbeddedONNXProvider -v`
Expected: PASS.

### Task 2: Record timing results durably

**Files:**
- Create: `docs/superpowers/notes/2026-05-04-daemon-cli-onnx-timings.md`

- [ ] **Step 1: Write the concise timing note**

Record the measured values:

```md
- direct add: 797 ms
- direct search: 778 ms
- daemon cold add: 4860 ms
- daemon warm search: 11 ms
```

and the conclusion that daemon mode pays the cold ONNX init once, then strongly amortizes later CLI latency.

- [ ] **Step 2: Verify the note is present and concise**

Read the file and confirm it states both the numbers and the amortization conclusion.

### Task 3: Re-run relevant verification

**Files:**
- Modify: none
- Test: `internal/cli/app_onnx_integration_test.go`

- [ ] **Step 1: Run the relevant Go tests**

Run: `rtk go test ./internal/cli ./internal/daemon ./internal/store ./cmd/tagmem`
Expected: PASS.

- [ ] **Step 2: Run the ONNX-tagged CLI coverage test**

Run: `rtk go test -tags tagmem_onnx ./internal/cli -run TestAppDaemonHotPathWithEmbeddedONNXProvider -v`
Expected: PASS.

- [ ] **Step 3: Report the results**

Summarize the files changed, commands run, and whether the supported daemon-backed ONNX hot path now has durable coverage and durable timing notes.

## Self-Review

### Spec coverage

- Supported ONNX daemon-backed CLI hot path coverage: Task 1
- Durable repo-local timing note: Task 2
- Re-run relevant verification: Task 3

### Placeholder scan

- No placeholders remain.

### Type consistency

- Uses the same daemon-backed CLI path and embedded provider path already established by the design and prior Task 3 validation.
