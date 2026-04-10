#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
VERSION_TAG="${TAGMEM_IMAGE_TAG:-$(git -C "$REPO_ROOT" rev-parse --short HEAD)}"
PLATFORMS="${TAGMEM_IMAGE_PLATFORMS:-linux/amd64}"

log_status "Building and pushing ${IMAGE_REPO}:${VERSION_TAG}"
docker buildx build \
  --platform "$PLATFORMS" \
  -f "$REPO_ROOT/docker/Dockerfile.runtime" \
  -t "$IMAGE_REPO:$VERSION_TAG" \
  -t "$IMAGE_REPO:latest" \
  --push \
  "$REPO_ROOT"
log_success "Published ${IMAGE_REPO}:${VERSION_TAG} and :latest"
