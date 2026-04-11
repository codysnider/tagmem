#!/bin/bash
set -euo pipefail
source "$(dirname "$0")/../../common/init.sh"
source "$REPO_ROOT/scripts/common/output.sh"
source "$REPO_ROOT/scripts/common/banner.sh"
parse_verbose_flag "$@"
print_header

PART="${1:-}"
if [[ -n "$PART" ]]; then
  case "$PART" in
    patch|minor|major) ;;
    *)
      log_error "Unknown release part: $PART (use patch, minor, or major)"
      exit 1
      ;;
  esac
fi

if [[ -f "$REPO_ROOT/VERSION" ]]; then
  VERSION_FROM_FILE="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
else
  VERSION_FROM_FILE=""
fi
if [[ -n "$PART" ]]; then
  CURRENT="$VERSION_FROM_FILE"
  if [[ ! "$CURRENT" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    log_error "VERSION must be semver, found: $CURRENT"
    exit 1
  fi
  MAJOR="${BASH_REMATCH[1]}"
  MINOR="${BASH_REMATCH[2]}"
  PATCH="${BASH_REMATCH[3]}"
  case "$PART" in
    major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
    minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
    patch) PATCH=$((PATCH + 1)) ;;
  esac
  VERSION="$MAJOR.$MINOR.$PATCH"
  printf '%s\n' "$VERSION" > "$REPO_ROOT/VERSION"
  log_status "Version bumped: $CURRENT -> $VERSION"
else
  VERSION="${TAGMEM_RELEASE_VERSION:-$VERSION_FROM_FILE}"
  if [[ -z "$VERSION" ]]; then
    VERSION="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
  fi
fi

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
IMAGE_PLATFORMS="${TAGMEM_IMAGE_PLATFORMS:-linux/amd64,linux/arm64}"
DIST_DIR="$REPO_ROOT/dist/$VERSION"
TAG_NAME="v$VERSION"

mkdir -p "$DIST_DIR"

log_status "Building release binaries for $VERSION"

build_binary() {
  local goos="$1"
  local goarch="$2"
  local ext=""
  local name="tagmem_${VERSION}_${goos}_${goarch}"
  if [[ "$goos" == "windows" ]]; then
    ext=".exe"
  fi
  local outdir
  outdir="$(mktemp -d)"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/codysnider/tagmem/internal/buildinfo.Version=$VERSION" -o "$outdir/tagmem$ext" ./cmd/tagmem
  tar -C "$outdir" -czf "$DIST_DIR/${name}.tar.gz" "tagmem$ext"
  (cd "$outdir" && zip -q "$DIST_DIR/${name}.zip" "tagmem$ext")
  rm -rf "$outdir"
}

build_binary linux amd64
build_binary linux arm64
build_binary darwin amd64
build_binary darwin arm64
build_binary windows amd64
build_binary windows arm64

(cd "$DIST_DIR" && sha256sum * > SHA256SUMS)

log_success "Release binaries written to $DIST_DIR"

git -C "$REPO_ROOT" add VERSION
if ! git -C "$REPO_ROOT" diff --cached --quiet; then
  git -C "$REPO_ROOT" commit -m "Release $TAG_NAME"
fi

if ! git -C "$REPO_ROOT" rev-parse "$TAG_NAME" >/dev/null 2>&1; then
  git -C "$REPO_ROOT" tag "$TAG_NAME"
fi

log_status "Pushing git commit and tag"
git -C "$REPO_ROOT" push origin main
git -C "$REPO_ROOT" push origin "$TAG_NAME"

log_status "Building and pushing multi-arch runtime image"
docker buildx build \
  --platform "$IMAGE_PLATFORMS" \
  -f "$REPO_ROOT/docker/Dockerfile.runtime" \
  --build-arg TAGMEM_VERSION="$VERSION" \
  -t "$IMAGE_REPO:$VERSION" \
  -t "$IMAGE_REPO:latest" \
  --push \
  "$REPO_ROOT"

log_success "Published $IMAGE_REPO:$VERSION and :latest"

log_status "Publishing GitHub release $TAG_NAME"
gh release create "$TAG_NAME" "$DIST_DIR"/* --title "$TAG_NAME" --generate-notes --latest
log_success "Published GitHub release $TAG_NAME"
