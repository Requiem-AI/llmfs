package mountfs

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
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
	if err := prepareMountpoint(mountpoint); err != nil {
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

	server, err := mountWithRecovery(mountpoint, rootNode, opts)
	if err != nil {
		return err
	}
	fmt.Printf("Mounted and ready at %s (source: %s). Press Ctrl+C to unmount.\n", mountpoint, root)

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sig)
		<-sig
		fmt.Fprintln(os.Stderr, "\nInterrupt received, unmounting...")

		done := make(chan error, 1)
		go func() {
			done <- server.Unmount()
		}()

		warn := time.NewTimer(3 * time.Second)
		defer warn.Stop()
		for {
			select {
			case err := <-done:
				if err != nil {
					fmt.Fprintf(os.Stderr, "Unmount error: %v\n", err)
				}
				return
			case <-warn.C:
				fmt.Fprintln(os.Stderr, "Unmount still in progress. Press Ctrl+C again to force exit.")
				warn.Reset(3 * time.Second)
			case <-sig:
				fmt.Fprintln(os.Stderr, "\nSecond interrupt received, forcing exit.")
				os.Exit(130)
			}
		}
	}()

	server.Wait()
	return nil
}

func prepareMountpoint(mountpoint string) error {
	info, err := os.Stat(mountpoint)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		if err := os.Remove(mountpoint); err != nil {
			return fmt.Errorf("replace mountpoint file %s: %w", mountpoint, err)
		}
		return os.MkdirAll(mountpoint, 0o755)
	}
	if isTransportEndpointErr(err) {
		if unmountErr := forceUnmount(mountpoint); unmountErr != nil {
			return fmt.Errorf("recover stale mountpoint %s: %w", mountpoint, unmountErr)
		}
		if removeErr := os.RemoveAll(mountpoint); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("cleanup stale mountpoint %s: %w", mountpoint, removeErr)
		}
		if mkErr := os.MkdirAll(mountpoint, 0o755); mkErr != nil {
			return fmt.Errorf("recreate mountpoint %s: %w", mountpoint, mkErr)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("check mountpoint %s: %w", mountpoint, err)
	}
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("create mountpoint %s: %w", mountpoint, err)
	}
	return nil
}

func mountWithRecovery(mountpoint string, rootNode fs.InodeEmbedder, opts *fs.Options) (*fuse.Server, error) {
	server, err := fs.Mount(mountpoint, rootNode, opts)
	if err == nil {
		return server, nil
	}
	if !isRecoverableMountErr(err) {
		return nil, fmt.Errorf("mount: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Mount failed at %s: %v\nAttempting recovery and retry...\n", mountpoint, err)
	if unmountErr := forceUnmount(mountpoint); unmountErr != nil {
		return nil, fmt.Errorf("mount: %w (recovery unmount failed: %v)", err, unmountErr)
	}
	if prepErr := prepareMountpoint(mountpoint); prepErr != nil {
		return nil, fmt.Errorf("mount: %w (recovery prepare failed: %v)", err, prepErr)
	}
	server, err = fs.Mount(mountpoint, rootNode, opts)
	if err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}
	return server, nil
}

func isRecoverableMountErr(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "file exists") {
		return true
	}
	if strings.Contains(msg, "device or resource busy") {
		return true
	}
	return strings.Contains(msg, "transport endpoint is not connected")
}

func forceUnmount(mountpoint string) error {
	err := syscall.Unmount(mountpoint, 0)
	if err == nil || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOENT) {
		return nil
	}
	if runtime.GOOS == "linux" {
		// Retry once; some environments report transient busy/mount races on first attempt.
		retryErr := syscall.Unmount(mountpoint, 0)
		if retryErr == nil || errors.Is(retryErr, syscall.EINVAL) || errors.Is(retryErr, syscall.ENOENT) {
			return nil
		}
		return retryErr
	}
	return err
}

func isTransportEndpointErr(err error) bool {
	if errors.Is(err, syscall.ENOTCONN) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "transport endpoint is not connected")
}
