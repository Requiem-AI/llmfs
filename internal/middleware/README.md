# Middleware Development Guide

Middleware implementations are split into dedicated packages under `internal/middleware/<name>`.

## Why this layout

- Keeps pipeline runtime (`internal/transform`) focused and stable.
- Makes middleware PRs isolated and easier to review.
- Allows middleware-specific dependencies to stay scoped to middleware packages.

## Add a new middleware

1. Create a new package under `internal/middleware/<name>`.
2. Implement the `transform.Middleware` interface.
3. Register it in `internal/middleware/defaults/defaults.go` if it should be built-in.
4. Add it to `.llmfs/settings.json` (or `examples/settings.example.json`) under `available_plugins`.
5. Add package-local tests for behavior and edge cases.

Example skeleton:

```go
package mymiddleware

import "llmfs/internal/transform"

type middleware struct{}

func New() transform.Middleware {
	return &middleware{}
}

func (m *middleware) Name() string {
	return "my_middleware"
}

func (m *middleware) Handle(ctx transform.Context, stage transform.Stage, content []byte) (transform.Result, error) {
	_ = ctx
	_ = stage
	return transform.Result{Content: content, Allowed: true}, nil
}
```

## Test loop

```bash
go test ./internal/middleware/...
go test ./internal/transform -v
go test ./...
```
