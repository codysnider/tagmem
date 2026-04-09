#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
log_status "Building Docker dev image"
docker compose -f "$COMPOSE_FILE" build dev
log_success "Docker dev image built"
