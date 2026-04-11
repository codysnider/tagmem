# Install

`tagmem` supports a Docker-first installation path with a release-binary fallback.

The installer is interactive by default and will:

- detect your operating system and architecture
- check whether Docker is available
- choose Docker first when possible
- fall back to a release binary tarball when Docker is unavailable
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
2. run a smoke test with `help`
3. install a `tagmem` wrapper
4. install a `tagmem-mcp` wrapper

### Release binary fallback

If Docker is not available, the installer will:

1. detect the current OS and architecture
2. download the matching release tarball
3. extract the binary into the local install root
4. install `tagmem` and `tagmem-mcp` wrapper scripts

## OpenCode patching

If an OpenCode config file is detected, the installer will:

- show the path
- ask before patching
- back up the file first
- add or update the `tagmem` MCP entry

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
