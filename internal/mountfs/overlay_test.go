package mountfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOverlayWriteReadIsolation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("base"), 0o644); err != nil {
		t.Fatalf("seed base file: %v", err)
	}
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s1")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}

	if err := overlay.WriteFile("a.txt", []byte("overlay"), 0o644); err != nil {
		t.Fatalf("overlay write: %v", err)
	}
	got, err := overlay.ReadFile("a.txt")
	if err != nil {
		t.Fatalf("overlay read: %v", err)
	}
	if string(got) != "overlay" {
		t.Fatalf("overlay read = %q, want overlay", string(got))
	}
	base, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatalf("base read: %v", err)
	}
	if string(base) != "base" {
		t.Fatalf("base read = %q, want base", string(base))
	}
}

func TestOverlayReadDirMergeAndDeleteMask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("k"), 0o644); err != nil {
		t.Fatalf("seed keep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "drop.txt"), []byte("d"), 0o644); err != nil {
		t.Fatalf("seed drop: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "base-dir"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s2")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.DeleteFile("drop.txt"); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if err := overlay.WriteFile("add.txt", []byte("a"), 0o644); err != nil {
		t.Fatalf("add file: %v", err)
	}
	if err := overlay.Mkdir("new-dir", 0o755); err != nil {
		t.Fatalf("add dir: %v", err)
	}

	entries, err := overlay.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{"keep.txt", "base-dir", "add.txt", "new-dir"} {
		if !names[want] {
			t.Fatalf("missing %q in merged readdir", want)
		}
	}
	if names["drop.txt"] {
		t.Fatalf("drop.txt should be masked by overlay tombstone")
	}
}

func TestOverlayRenameFileFromBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s3")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.Rename("old.txt", "new.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := overlay.ReadFile("old.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old should be masked, got err=%v", err)
	}
	b, err := overlay.ReadFile("new.txt")
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if string(b) != "v1" {
		t.Fatalf("new content = %q, want v1", string(b))
	}
	baseOld, err := os.ReadFile(filepath.Join(root, "old.txt"))
	if err != nil {
		t.Fatalf("base old read: %v", err)
	}
	if string(baseOld) != "v1" {
		t.Fatalf("base old changed to %q", string(baseOld))
	}
}

func TestOverlayRenameDirFromBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o755); err != nil {
		t.Fatalf("seed dirs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "nested", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s4")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.Rename("src", "dst"); err != nil {
		t.Fatalf("rename dir: %v", err)
	}

	if _, err := overlay.Stat("src"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("src should be masked, err=%v", err)
	}
	a, err := overlay.ReadFile("dst/a.txt")
	if err != nil {
		t.Fatalf("read moved a: %v", err)
	}
	b, err := overlay.ReadFile("dst/nested/b.txt")
	if err != nil {
		t.Fatalf("read moved b: %v", err)
	}
	if string(a) != "A" || string(b) != "B" {
		t.Fatalf("moved contents mismatch: a=%q b=%q", string(a), string(b))
	}
	if _, err := os.Stat(filepath.Join(root, "src", "a.txt")); err != nil {
		t.Fatalf("base src should remain: %v", err)
	}
}

func TestOverlaySymlinkAndReadlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s5")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.WriteFile("target.txt", []byte("ok"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := overlay.WriteSymlink("link.txt", "target.txt"); err != nil {
		t.Fatalf("write symlink: %v", err)
	}
	target, err := overlay.Readlink("link.txt")
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("readlink=%q want target.txt", target)
	}
	b, err := overlay.ReadFile("link.txt")
	if err != nil {
		t.Fatalf("read through symlink: %v", err)
	}
	if string(b) != "ok" {
		t.Fatalf("symlink read=%q want ok", string(b))
	}
}

func TestOverlayTruncateChmodSetMtime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	overlay, err := NewOverlayAdapter(root, filepath.Join(root, ".llmfs", "overlays"), "s6")
	if err != nil {
		t.Fatalf("new overlay: %v", err)
	}
	if err := overlay.Truncate("f.txt", 2); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := overlay.Chmod("f.txt", 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	wantMtime := time.Unix(1_700_000_000, 0).UTC()
	if err := overlay.SetMtime("f.txt", wantMtime); err != nil {
		t.Fatalf("setmtime: %v", err)
	}

	b, err := overlay.ReadFile("f.txt")
	if err != nil {
		t.Fatalf("overlay read: %v", err)
	}
	if string(b) != "he" {
		t.Fatalf("overlay content=%q want he", string(b))
	}
	fi, err := overlay.Stat("f.txt")
	if err != nil {
		t.Fatalf("overlay stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o want 600", fi.Mode().Perm())
	}
	if fi.ModTime().UTC().Unix() != wantMtime.Unix() {
		t.Fatalf("mtime=%v want %v", fi.ModTime().UTC(), wantMtime)
	}
	base, err := os.ReadFile(filepath.Join(root, "f.txt"))
	if err != nil {
		t.Fatalf("base read: %v", err)
	}
	if string(base) != "hello" {
		t.Fatalf("base content changed to %q", string(base))
	}
}
