#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
log_status "Running doctor in Docker"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc 'TIERED_MEMORY_EMBED_PROVIDER=embedded go run ./cmd/tagmem doctor'
log_success "Doctor completed"
