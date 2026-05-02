#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}"
BENCH_PATH="${TAGMEM_BENCH_PATH:-component}"
OUTPUT_FILE="\$TAGMEM_BENCH_ROOT/longmemeval/${MODEL}.json"
INTERFACE_CACHE_DIR="\$TAGMEM_BENCH_ROOT/longmemeval/cache/${MODEL}"
if [[ "$BENCH_PATH" == "interface" ]]; then
  OUTPUT_FILE="\$TAGMEM_BENCH_ROOT/longmemeval/${MODEL}-interface.json"
elif [[ "$BENCH_PATH" == "both" ]]; then
  OUTPUT_FILE="\$TAGMEM_BENCH_ROOT/longmemeval/${MODEL}-both.json"
fi
log_status "Running LongMemEval in Docker for ${MODEL} (${BENCH_PATH})"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TAGMEM_BENCH_ROOT/longmemeval" &&
TAGMEM_EMBED_PROVIDER=embedded \
TAGMEM_EMBED_MODEL="'"$MODEL"'" \
go run -tags tagmem_onnx ./cmd/tagmem bench longmemeval \
  --path "'"$BENCH_PATH"'" \
  --interface-cache-dir "'"$INTERFACE_CACHE_DIR"'" \
  --out "'"$OUTPUT_FILE"'" \
  "$TAGMEM_DATASET_ROOT/longmemeval_s_cleaned.json"'
log_success "LongMemEval completed"
