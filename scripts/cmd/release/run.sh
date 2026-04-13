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
  log_status "Version bump planned: $CURRENT -> $VERSION"
else
  VERSION="${TAGMEM_RELEASE_VERSION:-$VERSION_FROM_FILE}"
  if [[ -z "$VERSION" ]]; then
    VERSION="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
  fi
fi

IMAGE_REPO="${TAGMEM_IMAGE_REPO:-ghcr.io/codysnider/tagmem}"
TAG_NAME="v$VERSION"

log_status "Running release preflight"
TAGMEM_RELEASE_VERSION="$VERSION" "$REPO_ROOT/scripts/cmd/release-check/run.sh"

if [[ -n "$PART" ]]; then
  printf '%s\n' "$VERSION" > "$REPO_ROOT/VERSION"
  log_status "Version bumped: $CURRENT -> $VERSION"
fi

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

log_status "Publishing CPU and GPU runtime images"
TAGMEM_IMAGE_REPO="$IMAGE_REPO" TAGMEM_IMAGE_TAG="$VERSION" "$REPO_ROOT/scripts/cmd/release-image/run.sh"

log_success "Published runtime images for $IMAGE_REPO"

log_status "Validating released images on release hosts"
TAGMEM_RELEASE_VERSION="$VERSION" TAGMEM_IMAGE_REPO="$IMAGE_REPO" "$REPO_ROOT/scripts/cmd/release-host-validate/run.sh"

log_success "Release host validation completed"

log_status "Publishing GitHub release $TAG_NAME"
gh release create "$TAG_NAME" --title "$TAG_NAME" --generate-notes --latest
log_success "Published GitHub release $TAG_NAME"
