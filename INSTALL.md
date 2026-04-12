# Install

`tagmem` supports a Docker-first installation path.

The installer is interactive by default and will:

- detect your operating system and architecture
- check whether Docker is available
- choose Docker first when possible
- fall back to a native linux/amd64 release binary tarball when Docker is unavailable
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

## What gets installed

The installer creates:

- `tagmem`
- `tagmem-mcp`

in a local bin directory, typically:

- `~/.local/bin`
  or
- `~/bin`

## Install modes

### Docker-first

If Docker is available, the installer will:

1. pull `ghcr.io/codysnider/tagmem:latest`
2. probe whether the Docker runtime can use the embedded model successfully
3. use GPU-backed wrappers when the probe succeeds
4. fall back to CPU-safe wrappers when the probe fails
5. install a `tagmem` wrapper
6. install a `tagmem-mcp` wrapper

### Release binary fallback

If Docker is not available on linux/amd64, the installer will:

1. detect the current OS and architecture
2. download the matching release tarball
3. extract the binary into the local install root
4. install `tagmem` and `tagmem-mcp` wrapper scripts

On other platforms, the installer requires Docker until native ONNX binaries are available.

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

- `TAGMEM_IMAGE_REF`
- `TAGMEM_RELEASES_URL`
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
