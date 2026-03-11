package mountfs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestShouldTransformPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path       string
		applyToAll bool
		want       bool
	}{
		{path: "/repo/README.md", applyToAll: true, want: true},
		{path: "/repo/.README.md.swp", applyToAll: true, want: false},
		{path: "/repo/README.md.lock", applyToAll: true, want: false},
		{path: "/repo/README.md~", applyToAll: true, want: false},
		{path: "/repo/.#README.md", applyToAll: true, want: false},
		{path: "/repo/README.md", applyToAll: false, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := shouldTransformPath(tc.path, tc.applyToAll); got != tc.want {
				t.Fatalf("shouldTransformPath(%q, %v) = %v, want %v", tc.path, tc.applyToAll, got, tc.want)
			}
		})
	}
}

func TestZeroLengthWriteDoesNotDirtyHandle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "README.md")
	if err := os.WriteFile(p, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	h := newTransFileHandle(p, syscall.O_WRONLY, nil, false, false)
	n, errno := h.Write(context.Background(), []byte{}, 0)
	if errno != 0 {
		t.Fatalf("write errno = %d", errno)
	}
	if n != 0 {
		t.Fatalf("write bytes = %d, want 0", n)
	}
	if h.dirty {
		t.Fatal("handle unexpectedly marked dirty after zero-length write")
	}
	if errno := h.Flush(context.Background()); errno != 0 {
		t.Fatalf("flush errno = %d", errno)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("file content = %q, want %q", string(got), "original")
	}
}
