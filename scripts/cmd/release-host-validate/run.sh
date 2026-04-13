#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
VERSION_TAG="${TAGMEM_RELEASE_VERSION:-$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")}" 
LAPTOP_HOST="${TAGMEM_LINUX_CPU_REMOTE_HOST:-}"
LAPTOP_USER="${TAGMEM_LINUX_CPU_REMOTE_USER:-}"
LAPTOP_KEY="${TAGMEM_LINUX_CPU_REMOTE_KEY:-}"
ARM64_HOST="${TAGMEM_ARM64_REMOTE_HOST:-}"
ARM64_USER="${TAGMEM_ARM64_REMOTE_USER:-}"
ARM64_KEY="${TAGMEM_ARM64_REMOTE_KEY:-}"

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    log_error "Required command not found: $name"
    exit 1
  fi
}

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    log_error "Required environment variable missing: $name"
    exit 1
  fi
}

validate_local_gpu() {
  local root
  root="$(mktemp -d)"
  export TAGMEM_CPU_IMAGE_REF="$IMAGE_REPO:$VERSION_TAG-cpu"
  export TAGMEM_GPU_IMAGE_REF="$IMAGE_REPO:$VERSION_TAG-gpu"
  export TAGMEM_DATA_ROOT="$root/data"
  export TAGMEM_CONFIG_ROOT="$root/config"
  export TAGMEM_CACHE_ROOT="$root/cache"
  export TAGMEM_BIN_DIR="$root/bin"
  bash "$REPO_ROOT/scripts/install.sh" --yes --no-patch-opencode
  "$root/bin/tagmem" doctor
  "$root/bin/tagmem" add --depth 0 --title office-release-$VERSION_TAG --body "office release $VERSION_TAG validation"
  "$root/bin/tagmem" search office-release-$VERSION_TAG
}

validate_remote_cpu() {
  local host="$1"
  local user="$2"
  local key="$3"
  local extra_path="$4"
  local install_path="/tmp/tagmem-install-validate.sh"
  local ssh_args=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new)
  if [[ -n "$key" ]]; then
    ssh_args+=(-i "$key")
  fi
  ssh "${ssh_args[@]}" "$user@$host" "cat > '$install_path' && chmod +x '$install_path'" < "$REPO_ROOT/scripts/install.sh"
  ssh "${ssh_args[@]}" "$user@$host" "/bin/bash -lc 'set -euo pipefail; if [[ "$extra_path" != "__KEEP__" ]]; then export PATH=\"$extra_path\"; fi; root=\$(mktemp -d); export TAGMEM_CPU_IMAGE_REF=\"$IMAGE_REPO:$VERSION_TAG-cpu\" TAGMEM_GPU_IMAGE_REF=\"$IMAGE_REPO:$VERSION_TAG-gpu\" TAGMEM_DATA_ROOT=\"\$root/data\" TAGMEM_CONFIG_ROOT=\"\$root/config\" TAGMEM_CACHE_ROOT=\"\$root/cache\" TAGMEM_BIN_DIR=\"\$root/bin\"; \"$install_path\" --yes --no-patch-opencode; \"\$root/bin/tagmem\" doctor; \"\$root/bin/tagmem\" add --depth 0 --title release-$VERSION_TAG --body \"remote release $VERSION_TAG validation\"; \"\$root/bin/tagmem\" search release-$VERSION_TAG'"
}

require_command ssh
require_env TAGMEM_LINUX_CPU_REMOTE_HOST
require_env TAGMEM_LINUX_CPU_REMOTE_USER
require_env TAGMEM_ARM64_REMOTE_HOST
require_env TAGMEM_ARM64_REMOTE_USER

log_status "Validating Office GPU installer path"
validate_local_gpu

log_status "Validating Laptop CPU installer path"
validate_remote_cpu "$LAPTOP_HOST" "$LAPTOP_USER" "$LAPTOP_KEY" '__KEEP__'

log_status "Validating Mac arm64 CPU installer path"
validate_remote_cpu "$ARM64_HOST" "$ARM64_USER" "$ARM64_KEY" '/usr/local/bin:/opt/homebrew/bin:/Applications/Docker.app/Contents/Resources/bin:/usr/bin:/bin:/usr/sbin:/sbin'

log_success "Release host validation completed"
