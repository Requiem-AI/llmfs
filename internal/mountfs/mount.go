package mountfs

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"llmfs/internal/codec"
)

func Mount(root, mountpoint, cfgPath string) error {
	_, cdc, err := codec.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	mountpoint, err = filepath.Abs(mountpoint)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return err
	}

	rootData := &fs.LoopbackRoot{Path: root}
	rootData.NewNode = newTransNode(rootData, cdc)
	rootNode := newTransNode(rootData, cdc)(rootData, nil, "", nil)

	sec := time.Second
	opts := &fs.Options{
		AttrTimeout:     &sec,
		EntryTimeout:    &sec,
		NullPermissions: true,
		MountOptions: fuse.MountOptions{
			Name:   "llmfs",
			FsName: root,
		},
	}

	server, err := fs.Mount(mountpoint, rootNode, opts)
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		_ = server.Unmount()
	}()

	server.Wait()
	return nil
}
