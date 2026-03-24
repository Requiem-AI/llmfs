# llmfs

`llmfs` is a FUSE-backed overlay filesystem for LLM workflows.

It mounts a transformed view of your repo so LLM tools read/write token-optimized content, while your real files on disk stay canonical.

## Core idea

`llmfs` has two layers:

1. Source filesystem: your real repository files.
2. Overlay filesystem: mounted view exposed to tooling (usually `.llmfs/mount`).

Reads go through the overlay pipeline (`serve`), and writes return through the inverse path (`commit`) before being saved back to the source files.

## Overlay system

The overlay is middleware-driven and ordered.

- `serve` (read): middlewares run in configured order.
- `commit` (write): the same middlewares run in reverse order.
- This lets `llmfs` combine policy checks with reversible transforms (such as codec encode/decode).

Default built-ins:

- `deny_env_dotfiles`
- `redirect_env_to_agent`
- `codec`
- `deny_binary` (optional)

## Why overlays are useful

An overlay gives you multiple working trees from one source repo, without duplicating the repo itself.

Example layout:

```text
/work/repo                    # single canonical source tree
/work/repo/.llmfs/mount-ai    # overlay tree for AI assistant A
/tmp/repo-review              # overlay tree for review/check workflows
/tmp/repo-experiment          # overlay tree for isolated prompt experiments
```

Why this is good:

- Single source of truth: all edits still commit back to one real repo.
- Multiple contexts: each agent/tool can use its own mounted tree/path.
- Isolation without copies: no extra git clones, no branch-per-tool overhead.
- Safer experimentation: test prompt/tool behavior in separate overlay mountpoints.

## Quick start

Prereqs: Go 1.22+ and working FUSE support.

```bash
go build ./cmd/llmfs
./llmfs run --root . --mountpoint .llmfs/mount
```

Point your LLM tooling at `.llmfs/mount`.

## CLI

```bash
llmfs run [--root DIR] [--mountpoint DIR]
llmfs init [--root DIR]
llmfs explore [--root DIR]
llmfs encode [--config PATH] [--in FILE] [--out FILE]
llmfs decode [--config PATH] [--in FILE] [--out FILE]
llmfs version
```

## More detail

Deep documentation moved to [`docs/DEEP_DIVE.md`](./docs/DEEP_DIVE.md):

- config and settings schema
- transport formats (`tce1`/`tce2`)
- full command reference and workflow
- safety notes, development, and CI/release details

Contributor docs:

- middleware authoring: [`internal/middleware/README.md`](./internal/middleware/README.md)
- pipeline runtime: [`internal/transform/README.md`](./internal/transform/README.md)
