# llmfs Deep Dive

This document keeps the detailed operational and implementation info that used to live in the top-level README.

## Why this exists

LLMs are priced and limited by tokens, not bytes. `llmfs` reduces token usage for repetitive code/text by replacing common patterns with compact symbols and decoding back to original bytes on write.

## Features

- Token-aware dictionary exploration against a target tokenizer
- Reversible transport codecs:
  - `tce1`: human-readable escape+code format
  - `tce2`: compact symbol-based format (default)
- FUSE mount exposing encoded file content and decoding writes back to raw files
- Auto-generated LLM instructions file with dictionary mapping
- Config-driven behavior for candidate patterns and skipped directories
- Ordered middleware pipeline for pre-serve checks/transforms

## Repository layout

- CLI entry: `cmd/llmfs/main.go`
- Codec engine: `internal/codec`
- Dictionary explorer: `internal/explorer`
- FUSE mount implementation: `internal/mountfs`
- Middleware implementations: `internal/middleware/<name>`
- Middleware pipeline engine: `internal/transform`
- User settings loader: `internal/appcfg`
- CI/CD workflows: `.github/workflows`

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Requiem-AI/llmfs/main/install.sh | bash
```

Notes:

- Installer currently supports Linux `x86_64` (matches release artifact)
- Default install path is `/usr/local/bin` if writable, otherwise `~/.local/bin`
- Override install path with `INSTALL_DIR=/your/bin/path`

## Commands

```bash
llmfs                             # same as: llmfs run --mountpoint .llmfs/mount
llmfs explore [--root DIR] [--settings PATH]
llmfs init [--root DIR] [--config PATH] [--instructions PATH] [--settings PATH]
llmfs mount [--mountpoint DIR] [--root DIR] [--config PATH] [--settings PATH]
llmfs run [--mountpoint DIR] [--root DIR] [--config PATH] [--settings PATH]
llmfs encode [--config PATH] [--in FILE] [--out FILE]
llmfs decode [--config PATH] [--in FILE] [--out FILE]
llmfs version
```

Typical flow:

```bash
./llmfs explore --root .
./llmfs init --root .
./llmfs run --root . --mountpoint /tmp/repo-encoded
```

## Configuration

On first run, `llmfs` creates:

- `.llmfs/settings.json`
- `.llmfs/candidates.txt`

Tracked examples:

- `examples/settings.example.json`
- `examples/codec-config.tce2.example.json`
- `examples/codec-config.tce1.example.json`

Example `.llmfs/settings.json`:

```json
{
  "apply_to_all_files": true,
  "skip_paths": [
    ".git",
    ".llmfs",
    "node_modules",
    "vendor",
    "dist",
    "build",
    "bin",
    "out",
    "coverage",
    ".idea",
    ".vscode",
    ".venv",
    "venv",
    "target",
    ".next",
    ".turbo",
    "*.lock",
    "*.min.js",
    "*.map",
    "*.svg",
    "*.png",
    "*.jpg",
    "*.jpeg",
    "*.webp",
    "*.pdf"
  ],
  "middlewares": [
    { "name": "deny_env_dotfiles", "enabled": true },
    { "name": "redirect_env_to_agent", "enabled": true },
    { "name": "codec", "enabled": true },
    { "name": "deny_binary", "enabled": false }
  ],
  "candidates_file": ".llmfs/candidates.txt"
}
```

Behavior notes:

- `apply_to_all_files: true` applies transform to all files, regardless of extension
- `skip_paths` supports gitignore-like path patterns for files and directories (`name`, `dir/`, `*.ext`, `path/to/file`, optional `!` negate)
- dictionary candidates come from `candidates_file` (`.llmfs/candidates.txt` by default)
- `middlewares` defines ordered read/write modules
- add `-v` to `explore`, `init`, `mount`, or `run` to print scanned file paths

## Middleware layer details

The middleware layer is the transformation and policy boundary between raw repository bytes and what appears in the mounted filesystem.

- Read path (`serve`): raw bytes go through middlewares in listed order
- Write path (`commit`): edited bytes go through middlewares in reverse order
- Middleware can:
  - transform content (`Result.Content`)
  - allow/deny operation (`Result.Allowed`)
  - return rejection message (`Result.Message`)

Current built-ins:

- `deny_env_dotfiles`: blocks reads of `.env.*`
- `redirect_env_to_agent`: serves `.env.agent` when `.env` is read
- `codec`: encodes on `serve`, decodes on `commit`
- `deny_binary`: rejects NUL-byte content (optional)

Ordering guidance:

- put policy checks before content transforms
- put invertible transforms late in `serve` so inverse runs early in `commit`

Error behavior:

- middleware rejections map to permission-like mount failures
- middleware runtime errors map to invalid-operation style failures

Contributor docs:

- middleware development: [`internal/middleware/README.md`](../internal/middleware/README.md)
- transform runtime: [`internal/transform/README.md`](../internal/transform/README.md)

## Transport files

- `.llmfs/config.json`: generated codec config (version, escape symbol, dictionary)
- `.llmfs/INIT_INSTRUCTIONS.md`: generated instructions for LLM system/init prompt

## Format overview

### `tce1`

- Escape-based, human-readable mapping
- Easier to inspect manually
- Usually lower compression than `tce2`

### `tce2` (default)

- Direct symbol substitution with escape handling
- Optimized for token efficiency
- Better default choice for production token savings

## Safety and operations

- FUSE mount is a transformed view of real files; writes decode back to source files
- Keep normal VCS hygiene and backups when adopting mount-based workflows
- Mount commands fail if FUSE tooling is missing
- Dictionary exploration currently analyzes UTF-8 files for tokenizer scoring

## CI/CD

Build workflow: `.github/workflows/build-linux.yml`

- Runs on PRs and pushes to `main`
- Executes tests
- Builds Linux AMD64 binary
- Packages tarball with binary + docs/examples

Release workflow: `.github/workflows/release-main.yml`

- Runs on pushes to `main`
- Computes next patch semver tag (`vX.Y.Z`)
- Builds Linux AMD64 release bundle
- Creates tag + GitHub Release
- Uploads tarball and checksum (`.sha256`)

## Development

```bash
go test ./...
go build ./cmd/llmfs
```
