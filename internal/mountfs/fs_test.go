package mountfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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

	h := newTransFileHandle(p, "README.md", syscall.O_WRONLY, nil, false, false, nil)
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

func TestOverlayNodeSetattrDoesNotMutateBase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "setattr")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	root := newTestTransRootWithOverlay(t, dir, true, overlay)
	child := lookupChildNode(t, root, "f.txt")

	in := &fuse.SetAttrIn{}
	in.Valid = fuse.FATTR_SIZE | fuse.FATTR_MODE | fuse.FATTR_MTIME
	in.Size = 2
	in.Mode = 0o600
	in.Mtime = uint64(time.Unix(1_700_000_000, 0).Unix())
	var out fuse.AttrOut
	if errno := child.Setattr(context.Background(), nil, in, &out); errno != 0 {
		t.Fatalf("setattr errno=%d", errno)
	}

	got, err := overlay.ReadFile("f.txt")
	if err != nil {
		t.Fatalf("overlay read: %v", err)
	}
	if string(got) != "he" {
		t.Fatalf("overlay content=%q want he", string(got))
	}
	base, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err != nil {
		t.Fatalf("base read: %v", err)
	}
	if string(base) != "hello" {
		t.Fatalf("base content changed to %q", string(base))
	}
}

func TestOverlayNodeSymlinkAndLink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("src"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "links")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	root := newTestTransRootWithOverlay(t, dir, true, overlay)

	var out fuse.EntryOut
	if _, errno := root.Symlink(context.Background(), "src.txt", "sym.txt", &out); errno != 0 {
		t.Fatalf("symlink errno=%d", errno)
	}
	sym := lookupChildNode(t, root, "sym.txt")
	linkTarget, errno := sym.Readlink(context.Background())
	if errno != 0 {
		t.Fatalf("readlink errno=%d", errno)
	}
	if string(linkTarget) != "src.txt" {
		t.Fatalf("readlink=%q want src.txt", string(linkTarget))
	}

	src := lookupChildNode(t, root, "src.txt")
	var out2 fuse.EntryOut
	if _, errno := root.Link(context.Background(), src, "copy.txt", &out2); errno != 0 {
		t.Fatalf("link errno=%d", errno)
	}
	copyBytes, err := overlay.ReadFile("copy.txt")
	if err != nil {
		t.Fatalf("overlay copy read: %v", err)
	}
	if string(copyBytes) != "src" {
		t.Fatalf("copy content=%q want src", string(copyBytes))
	}
}

func TestOverlayNodeXattrRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "xattr")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	root := newTestTransRootWithOverlay(t, dir, true, overlay)
	child := lookupChildNode(t, root, "a.txt")

	if errno := child.Setxattr(context.Background(), "user.test", []byte("v"), 0); errno != syscall.ENOTSUP {
		t.Fatalf("setxattr errno=%d want ENOTSUP", errno)
	}
	if _, errno := child.Getxattr(context.Background(), "user.test", nil); errno != syscall.ENOTSUP {
		t.Fatalf("getxattr errno=%d want ENOTSUP", errno)
	}
	if _, errno := child.Listxattr(context.Background(), nil); errno != syscall.ENOTSUP {
		t.Fatalf("listxattr errno=%d want ENOTSUP", errno)
	}
	if errno := child.Removexattr(context.Background(), "user.test"); errno != syscall.ENOTSUP {
		t.Fatalf("removexattr errno=%d want ENOTSUP", errno)
	}
}

func TestOverlayLookupForOverlayOnlyEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "lookup")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.WriteFile("overlay-only.txt", []byte("v"), 0o644); err != nil {
		t.Fatalf("overlay write: %v", err)
	}

	root := newTestTransRootWithOverlay(t, dir, false, overlay)
	child := lookupChildNode(t, root, "overlay-only.txt")

	fh, _, errno := child.Open(context.Background(), syscall.O_RDONLY)
	if errno != 0 {
		t.Fatalf("open errno=%d", errno)
	}
	reader, ok := fh.(fs.FileReader)
	if !ok {
		t.Fatalf("open handle does not implement FileReader")
	}
	readResult, errno := reader.Read(context.Background(), make([]byte, 16), 0)
	if errno != 0 {
		t.Fatalf("read errno=%d", errno)
	}
	buf, st := readResult.Bytes(nil)
	if st != fuse.OK {
		t.Fatalf("read status=%d want OK", st)
	}
	if string(buf) != "v" {
		t.Fatalf("read content=%q want v", string(buf))
	}
	releaseFileHandle(t, fh)
	if _, err := os.Stat(filepath.Join(dir, "overlay-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("base file unexpectedly exists: %v", err)
	}
}

func TestOverlayNodeCreateWritesOverlayOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "create")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	root := newTestTransRootWithOverlay(t, dir, false, overlay)

	var out fuse.EntryOut
	_, fh, _, errno := root.Create(context.Background(), "new.txt", syscall.O_WRONLY, 0o644, &out)
	if errno != 0 {
		t.Fatalf("create errno=%d", errno)
	}
	writer, ok := fh.(fs.FileWriter)
	if !ok {
		t.Fatalf("create handle does not implement FileWriter")
	}
	written, errno := writer.Write(context.Background(), []byte("hello"), 0)
	if errno != 0 {
		t.Fatalf("write errno=%d", errno)
	}
	if written != uint32(len("hello")) {
		t.Fatalf("written=%d want %d", written, len("hello"))
	}
	releaseFileHandle(t, fh)

	got, err := overlay.ReadFile("new.txt")
	if err != nil {
		t.Fatalf("overlay read: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("overlay content=%q want hello", string(got))
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("base file unexpectedly exists: %v", err)
	}
}

func TestOverlayNodeRenameUnlinkRmdirDoNotMutateBase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "del.txt"), []byte("del"), 0o644); err != nil {
		t.Fatalf("seed del: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "empty-dir"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	overlay, err := NewOverlayAdapter(dir, filepath.Join(dir, ".llmfs", "overlays"), "mutations")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	root := newTestTransRootWithOverlay(t, dir, true, overlay)

	if errno := root.Rename(context.Background(), "old.txt", root, "new.txt", 0); errno != 0 {
		t.Fatalf("rename errno=%d", errno)
	}
	if errno := root.Unlink(context.Background(), "del.txt"); errno != 0 {
		t.Fatalf("unlink errno=%d", errno)
	}
	if errno := root.Rmdir(context.Background(), "empty-dir"); errno != 0 {
		t.Fatalf("rmdir errno=%d", errno)
	}

	newBytes, err := overlay.ReadFile("new.txt")
	if err != nil {
		t.Fatalf("overlay read new: %v", err)
	}
	if string(newBytes) != "old" {
		t.Fatalf("overlay new content=%q want old", string(newBytes))
	}
	if _, err := overlay.ReadFile("old.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay old should be masked, err=%v", err)
	}
	if _, err := overlay.ReadFile("del.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay del should be masked, err=%v", err)
	}
	if _, err := overlay.Stat("empty-dir"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlay empty-dir should be masked, err=%v", err)
	}

	baseOld, err := os.ReadFile(filepath.Join(dir, "old.txt"))
	if err != nil {
		t.Fatalf("base old read: %v", err)
	}
	if string(baseOld) != "old" {
		t.Fatalf("base old content changed to %q", string(baseOld))
	}
	baseDel, err := os.ReadFile(filepath.Join(dir, "del.txt"))
	if err != nil {
		t.Fatalf("base del read: %v", err)
	}
	if string(baseDel) != "del" {
		t.Fatalf("base del content changed to %q", string(baseDel))
	}
	if _, err := os.Stat(filepath.Join(dir, "empty-dir")); err != nil {
		t.Fatalf("base empty-dir removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("base new unexpectedly exists: %v", err)
	}
}

func newTestTransRoot(t *testing.T, dir string, applyToAll bool) *TransNode {
	return newTestTransRootWithOverlay(t, dir, applyToAll, nil)
}

func newTestTransRootWithOverlay(t *testing.T, dir string, applyToAll bool, overlay *OverlayAdapter) *TransNode {
	t.Helper()

	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatalf("stat root dir: %v", err)
	}

	rootData := &fs.LoopbackRoot{
		Path: dir,
		Dev:  uint64(st.Dev),
	}
	rootData.NewNode = newTransNode(rootData, nil, applyToAll, overlay)
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
