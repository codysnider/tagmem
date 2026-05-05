# CLI Profiling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in CLI profiling mode that reports real end-to-end command timing and a few meaningful internal phase timings for `add` and `search`.

**Architecture:** Introduce a small profiling helper in the CLI layer, activate it only with `TAGMEM_PROFILE=1`, and thread it through the real command execution path so timings reflect actual user-visible work rather than synthetic microbenchmarks.

**Tech Stack:** Go 1.25, existing CLI command flow, `time.Now`/`time.Since`, repository hooks for phase timing callbacks, `rtk go test`.

---

## File Structure

### Files to create

- `internal/cli/profile.go`
  - profiling flag detection
  - phase recorder
  - formatted stderr output

### Files to modify

- `internal/cli/app.go`
- `internal/cli/app_integration_test.go`
- `internal/store/repository.go`

## Task 1: Add opt-in CLI profiling helper and command-level output

**Files:**
- Create: `internal/cli/profile.go`
- Modify: `internal/cli/app.go`
- Modify: `internal/cli/app_integration_test.go`

- [ ] **Step 1: Write the failing CLI profiling tests**

Add tests to `internal/cli/app_integration_test.go` for:

- profiling off: normal `add` output unchanged and no `[profile]` block on stderr
- profiling on: `add` emits a `[profile] command=add` block to stderr
- profiling on: `search` emits a `[profile] command=search` block to stderr

Example test shape:

```go
func TestAppAddProfilingOffDoesNotEmitProfileBlock(t *testing.T) {
	useFakeProvider(t)
	root := t.TempDir()
	env := profileTestEnv(t, root)
	stdout, stderr, code := runApp(t, env, "add", "--title", "note", "--body", "body")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "[profile]") {
		t.Fatalf("stderr = %q, want no profile block", stderr)
	}
	if !strings.Contains(stdout, "added entry") {
		t.Fatalf("stdout = %q, want add success", stdout)
	}
}

func TestAppAddProfilingOnEmitsProfileBlock(t *testing.T) {
	useFakeProvider(t)
	root := t.TempDir()
	env := append(profileTestEnv(t, root), "TAGMEM_PROFILE=1")
	_, stderr, code := runApp(t, env, "add", "--title", "note", "--body", "body")
	if code != 0 {
		t.Fatalf("add exit=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "[profile] command=add") {
		t.Fatalf("stderr = %q, want add profile block", stderr)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/cli -run 'TestApp(AddProfilingOffDoesNotEmitProfileBlock|AddProfilingOnEmitsProfileBlock|SearchProfilingOnEmitsProfileBlock)' -v`

Expected: FAIL because profiling support does not exist yet.

- [ ] **Step 3: Add the profiling helper**

Create `internal/cli/profile.go`:

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type profiler struct {
	command string
	enabled bool
	out     io.Writer
	mu      sync.Mutex
	marks   []profileMark
	start   time.Time
}

type profileMark struct {
	name    string
	elapsed time.Duration
}

func newProfiler(command string, out io.Writer) *profiler {
	enabled := strings.TrimSpace(os.Getenv("TAGMEM_PROFILE")) == "1"
	return &profiler{command: command, enabled: enabled, out: out, start: time.Now()}
}

func (p *profiler) Enabled() bool { return p != nil && p.enabled }

func (p *profiler) Track(name string) func() {
	if !p.Enabled() {
		return func() {}
	}
	started := time.Now()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.marks = append(p.marks, profileMark{name: name, elapsed: time.Since(started)})
	}
}

func (p *profiler) Print() {
	if !p.Enabled() {
		return
	}
	fmt.Fprintf(p.out, "[profile] command=%s\n", p.command)
	for _, mark := range p.marks {
		fmt.Fprintf(p.out, "  %s: %.1fms\n", mark.name, float64(mark.elapsed.Microseconds())/1000)
	}
	fmt.Fprintf(p.out, "  total: %.1fms\n", float64(time.Since(p.start).Microseconds())/1000)
	}
```

- [ ] **Step 4: Wire command-level profiling into the App**

Update `internal/cli/app.go` so `Run()` creates a profiler after command parsing and command runners use it for command-level output.

Minimal shape:

```go
func (a *App) Run(args []string) int {
	// existing path resolution/provider work
	command := args[0]
	profiler := newProfiler(command, a.stderr)
	defer profiler.Print()

	// existing switch with profiler threaded into add/search helpers
}
```

It is acceptable to add a package-level hook or small field on `App` if needed, but keep the change minimal.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/cli -run 'TestApp(AddProfilingOffDoesNotEmitProfileBlock|AddProfilingOnEmitsProfileBlock|SearchProfilingOnEmitsProfileBlock)' -v`

Expected: PASS

## Task 2: Instrument repository phases for add and search

**Files:**
- Modify: `internal/store/repository.go`
- Modify: `internal/cli/app.go`
- Modify: `internal/cli/app_integration_test.go`

- [ ] **Step 1: Write failing phase-output tests**

Add tests proving that when `TAGMEM_PROFILE=1`:

- `add` profile output contains phase names for:
  - `resolve_paths`
  - `resolve_provider`
  - `repo_init`
  - `add_total`
  - `sqlite_mutation`
  - `vector_mutation`
- `search` profile output contains phase names for:
  - `repo_init`
  - `search_total`
  - `query_embedding`
  - `vector_query`
  - `sqlite_candidate_fetch`
  - `rerank`
  - `source_hydration`

- [ ] **Step 2: Run the tests to verify they fail**

Run: `rtk go test ./internal/cli -run 'TestApp(AddProfilingShowsPhaseBreakdown|SearchProfilingShowsPhaseBreakdown)' -v`

Expected: FAIL because phase-level instrumentation does not exist yet.

- [ ] **Step 3: Add lightweight repository profiling hooks**

Update `internal/store/repository.go` with an optional profiling callback mechanism, for example:

```go
type phaseRecorder func(name string, elapsed time.Duration)

type Repository struct {
	// existing fields
	recordPhase phaseRecorder
}

func (r *Repository) withPhase(name string, fn func() error) error {
	started := time.Now()
	err := fn()
	if r.recordPhase != nil {
		r.recordPhase(name, time.Since(started))
	}
	return err
}
```

Instrument only the meaningful phases needed for the spec.

- [ ] **Step 4: Wire CLI profiling to repository hooks**

Update `internal/cli/app.go` so `runAdd` and `querySearch` install a phase recorder onto the repository for the duration of the command when profiling is enabled.

Use command-level labels:

- `add_total`
- `search_total`

and repository labels:

- `sqlite_mutation`
- `vector_mutation`
- `query_embedding`
- `vector_query`
- `sqlite_candidate_fetch`
- `rerank`
- `source_hydration`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `rtk go test ./internal/cli -run 'TestApp(AddProfilingShowsPhaseBreakdown|SearchProfilingShowsPhaseBreakdown)' -v`

Expected: PASS

## Task 3: Validate with real commands and keep default behavior unchanged

**Files:**
- Modify: `internal/cli/app_integration_test.go` only if a final default-behavior assertion is needed

- [ ] **Step 1: Run focused command tests**

Run:

```bash
rtk go test ./internal/cli ./internal/store ./cmd/tagmem
```

Expected: PASS

- [ ] **Step 2: Run real CLI smoke commands with profiling enabled**

Use the existing test fixture project or temp directories and run:

```bash
TAGMEM_PROFILE=1 /usr/bin/go run ./cmd/tagmem add --title profile --body body
TAGMEM_PROFILE=1 /usr/bin/go run ./cmd/tagmem search profile
```

Expected:

- commands succeed
- stderr prints `[profile]` blocks with phase timings
- stdout remains in the normal user-facing format

- [ ] **Step 3: Record the resulting phase breakdowns in the final handoff**

Summarize which phase dominates small-store command latency.

## Self-Review

### Spec coverage

- opt-in CLI profiling mode: Task 1
- end-to-end `add` and `search` timing output: Tasks 1 and 2
- meaningful internal phase attribution: Task 2
- default behavior unchanged when profiling is off: Tasks 1 and 3

### Placeholder scan

- No placeholders remain.

### Type consistency

- profiler, phase names, and repository callback behavior are used consistently.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-03-cli-profiling.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
