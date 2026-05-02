# Local Daemon Multi-Client Design

Date: 2026-05-01

## Summary

Add a local-only `tagmem serve` daemon over a Unix socket so multiple local clients can share one hot metadata/index process without building their own external proxy.

## Goals

- Support multiple local clients safely against one shared local memory instance.
- Keep all data and IPC local to the machine.
- Let multiple `tagmem mcp` sessions share one hot SQLite/vector owner.
- Avoid forcing users to build their own proxy/multiplexer.

## Non-Goals

- Remote TCP service in this phase.
- Public stable network API contract.
- Replacing direct CLI mode for all commands immediately.

## Recommended Approach

Implement a Unix-socket daemon started with `tagmem serve`.

- The daemon owns SQLite, source blobs, and the vector index.
- It accepts newline-delimited JSON request/response messages over a local Unix socket.
- MCP can proxy tool operations through the daemon when configured or when the socket is present.
- Direct local mode remains available when the daemon is not running.

## Architecture

### Daemon

- new `internal/daemon` package
- one listener on a local Unix socket
- concurrent request handling for reads
- serialized writes through the repository’s existing locking/consistency model

### Protocol

Small internal protocol with commands matching current repository/tool operations:

- `status`
- `paths`
- `list_entries`
- `search`
- `show_entry`
- `add_entry`
- `delete_entry`
- `list_depths`
- `list_tags`
- `get_tag_map`
- `doctor`
- `rebuild_index`
- `diary_read`
- `diary_write`
- `kg_query`
- `kg_add`
- `kg_invalidate`
- `kg_timeline`
- `kg_stats`
- `graph_traverse`
- `find_bridges`
- `graph_stats`
- `check_duplicate`

### MCP Integration

`tagmem mcp` should continue serving stdio MCP, but it may use a daemon-backed backend instead of a direct repository backend when the daemon socket is enabled/available.

That keeps MCP compatibility while solving the multi-client shared-store problem.

### Locality

- Unix socket only in this phase
- no remote bind
- socket path under runtime dir when available, otherwise under local data root

## Rollout

1. Add daemon package and serve command.
2. Add daemon client/backend.
3. Make MCP optionally use daemon backend.
4. Add integration tests proving two clients can share one daemon-backed store.

## Recommendation

Proceed with a Unix-socket daemon plus MCP backend proxy path first. It is the smallest local-first answer to the remaining multi-client architecture criticism.
