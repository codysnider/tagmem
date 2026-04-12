#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}"
BASELINE_PATH="$REPO_ROOT/benchmarks/guards/longmemeval-${MODEL}.json"
RESULT_PATH="$TAGMEM_DATA_ROOT/bench-results/longmemeval/${MODEL}.json"

log_status "Running focused Go tests"
rtk go test ./internal/store ./internal/importer ./internal/cli ./internal/mcp
log_success "Focused Go tests passed"

log_status "Preparing benchmark datasets"
"$REPO_ROOT/scripts/cmd/docker-datasets/run.sh"

log_status "Running LongMemEval release guardrail for ${MODEL}"
TAGMEM_EMBED_MODEL="$MODEL" "$REPO_ROOT/scripts/cmd/docker-bench-longmemeval/run.sh"

if [[ ! -f "$BASELINE_PATH" ]]; then
  log_error "Missing LongMemEval baseline: $BASELINE_PATH"
  exit 1
fi
if [[ ! -f "$RESULT_PATH" ]]; then
  log_error "Missing LongMemEval result: $RESULT_PATH"
  exit 1
fi

log_status "Checking LongMemEval regression guardrail"
go run "$REPO_ROOT/scripts/cmd/release-check/compare_longmemeval.go" "$BASELINE_PATH" "$RESULT_PATH"
log_success "Release guardrail completed"
