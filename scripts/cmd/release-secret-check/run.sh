#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

log_status "Checking local-only files are ignored"
if git -C "$REPO_ROOT" ls-files --error-unmatch AGENTS.md >/dev/null 2>&1; then
  log_error "AGENTS.md is tracked by git; it must remain local-only"
  exit 1
fi
if ! git -C "$REPO_ROOT" check-ignore -q AGENTS.md; then
  log_error "AGENTS.md is not ignored; add it to .gitignore before releasing"
  exit 1
fi

log_status "Scanning tracked files for obvious secrets and internal network references"
PATTERN='gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]+|10\.20\.0\.[0-9]+|Codys-MacBook-Pro-2\.local'
MATCHES="$(git -C "$REPO_ROOT" grep -nE "$PATTERN" -- . || true)"
if [[ -n "$MATCHES" ]]; then
  printf '%s\n' "$MATCHES" >&2
  log_error "Tracked files contain a likely secret or internal host reference"
  exit 1
fi

log_success "Secret and internal-host guard passed"
