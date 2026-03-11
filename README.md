# llmfs

`llmfs` mounts an encoded view of your repository so an LLM can read/write through a token-optimized transport format.

## What it does

- Scans your repository and builds a dictionary tuned for code/Markdown token savings (`llmfs init`)
- Supports two formats:
  - `tce1`: human-readable `ESC+CODE` style
  - `tce2`: compact direct-symbol transport (recommended)
- Writes:
  - `.llmfs/config.json` (codec config)
  - `.llmfs/INIT_INSTRUCTIONS.md` (paste into your LLM system/init prompt)
- Mounts a FUSE filesystem where eligible text files are exposed in encoded form
- Decodes encoded writes back to raw on-disk files

## Install

```bash
go build ./cmd/llmfs
```

## Workflow

```bash
# 1) Build binary
go build ./cmd/llmfs

# 2) Mount encoded view (auto-generates config/instructions if missing)
mkdir -p /tmp/repo-encoded
./llmfs run --root . --mountpoint /tmp/repo-encoded
```

Use files under `/tmp/repo-encoded` when exchanging content with your LLM.

## Optional tuning

```bash
# See estimated token savings
./llmfs explore --root .

# Advanced knobs (usually unnecessary)
./llmfs init --root . --format tce2 --dict-size 128 --tokenizer cl100k_base
```

## Notes

- Eligible files are common code/text extensions (`.go`, `.md`, `.py`, `.ts`, etc.)
- Binary/non-UTF8 files are passed through unmodified
- Requires FUSE support on your OS
