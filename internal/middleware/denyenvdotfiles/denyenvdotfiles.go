package denyenvdotfiles

import (
	"path/filepath"
	"strings"

	"llmfs/internal/transform"
)

type middleware struct{}

func New() transform.Middleware {
	return &middleware{}
}

func (m *middleware) Name() string {
	return "deny_env_dotfiles"
}

func (m *middleware) Handle(ctx transform.Context, stage transform.Stage, content []byte) (transform.Result, error) {
	if stage != transform.StageServe {
		return transform.Result{Content: content, Allowed: true}, nil
	}
	name := filepath.Base(ctx.Path)
	if strings.HasPrefix(name, ".env.") {
		return transform.Result{
			Content: content,
			Allowed: false,
			Message: "reading .env.* files is blocked",
		}, nil
	}
	return transform.Result{Content: content, Allowed: true}, nil
}
