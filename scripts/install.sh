#!/usr/bin/env bash
set -euo pipefail

TAGMEM_CPU_IMAGE_REF="${TAGMEM_CPU_IMAGE_REF:-ghcr.io/codysnider/tagmem:latest-cpu}"
TAGMEM_GPU_IMAGE_REF="${TAGMEM_GPU_IMAGE_REF:-ghcr.io/codysnider/tagmem:latest-gpu}"
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

render_with_python() {
  local path="$1"
  local template="$2"
  local data_root="$3"
  local config_root="$4"
  local cache_root="$5"
  local image_ref="$6"
  local default_accel="$7"
  python3 - <<'PY' "$path" "$template" "$data_root" "$config_root" "$cache_root" "$image_ref" "$default_accel"
from pathlib import Path
import sys
path, template, data_root, config_root, cache_root, image_ref, default_accel = sys.argv[1:8]
text = template.replace('@DATA_ROOT@', data_root)
text = text.replace('@CONFIG_ROOT@', config_root)
text = text.replace('@CACHE_ROOT@', cache_root)
text = text.replace('@IMAGE_REF@', image_ref)
text = text.replace('@DEFAULT_ACCEL@', default_accel)
Path(path).write_text(text)
PY
  chmod +x "$path"
}

write_docker_wrapper() {
  local path="$1" image_ref="$2" gpu_mode="$3" data_root="$4" config_root="$5" cache_root="$6" default_accel="$7"
  local template
  if [[ "$gpu_mode" == "gpu" ]]; then
    template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
exec docker run --rm \
  --gpus all \
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
  else
    template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
exec docker run --rm \
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
  fi
  render_with_python "$path" "$template" "$data_root" "$config_root" "$cache_root" "$image_ref" "$default_accel"
}

write_docker_mcp_wrapper() {
  local path="$1" image_ref="$2" gpu_mode="$3" data_root="$4" config_root="$5" cache_root="$6" default_accel="$7"
  local template
  if [[ "$gpu_mode" == "gpu" ]]; then
    template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
exec docker run -i --rm --init \
  --gpus all \
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
  else
    template=$(cat <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
DATA_ROOT="${TAGMEM_DATA_ROOT:-@DATA_ROOT@}"
CONFIG_ROOT="${TAGMEM_CONFIG_ROOT:-@CONFIG_ROOT@}"
CACHE_ROOT="${TAGMEM_CACHE_ROOT:-@CACHE_ROOT@}"
IMAGE_REF="${TAGMEM_IMAGE_REF:-@IMAGE_REF@}"
mkdir -p "$DATA_ROOT" "$CONFIG_ROOT" "$CACHE_ROOT"
exec docker run -i --rm --init \
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
  fi
  render_with_python "$path" "$template" "$data_root" "$config_root" "$cache_root" "$image_ref" "$default_accel"
}

validate_doctor_output() {
  local subject="$1" output="$2"
  if grep -q 'embedded hash fallback' <<<"$output"; then
    printf '%s\n' "$output" >&2
    printf '%s validation failed: embedded hash fallback is not supported for installer installs.\n' "$subject" >&2
    return 1
  fi
}

validate_docker_image() {
  local subject="$1" image_ref="$2" accel="$3" gpu_mode="$4"
  local output
  local cmd=(docker run --rm)
  if [[ "$gpu_mode" == "gpu" ]]; then
    cmd+=(--gpus all)
  fi
  cmd+=(
    -e TAGMEM_EMBED_PROVIDER=embedded
    -e TAGMEM_EMBED_MODEL="${TAGMEM_EMBED_MODEL:-bge-small-en-v1.5}"
    -e TAGMEM_EMBED_ACCEL="$accel"
    "$image_ref"
    doctor
  )
  if ! output="$("${cmd[@]}" 2>&1)"; then
    printf '%s\n' "$output" >&2
    return 1
  fi
  if ! validate_doctor_output "$subject" "$output"; then
    return 1
  fi
  if [[ "$gpu_mode" == "gpu" ]] && ! grep -q 'device:[[:space:]]*cuda' <<<"$output"; then
    return 1
  fi
}

ensure_docker_image() {
  local image_ref="$1"
  if docker image inspect "$image_ref" >/dev/null 2>&1; then
    printf 'Using local image %s\n' "$image_ref"
    return 0
  fi
  printf 'Pulling %s\n' "$image_ref"
  docker pull "$image_ref"
}

patch_opencode() {
  local mcp_wrapper="$1"
  local cfg created=0 opencode_dir remember_url remember_compact_url config_dir
  local default_config_path="$HOME/.config/opencode/opencode.json"
  remember_url="$TAGMEM_RAW_BASE/assets/opencode/commands/remember.md"
  remember_compact_url="$TAGMEM_RAW_BASE/assets/opencode/commands/remember-compact.md"

  if ! command -v opencode >/dev/null 2>&1; then
    printf 'OpenCode binary not found on PATH.\n'
    printf 'Use this MCP command if needed: %s\n' "$mcp_wrapper"
    return 0
  fi

  if [[ -n "${OPENCODE_CONFIG:-}" ]]; then
    cfg="$OPENCODE_CONFIG"
  elif config_dir="$(opencode debug paths 2>/dev/null | awk '$1 == "config" { print $2 }')" && [[ -n "$config_dir" ]]; then
    cfg="$config_dir/opencode.json"
  else
    cfg="$default_config_path"
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
  local os arch data_root config_root cache_root bin_dir docker_ok=0 selected_image_ref selected_accel selected_mode
  require_command python3
  require_command grep
  os="$(detect_os)"
  arch="$(detect_arch)"
  data_root="${TAGMEM_DATA_ROOT:-$(default_data_root)}"
  config_root="${TAGMEM_CONFIG_ROOT:-$(default_config_root)}"
  cache_root="${TAGMEM_CACHE_ROOT:-$(default_cache_root)}"
  bin_dir="${TAGMEM_BIN_DIR:-$(detect_bin_dir)}"

  mkdir -p "$data_root" "$config_root" "$cache_root" "$bin_dir"

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
  printf '  CPU image:     %s\n' "$TAGMEM_CPU_IMAGE_REF"
  printf '  GPU image:     %s\n' "$TAGMEM_GPU_IMAGE_REF"
  printf '  Dry run:       %s\n' "$( [[ "$TAGMEM_DRY_RUN" == 1 ]] && printf yes || printf no )"
  printf '  Install mode:  docker image\n'

  if ! ask "Proceed with installation?" yes; then
    printf 'Installation cancelled.\n'
    exit 0
  fi

  if [[ "$TAGMEM_DRY_RUN" == "1" ]]; then
    printf 'Dry run complete. No changes were made.\n'
    exit 0
  fi

  if [[ "$docker_ok" != 1 ]]; then
    printf 'Docker is required for the installer.\n' >&2
    printf 'Use Docker on %s/%s, or build tagmem from source manually on linux/amd64.\n' "$os" "$arch" >&2
    exit 1
  fi

  require_command docker
  selected_image_ref="$TAGMEM_CPU_IMAGE_REF"
  selected_accel="cpu"
  selected_mode="cpu"

  if command -v nvidia-smi >/dev/null 2>&1; then
    if ensure_docker_image "$TAGMEM_GPU_IMAGE_REF" >/dev/null 2>&1; then
      printf 'Running Docker GPU smoke test...\n'
      if validate_docker_image "Docker GPU image" "$TAGMEM_GPU_IMAGE_REF" cuda gpu; then
        selected_image_ref="$TAGMEM_GPU_IMAGE_REF"
        selected_accel="cuda"
        selected_mode="gpu"
        printf '  Docker GPU probe: usable\n'
      else
        printf '  Docker GPU probe: failed, falling back to CPU image\n'
      fi
    else
      printf '  Docker GPU probe: could not pull GPU image, falling back to CPU image\n'
    fi
  else
    printf '  Docker GPU probe: skipped, no NVIDIA GPU detected\n'
  fi

  if [[ "$selected_mode" == "cpu" ]]; then
    ensure_docker_image "$TAGMEM_CPU_IMAGE_REF"
    printf 'Running Docker CPU smoke test...\n'
    if ! validate_docker_image "Docker CPU image" "$TAGMEM_CPU_IMAGE_REF" cpu cpu; then
      printf 'Docker CPU image validation failed.\n' >&2
      exit 1
    fi
  fi

  write_docker_wrapper "$bin_dir/tagmem" "$selected_image_ref" "$selected_mode" "$data_root" "$config_root" "$cache_root" "$selected_accel"
  write_docker_mcp_wrapper "$bin_dir/tagmem-mcp" "$selected_image_ref" "$selected_mode" "$data_root" "$config_root" "$cache_root" "$selected_accel"

  patch_opencode "$bin_dir/tagmem-mcp"

  printf '\nInstalled tagmem via docker (%s image)\n' "$selected_mode"
  printf '  tagmem:     %s\n' "$bin_dir/tagmem"
  printf '  tagmem-mcp: %s\n' "$bin_dir/tagmem-mcp"
  printf '  image:      %s\n' "$selected_image_ref"
  if path_contains "$bin_dir"; then
    printf '\n%s is already on your PATH.\n' "$bin_dir"
  else
    printf '\n%s is not on your PATH. Add it and restart your shell.\n' "$bin_dir"
    printf 'Suggested line:\n'
    printf '  export PATH="%s:$PATH"\n' "$bin_dir"
  fi
}

main "$@"
