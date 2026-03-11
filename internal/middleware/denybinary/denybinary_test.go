package denybinary

import (
	"testing"

	"llmfs/internal/transform"
)

func TestDenyBinaryMiddleware(t *testing.T) {
	m := New()
	blocked, err := m.Handle(transform.Context{}, transform.StageServe, []byte("abc\x00def"))
	if err != nil {
		t.Fatalf("handle blocked: %v", err)
	}
	if blocked.Allowed {
		t.Fatal("expected NUL byte payload to be blocked")
	}

	allowed, err := m.Handle(transform.Context{}, transform.StageServe, []byte("abcdef"))
	if err != nil {
		t.Fatalf("handle allowed: %v", err)
	}
	if !allowed.Allowed {
		t.Fatal("expected plain text payload to be allowed")
	}
}
