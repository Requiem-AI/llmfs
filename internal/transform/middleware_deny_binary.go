package transform

import "bytes"

type denyBinaryMiddleware struct{}

func NewDenyBinaryMiddleware() Middleware {
	return &denyBinaryMiddleware{}
}

func (m *denyBinaryMiddleware) Name() string {
	return "deny_binary"
}

func (m *denyBinaryMiddleware) Handle(_ Context, _ Stage, content []byte) (Result, error) {
	if bytes.IndexByte(content, 0) >= 0 {
		return Result{
			Content: content,
			Allowed: false,
			Message: "content appears to be binary (NUL byte detected)",
		}, nil
	}
	return Result{Content: content, Allowed: true}, nil
}
