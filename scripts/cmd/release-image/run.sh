#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
VERSION_TAG="${TAGMEM_IMAGE_TAG:-$(git -C "$REPO_ROOT" rev-parse --short HEAD)}"
CPU_PLATFORMS="${TAGMEM_CPU_IMAGE_PLATFORMS:-linux/amd64,linux/arm64}"
GPU_PLATFORMS="${TAGMEM_GPU_IMAGE_PLATFORMS:-linux/amd64}"
CPU_RUNTIME_BASE="${TAGMEM_CPU_RUNTIME_BASE:-debian:bookworm-slim}"
GPU_RUNTIME_BASE="${TAGMEM_GPU_RUNTIME_BASE:-nvidia/cuda:13.0.0-cudnn-runtime-ubuntu24.04}"
PUBLISH_CPU_ALIASES="${TAGMEM_PUBLISH_CPU_ALIASES:-1}"

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

contains_platform() {
  local platforms_csv="$1"
  local needle="$2"
  local platform
  IFS=',' read -r -a platform_list <<< "$platforms_csv"
  for platform in "${platform_list[@]}"; do
    platform="$(printf '%s' "$platform" | sed 's/^ *//;s/ *$//')"
    if [[ "$platform" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

validate_doctor_output() {
  local subject="$1"
  local output="$2"
  if grep -q 'embedded hash fallback' <<<"$output"; then
    printf '%s\n' "$output"
    log_error "$subject fell back to embedded hash embeddings"
    exit 1
  fi
}

validate_cpu_image() {
  local image_ref="$1"
  local output
  if ! output="$(docker run --rm --platform linux/amd64 -e TAGMEM_EMBED_PROVIDER=embedded -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" -e TAGMEM_EMBED_ACCEL=cpu "$image_ref" doctor 2>&1)"; then
    printf '%s\n' "$output"
    log_error "CPU runtime image failed doctor validation"
    exit 1
  fi
  validate_doctor_output "CPU runtime image" "$output"
  log_success "Validated amd64 CPU runtime image"
}

validate_gpu_image() {
  local image_ref="$1"
  local output
  require_command nvidia-smi
  if ! output="$(docker run --rm --platform linux/amd64 --gpus all -e TAGMEM_EMBED_PROVIDER=embedded -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" -e TAGMEM_EMBED_ACCEL=cuda "$image_ref" doctor 2>&1)"; then
    printf '%s\n' "$output"
    log_error "GPU runtime image failed doctor validation"
    exit 1
  fi
  validate_doctor_output "GPU runtime image" "$output"
  if ! grep -q 'device:[[:space:]]*cuda' <<<"$output"; then
    printf '%s\n' "$output"
    log_error "GPU runtime image did not report a CUDA execution device"
    exit 1
  fi
  log_success "Validated amd64 GPU runtime image"
}

build_local_image() {
  local image_ref="$1"
  local runtime_base="$2"
  docker build \
    --build-arg TARGETOS=linux \
    --build-arg TARGETARCH=amd64 \
    --build-arg TAGMEM_VERSION="$VERSION_TAG" \
    --build-arg RUNTIME_BASE="$runtime_base" \
    -f "$REPO_ROOT/docker/Dockerfile.runtime" \
    -t "$image_ref" \
    "$REPO_ROOT"
}

login_ghcr() {
  local tmpcfg
  tmpcfg="$(mktemp -d)"
  trap 'rm -rf "$tmpcfg"' EXIT
  export DOCKER_CONFIG="$tmpcfg"
  printf '%s' "$GH_TOKEN" | docker login ghcr.io -u codysnider --password-stdin >/dev/null
}

publish_cpu_manifest() {
  local tags=("$IMAGE_REPO:${VERSION_TAG}-cpu" "$IMAGE_REPO:latest-cpu")
  local create_args=()
  if [[ "$PUBLISH_CPU_ALIASES" == "1" ]]; then
    tags+=("$IMAGE_REPO:${VERSION_TAG}" "$IMAGE_REPO:latest")
  fi
  for tag in "${tags[@]}"; do
    create_args+=( -t "$tag" )
  done
  docker buildx imagetools create "${create_args[@]}" "$IMAGE_REPO:${VERSION_TAG}-cpu-amd64" "$IMAGE_REPO:${VERSION_TAG}-cpu-arm64"
}

require_command docker
require_env GH_TOKEN

for platform in linux/amd64 linux/arm64; do
  if contains_platform "$CPU_PLATFORMS" "$platform"; then
    log_verbose "CPU publish includes $platform"
  fi
done
if ! contains_platform "$GPU_PLATFORMS" "linux/amd64"; then
  log_error "GPU image publish must include linux/amd64"
  exit 1
fi

login_ghcr

cpu_local_image="tagmem-release-cpu:${VERSION_TAG}"
gpu_local_image="tagmem-release-gpu:${VERSION_TAG}"

log_status "Building local amd64 CPU runtime validation image"
build_local_image "$cpu_local_image" "$CPU_RUNTIME_BASE"
validate_cpu_image "$cpu_local_image"

log_status "Building local amd64 GPU runtime validation image"
build_local_image "$gpu_local_image" "$GPU_RUNTIME_BASE"
validate_gpu_image "$gpu_local_image"

log_status "Publishing linux/amd64 CPU image"
docker buildx build --platform linux/amd64 --build-arg TAGMEM_VERSION="$VERSION_TAG" --build-arg RUNTIME_BASE="$CPU_RUNTIME_BASE" -f "$REPO_ROOT/docker/Dockerfile.runtime" -t "$IMAGE_REPO:${VERSION_TAG}-cpu-amd64" --push "$REPO_ROOT"

log_status "Publishing linux/amd64 GPU image"
docker buildx build --platform linux/amd64 --build-arg TAGMEM_VERSION="$VERSION_TAG" --build-arg RUNTIME_BASE="$GPU_RUNTIME_BASE" -f "$REPO_ROOT/docker/Dockerfile.runtime" -t "$IMAGE_REPO:${VERSION_TAG}-gpu" -t "$IMAGE_REPO:latest-gpu" --push "$REPO_ROOT"

if contains_platform "$CPU_PLATFORMS" "linux/arm64"; then
  log_status "Publishing linux/arm64 CPU image on remote arm64 host"
  TAGMEM_IMAGE_REPO="$IMAGE_REPO" TAGMEM_IMAGE_TAG="$VERSION_TAG" TAGMEM_CPU_RUNTIME_BASE="$CPU_RUNTIME_BASE" "$REPO_ROOT/scripts/cmd/release-image-arm64-remote/run.sh"
fi

log_status "Publishing CPU manifest tags"
publish_cpu_manifest

docker image rm "$cpu_local_image" "$gpu_local_image" >/dev/null 2>&1 || true

log_success "Published CPU and GPU runtime images for $VERSION_TAG"
