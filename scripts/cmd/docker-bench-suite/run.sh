#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}"
log_status "Running benchmark suite in Docker for ${MODEL}"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TAGMEM_BENCH_ROOT/'"$MODEL"'" &&
TAGMEM_EMBED_PROVIDER=embedded \
TAGMEM_EMBED_MODEL="'"$MODEL"'" \
go run -tags tagmem_onnx ./cmd/tagmem bench suite \
  --longmemeval "$TAGMEM_DATASET_ROOT/longmemeval_s_cleaned.json" \
  --locomo "$TAGMEM_DATASET_ROOT/locomo/data/locomo10.json" \
  --membench "$TAGMEM_DATASET_ROOT/membench/MemData/FirstAgent" \
  --convomem-limit 50 \
  --convomem-cache-dir "$TAGMEM_DATASET_ROOT/convomem_cache" \
  --out-dir "$TAGMEM_BENCH_ROOT/'"$MODEL"'" \
  --perf-entries 1000 \
  --perf-searches 200'
log_success "Benchmark suite completed"
