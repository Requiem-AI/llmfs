package mountfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

func TestIsTransportEndpointErr(t *testing.T) {
	t.Parallel()

	if !isTransportEndpointErr(syscall.ENOTCONN) {
		t.Fatal("expected ENOTCONN to be detected as transport endpoint error")
	}
	if !isTransportEndpointErr(errors.New("stat x: transport endpoint is not connected")) {
		t.Fatal("expected matching string to be detected as transport endpoint error")
	}
	if isTransportEndpointErr(errors.New("permission denied")) {
		t.Fatal("did not expect unrelated error to be detected as transport endpoint error")
	}
}

func TestStaleMountTargetsIncludesParent(t *testing.T) {
	t.Parallel()

	mountpoint := "/repo/.llmfs/mount"
	targets := staleMountTargets(mountpoint)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0] != "/repo/.llmfs/mount" {
		t.Fatalf("unexpected first target: %s", targets[0])
	}
	if targets[1] != "/repo/.llmfs" {
		t.Fatalf("unexpected second target: %s", targets[1])
	}
}

func TestIsUnmountNoopOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want bool
	}{
		{name: "not-mounted", out: "fusermount: entry for /x not found in /etc/mtab", want: true},
		{name: "transport-endpoint", out: "Transport endpoint is not connected", want: true},
		{name: "empty", out: "", want: false},
		{name: "real-error", out: "operation not permitted", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isUnmountNoopOutput([]byte(tc.out))
			if got != tc.want {
				t.Fatalf("isUnmountNoopOutput(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
