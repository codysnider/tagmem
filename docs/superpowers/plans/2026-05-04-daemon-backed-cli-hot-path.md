# Daemon-Backed CLI Hot Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `tagmem add` and `tagmem search` reuse the local daemon’s hot provider/repository state to reduce user-visible command latency.

**Architecture:** Add an opt-in daemon client path for `add` and `search` in the CLI, map current CLI arguments onto the daemon protocol, and preserve current stdout/stderr formatting while reusing the daemon’s hot embedded ONNX/runtime state.

**Tech Stack:** Go 1.25, existing local daemon protocol/client/backend, existing CLI command rendering, `rtk go test`, supported Docker/ONNX command validation.

---

## File Structure

### Files to modify

- `internal/cli/app.go`
- `internal/cli/app_integration_test.go`
- `internal/cli/mcp.go` only if a small shared daemon-selection helper should be reused
- `internal/daemon/client.go` if tiny payload helpers are needed

## Task 1: Add daemon-routing helper for add/search

**Files:**
- Modify: `internal/cli/app.go`
- Modify: `internal/cli/app_integration_test.go`

- [ ] **Step 1: Write failing CLI daemon-routing tests**

Add focused tests proving:

- `add` uses daemon when `TAGMEM_USE_DAEMON=1` and daemon is live
- `search` uses daemon when `TAGMEM_USE_DAEMON=1` and daemon is live
- stdout remains in the same user-facing format

- [ ] **Step 2: Run the tests to verify failure**

Run: `rtk go test ./internal/cli -run 'TestApp(AddUsesLiveDaemonWhenEnabled|SearchUsesLiveDaemonWhenEnabled)' -v`

Expected: FAIL because add/search do not route through the daemon yet.

- [ ] **Step 3: Add a small CLI daemon-routing helper**

Update `internal/cli/app.go` with a helper that:

- checks `TAGMEM_USE_DAEMON=1`
- probes the daemon socket using the existing daemon client path
- chooses daemon-backed execution for `add` and `search` only when the daemon is reachable

- [ ] **Step 4: Wire `runAdd` and `runSearch` through the daemon when enabled**

Map current CLI arguments to daemon requests:

- `add` → `add_entry`
- `search` → `search`

Preserve stdout rendering:

- `add`: `added entry <id> at depth <depth>`
- `search`: current `formatSearchResultLine(...)` output

- [ ] **Step 5: Run the tests to verify pass**

Run: `rtk go test ./internal/cli -run 'TestApp(AddUsesLiveDaemonWhenEnabled|SearchUsesLiveDaemonWhenEnabled)' -v`

Expected: PASS

## Task 2: Keep direct mode and failure behavior correct

**Files:**
- Modify: `internal/cli/app_integration_test.go`
- Modify: `internal/cli/app.go`

- [ ] **Step 1: Write failing mode/fallback tests**

Add tests proving:

- with `TAGMEM_USE_DAEMON` unset, direct mode still works
- with `TAGMEM_USE_DAEMON=1` but no live daemon, the command fails clearly instead of silently hanging or partially succeeding

- [ ] **Step 2: Run the tests to verify failure**

Run: `rtk go test ./internal/cli -run 'TestApp(AddFallsBackToDirectWhenDisabled|AddFailsWhenDaemonRequiredButUnavailable|SearchFailsWhenDaemonRequiredButUnavailable)' -v`

Expected: FAIL until the new routing/failure behavior is fully defined.

- [ ] **Step 3: Implement the explicit failure behavior**

Recommended behavior:

- `TAGMEM_USE_DAEMON=1` means daemon-backed `add`/`search` is required
- if no live daemon is reachable, return a clear error on stderr and non-zero exit

- [ ] **Step 4: Run the tests to verify pass**

Run: `rtk go test ./internal/cli -run 'TestApp(AddFallsBackToDirectWhenDisabled|AddFailsWhenDaemonRequiredButUnavailable|SearchFailsWhenDaemonRequiredButUnavailable)' -v`

Expected: PASS

## Task 3: Re-profile real CLI add/search on the supported Docker/ONNX path

**Files:**
- No code changes required unless tiny test glue is needed

- [ ] **Step 1: Run package verification**

Run:

```bash
rtk go test ./internal/cli ./internal/daemon ./internal/store ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run supported Docker/ONNX add/search with daemon disabled and enabled**

Run equivalent pairs for comparison:

```bash
# direct
TAGMEM_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem add ...
TAGMEM_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem search ...

# daemon-backed
TAGMEM_USE_DAEMON=1 TAGMEM_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem add ...
TAGMEM_USE_DAEMON=1 TAGMEM_PROFILE=1 TAGMEM_EMBED_PROVIDER=embedded go run -tags tagmem_onnx ./cmd/tagmem search ...
```

Expected:

- daemon-backed commands succeed on the supported path
- stdout stays unchanged
- command time should drop materially if repeated provider init was the real hotspot

- [ ] **Step 3: Record before/after numbers in the final handoff**

Include:

- direct add/search timing
- daemon-backed add/search timing
- whether the embedded ONNX init cost is effectively amortized

## Self-Review

### Spec coverage

- daemon-backed add/search hot path: Task 1
- direct-vs-daemon mode correctness: Task 2
- supported Docker/ONNX timing comparison: Task 3

### Placeholder scan

- No placeholders remain.

### Type consistency

- `TAGMEM_USE_DAEMON=1` is the single control for this first daemon-backed CLI hot path.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-04-daemon-backed-cli-hot-path.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
