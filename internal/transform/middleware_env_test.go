package transform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDenyEnvDotfilesMiddleware(t *testing.T) {
	m := NewDenyEnvDotfilesMiddleware()
	got, err := m.Handle(Context{Path: "/tmp/.env.local"}, StageServe, []byte("x"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got.Allowed {
		t.Fatal("expected .env.* to be blocked")
	}
}

func TestRedirectEnvToAgentMiddleware(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.agent"), []byte("AGENT=1"), 0o644); err != nil {
		t.Fatalf("write .env.agent: %v", err)
	}
	m := NewRedirectEnvToAgentMiddleware()
	got, err := m.Handle(Context{Path: filepath.Join(dir, ".env")}, StageServe, []byte("SECRET=1"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !got.Allowed {
		t.Fatal("expected .env read to be allowed via redirect")
	}
	if string(got.Content) != "AGENT=1" {
		t.Fatalf("unexpected redirected content: %q", string(got.Content))
	}
}

func TestRedirectEnvToAgentMiddlewareMissingTarget(t *testing.T) {
	dir := t.TempDir()
	m := NewRedirectEnvToAgentMiddleware()
	got, err := m.Handle(Context{Path: filepath.Join(dir, ".env")}, StageServe, []byte("SECRET=1"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got.Allowed {
		t.Fatal("expected missing .env.agent to be blocked")
	}
}
