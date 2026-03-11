# Transform Pipeline Guide

This package contains the mount-time middleware pipeline runtime used by `llmfs`.

## Pipeline contract

- Middleware interface: `Middleware` in `pipeline.go`
- Stages:
  - `serve`: raw file -> mounted view
  - `commit`: mounted view -> raw file
- Execution order:
  - `Serve(...)` runs middlewares in configured order
  - `Commit(...)` runs middlewares in reverse order

Each middleware returns:

- `Content`: next payload
- `Allowed`: whether processing continues
- `Message`: rejection reason surfaced to callers

If `Allowed` is `false`, pipeline returns `RejectedError`.

Middleware implementations are maintained in `internal/middleware/<name>`.

For contributor instructions on creating or updating middleware packages, see [`internal/middleware/README.md`](../middleware/README.md).

## Design guidance

- Stage-aware behavior:
  - If logic is read-only, no-op on `commit`.
  - If transform must be reversible, implement inverse logic across `serve`/`commit`.
- Ordering:
  - Place guards/validators before mutating transforms.
  - Consider that `commit` order is reversed.
- Context usage:
  - `ctx.Path` gives target file path.
  - `ctx.Info` may be `nil`; code should tolerate missing file info.
- Rejections vs errors:
  - Use `Allowed: false` for policy denials expected in normal operation.
  - Return `error` for unexpected failures (I/O, parse failures, invalid options).

## Quick test loop

From repo root:

```bash
go test ./internal/transform -v
go test ./...
```
