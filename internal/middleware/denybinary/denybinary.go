package denybinary

import (
	"bytes"

	"llmfs/internal/transform"
)

type middleware struct{}

func New() transform.Middleware {
	return &middleware{}
}

func (m *middleware) Name() string {
	return "deny_binary"
}

func (m *middleware) Handle(_ transform.Context, _ transform.Stage, content []byte) (transform.Result, error) {
	if bytes.IndexByte(content, 0) >= 0 {
		return transform.Result{
			Content: content,
			Allowed: false,
			Message: "content appears to be binary (NUL byte detected)",
		}, nil
	}
	return transform.Result{Content: content, Allowed: true}, nil
}
