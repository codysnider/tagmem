#!/bin/bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/docker/docker-compose.yml"
TAGMEM_DATA_ROOT="${TAGMEM_DATA_ROOT:-${HOME:-$REPO_ROOT}/.local/share/tagmem}"
export TAGMEM_DATA_ROOT

parse_verbose_flag() {
  VERBOSE=0
  for arg in "$@"; do
    if [[ "$arg" == "-v" || "$arg" == "--verbose" ]]; then
      VERBOSE=1
    fi
  done
}
