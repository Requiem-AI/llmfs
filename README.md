# llmfs

`llmfs` is a Go CLI that mounts an encoded view of a repository via FUSE so LLM-facing file I/O can use a token-optimized transport format instead of raw source text.

## Why this exists

LLMs are priced and limited by tokens, not bytes. `llmfs` tries to reduce token usage for repetitive code/text by replacing common patterns with compact symbols and decoding back to the original bytes on write.

## Features

- Token-aware dictionary exploration against a target tokenizer
- Reversible transport codecs:
  - `tce1`: human-readable escape+code format
  - `tce2`: compact symbol-based format (default)
- FUSE mount that exposes encoded file content and decodes writes back to raw files
- Auto-generated LLM instructions file with dictionary mapping
- Config-driven behavior for candidate patterns and skipped directories
- Ordered middleware pipeline for pre-serve checks/transforms
- Linux build CI + automatic versioned release workflow on `main`

## Repository layout

- CLI entry: `cmd/llmfs/main.go`
- Codec engine: `internal/codec`
- Dictionary explorer: `internal/explorer`
- FUSE mount implementation: `internal/mountfs`
- Middleware implementations: `internal/middleware/<name>`
- Middleware pipeline engine: `internal/transform`
- User settings loader: `internal/appcfg`
- CI/CD workflows: `.github/workflows`

## Prerequisites

- Go 1.22+
- FUSE support installed on the host OS
- Permissions to mount FUSE filesystems

## One-line install

```bash
curl -fsSL https://raw.githubusercontent.com/Requiem-AI/llmfs/main/install.sh | bash
```

Notes:

- Installer currently supports Linux `x86_64` (matches release artifact)
- Default install path is `/usr/local/bin` if writable, otherwise `~/.local/bin`
- Override install path with `INSTALL_DIR=/your/bin/path`

## Quick start

```bash
# Build
go build ./cmd/llmfs

# Simplest: run with sane defaults
# - root: current directory
# - mountpoint: .llmfs/mount
# - auto-creates .llmfs settings/config/instructions if missing
./llmfs
```

Use `.llmfs/mount` (or your chosen mountpoint) as the path you expose to your LLM tooling.

## CLI commands

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

### Typical flow

```bash
# Optional: inspect expected token savings
./llmfs explore --root .

# Optional: explicitly generate transport config + prompt instructions
./llmfs init --root .

# Start mount
./llmfs run --root . --mountpoint /tmp/repo-encoded
```

## Configuration

`llmfs` is settings-driven. On first run it creates:

- `.llmfs/settings.json`
- `.llmfs/candidates.txt`

Tracked examples for PRs/docs live in [`examples/`](./examples):

- `examples/settings.example.json`
- `examples/codec-config.tce2.example.json`
- `examples/codec-config.tce1.example.json`

### `.llmfs/settings.json`

Example:

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

- `apply_to_all_files: true` means mount-time transform applies to all files, regardless of extension
- `skip_paths` supports gitignore-like path patterns for both files and directories (`name`, `dir/`, `*.ext`, `path/to/file`, and optional `!` negate)
- dictionary candidates are sourced from `candidates_file` (`.llmfs/candidates.txt` by default)
- `middlewares` defines ordered modules for read/write transform in FUSE:
  - `serve` path runs in listed order
  - `commit` path runs in reverse order
  - `deny_env_dotfiles` blocks reads of filenames matching `.env.*`
  - `redirect_env_to_agent` serves `.env.agent` when `.env` is read
  - `codec` middleware now owns encode/decode behavior
  - `deny_binary` rejects content containing NUL bytes (optional check)
- `candidates_file` allows external editable candidate list (default path shown above)

Verbose scan output:

- Add `-v` to `explore`, `init`, `mount`, or `run` to print each scanned file path as dictionary defaults are generated.

## Middleware Layer

The middleware layer is the transformation and policy boundary between raw repository bytes and what appears in the mounted filesystem.

Execution model:

- Read path (`serve` stage): raw file bytes are passed through middlewares in listed order.
- Write path (`commit` stage): user-edited bytes are passed through the same middlewares in reverse order before writing to disk.
- A middleware can:
  - transform content (`Result.Content`)
  - allow or deny the operation (`Result.Allowed`)
  - return an explicit rejection message (`Result.Message`)

Current built-in middlewares:

- `deny_env_dotfiles`: blocks reads of `.env.*` files
- `redirect_env_to_agent`: when reading `.env`, serves `.env.agent` instead
- `codec`: encodes on `serve`, decodes on `commit`
- `deny_binary`: rejects NUL-byte content (optional)

Why ordering matters:

- Security/policy checks should generally run before content transforms.
- Invertible transforms (like `codec`) should usually be late in `serve`, so their inverse runs early in `commit`.
- Since `commit` reverses order, think in terms of a forward read pipeline and a mirrored write pipeline.

Error behavior:

- Middleware rejections map to permission-like failures for the mount client.
- Internal middleware errors map to invalid-operation style failures.

For contributor instructions on adding new middleware, see [internal/middleware/README.md](./internal/middleware/README.md).

## Transport files

- `.llmfs/config.json`: generated codec config (version, escape symbol, dictionary)
- `.llmfs/INIT_INSTRUCTIONS.md`: generated instructions to paste into LLM system/init prompt

## Format overview

### `tce1`

- Escape-based, human-readable mapping
- Easier to inspect manually
- Usually lower compression than `tce2`

### `tce2` (default)

- Direct symbol substitution with escape handling
- Optimized for token efficiency, not readability
- Better default choice for production token savings

## Safety and operational caveats

- FUSE mount is a transformed view of your real files; writes decode back to underlying files
- Keep backups/normal VCS hygiene when introducing mount-based workflows
- If your environment lacks FUSE tooling, mount commands will fail
- Dictionary exploration currently analyzes UTF-8 files for tokenizer scoring

## CI/CD

### Build workflow

`.github/workflows/build-linux.yml`

- Runs on pull requests and pushes to `main`
- Executes tests
- Builds Linux AMD64 binary
- Packages release tarball containing:
  - `llmfs` binary
  - `README.md`
  - `examples/` config templates
- Uploads `.tar.gz` artifact

### Release workflow

`.github/workflows/release-main.yml`

- Runs on pushes to `main`
- Computes next patch semver tag (`vX.Y.Z`)
- Builds Linux AMD64 binary
- Packages `.tar.gz` bundle (binary + examples)
- Creates tag and GitHub Release
- Uses generated release notes
- Attaches release tarball asset + checksum (`.sha256`)

## Development

```bash
go test ./...
go build ./cmd/llmfs
```

## License

Add a license file (`LICENSE`) before broad public distribution.
