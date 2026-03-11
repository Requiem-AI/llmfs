package mountfs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
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

func TestOpenSkipsTransformForLockFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "README.md.lock")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}

	root := newTestTransRoot(t, dir, true)
	child := lookupChildNode(t, root, "README.md.lock")

	fh, fuseFlags, errno := child.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("open errno = %d", errno)
	}
	if _, ok := fh.(*transFileHandle); ok {
		t.Fatal("expected loopback file handle for lock file, got transFileHandle")
	}
	if fuseFlags&fuse.FOPEN_DIRECT_IO != 0 {
		t.Fatalf("open flags = %#x, expected no direct IO for lock file", fuseFlags)
	}
	releaseFileHandle(t, fh)
}

func TestOpenUsesTransformForRegularFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "README.md")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	root := newTestTransRoot(t, dir, true)
	child := lookupChildNode(t, root, "README.md")

	fh, fuseFlags, errno := child.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("open errno = %d", errno)
	}
	if _, ok := fh.(*transFileHandle); !ok {
		t.Fatal("expected transFileHandle for regular file")
	}
	if fuseFlags&fuse.FOPEN_DIRECT_IO == 0 {
		t.Fatalf("open flags = %#x, expected direct IO for transformed file", fuseFlags)
	}
}

func TestCreateSkipsTransformForTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := newTestTransRoot(t, dir, true)

	var out fuse.EntryOut
	_, fh, fuseFlags, errno := root.Create(context.Background(), ".README.md.swp", syscall.O_WRONLY, 0o644, &out)
	if errno != 0 {
		t.Fatalf("create errno = %d", errno)
	}
	if _, ok := fh.(*transFileHandle); ok {
		t.Fatal("expected loopback file handle for temp file, got transFileHandle")
	}
	if fuseFlags&fuse.FOPEN_DIRECT_IO != 0 {
		t.Fatalf("create flags = %#x, expected no direct IO for temp file", fuseFlags)
	}
	releaseFileHandle(t, fh)
}

func TestCreateUsesTransformForRegularFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := newTestTransRoot(t, dir, true)

	var out fuse.EntryOut
	_, fh, fuseFlags, errno := root.Create(context.Background(), "README.md", syscall.O_WRONLY, 0o644, &out)
	if errno != 0 {
		t.Fatalf("create errno = %d", errno)
	}
	if _, ok := fh.(*transFileHandle); !ok {
		t.Fatal("expected transFileHandle for regular file")
	}
	if fuseFlags&fuse.FOPEN_DIRECT_IO == 0 {
		t.Fatalf("create flags = %#x, expected direct IO for transformed file", fuseFlags)
	}
	releaseFileHandle(t, fh)
}

func newTestTransRoot(t *testing.T, dir string, applyToAll bool) *TransNode {
	t.Helper()

	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatalf("stat root dir: %v", err)
	}

	rootData := &fs.LoopbackRoot{
		Path: dir,
		Dev:  uint64(st.Dev),
	}
	rootData.NewNode = newTransNode(rootData, nil, applyToAll)
	rootData.RootNode = rootData.NewNode(rootData, nil, "", &st)
	_ = fs.NewNodeFS(rootData.RootNode, &fs.Options{})

	root, ok := rootData.RootNode.(*TransNode)
	if !ok {
		t.Fatal("root node is not TransNode")
	}
	return root
}

func lookupChildNode(t *testing.T, root *TransNode, name string) *TransNode {
	t.Helper()

	var out fuse.EntryOut
	inode, errno := root.Lookup(context.Background(), name, &out)
	if errno != 0 {
		t.Fatalf("lookup %q errno = %d", name, errno)
	}
	if ok := root.AddChild(name, inode, true); !ok {
		t.Fatalf("attach %q as child failed", name)
	}
	child, ok := inode.Operations().(*TransNode)
	if !ok {
		t.Fatalf("lookup %q returned non-TransNode operations", name)
	}
	return child
}

func releaseFileHandle(t *testing.T, fh fs.FileHandle) {
	t.Helper()
	if releaser, ok := fh.(fs.FileReleaser); ok {
		if errno := releaser.Release(context.Background()); errno != 0 {
			t.Fatalf("release errno = %d", errno)
		}
	}
}
