#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
MODEL="${TIERED_MEMORY_EMBED_MODEL:-bge-small-en-v1.5}"
log_status "Running benchmark suite in Docker for ${MODEL}"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TIERED_MEMORY_BENCH_ROOT/'"$MODEL"'" &&
TIERED_MEMORY_EMBED_PROVIDER=embedded \
TIERED_MEMORY_EMBED_MODEL="'"$MODEL"'" \
go run ./cmd/tagmem bench suite \
  --longmemeval "$TIERED_MEMORY_DATASET_ROOT/longmemeval_s_cleaned.json" \
  --locomo "$TIERED_MEMORY_DATASET_ROOT/locomo/data/locomo10.json" \
  --membench "$TIERED_MEMORY_DATASET_ROOT/membench/MemData/FirstAgent" \
  --convomem-limit 50 \
  --convomem-cache-dir "$TIERED_MEMORY_DATASET_ROOT/convomem_cache" \
  --out-dir "$TIERED_MEMORY_BENCH_ROOT/'"$MODEL"'" \
  --perf-entries 1000 \
  --perf-searches 200'
log_success "Benchmark suite completed"
