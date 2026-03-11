package redirectenvtoagent

import (
	"os"
	"path/filepath"

	"llmfs/internal/transform"
)

type middleware struct{}

func New() transform.Middleware {
	return &middleware{}
}

func (m *middleware) Name() string {
	return "redirect_env_to_agent"
}

func (m *middleware) Handle(ctx transform.Context, stage transform.Stage, content []byte) (transform.Result, error) {
	if stage != transform.StageServe {
		return transform.Result{Content: content, Allowed: true}, nil
	}
	if filepath.Base(ctx.Path) != ".env" {
		return transform.Result{Content: content, Allowed: true}, nil
	}
	agentPath := filepath.Join(filepath.Dir(ctx.Path), ".env.agent")
	b, err := os.ReadFile(agentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return transform.Result{
				Content: content,
				Allowed: false,
				Message: "missing .env.agent for .env redirect",
			}, nil
		}
		return transform.Result{}, err
	}
	return transform.Result{Content: b, Allowed: true}, nil
}
