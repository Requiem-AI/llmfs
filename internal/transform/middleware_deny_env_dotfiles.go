package transform

import (
	"path/filepath"
	"strings"
)

type denyEnvDotfilesMiddleware struct{}

func NewDenyEnvDotfilesMiddleware() Middleware {
	return &denyEnvDotfilesMiddleware{}
}

func (m *denyEnvDotfilesMiddleware) Name() string {
	return "deny_env_dotfiles"
}

func (m *denyEnvDotfilesMiddleware) Handle(ctx Context, stage Stage, content []byte) (Result, error) {
	if stage != StageServe {
		return Result{Content: content, Allowed: true}, nil
	}
	name := filepath.Base(ctx.Path)
	if strings.HasPrefix(name, ".env.") {
		return Result{
			Content: content,
			Allowed: false,
			Message: "reading .env.* files is blocked",
		}, nil
	}
	return Result{Content: content, Allowed: true}, nil
}
