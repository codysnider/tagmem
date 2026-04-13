#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
VERSION_TAG="${TAGMEM_IMAGE_TAG:-$(git -C "$REPO_ROOT" rev-parse --short HEAD)}"
CPU_RUNTIME_BASE="${TAGMEM_CPU_RUNTIME_BASE:-debian:bookworm-slim}"
REMOTE_HOST="${TAGMEM_ARM64_REMOTE_HOST:-}"
REMOTE_USER="${TAGMEM_ARM64_REMOTE_USER:-}"
REMOTE_KEY="${TAGMEM_ARM64_REMOTE_KEY:-}"
REMOTE_WORKDIR="${TAGMEM_ARM64_REMOTE_WORKDIR:-/tmp/tagmem-release-$VERSION_TAG}"
REMOTE_DOCKER_PATH="${TAGMEM_ARM64_REMOTE_DOCKER_PATH:-/usr/local/bin:/opt/homebrew/bin:/Applications/Docker.app/Contents/Resources/bin:\$PATH}"

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

ssh_args=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new)
if [[ -n "$REMOTE_KEY" ]]; then
  ssh_args+=(-i "$REMOTE_KEY")
fi

require_command ssh
require_command tar
require_env GH_TOKEN
require_env TAGMEM_ARM64_REMOTE_HOST
require_env TAGMEM_ARM64_REMOTE_USER

log_status "Syncing repository to remote arm64 host"
ssh "${ssh_args[@]}" "$REMOTE_USER@$REMOTE_HOST" "/bin/bash -lc 'rm -rf \"$REMOTE_WORKDIR\" && mkdir -p \"$REMOTE_WORKDIR\"'"
tar --exclude=.git --exclude=dist --exclude=.opencode -C "$REPO_ROOT" -cf - . | ssh "${ssh_args[@]}" "$REMOTE_USER@$REMOTE_HOST" "tar -C '$REMOTE_WORKDIR' -xf -"

log_status "Building, validating, and pushing linux/arm64 CPU image on remote host"
ssh "${ssh_args[@]}" "$REMOTE_USER@$REMOTE_HOST" "/bin/bash -lc 'set -euo pipefail; export PATH=\"$REMOTE_DOCKER_PATH\"; cd \"$REMOTE_WORKDIR\"; tmpcfg=\$(mktemp -d); trap \"rm -rf \\\"\$tmpcfg\\\"\" EXIT; auth=\$(printf %s codysnider:$GH_TOKEN | base64); printf \"{\\\"auths\\\":{\\\"ghcr.io\\\":{\\\"auth\\\":\\\"%s\\\"}}}\" \"\$auth\" > \"\$tmpcfg/config.json\"; export DOCKER_CONFIG=\"\$tmpcfg\"; docker build --platform linux/arm64 --build-arg BUILDPLATFORM=linux/arm64 --build-arg TARGETPLATFORM=linux/arm64 --build-arg TARGETOS=linux --build-arg TARGETARCH=arm64 --build-arg TAGMEM_VERSION=$VERSION_TAG --build-arg RUNTIME_BASE=$CPU_RUNTIME_BASE -f docker/Dockerfile.runtime -t $IMAGE_REPO:$VERSION_TAG-cpu-arm64 .; output=\$(docker run --rm --platform linux/arm64 -e TAGMEM_EMBED_PROVIDER=embedded -e TAGMEM_EMBED_MODEL=${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5} -e TAGMEM_EMBED_ACCEL=cpu $IMAGE_REPO:$VERSION_TAG-cpu-arm64 doctor 2>&1); printf \"%s\\n\" \"\$output\"; if grep -q \"embedded hash fallback\" <<<\"\$output\"; then exit 1; fi; docker push $IMAGE_REPO:$VERSION_TAG-cpu-arm64'"

log_success "Published linux/arm64 CPU image: $IMAGE_REPO:$VERSION_TAG-cpu-arm64"
