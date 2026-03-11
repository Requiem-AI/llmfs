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

	"llmfs/internal/appcfg"
	"llmfs/internal/codec"
	"llmfs/internal/transform"
)

func Mount(root, mountpoint, cfgPath string, settings appcfg.Settings) error {
	_, cdc, err := codec.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config %s: %w", cfgPath, err)
	}
	registry := transform.NewDefaultRegistry(cdc)
	modules := make([]transform.ModuleConfig, 0, len(settings.Middlewares))
	for _, m := range settings.Middlewares {
		modules = append(modules, transform.ModuleConfig{
			Name:    m.Name,
			Enabled: m.Enabled,
			Options: m.Options,
		})
	}
	pipeline, err := transform.NewPipeline(modules, registry)
	if err != nil {
		return fmt.Errorf("build middleware pipeline: %w", err)
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
	rootData.NewNode = newTransNode(rootData, pipeline, settings.ApplyToAllFiles)
	rootNode := newTransNode(rootData, pipeline, settings.ApplyToAllFiles)(rootData, nil, "", nil)

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
	fmt.Printf("Mounted and ready at %s (source: %s). Press Ctrl+C to unmount.\n", mountpoint, root)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		_ = server.Unmount()
	}()

	server.Wait()
	return nil
}
