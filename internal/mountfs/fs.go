package mountfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"llmfs/internal/transform"
)

type TransNode struct {
	fs.LoopbackNode
	pipeline       *transform.Pipeline
	applyToAllFile bool
	overlay        *OverlayAdapter
	virtualRelPath string
}

func newTransNode(rootData *fs.LoopbackRoot, p *transform.Pipeline, applyToAll bool, overlay *OverlayAdapter) func(*fs.LoopbackRoot, *fs.Inode, string, *syscall.Stat_t) fs.InodeEmbedder {
	return func(rd *fs.LoopbackRoot, _ *fs.Inode, _ string, _ *syscall.Stat_t) fs.InodeEmbedder {
		return &TransNode{
			LoopbackNode:   fs.LoopbackNode{RootData: rd},
			pipeline:       p,
			applyToAllFile: applyToAll,
			overlay:        overlay,
		}
	}
}

var _ = (fs.NodeOpener)((*TransNode)(nil))
var _ = (fs.NodeLookuper)((*TransNode)(nil))
var _ = (fs.NodeGetattrer)((*TransNode)(nil))
var _ = (fs.NodeSetattrer)((*TransNode)(nil))
var _ = (fs.NodeReaddirer)((*TransNode)(nil))
var _ = (fs.NodeOpendirHandler)((*TransNode)(nil))
var _ = (fs.NodeMkdirer)((*TransNode)(nil))
var _ = (fs.NodeMknoder)((*TransNode)(nil))
var _ = (fs.NodeSymlinker)((*TransNode)(nil))
var _ = (fs.NodeReadlinker)((*TransNode)(nil))
var _ = (fs.NodeLinker)((*TransNode)(nil))
var _ = (fs.NodeUnlinker)((*TransNode)(nil))
var _ = (fs.NodeRmdirer)((*TransNode)(nil))
var _ = (fs.NodeRenamer)((*TransNode)(nil))
var _ = (fs.NodeStatfser)((*TransNode)(nil))
var _ = (fs.NodeGetxattrer)((*TransNode)(nil))
var _ = (fs.NodeListxattrer)((*TransNode)(nil))
var _ = (fs.NodeSetxattrer)((*TransNode)(nil))
var _ = (fs.NodeRemovexattrer)((*TransNode)(nil))
var _ = (fs.NodeCopyFileRanger)((*TransNode)(nil))

func (n *TransNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "lookup", &errno)
	if n.overlay != nil {
		rel := n.childRel(name)
		exists, isDir, err := n.overlay.Exists(rel)
		if err != nil {
			return nil, overlayErrno(err)
		}
		if !exists {
			return nil, syscall.ENOENT
		}
		if _, err := os.Lstat(filepath.Join(n.RootData.Path, filepath.FromSlash(rel))); err == nil {
			return n.LoopbackNode.Lookup(ctx, name, out)
		}
		child := n.newVirtualChild(rel)
		mode := uint32(syscall.S_IFREG)
		if isDir {
			mode = syscall.S_IFDIR
		}
		inode := n.NewInode(ctx, child, fs.StableAttr{Mode: mode})
		if fi, err := n.overlay.Stat(rel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		return inode, 0
	}
	return n.LoopbackNode.Lookup(ctx, name, out)
}

func (n *TransNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) (errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "getattr", &errno)
	if n.overlay != nil {
		fi, err := n.overlay.Stat(n.relPath())
		if err != nil {
			return overlayErrno(err)
		}
		attr := fuse.ToAttr(fi)
		if attr != nil {
			out.Attr = *attr
		}
		return 0
	}
	return n.LoopbackNode.Getattr(ctx, f, out)
}

func (n *TransNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) (errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "setattr", &errno)
	if n.overlay != nil {
		rel := n.relPath()
		if mode, ok := in.GetMode(); ok {
			if err := n.overlay.Chmod(rel, os.FileMode(mode)); err != nil {
				return overlayErrno(err)
			}
		}
		if size, ok := in.GetSize(); ok {
			if err := n.overlay.Truncate(rel, int64(size)); err != nil {
				return overlayErrno(err)
			}
		}
		if mtime, ok := in.GetMTime(); ok {
			if err := n.overlay.SetMtime(rel, mtime); err != nil {
				return overlayErrno(err)
			}
		}
		fi, err := n.overlay.Stat(rel)
		if err != nil {
			return overlayErrno(err)
		}
		if attr := fuse.ToAttr(fi); attr != nil {
			out.Attr = *attr
		}
		return 0
	}
	return n.LoopbackNode.Setattr(ctx, f, in, out)
}

func (n *TransNode) Readdir(ctx context.Context) (stream fs.DirStream, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "readdir", &errno)
	if n.overlay != nil {
		entries, err := n.overlay.ReadDir(n.relPath())
		if err != nil {
			return nil, overlayErrno(err)
		}
		out := make([]fuse.DirEntry, 0, len(entries))
		for _, e := range entries {
			mode := uint32(syscall.S_IFREG)
			if e.IsDir {
				mode = syscall.S_IFDIR
			}
			out = append(out, fuse.DirEntry{Name: e.Name, Mode: mode})
		}
		return fs.NewListDirStream(out), 0
	}
	return n.LoopbackNode.Readdir(ctx)
}

func (n *TransNode) OpendirHandle(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "opendir", &errno)
	return n.LoopbackNode.OpendirHandle(ctx, flags)
}

func (n *TransNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "mkdir", &errno)
	if n.overlay != nil {
		rel := n.childRel(name)
		if err := n.overlay.Mkdir(rel, os.FileMode(mode)); err != nil {
			return nil, overlayErrno(err)
		}
		child := n.newVirtualChild(rel)
		inode := n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFDIR})
		if fi, err := n.overlay.Stat(rel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		return inode, 0
	}
	return n.LoopbackNode.Mkdir(ctx, name, mode, out)
}

func (n *TransNode) Mknod(ctx context.Context, name string, mode, rdev uint32, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "mknod", &errno)
	if n.overlay != nil {
		if mode&syscall.S_IFMT != syscall.S_IFREG {
			return nil, syscall.ENOTSUP
		}
		rel := n.childRel(name)
		if err := n.overlay.CreateEmptyFile(rel, os.FileMode(mode)); err != nil {
			return nil, overlayErrno(err)
		}
		child := n.newVirtualChild(rel)
		inode := n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFREG})
		if fi, err := n.overlay.Stat(rel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		return inode, 0
	}
	return n.LoopbackNode.Mknod(ctx, name, mode, rdev, out)
}

func (n *TransNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "symlink", &errno)
	if n.overlay != nil {
		rel := n.childRel(name)
		if err := n.overlay.WriteSymlink(rel, target); err != nil {
			return nil, overlayErrno(err)
		}
		child := n.newVirtualChild(rel)
		inode := n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFLNK})
		if fi, err := n.overlay.Stat(rel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		return inode, 0
	}
	return n.LoopbackNode.Symlink(ctx, target, name, out)
}

func (n *TransNode) Readlink(ctx context.Context) (dest []byte, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "readlink", &errno)
	if n.overlay != nil {
		target, err := n.overlay.Readlink(n.relPath())
		if err != nil {
			return nil, overlayErrno(err)
		}
		return []byte(target), 0
	}
	return n.LoopbackNode.Readlink(ctx)
}

func (n *TransNode) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "link", &errno)
	if n.overlay != nil {
		targetNode, ok := target.(*TransNode)
		if !ok {
			return nil, syscall.EXDEV
		}
		targetRel := targetNode.relPath()
		data, err := n.overlay.ReadFile(targetRel)
		if err != nil {
			return nil, overlayErrno(err)
		}
		rel := n.childRel(name)
		if err := n.overlay.WriteFile(rel, data, 0o644); err != nil {
			return nil, overlayErrno(err)
		}
		child := n.newVirtualChild(rel)
		inode := n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFREG})
		if fi, err := n.overlay.Stat(rel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		return inode, 0
	}
	return n.LoopbackNode.Link(ctx, target, name, out)
}

func (n *TransNode) Unlink(ctx context.Context, name string) (errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "unlink", &errno)
	if n.overlay != nil {
		return overlayErrno(n.overlay.DeleteFile(n.childRel(name)))
	}
	return n.LoopbackNode.Unlink(ctx, name)
}

func (n *TransNode) Rmdir(ctx context.Context, name string) (errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "rmdir", &errno)
	if n.overlay != nil {
		return overlayErrno(n.overlay.RemoveDir(n.childRel(name)))
	}
	return n.LoopbackNode.Rmdir(ctx, name)
}

func (n *TransNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) (errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "rename", &errno)
	if n.overlay != nil {
		parentNode, ok := newParent.(*TransNode)
		if !ok {
			return syscall.EXDEV
		}
		oldRel := n.childRel(name)
		newRel := parentNode.childRel(newName)
		return overlayErrno(n.overlay.Rename(oldRel, newRel))
	}
	return n.LoopbackNode.Rename(ctx, name, newParent, newName, flags)
}

func (n *TransNode) Statfs(ctx context.Context, out *fuse.StatfsOut) (errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "statfs", &errno)
	return n.LoopbackNode.Statfs(ctx, out)
}

func (n *TransNode) Getxattr(ctx context.Context, attr string, dest []byte) (sz uint32, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "getxattr", &errno)
	if n.overlay != nil {
		return 0, syscall.ENOTSUP
	}
	return n.LoopbackNode.Getxattr(ctx, attr, dest)
}

func (n *TransNode) Listxattr(ctx context.Context, dest []byte) (sz uint32, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "listxattr", &errno)
	if n.overlay != nil {
		return 0, syscall.ENOTSUP
	}
	return n.LoopbackNode.Listxattr(ctx, dest)
}

func (n *TransNode) Setxattr(ctx context.Context, attr string, data []byte, flags uint32) (errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "setxattr", &errno)
	if n.overlay != nil {
		return syscall.ENOTSUP
	}
	return n.LoopbackNode.Setxattr(ctx, attr, data, flags)
}

func (n *TransNode) Removexattr(ctx context.Context, attr string) (errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "removexattr", &errno)
	if n.overlay != nil {
		return syscall.ENOTSUP
	}
	return n.LoopbackNode.Removexattr(ctx, attr)
}

func (n *TransNode) CopyFileRange(ctx context.Context, fhIn fs.FileHandle, offIn uint64, out *fs.Inode, fhOut fs.FileHandle, offOut uint64, len uint64, flags uint64) (written uint32, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "copyfilerange", &errno)
	if n.overlay != nil {
		return 0, syscall.ENOTSUP
	}
	return n.LoopbackNode.CopyFileRange(ctx, fhIn, offIn, out, fhOut, offOut, len, flags)
}

func (n *TransNode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer recoverAsErrno(n.realPath(), "open", &errno)
	if n.overlay != nil {
		h := newTransFileHandle(n.realPath(), n.relPath(), flags, n.pipeline, false, n.applyToAllFile, n.overlay)
		return h, fuse.FOPEN_DIRECT_IO, 0
	}
	if !shouldTransformPath(n.realPath(), n.applyToAllFile) {
		return n.LoopbackNode.Open(ctx, flags)
	}
	h := newTransFileHandle(n.realPath(), n.relPath(), flags, n.pipeline, false, n.applyToAllFile, nil)
	return h, fuse.FOPEN_DIRECT_IO, 0
}

var _ = (fs.NodeCreater)((*TransNode)(nil))

func (n *TransNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer recoverAsErrno(filepath.Join(n.realPath(), name), "create", &errno)
	childPath := filepath.Join(n.realPath(), name)
	childRel := n.childRel(name)
	if n.overlay != nil {
		if err := n.overlay.CreateEmptyFile(childRel, os.FileMode(mode)); err != nil {
			return nil, nil, 0, overlayErrno(err)
		}
		child := n.newVirtualChild(childRel)
		inode = n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFREG})
		if fi, err := n.overlay.Stat(childRel); err == nil {
			if attr := fuse.ToAttr(fi); attr != nil {
				out.Attr = *attr
			}
		}
		h := newTransFileHandle(childPath, childRel, flags, n.pipeline, true, n.applyToAllFile, n.overlay)
		return inode, h, fuse.FOPEN_DIRECT_IO, 0
	}
	inode, rawHandle, fuseFlags, errno := n.LoopbackNode.Create(ctx, name, flags, mode, out)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	child, ok := inode.Operations().(*TransNode)
	if !ok {
		if releaser, ok := rawHandle.(fs.FileReleaser); ok {
			_ = releaser.Release(ctx)
		}
		return nil, nil, 0, syscall.EIO
	}
	if !shouldTransformPath(childPath, child.applyToAllFile) {
		return inode, rawHandle, fuseFlags, 0
	}
	if releaser, ok := rawHandle.(fs.FileReleaser); ok {
		_ = releaser.Release(ctx)
	}
	h := newTransFileHandle(childPath, childRel, flags, child.pipeline, true, child.applyToAllFile, nil)
	return inode, h, fuseFlags | fuse.FOPEN_DIRECT_IO, 0
}

func (n *TransNode) realPath() string {
	rel := n.relPath()
	return filepath.Join(n.RootData.Path, rel)
}

func (n *TransNode) relPath() string {
	if n.virtualRelPath != "" {
		return cleanRelPath(n.virtualRelPath)
	}
	rel := n.Path(n.Root())
	if rel == "." || rel == "/" {
		return ""
	}
	return cleanRelPath(rel)
}

func (n *TransNode) childRel(name string) string {
	return cleanRelPath(path.Join(n.relPath(), name))
}

func (n *TransNode) newVirtualChild(rel string) *TransNode {
	return &TransNode{
		LoopbackNode:   fs.LoopbackNode{RootData: n.RootData},
		pipeline:       n.pipeline,
		applyToAllFile: n.applyToAllFile,
		overlay:        n.overlay,
		virtualRelPath: rel,
	}
}

type transFileHandle struct {
	path           string
	relPath        string
	flags          uint32
	pipeline       *transform.Pipeline
	writable       bool
	created        bool
	applyTransform bool
	overlay        *OverlayAdapter

	mu        sync.Mutex
	prepared  bool
	dirty     bool
	transform bool
	buf       []byte
}

func newTransFileHandle(path, relPath string, flags uint32, p *transform.Pipeline, created bool, applyToAll bool, overlay *OverlayAdapter) *transFileHandle {
	writable := flags&syscall.O_WRONLY != 0 || flags&syscall.O_RDWR != 0
	return &transFileHandle{
		path:           path,
		relPath:        cleanRelPath(relPath),
		flags:          flags,
		pipeline:       p,
		writable:       writable,
		created:        created,
		applyTransform: shouldTransformPath(path, applyToAll),
		overlay:        overlay,
	}
}

var _ = (fs.FileReader)((*transFileHandle)(nil))

func (h *transFileHandle) Read(_ context.Context, dest []byte, off int64) (result fuse.ReadResult, errno syscall.Errno) {
	defer recoverAsErrno(h.path, "read", &errno)
	h.mu.Lock()
	defer h.mu.Unlock()
	view, errNo := h.currentViewLocked()
	if errNo != 0 {
		return nil, errNo
	}
	if off >= int64(len(view)) {
		return fuse.ReadResultData(nil), 0
	}
	end := int(off) + len(dest)
	if end > len(view) {
		end = len(view)
	}
	return fuse.ReadResultData(view[off:end]), 0
}

var _ = (fs.FileWriter)((*transFileHandle)(nil))

func (h *transFileHandle) Write(_ context.Context, data []byte, off int64) (written uint32, errno syscall.Errno) {
	defer recoverAsErrno(h.path, "write", &errno)
	if !h.writable {
		return 0, syscall.EPERM
	}
	if len(data) == 0 {
		return 0, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if errNo := h.prepareWriteLocked(); errNo != 0 {
		return 0, errNo
	}
	if off < 0 {
		return 0, syscall.EINVAL
	}
	need := int(off) + len(data)
	if need > len(h.buf) {
		n := make([]byte, need)
		copy(n, h.buf)
		h.buf = n
	}
	copy(h.buf[off:], data)
	h.dirty = true
	return uint32(len(data)), 0
}

var _ = (fs.FileFlusher)((*transFileHandle)(nil))

func (h *transFileHandle) Flush(_ context.Context) (errno syscall.Errno) {
	defer recoverAsErrno(h.path, "flush", &errno)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commitLocked()
}

var _ = (fs.FileReleaser)((*transFileHandle)(nil))

func (h *transFileHandle) Release(_ context.Context) (errno syscall.Errno) {
	defer recoverAsErrno(h.path, "release", &errno)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commitLocked()
}

func (h *transFileHandle) currentViewLocked() ([]byte, syscall.Errno) {
	if h.prepared {
		return append([]byte(nil), h.buf...), 0
	}
	raw, err := h.readRaw()
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, 0
		}
		return nil, fs.ToErrno(err)
	}
	if h.applyTransform {
		transformed, err := h.pipeline.Serve(h.middlewareContext(), raw)
		if err != nil {
			return nil, middlewareErrno(err)
		}
		return transformed, 0
	}
	return raw, 0
}

func (h *transFileHandle) prepareWriteLocked() syscall.Errno {
	if h.prepared {
		return 0
	}
	raw, err := h.readRaw()
	if err != nil {
		if !os.IsNotExist(err) {
			return fs.ToErrno(err)
		}
		raw = []byte{}
	}
	h.transform = h.applyTransform
	if h.transform {
		transformed, err := h.pipeline.Serve(h.middlewareContext(), raw)
		if err != nil {
			return middlewareErrno(err)
		}
		h.buf = transformed
	} else {
		h.buf = raw
	}
	if h.flags&syscall.O_TRUNC != 0 || h.created {
		h.buf = []byte{}
	}
	h.prepared = true
	return 0
}

func (h *transFileHandle) commitLocked() syscall.Errno {
	if !h.dirty {
		return 0
	}
	payload := h.buf
	if h.transform {
		decoded, err := h.pipeline.Commit(h.middlewareContext(), payload)
		if err != nil {
			return middlewareErrno(err)
		}
		payload = decoded
	}
	if h.overlay != nil {
		if err := h.overlay.WriteFile(h.relPath, payload, 0o644); err != nil {
			return overlayErrno(err)
		}
	} else {
		if err := os.WriteFile(h.path, payload, 0o644); err != nil {
			return fs.ToErrno(err)
		}
	}
	h.dirty = false
	return 0
}

func (h *transFileHandle) middlewareContext() transform.Context {
	info, _ := h.statRaw()
	return transform.Context{
		Path: h.path,
		Info: info,
	}
}

func (h *transFileHandle) readRaw() ([]byte, error) {
	if h.overlay != nil {
		return h.overlay.ReadFile(h.relPath)
	}
	return os.ReadFile(h.path)
}

func (h *transFileHandle) statRaw() (os.FileInfo, error) {
	if h.overlay != nil {
		return h.overlay.Stat(h.relPath)
	}
	return os.Stat(h.path)
}

func middlewareErrno(err error) syscall.Errno {
	var rejected *transform.RejectedError
	if errors.As(err, &rejected) {
		return syscall.EACCES
	}
	return syscall.EINVAL
}

func recoverAsErrno(path, op string, errno *syscall.Errno) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "llmfs recovered panic during %s on %s: %v\n%s", op, path, r, debug.Stack())
		if errno != nil {
			*errno = syscall.EIO
		}
	}
}

func shouldTransformPath(path string, applyToAll bool) bool {
	if !applyToAll {
		return false
	}
	name := strings.ToLower(filepath.Base(path))
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, ".#") {
		return false
	}
	if strings.HasSuffix(name, "~") {
		return false
	}
	for _, suffix := range []string{".swp", ".swo", ".swx", ".tmp", ".temp", ".lock", ".lck", ".bak"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}
