package transform

import (
	"os"
	"path/filepath"
)

type redirectEnvToAgentMiddleware struct{}

func NewRedirectEnvToAgentMiddleware() Middleware {
	return &redirectEnvToAgentMiddleware{}
}

func (m *redirectEnvToAgentMiddleware) Name() string {
	return "redirect_env_to_agent"
}

func (m *redirectEnvToAgentMiddleware) Handle(ctx Context, stage Stage, content []byte) (Result, error) {
	if stage != StageServe {
		return Result{Content: content, Allowed: true}, nil
	}
	if filepath.Base(ctx.Path) != ".env" {
		return Result{Content: content, Allowed: true}, nil
	}
	agentPath := filepath.Join(filepath.Dir(ctx.Path), ".env.agent")
	b, err := os.ReadFile(agentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{
				Content: content,
				Allowed: false,
				Message: "missing .env.agent for .env redirect",
			}, nil
		}
		return Result{}, err
	}
	return Result{Content: b, Allowed: true}, nil
}
