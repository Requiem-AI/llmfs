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
	available := []Middleware{
		&testMiddleware{
			name: "a",
			handle: func(_ Stage, content []byte) (Result, error) {
				return Result{Content: append(content, 'A'), Allowed: true}, nil
			},
		},
		&testMiddleware{
			name: "b",
			handle: func(stage Stage, content []byte) (Result, error) {
				if stage == StageServe {
					return Result{Content: append(content, 'B'), Allowed: true}, nil
				}
				return Result{Content: append(content, 'b'), Allowed: true}, nil
			},
		},
		&testMiddleware{
			name: "c",
			handle: func(stage Stage, content []byte) (Result, error) {
				if stage == StageServe {
					return Result{Content: append(content, 'C'), Allowed: true}, nil
				}
				return Result{Content: append(content, 'c'), Allowed: true}, nil
			},
		},
	}
	p, err := NewPipeline([]string{"a", "b", "c"}, available)
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
	p, err := NewPipeline([]string{"reject"}, []Middleware{
		&testMiddleware{
			name: "reject",
			handle: func(_ Stage, content []byte) (Result, error) {
				return Result{Content: content, Allowed: false, Message: "blocked"}, nil
			},
		},
	})
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
	p, err := NewPipeline([]string{"panic"}, []Middleware{
		&testMiddleware{
			name: "panic",
			handle: func(_ Stage, _ []byte) (Result, error) {
				panic("boom")
			},
		},
	})
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

func TestPipelineErrorsWhenPluginUnavailable(t *testing.T) {
	_, err := NewPipeline([]string{"missing"}, []Middleware{
		&testMiddleware{name: "a", handle: func(_ Stage, content []byte) (Result, error) {
			return Result{Content: content, Allowed: true}, nil
		}},
	})
	if err == nil {
		t.Fatal("expected unavailable plugin error")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unexpected error: %v", err)
	}
}
