# Install

`tagmem` currently installs through Docker.

The installer is interactive by default and will:

- detect your operating system and architecture
- require Docker for the simple install path
- detect whether the GPU image is usable on NVIDIA hosts
- fall back to the CPU image when GPU validation fails
- install local wrapper commands
- detect and optionally patch OpenCode configuration
- create a backup before modifying any config file

## One-line install

```bash
curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash
```

## Non-interactive install

```bash
curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash -s -- --yes
```

## First use

After installation, just use `tagmem`.

Local storage is created automatically on first use. `tagmem init` is available as an optional bootstrap command if you want to precreate storage and print the resolved paths.

Published images:

- `ghcr.io/codysnider/tagmem:latest-cpu`
- `ghcr.io/codysnider/tagmem:latest-gpu`

## What gets installed

The installer creates:

- `tagmem`
- `tagmem-mcp`

in a local bin directory, typically:

- `~/.local/bin`
  or
- `~/bin`

## Install modes

### Docker CPU image

If GPU validation is skipped or fails, the installer will:

1. pull `ghcr.io/codysnider/tagmem:latest-cpu`
2. validate that the embedded model works on CPU
3. install a `tagmem` wrapper
4. install a `tagmem-mcp` wrapper

### Docker GPU image

If an NVIDIA GPU is detected and the GPU image validates successfully, the installer will:

1. pull `ghcr.io/codysnider/tagmem:latest-gpu`
2. validate that the embedded model runs on CUDA
3. install a `tagmem` wrapper
4. install a `tagmem-mcp` wrapper

## Manual source build

If you want a non-Docker setup, build from source manually.

Current native ONNX source-build support is `linux/amd64` and `linux/arm64` for CPU, with CUDA support limited to `linux/amd64`.

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags tagmem_onnx -o tagmem ./cmd/tagmem
```

Source builds are an advanced path. The supported simple install path is Docker.

## Optional OpenCode setup

If OpenCode is detected during an interactive install and its config is readable and writable, the installer can offer to patch it.

When patching OpenCode, the installer will:

- detect the `opencode` binary on `PATH`
- choose the documented global config path (or `OPENCODE_CONFIG` if set)
- create a new config file if one does not already exist
- validate existing JSON with `jq`
- ask before patching during interactive installs
- back up the file first when modifying an existing config
- add or update the `tagmem` MCP entry

If `jq` is unavailable, the config is invalid, or the config path is not patchable, the installer will stop short of patching and print the MCP command path you can use manually.

## Environment variables

Optional installer overrides:

- `TAGMEM_CPU_IMAGE_REF`
- `TAGMEM_GPU_IMAGE_REF`
- `TAGMEM_DATA_ROOT`
- `TAGMEM_CONFIG_ROOT`
- `TAGMEM_CACHE_ROOT`
- `TAGMEM_BIN_DIR`

## Examples

### Force OpenCode patching

```bash
curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash -s -- --patch-opencode
```

### Skip OpenCode patching

```bash
curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash -s -- --no-patch-opencode
```

### Override data root

```bash
TAGMEM_DATA_ROOT=/path/to/tagmem-data curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash
```

### Override CPU image ref

```bash
TAGMEM_CPU_IMAGE_REF=ghcr.io/codysnider/tagmem:latest-cpu curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash
```

### Override GPU image ref

```bash
TAGMEM_GPU_IMAGE_REF=ghcr.io/codysnider/tagmem:latest-gpu curl -fsSL https://raw.githubusercontent.com/codysnider/tagmem/main/scripts/install.sh | bash
```
