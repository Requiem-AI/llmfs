package denyenvdotfiles

import (
	"testing"

	"llmfs/internal/transform"
)

func TestDenyEnvDotfilesMiddleware(t *testing.T) {
	m := New()
	got, err := m.Handle(transform.Context{Path: "/tmp/.env.local"}, transform.StageServe, []byte("x"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got.Allowed {
		t.Fatal("expected .env.* to be blocked")
	}
}
