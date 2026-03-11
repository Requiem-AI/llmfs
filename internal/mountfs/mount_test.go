package mountfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareMountpointCreatesMissingDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mountpoint := filepath.Join(base, ".llmfs", "mount")

	if err := prepareMountpoint(mountpoint); err != nil {
		t.Fatalf("prepareMountpoint returned error: %v", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("stat mountpoint: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mountpoint is not a directory: mode=%v", info.Mode())
	}
}

func TestPrepareMountpointKeepsExistingDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mountpoint := filepath.Join(base, ".llmfs", "mount")
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		t.Fatalf("mkdir mountpoint: %v", err)
	}

	if err := prepareMountpoint(mountpoint); err != nil {
		t.Fatalf("prepareMountpoint returned error: %v", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("stat mountpoint: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mountpoint is not a directory: mode=%v", info.Mode())
	}
}

func TestPrepareMountpointReplacesFile(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mountpoint := filepath.Join(base, ".llmfs", "mount")
	if err := os.MkdirAll(filepath.Dir(mountpoint), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(mountpoint, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if err := prepareMountpoint(mountpoint); err != nil {
		t.Fatalf("prepareMountpoint returned error: %v", err)
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("stat mountpoint: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("mountpoint is not a directory: mode=%v", info.Mode())
	}
}
