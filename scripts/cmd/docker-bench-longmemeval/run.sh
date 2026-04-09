#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
MODEL="${TIERED_MEMORY_EMBED_MODEL:-bge-small-en-v1.5}"
log_status "Running LongMemEval in Docker for ${MODEL}"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TIERED_MEMORY_BENCH_ROOT/longmemeval" &&
TIERED_MEMORY_EMBED_PROVIDER=embedded \
TIERED_MEMORY_EMBED_MODEL="'"$MODEL"'" \
go run ./cmd/tagmem bench longmemeval \
  --out "$TIERED_MEMORY_BENCH_ROOT/longmemeval/'"$MODEL"'.json" \
  "$TIERED_MEMORY_DATASET_ROOT/longmemeval_s_cleaned.json"'
log_success "LongMemEval completed"
