package transform

import (
	"strings"
	"testing"
)

type testMiddleware struct {
	name   string
	handle func(stage Stage, content []byte) (Result, error)
}

func (m *testMiddleware) Name() string { return m.name }

func (m *testMiddleware) Handle(_ Context, stage Stage, content []byte) (Result, error) {
	return m.handle(stage, content)
}

func TestPipelineServeOrderAndCommitReverseOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register("a", func(_ map[string]any) (Middleware, error) {
		return &testMiddleware{
			name: "a",
			handle: func(_ Stage, content []byte) (Result, error) {
				return Result{Content: append(content, 'A'), Allowed: true}, nil
			},
		}, nil
	})
	reg.Register("b", func(_ map[string]any) (Middleware, error) {
		return &testMiddleware{
			name: "b",
			handle: func(stage Stage, content []byte) (Result, error) {
				if stage == StageServe {
					return Result{Content: append(content, 'B'), Allowed: true}, nil
				}
				return Result{Content: append(content, 'b'), Allowed: true}, nil
			},
		}, nil
	})
	reg.Register("c", func(_ map[string]any) (Middleware, error) {
		return &testMiddleware{
			name: "c",
			handle: func(stage Stage, content []byte) (Result, error) {
				if stage == StageServe {
					return Result{Content: append(content, 'C'), Allowed: true}, nil
				}
				return Result{Content: append(content, 'c'), Allowed: true}, nil
			},
		}, nil
	})
	p, err := NewPipeline([]ModuleConfig{
		{Name: "a", Enabled: true},
		{Name: "b", Enabled: true},
		{Name: "c", Enabled: true},
	}, reg)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}

	served, err := p.Serve(Context{}, []byte("x"))
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if string(served) != "xABC" {
		t.Fatalf("serve order mismatch: %q", string(served))
	}

	committed, err := p.Commit(Context{}, []byte("x"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if string(committed) != "xcbA" {
		t.Fatalf("commit order mismatch: %q", string(committed))
	}
}

func TestPipelineReject(t *testing.T) {
	reg := NewRegistry()
	reg.Register("reject", func(_ map[string]any) (Middleware, error) {
		return &testMiddleware{
			name: "reject",
			handle: func(_ Stage, content []byte) (Result, error) {
				return Result{Content: content, Allowed: false, Message: "blocked"}, nil
			},
		}, nil
	})
	p, err := NewPipeline([]ModuleConfig{{Name: "reject", Enabled: true}}, reg)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	_, err = p.Serve(Context{}, []byte("x"))
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipelineRecoversMiddlewarePanic(t *testing.T) {
	reg := NewRegistry()
	reg.Register("panic", func(_ map[string]any) (Middleware, error) {
		return &testMiddleware{
			name: "panic",
			handle: func(_ Stage, _ []byte) (Result, error) {
				panic("boom")
			},
		}, nil
	})
	p, err := NewPipeline([]ModuleConfig{{Name: "panic", Enabled: true}}, reg)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	_, err = p.Serve(Context{}, []byte("x"))
	if err == nil {
		t.Fatal("expected panic recovery error")
	}
	if !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("unexpected error: %v", err)
	}
}
