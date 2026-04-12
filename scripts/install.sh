#!/usr/bin/env bash
set -euo pipefail

TAGMEM_IMAGE_REF="${TAGMEM_IMAGE_REF:-ghcr.io/codysnider/tagmem:latest}"
TAGMEM_RELEASES_URL="${TAGMEM_RELEASES_URL:-https://api.github.com/repos/codysnider/tagmem/releases/latest}"
TAGMEM_RAW_BASE="${TAGMEM_RAW_BASE:-https://raw.githubusercontent.com/codysnider/tagmem/main}"
TAGMEM_PATCH_OPENCODE="ask"
TAGMEM_YES=0
TAGMEM_DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|--non-interactive)
      TAGMEM_YES=1
      ;;
    --patch-opencode)
      TAGMEM_PATCH_OPENCODE="yes"
      ;;
    --no-patch-opencode)
      TAGMEM_PATCH_OPENCODE="no"
      ;;
    --dry-run)
      TAGMEM_DRY_RUN=1
      ;;
    *)
      printf 'Unknown option: %s\n' "$1" >&2
      exit 1
      ;;
  esac
  shift
done

detect_os() {
  case "$(uname -s 2>/dev/null || echo unknown)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    *) printf 'unknown' ;;
  esac
}

detect_arch() {
  case "$(uname -m 2>/dev/null || echo unknown)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) printf 'unknown' ;;
  esac
}

default_data_root() {
  case "$(detect_os)" in
    darwin) printf '%s' "$HOME/Library/Application Support/tagmem" ;;
    *) printf '%s' "${XDG_DATA_HOME:-$HOME/.local/share}/tagmem" ;;
  esac
}

default_config_root() {
  case "$(detect_os)" in
    darwin) printf '%s' "$HOME/Library/Application Support/tagmem/config" ;;
    *) printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}/tagmem" ;;
  esac
}

default_cache_root() {
  case "$(detect_os)" in
    darwin) printf '%s' "$HOME/Library/Caches/tagmem" ;;
    *) printf '%s' "${XDG_CACHE_HOME:-$HOME/.cache}/tagmem" ;;
  esac
}

detect_bin_dir() {
  if [[ -d "$HOME/.local/bin" || ! -d "$HOME/bin" ]]; then
    printf '%s' "$HOME/.local/bin"
  else
    printf '%s' "$HOME/bin"
  fi
}

ask() {
  local prompt="$1"
  local default_yes="$2"
  if [[ "$TAGMEM_YES" == "1" ]]; then
    [[ "$default_yes" == "yes" ]] && return 0 || return 1
  fi
  if [[ "$default_yes" == "yes" ]]; then
    read -r -p "$prompt [Y/n] " reply
    [[ -z "$reply" || "$reply" =~ ^[Yy]$ ]]
  else
    read -r -p "$prompt [y/N] " reply
    [[ "$reply" =~ ^[Yy]$ ]]
  fi
}

path_contains() {
  local dir="$1"
  case ":$PATH:" in
    *":$dir:"*) return 0 ;;
    *) return 1 ;;
  esac
}

backup_file() {
  local file="$1"
  local stamp
  stamp="$(date +%Y%m%d-%H%M%S)"
  cp "$file" "$file.bak.$stamp"
  printf 'Backed up %s -> %s.bak.%s\n' "$file" "$file" "$stamp"
}

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "$name" >&2
    exit 1
  fi
}

supports_release_binary() {
  local os="$1" arch="$2"
  [[ "$os" == "linux" && "$arch" == "amd64" ]]
}

render_with_python() {
  local path="$1"
  local template="$2"
  local data_root="$3"
  local config_root="$4"
  local cache_root="$5"
  local gpu_snippet="$6"
  local default_accel="$7"
  python3 - <<'PY' "$path" "$template" "$data_root" "$config_root" "$cache_root" "$TAGMEM_IMAGE_REF" "$gpu_snippet" "$default_accel"
from pathlib import Path
import sys
path, template, data_root, config_root, cache_root, image_ref, gpu_snippet, default_accel = sys.argv[1:9]
text = template.replace('@DATA_ROOT@', data_root)
text = text.replace('@CONFIG_ROOT@', config_root)
text = text.replace('@CACHE_ROOT@', cache_root)
text = text.replace('@IMAGE_REF@', image_ref)
text = text.replace('@GPU_SNIPPET@', gpu_snippet)
text = text.replace('@DEFAULT_ACCEL@', default_accel)
Path(path).write_text(text)
PY
  chmod +x "$path"
}

docker_gpu_snippet='GPU_ARGS=()
if [[ "${TAGMEM_DOCKER_GPU:-auto}" == "on" ]]; then
  GPU_ARGS=(--gpus all)
elif [[ "${TAGMEM_DOCKER_GPU:-auto}" == "auto" ]] && command -v nvidia-smi >/dev/null 2>&1; then
  GPU_ARGS=(--gpus all)
fi'

write_docker_wrapper() {
  local path="$1" data_root="$2" config_root="$3" cache_root="$4" default_accel="$5"
  local template
  template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
@GPU_SNIPPET@
exec docker run --rm \
  "${GPU_ARGS[@]}" \
  -v "$DATA_ROOT:$DATA_ROOT" \
  -v "$CONFIG_ROOT:$CONFIG_ROOT" \
  -v "$CACHE_ROOT:$CACHE_ROOT" \
  -e TAGMEM_DATA_ROOT="$DATA_ROOT" \
  -e TAGMEM_CONFIG_ROOT="$CONFIG_ROOT" \
  -e TAGMEM_CACHE_ROOT="$CACHE_ROOT" \
  -e XDG_CONFIG_HOME="$CONFIG_ROOT" \
  -e XDG_DATA_HOME="$DATA_ROOT" \
  -e XDG_CACHE_HOME="$CACHE_ROOT" \
  -e TAGMEM_EMBED_PROVIDER="${TAGMEM_EMBED_PROVIDER:-embedded}" \
  -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" \
  -e TAGMEM_EMBED_ACCEL="${TAGMEM_EMBED_ACCEL:-@DEFAULT_ACCEL@}" \
  "$IMAGE_REF" "$@"
EOF
)
  render_with_python "$path" "$template" "$data_root" "$config_root" "$cache_root" "$docker_gpu_snippet" "$default_accel"
}

write_docker_mcp_wrapper() {
  local path="$1" data_root="$2" config_root="$3" cache_root="$4" default_accel="$5"
  local template
  template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
@GPU_SNIPPET@
exec docker run -i --rm --init \
  "${GPU_ARGS[@]}" \
  -v "$DATA_ROOT:$DATA_ROOT" \
  -v "$CONFIG_ROOT:$CONFIG_ROOT" \
  -v "$CACHE_ROOT:$CACHE_ROOT" \
  -e TAGMEM_DATA_ROOT="$DATA_ROOT" \
  -e TAGMEM_CONFIG_ROOT="$CONFIG_ROOT" \
  -e TAGMEM_CACHE_ROOT="$CACHE_ROOT" \
  -e XDG_CONFIG_HOME="$CONFIG_ROOT" \
  -e XDG_DATA_HOME="$DATA_ROOT" \
  -e XDG_CACHE_HOME="$CACHE_ROOT" \
  -e TAGMEM_EMBED_PROVIDER="${TAGMEM_EMBED_PROVIDER:-embedded}" \
  -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" \
  -e TAGMEM_EMBED_ACCEL="${TAGMEM_EMBED_ACCEL:-@DEFAULT_ACCEL@}" \
  "$IMAGE_REF" mcp
EOF
)
  render_with_python "$path" "$template" "$data_root" "$config_root" "$cache_root" "$docker_gpu_snippet" "$default_accel"
}

write_binary_wrapper() {
  local path="$1" real_bin="$2" data_root="$3" config_root="$4" cache_root="$5"
  cat >"$path" <<EOF
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="\${TAGMEM_DATA_ROOT:-$data_root}"
CONFIG_ROOT="\${TAGMEM_CONFIG_ROOT:-$config_root}"
CACHE_ROOT="\${TAGMEM_CACHE_ROOT:-$cache_root}"
mkdir -p "\$DATA_ROOT" "\$CONFIG_ROOT" "\$CACHE_ROOT"
export TAGMEM_DATA_ROOT="\$DATA_ROOT"
export TAGMEM_CONFIG_ROOT="\$CONFIG_ROOT"
export TAGMEM_CACHE_ROOT="\$CACHE_ROOT"
exec "$real_bin" "\$@"
EOF
  chmod +x "$path"
}

write_binary_mcp_wrapper() {
  local path="$1" real_bin="$2" data_root="$3" config_root="$4" cache_root="$5"
  cat >"$path" <<EOF
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="\${TAGMEM_DATA_ROOT:-$data_root}"
CONFIG_ROOT="\${TAGMEM_CONFIG_ROOT:-$config_root}"
CACHE_ROOT="\${TAGMEM_CACHE_ROOT:-$cache_root}"
mkdir -p "\$DATA_ROOT" "\$CONFIG_ROOT" "\$CACHE_ROOT"
export TAGMEM_DATA_ROOT="\$DATA_ROOT"
export TAGMEM_CONFIG_ROOT="\$CONFIG_ROOT"
export TAGMEM_CACHE_ROOT="\$CACHE_ROOT"
exec "$real_bin" mcp
EOF
  chmod +x "$path"
}

latest_version() {
  python3 - <<'PY' "$TAGMEM_RELEASES_URL"
import json, sys, urllib.request
with urllib.request.urlopen(sys.argv[1]) as resp:
    data = json.load(resp)
print(data['tag_name'].lstrip('v'))
PY
}

download_release_binary() {
  local os="$1" arch="$2" install_root="$3"
  local version asset url tmpdir target
  require_command curl
  require_command tar
  version="$(latest_version)"
  asset="tagmem_${version}_${os}_${arch}.tar.gz"
  url="https://github.com/codysnider/tagmem/releases/download/v${version}/${asset}"
  tmpdir="$(mktemp -d)"
  target="$install_root/bin/tagmem"
  curl -fsSL "$url" -o "$tmpdir/$asset"
  tar -C "$install_root/bin" -xzf "$tmpdir/$asset"
  rm -rf "$tmpdir"
  if [[ ! -x "$target" ]]; then
    printf 'Extracted release binary not found: %s\n' "$target" >&2
    return 1
  fi
}

validate_doctor_output() {
  local subject="$1" output="$2"
  if grep -q 'embedded hash fallback' <<<"$output"; then
    printf '%s\n' "$output" >&2
    printf '%s validation failed: embedded hash fallback is not supported for installer installs.\n' "$subject" >&2
    return 1
  fi
}

validate_release_binary() {
  local real_bin="$1" data_root="$2" config_root="$3" cache_root="$4"
  local output
  printf 'Validating release binary...\n'
  if ! output="$(TAGMEM_DATA_ROOT="$data_root" TAGMEM_CONFIG_ROOT="$config_root" TAGMEM_CACHE_ROOT="$cache_root" "$real_bin" doctor 2>&1)"; then
    printf '%s\n' "$output" >&2
    return 1
  fi
  validate_doctor_output "Release binary" "$output"
}

patch_opencode() {
  local mcp_wrapper="$1"
  local cfg created=0 opencode_dir remember_url remember_compact_url
  local default_global_linux="$HOME/.config/opencode/opencode.json"
  local default_global_macos="$HOME/Library/Application Support/opencode/opencode.json"
  remember_url="$TAGMEM_RAW_BASE/assets/opencode/commands/remember.md"
  remember_compact_url="$TAGMEM_RAW_BASE/assets/opencode/commands/remember-compact.md"

  if ! command -v opencode >/dev/null 2>&1; then
    printf 'OpenCode binary not found on PATH.\n'
    printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
    return 0
  fi

  if [[ -n "${OPENCODE_CONFIG:-}" ]]; then
    cfg="$OPENCODE_CONFIG"
  elif [[ "$(detect_os)" == "darwin" ]]; then
    cfg="$default_global_macos"
  else
    cfg="$default_global_linux"
  fi

  printf 'OpenCode detected: %s\n' "$(command -v opencode)"
  printf 'Target OpenCode config: %s\n' "$cfg"

  if ! command -v jq >/dev/null 2>&1; then
    printf 'jq not found; skipping OpenCode patch. Use this MCP command if needed: %s\n' "$mcp_wrapper"
    return 0
  fi

  opencode_dir="$(dirname "$cfg")"
  if ! mkdir -p "$opencode_dir"; then
    printf 'OpenCode config directory is not writable: %s\n' "$opencode_dir"
    printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
    return 0
  fi

  if [[ -f "$cfg" ]]; then
    if [[ ! -r "$cfg" || ! -w "$cfg" ]]; then
      printf 'OpenCode config is not readable and writable: %s\n' "$cfg"
      printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
      return 0
    fi
    if ! jq empty "$cfg" >/dev/null 2>&1; then
      printf 'OpenCode config is not valid JSON: %s\n' "$cfg" >&2
      printf 'Manual MCP command: %s\n' "$mcp_wrapper" >&2
      return 0
    fi
  elif [[ ! -w "$opencode_dir" ]]; then
    printf 'OpenCode config directory is not writable: %s\n' "$opencode_dir"
    printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
    return 0
  fi

  case "$TAGMEM_PATCH_OPENCODE" in
    no)
      printf 'Skipping OpenCode patch. Use this MCP command if needed: %s\n' "$mcp_wrapper"
      return 0
      ;;
    ask)
      if [[ "$TAGMEM_YES" == "1" ]]; then
        printf 'Skipping OpenCode patch during non-interactive install. Use --patch-opencode to enable it.\n'
        printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
        return 0
      fi
      if ! ask "Patch OpenCode config?" yes; then
        printf 'Skipping OpenCode patch. Use this MCP command if needed: %s\n' "$mcp_wrapper"
        return 0
      fi
      ;;
  esac

  if [[ ! -f "$cfg" ]]; then
    printf '{\n  "mcp": {}\n}\n' > "$cfg"
    created=1
    printf 'Created new OpenCode config: %s\n' "$cfg"
  fi

  if [[ "$created" == "0" ]]; then
    backup_file "$cfg"
  fi

  local tmp
  tmp="$(mktemp)"
  jq --arg wrapper "$mcp_wrapper" '.mcp = (.mcp // {}) | .mcp.tagmem = {type:"local", command:[$wrapper], enabled:true, timeout:20000}' "$cfg" > "$tmp"
  mv "$tmp" "$cfg"
  printf 'Patched OpenCode config: %s\n' "$cfg"

  mkdir -p "$opencode_dir/commands"
  curl -fsSL "$remember_url" -o "$opencode_dir/commands/remember.md"
  curl -fsSL "$remember_compact_url" -o "$opencode_dir/commands/remember-compact.md"
  printf 'Installed OpenCode commands: remember, remember-compact\n'
}

main() {
  local os arch data_root config_root cache_root bin_dir install_root backend real_bin docker_ok=0 default_accel=auto
  require_command python3
  require_command grep
  os="$(detect_os)"
  arch="$(detect_arch)"
  data_root="${TAGMEM_DATA_ROOT:-$(default_data_root)}"
  config_root="${TAGMEM_CONFIG_ROOT:-$(default_config_root)}"
  cache_root="${TAGMEM_CACHE_ROOT:-$(default_cache_root)}"
  bin_dir="${TAGMEM_BIN_DIR:-$(detect_bin_dir)}"
  install_root="$data_root/install"

  mkdir -p "$data_root" "$config_root" "$cache_root" "$bin_dir" "$install_root/bin"

  if command -v docker >/dev/null 2>&1; then
    docker_ok=1
  fi

  printf 'Detected environment\n'
  printf '  OS:            %s\n' "$os"
  printf '  Architecture:  %s\n' "$arch"
  printf '  Docker:        %s\n' "$( [[ "$docker_ok" == 1 ]] && printf found || printf missing )"
  printf '  Data root:     %s\n' "$data_root"
  printf '  Config root:   %s\n' "$config_root"
  printf '  Cache root:    %s\n' "$cache_root"
  printf '  Bin dir:       %s\n' "$bin_dir"
  printf '  Dry run:       %s\n' "$( [[ "$TAGMEM_DRY_RUN" == 1 ]] && printf yes || printf no )"

  if [[ "$docker_ok" == 1 ]]; then
    backend="docker"
    printf '  Install mode:  docker image\n'
  else
    backend="binary"
    printf '  Install mode:  release tarball\n'
  fi

  if ! ask "Proceed with installation?" yes; then
    printf 'Installation cancelled.\n'
    exit 0
  fi

  if [[ "$TAGMEM_DRY_RUN" == "1" ]]; then
    printf 'Dry run complete. No changes were made.\n'
    exit 0
  fi

  if [[ "$backend" == "docker" ]]; then
    require_command docker
    printf 'Pulling %s\n' "$TAGMEM_IMAGE_REF"
    docker pull "$TAGMEM_IMAGE_REF"
    printf 'Running Docker smoke test...\n'
    if [[ "${TAGMEM_DOCKER_GPU:-auto}" == "on" ]] || ([[ "${TAGMEM_DOCKER_GPU:-auto}" == "auto" ]] && command -v nvidia-smi >/dev/null 2>&1); then
      if ! probe_output="$(docker run --rm --gpus all -e TAGMEM_EMBED_PROVIDER=embedded -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" -e TAGMEM_EMBED_ACCEL=auto "$TAGMEM_IMAGE_REF" doctor 2>&1)"; then
        printf '%s\n' "$probe_output" >&2
        exit 1
      fi
      if ! validate_doctor_output "Docker image" "$probe_output"; then
        exit 1
      fi
      if grep -q 'device:[[:space:]]*cuda' <<<"$probe_output"; then
        default_accel=auto
        printf '  Docker GPU probe: usable\n'
      else
        default_accel=cpu
        printf '  Docker GPU probe: failed, falling back to CPU wrappers\n'
      fi
    else
      default_accel=cpu
      if ! probe_output="$(docker run --rm -e TAGMEM_EMBED_PROVIDER=embedded -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}" -e TAGMEM_EMBED_ACCEL=cpu "$TAGMEM_IMAGE_REF" doctor 2>&1)"; then
        printf '%s\n' "$probe_output" >&2
        exit 1
      fi
      if ! validate_doctor_output "Docker image" "$probe_output"; then
        exit 1
      fi
      printf '  Docker GPU probe: skipped, using CPU wrappers\n'
    fi
    write_docker_wrapper "$bin_dir/tagmem" "$data_root" "$config_root" "$cache_root" "$default_accel"
    write_docker_mcp_wrapper "$bin_dir/tagmem-mcp" "$data_root" "$config_root" "$cache_root" "$default_accel"
  else
    if ! supports_release_binary "$os" "$arch"; then
      printf 'Release binary fallback is currently supported only on linux/amd64.\n' >&2
      printf 'Use Docker on %s/%s or wait for native ONNX support on this platform.\n' "$os" "$arch" >&2
      exit 1
    fi
    printf 'Downloading release binary for %s/%s\n' "$os" "$arch"
    download_release_binary "$os" "$arch" "$install_root"
    real_bin="$install_root/bin/tagmem"
    validate_release_binary "$real_bin" "$data_root" "$config_root" "$cache_root"
    write_binary_wrapper "$bin_dir/tagmem" "$real_bin" "$data_root" "$config_root" "$cache_root"
    write_binary_mcp_wrapper "$bin_dir/tagmem-mcp" "$real_bin" "$data_root" "$config_root" "$cache_root"
  fi

  patch_opencode "$bin_dir/tagmem-mcp"

  printf '\nInstalled tagmem via %s\n' "$backend"
  printf '  tagmem:     %s\n' "$bin_dir/tagmem"
  printf '  tagmem-mcp: %s\n' "$bin_dir/tagmem-mcp"
  if path_contains "$bin_dir"; then
    printf '\n%s is already on your PATH.\n' "$bin_dir"
  else
    printf '\n%s is not on your PATH. Add it and restart your shell.\n' "$bin_dir"
    printf 'Suggested line:\n'
    printf '  export PATH="%s:$PATH"\n' "$bin_dir"
  fi
}

main "$@"
