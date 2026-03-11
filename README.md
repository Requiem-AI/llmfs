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
- User settings loader: `internal/appcfg`
- CI/CD workflows: `.github/workflows`

## Prerequisites

- Go 1.22+
- FUSE support installed on the host OS
- Permissions to mount FUSE filesystems

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
- `.llmfs/skip_dirs.txt`

Tracked examples for PRs/docs live in [`examples/`](./examples):

- `examples/settings.example.json`
- `examples/codec-config.tce2.example.json`
- `examples/codec-config.tce1.example.json`

### `.llmfs/settings.json`

Example:

```json
{
  "apply_to_all_files": true,
  "skip_dirs": [".git", ".llmfs", "node_modules"],
  "candidates": ["func ", "return ", "if ", " := "],
  "middlewares": [
    { "name": "deny_env_dotfiles", "enabled": true },
    { "name": "redirect_env_to_agent", "enabled": true },
    { "name": "codec", "enabled": true },
    { "name": "deny_binary", "enabled": false }
  ],
  "skip_dirs_file": ".llmfs/skip_dirs.txt",
  "candidates_file": ".llmfs/candidates.txt"
}
```

Behavior notes:

- `apply_to_all_files: true` means mount-time transform applies to all files, regardless of extension
- `skip_dirs` controls directories excluded from dictionary exploration
- `candidates` is the base candidate list used for dictionary scoring
- `middlewares` defines ordered modules for read/write transform in FUSE:
  - `serve` path runs in listed order
  - `commit` path runs in reverse order
  - `deny_env_dotfiles` blocks reads of filenames matching `.env.*`
  - `redirect_env_to_agent` serves `.env.agent` when `.env` is read
  - `codec` middleware now owns encode/decode behavior
  - `deny_binary` rejects content containing NUL bytes (optional check)
- `*_file` values allow external editable lists (default paths shown above)

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
