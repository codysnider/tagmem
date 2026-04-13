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

require_builder_platforms() {
  local label="$1"
  local platforms_csv="$2"
  local inspect_output supported requested
  inspect_output="$(docker buildx inspect --bootstrap)"
  supported="$(awk '/Platforms:/ {$1=""; sub(/^ /, ""); print}' <<<"$inspect_output" | tr ',' '\n' | sed 's/^ *//;s/ *$//' | sort -u)"
  IFS=',' read -r -a requested <<< "$platforms_csv"
  for platform in "${requested[@]}"; do
    platform="$(printf '%s' "$platform" | sed 's/^ *//;s/ *$//')"
    if ! grep -Fxq "$platform" <<<"$supported"; then
      log_error "$label requires buildx support for $platform, but the active builder does not advertise it."
      printf '%s\n' "$inspect_output"
      exit 1
    fi
  done
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
  log_success "Validated CPU runtime image"
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
  log_success "Validated GPU runtime image"
}

build_local_image() {
  local image_ref="$1"
  local runtime_base="$2"
  docker build \
    --build-arg TARGETARCH=amd64 \
    --build-arg TAGMEM_VERSION="$VERSION_TAG" \
    --build-arg RUNTIME_BASE="$runtime_base" \
    -f "$REPO_ROOT/docker/Dockerfile.runtime" \
    -t "$image_ref" \
    "$REPO_ROOT"
}

push_image() {
  local platforms="$1"
  local runtime_base="$2"
  shift
  shift
  local args=(
    --platform "$platforms"
    -f "$REPO_ROOT/docker/Dockerfile.runtime"
    --build-arg TAGMEM_VERSION="$VERSION_TAG"
    --build-arg RUNTIME_BASE="$runtime_base"
    --push
  )
  local tag
  for tag in "$@"; do
    args+=( -t "$tag" )
  done
  args+=( "$REPO_ROOT" )
  docker buildx build "${args[@]}"
}

IFS=',' read -r -a cpu_platform_list <<< "$CPU_PLATFORMS"
for platform in "${cpu_platform_list[@]}"; do
  case "$platform" in
    linux/amd64|linux/arm64) ;;
    *)
      log_error "CPU image platform $platform is not supported. Only linux/amd64 and linux/arm64 are currently allowed."
      exit 1
      ;;
  esac
done

IFS=',' read -r -a gpu_platform_list <<< "$GPU_PLATFORMS"
for platform in "${gpu_platform_list[@]}"; do
  if [[ "$platform" != "linux/amd64" ]]; then
    log_error "GPU image platform $platform is not supported. Only linux/amd64 is currently allowed."
    exit 1
  fi
done

require_builder_platforms "CPU image publish" "$CPU_PLATFORMS"
require_builder_platforms "GPU image publish" "$GPU_PLATFORMS"

cpu_local_image="tagmem-release-cpu:${VERSION_TAG}"
gpu_local_image="tagmem-release-gpu:${VERSION_TAG}"
cpu_tags=("$IMAGE_REPO:${VERSION_TAG}-cpu" "$IMAGE_REPO:latest-cpu")
if [[ "$PUBLISH_CPU_ALIASES" == "1" ]]; then
  cpu_tags+=("$IMAGE_REPO:${VERSION_TAG}" "$IMAGE_REPO:latest")
fi
gpu_tags=("$IMAGE_REPO:${VERSION_TAG}-gpu" "$IMAGE_REPO:latest-gpu")

log_status "Building local CPU runtime validation image"
build_local_image "$cpu_local_image" "$CPU_RUNTIME_BASE"
validate_cpu_image "$cpu_local_image"

log_status "Building local GPU runtime validation image"
build_local_image "$gpu_local_image" "$GPU_RUNTIME_BASE"
validate_gpu_image "$gpu_local_image"

log_status "Publishing CPU runtime image tags"
push_image "$CPU_PLATFORMS" "$CPU_RUNTIME_BASE" "${cpu_tags[@]}"

log_status "Publishing GPU runtime image tags"
push_image "$GPU_PLATFORMS" "$GPU_RUNTIME_BASE" "${gpu_tags[@]}"

docker image rm "$cpu_local_image" "$gpu_local_image" >/dev/null 2>&1 || true

log_success "Published CPU tags: ${cpu_tags[*]}"
log_success "Published GPU tags: ${gpu_tags[*]}"
