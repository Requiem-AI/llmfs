# Middleware Development Guide

This package contains the mount-time transformation pipeline used by `llmfs`.

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

## Add a new middleware

1. Create a new file in this directory, for example `middleware_<name>.go`.
2. Implement the interface:

```go
type myMiddleware struct{}

func NewMyMiddleware(options map[string]any) (Middleware, error) {
	// Parse and validate options here if needed.
	return &myMiddleware{}, nil
}

func (m *myMiddleware) Name() string {
	return "my_middleware"
}

func (m *myMiddleware) Handle(ctx Context, stage Stage, content []byte) (Result, error) {
	_ = ctx
	_ = stage
	return Result{Content: content, Allowed: true}, nil
}
```

3. Register it in `NewDefaultRegistry(...)` in `defaults.go`:

```go
r.Register("my_middleware", func(options map[string]any) (Middleware, error) {
	return NewMyMiddleware(options)
})
```

4. Add it to `.llmfs/settings.json` (or `examples/settings.example.json`) under `middlewares`:

```json
{
  "name": "my_middleware",
  "enabled": true,
  "options": {
    "example": "value"
  }
}
```

5. Add tests in this package:
  - unit tests for middleware behavior
  - pipeline-order tests if ordering is important

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

## Option handling pattern

`ModuleConfig.Options` is a `map[string]any` from settings JSON.

- Validate required options in constructor.
- Return a descriptive error if options are malformed.
- Keep defaults local to middleware constructor.

## Quick test loop

From repo root:

```bash
go test ./internal/transform -v
go test ./...
```
