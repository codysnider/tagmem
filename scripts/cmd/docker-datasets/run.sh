#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header
log_status "Preparing benchmark datasets in Docker data root"
docker compose -f "$COMPOSE_FILE" run --rm dev bash -lc '
mkdir -p "$TAGMEM_DATASET_ROOT" &&
test -f "$TAGMEM_DATASET_ROOT/longmemeval_s_cleaned.json" || curl -fsSL -o "$TAGMEM_DATASET_ROOT/longmemeval_s_cleaned.json" "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_s_cleaned.json" &&
test -d "$TAGMEM_DATASET_ROOT/locomo" || git clone --depth 1 "https://github.com/snap-research/locomo.git" "$TAGMEM_DATASET_ROOT/locomo" &&
test -d "$TAGMEM_DATASET_ROOT/membench" || git clone --depth 1 "https://github.com/import-myself/Membench.git" "$TAGMEM_DATASET_ROOT/membench"'
log_success "Datasets ready"
