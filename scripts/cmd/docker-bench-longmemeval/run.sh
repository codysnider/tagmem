#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}"
log_status "Running LongMemEval in Docker for ${MODEL}"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TAGMEM_BENCH_ROOT/longmemeval" &&
TAGMEM_EMBED_PROVIDER=embedded \
TAGMEM_EMBED_MODEL="'"$MODEL"'" \
go run ./cmd/tagmem bench longmemeval \
  --out "$TAGMEM_BENCH_ROOT/longmemeval/'"$MODEL"'.json" \
  "$TAGMEM_DATASET_ROOT/longmemeval_s_cleaned.json"'
log_success "LongMemEval completed"
