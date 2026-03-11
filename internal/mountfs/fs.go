package mountfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
}

func newTransNode(rootData *fs.LoopbackRoot, p *transform.Pipeline, applyToAll bool) func(*fs.LoopbackRoot, *fs.Inode, string, *syscall.Stat_t) fs.InodeEmbedder {
	return func(rd *fs.LoopbackRoot, _ *fs.Inode, _ string, _ *syscall.Stat_t) fs.InodeEmbedder {
		return &TransNode{
			LoopbackNode:   fs.LoopbackNode{RootData: rd},
			pipeline:       p,
			applyToAllFile: applyToAll,
		}
	}
}

var _ = (fs.NodeOpener)((*TransNode)(nil))

func (n *TransNode) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	h := newTransFileHandle(n.realPath(), flags, n.pipeline, false, n.applyToAllFile)
	return h, fuse.FOPEN_DIRECT_IO, 0
}

var _ = (fs.NodeCreater)((*TransNode)(nil))

func (n *TransNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	inode, _, fuseFlags, errno := n.LoopbackNode.Create(ctx, name, flags, mode, out)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	child, ok := inode.Operations().(*TransNode)
	if !ok {
		return nil, nil, 0, syscall.EIO
	}
	h := newTransFileHandle(child.realPath(), flags, child.pipeline, true, child.applyToAllFile)
	return inode, h, fuseFlags | fuse.FOPEN_DIRECT_IO, 0
}

func (n *TransNode) realPath() string {
	rel := n.Path(n.Root())
	return filepath.Join(n.RootData.Path, rel)
}

type transFileHandle struct {
	path          string
	flags         uint32
	pipeline      *transform.Pipeline
	writable      bool
	created       bool
	applyToAllRaw bool

	mu        sync.Mutex
	prepared  bool
	dirty     bool
	transform bool
	buf       []byte
}

func newTransFileHandle(path string, flags uint32, p *transform.Pipeline, created bool, applyToAll bool) *transFileHandle {
	writable := flags&syscall.O_WRONLY != 0 || flags&syscall.O_RDWR != 0
	return &transFileHandle{path: path, flags: flags, pipeline: p, writable: writable, created: created, applyToAllRaw: applyToAll}
}

var _ = (fs.FileReader)((*transFileHandle)(nil))

func (h *transFileHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
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

func (h *transFileHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if !h.writable {
		return 0, syscall.EPERM
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

func (h *transFileHandle) Flush(_ context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commitLocked()
}

var _ = (fs.FileReleaser)((*transFileHandle)(nil))

func (h *transFileHandle) Release(_ context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.commitLocked()
}

func (h *transFileHandle) currentViewLocked() ([]byte, syscall.Errno) {
	if h.prepared {
		return append([]byte(nil), h.buf...), 0
	}
	raw, err := os.ReadFile(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, 0
		}
		return nil, fs.ToErrno(err)
	}
	if h.applyToAllRaw {
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
	raw, err := os.ReadFile(h.path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fs.ToErrno(err)
		}
		raw = []byte{}
	}
	h.transform = h.applyToAllRaw
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
	if err := os.WriteFile(h.path, payload, 0o644); err != nil {
		return fs.ToErrno(err)
	}
	h.dirty = false
	return 0
}

func (h *transFileHandle) middlewareContext() transform.Context {
	info, _ := os.Stat(h.path)
	return transform.Context{
		Path: h.path,
		Info: info,
	}
}

func middlewareErrno(err error) syscall.Errno {
	var rejected *transform.RejectedError
	if errors.As(err, &rejected) {
		return syscall.EACCES
	}
	return syscall.EINVAL
}
