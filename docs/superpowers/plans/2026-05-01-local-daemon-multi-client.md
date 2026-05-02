# Local Daemon Multi-Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a local Unix-socket daemon so multiple local clients, especially multiple MCP sessions, can share one hot TagMem instance safely.

**Architecture:** Introduce an internal daemon server/client protocol over a Unix socket, add a `serve` CLI command, and make `tagmem mcp` able to proxy to the daemon backend instead of owning the repository directly.

**Tech Stack:** Go 1.25, Unix domain sockets, newline-delimited JSON protocol, existing repository/kg/diary stores, MCP stdio server, `rtk go test`.

---

## File Structure

### Files to create

- `internal/daemon/protocol.go`
- `internal/daemon/server.go`
- `internal/daemon/client.go`
- `internal/daemon/backend.go`
- `internal/daemon/server_test.go`
- `internal/cli/serve.go`

### Files to modify

- `internal/cli/app.go`
- `internal/cli/mcp.go`
- `internal/mcp/server.go`
- `internal/mcp/server_integration_test.go`
- `internal/xdg/paths.go`
- `internal/xdg/paths_test.go`

## Task 1: Add socket path resolution and daemon protocol/client

**Files:**
- Create: `internal/daemon/protocol.go`
- Create: `internal/daemon/client.go`
- Modify: `internal/xdg/paths.go`
- Modify: `internal/xdg/paths_test.go`

- [ ] **Step 1: Write failing path/protocol tests**

Add tests for:
- resolved socket path exists in `xdg.Paths`
- daemon client round-trip decode/encode helpers compile and pass basic framing

- [ ] **Step 2: Run tests to verify failure**

Run: `rtk go test ./internal/xdg ./internal/daemon -run 'Test.*Socket|Test.*Protocol' -v`

Expected: FAIL because socket path/protocol do not exist yet.

- [ ] **Step 3: Add socket path to xdg paths**

Add `SocketPath string` to `xdg.Paths` and resolve it from:
- `${XDG_RUNTIME_DIR}/tagmem/tagmem.sock` when `XDG_RUNTIME_DIR` is set
- otherwise `${DataDir}/tagmem.sock`

- [ ] **Step 4: Add daemon protocol and client**

Create a small internal newline-delimited JSON protocol with:
- request id
- command name
- payload
- success/error response

Implement a client that can:
- connect to the socket
- send one request
- receive one response

- [ ] **Step 5: Run tests to verify pass**

Run: `rtk go test ./internal/xdg ./internal/daemon -run 'Test.*Socket|Test.*Protocol' -v`

Expected: PASS

## Task 2: Add daemon server and serve command

**Files:**
- Create: `internal/daemon/backend.go`
- Create: `internal/daemon/server.go`
- Create: `internal/daemon/server_test.go`
- Create: `internal/cli/serve.go`
- Modify: `internal/cli/app.go`

- [ ] **Step 1: Write failing daemon server test**

Add a server integration test that:
- starts daemon on temp socket
- sends `status` request via daemon client
- gets a valid response

- [ ] **Step 2: Run test to verify failure**

Run: `rtk go test ./internal/daemon -run TestDaemonStatusRoundTrip -v`

Expected: FAIL because server/backend/serve command do not exist.

- [ ] **Step 3: Add backend and server**

Implement backend methods that wrap current repo/kg/diary behavior for core commands.

Implement server that:
- creates/removes Unix socket safely
- accepts local client connections
- decodes requests
- dispatches backend operations
- writes JSON responses

- [ ] **Step 4: Add `tagmem serve` command**

Wire `serve` into `internal/cli/app.go` and implement `internal/cli/serve.go` so it:
- resolves paths/provider/repo
- initializes repo
- starts daemon server on resolved socket path

- [ ] **Step 5: Run daemon tests**

Run: `rtk go test ./internal/daemon ./internal/cli -run 'TestDaemonStatusRoundTrip|TestApp.*' -v`

Expected: PASS

## Task 3: Make MCP optionally use daemon backend

**Files:**
- Modify: `internal/cli/mcp.go`
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/server_integration_test.go`

- [ ] **Step 1: Write failing MCP-through-daemon test**

Add an integration test that:
- starts daemon on temp socket
- starts MCP server configured to use daemon backend
- executes a basic MCP tool such as `tagmem_status` or `tagmem_add_entry`
- verifies success

- [ ] **Step 2: Run test to verify failure**

Run: `rtk go test ./internal/mcp -run TestMCPUsesDaemonBackend -v`

Expected: FAIL because MCP only knows direct repository mode.

- [ ] **Step 3: Add daemon-backed MCP mode**

Refactor MCP server construction so handlers can call either:
- direct backend
- daemon client backend

In `runMCP`, if daemon socket exists or an env/flag enables it, use daemon backend.

- [ ] **Step 4: Run tests to verify pass**

Run: `rtk go test ./internal/mcp -run TestMCPUsesDaemonBackend -v`

Expected: PASS

## Task 4: End-to-end multi-client verification

**Files:**
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/mcp/server_integration_test.go`

- [ ] **Step 1: Write failing multi-client test**

Add an end-to-end test that:
- starts one daemon
- creates two clients (daemon client or two MCP-backed clients)
- client A writes an entry
- client B reads/searches it successfully

- [ ] **Step 2: Run test to verify failure**

Run: `rtk go test ./internal/daemon ./internal/mcp -run 'TestDaemonMultiClientSharedStore|TestMCPUsesDaemonBackend' -v`

Expected: FAIL until shared daemon ownership is fully wired.

- [ ] **Step 3: Implement/fix shared-store behavior as needed**

Ensure:
- write serialization works
- read-after-write works across clients
- socket lifecycle is clean in tests

- [ ] **Step 4: Run final verification**

Run:

```bash
rtk go test ./internal/daemon ./internal/mcp ./internal/cli ./cmd/tagmem
```

Expected: PASS

## Self-Review

### Spec coverage

- local-only daemon: Tasks 1 and 2
- `serve` command: Task 2
- daemon client/backend: Tasks 1 and 2
- MCP proxy to daemon: Task 3
- multi-client shared-store proof: Task 4

### Placeholder scan

- No placeholders remain.

### Type consistency

- SocketPath, daemon client/server protocol, and MCP daemon backend are used consistently.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-01-local-daemon-multi-client.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
