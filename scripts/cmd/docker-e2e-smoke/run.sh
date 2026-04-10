#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
log_status "Running Docker MCP and ingest smoke flow"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p /tmp/tm-smoke/notes /tmp/tm-smoke/chats &&
printf "I graduated with a degree in Business Administration.\n" > /tmp/tm-smoke/notes/degree.md &&
printf "> What degree did I graduate with?\nYou graduated with a degree in Business Administration.\n" > /tmp/tm-smoke/chats/session.md &&
mkdir -p "$XDG_CONFIG_HOME/tagmem" &&
printf "You are a benchmark smoke memory system.\n" > "$XDG_CONFIG_HOME/tagmem/identity.txt" &&
TAGMEM_EMBED_PROVIDER=embedded \
go run ./cmd/tagmem ingest --mode files --depth 1 /tmp/tm-smoke/notes &&
TAGMEM_EMBED_PROVIDER=embedded \
go run ./cmd/tagmem ingest --mode conversations --depth 2 /tmp/tm-smoke/chats &&
TAGMEM_EMBED_PROVIDER=embedded \
go run ./cmd/tagmem context --limit 5 &&
TAGMEM_EMBED_PROVIDER=embedded \
go run ./cmd/tagmem doctor'
log_success "Docker end-to-end smoke flow completed"
