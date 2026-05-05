# Daemon-Backed CLI Hot Path Design

Date: 2026-05-04

## Summary

Reduce user-visible `tagmem add` and `tagmem search` latency by letting those CLI commands optionally route through the local daemon, so they can reuse one hot embedded model/session and one hot repository/index instead of paying startup costs in every fresh CLI process.

## Goals

- Improve the real CLI product path for `add` and `search`.
- Reuse the daemon’s hot embedded provider and repository state.
- Keep everything local-only.
- Preserve current direct CLI mode as a fallback/explicit mode.

## Non-Goals

- Routing every CLI command through the daemon in this phase.
- Changing MCP behavior again in this phase.
- Removing direct CLI mode entirely.

## Recommended Approach

Add an opt-in daemon-backed CLI path for `add` and `search`, controlled by a simple mode selection rule:

- if daemon mode is explicitly enabled and the daemon is reachable, use daemon-backed command execution
- otherwise, keep current direct local behavior

The daemon already supports the necessary commands (`add_entry`, `search`), so the main work is client-side routing and preserving CLI output compatibility.

## Architecture

### CLI Routing

Add a small routing decision for `add` and `search` only.

Recommended activation:

- `TAGMEM_USE_DAEMON=1`

Optional later: opportunistic auto-use when a live daemon is present.

### Command Semantics

When daemon mode is active:

- `add` sends the existing fields to daemon `add_entry`
- `search` sends the existing query/depth/tag/limit to daemon `search`

The CLI should continue to render stdout/stderr exactly as it does today.

### Locality

- Unix socket only
- same local daemon process as the multi-client work
- no remote network behavior

## Recommendation

Proceed with daemon-backed `add` and `search` first. That directly targets the biggest measured user-visible latency source: repeated per-process embedded model/provider initialization.
