package mountfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
)

const (
	overlayIndexVersion = 1
	defaultFileMode     = 0o644
	defaultDirMode      = 0o755
)

type overlayKind string

const (
	overlayKindFile    overlayKind = "file"
	overlayKindDir     overlayKind = "dir"
	overlayKindSymlink overlayKind = "symlink"
	overlayKindDelete  overlayKind = "delete"
)

type overlayEntry struct {
	Kind       overlayKind `json:"kind"`
	ObjectSHA  string      `json:"object_sha,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
	Mode       uint32      `json:"mode,omitempty"`
	Size       int64       `json:"size,omitempty"`
	ModTimeUTC int64       `json:"mod_time_utc,omitempty"`
	BaseSHA256 string      `json:"base_sha256,omitempty"`
}

type overlayIndex struct {
	Version int                     `json:"version"`
	Entries map[string]overlayEntry `json:"entries"`
}

type OverlayDirEntry struct {
	Name  string
	IsDir bool
}

type OverlayAdapter struct {
	baseRoot    string
	overlayRoot string
	sessionID   string
	sessionRoot string
	objectsDir  string
	indexPath   string

	mu    sync.RWMutex
	index overlayIndex
}

func NewOverlayAdapter(baseRoot, overlaysRoot, sessionID string) (*OverlayAdapter, error) {
	baseAbs, err := filepath.Abs(baseRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(overlaysRoot) == "" {
		overlaysRoot = ".llmfs/overlays"
	}
	overlaysAbs := overlaysRoot
	if !filepath.IsAbs(overlaysAbs) {
		overlaysAbs = filepath.Join(baseAbs, overlaysRoot)
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = fmt.Sprintf("mnt-%d", time.Now().UnixNano())
	}
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("invalid overlay session id")
	}

	sessionRoot := filepath.Join(overlaysAbs, sessionID)
	objectsDir := filepath.Join(sessionRoot, "objects")
	indexPath := filepath.Join(sessionRoot, "index.json")
	if err := os.MkdirAll(objectsDir, 0o755); err != nil {
		return nil, err
	}

	ad := &OverlayAdapter{
		baseRoot:    baseAbs,
		overlayRoot: overlaysAbs,
		sessionID:   sessionID,
		sessionRoot: sessionRoot,
		objectsDir:  objectsDir,
		indexPath:   indexPath,
		index: overlayIndex{
			Version: overlayIndexVersion,
			Entries: map[string]overlayEntry{},
		},
	}
	if err := ad.loadIndex(); err != nil {
		return nil, err
	}
	return ad, nil
}

func (a *OverlayAdapter) SessionID() string {
	return a.sessionID
}

func (a *OverlayAdapter) SessionRoot() string {
	return a.sessionRoot
}

func (a *OverlayAdapter) OverlayRoot() string {
	return a.overlayRoot
}

func (a *OverlayAdapter) Exists(rel string) (bool, bool, error) {
	rel = cleanRelPath(rel)
	if rel == "" {
		return true, true, nil
	}

	a.mu.RLock()
	e, ok := a.index.Entries[rel]
	a.mu.RUnlock()
	if ok {
		switch e.Kind {
		case overlayKindDelete:
			return false, false, nil
		case overlayKindDir:
			return true, true, nil
		case overlayKindSymlink:
			return true, false, nil
		case overlayKindFile:
			return true, false, nil
		}
	}

	fi, err := os.Lstat(a.basePath(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, fi.IsDir(), nil
}

func (a *OverlayAdapter) Stat(rel string) (os.FileInfo, error) {
	rel = cleanRelPath(rel)
	if rel == "" {
		return os.Stat(a.baseRoot)
	}

	a.mu.RLock()
	e, ok := a.index.Entries[rel]
	a.mu.RUnlock()
	if ok {
		switch e.Kind {
		case overlayKindDelete:
			return nil, os.ErrNotExist
		case overlayKindDir:
			return virtualFileInfo{
				name:    filepath.Base(rel),
				size:    0,
				mode:    os.ModeDir | os.FileMode(orDefaultPerm(e.Mode, defaultDirMode)),
				modTime: unixOrNow(e.ModTimeUTC),
				isDir:   true,
			}, nil
		case overlayKindSymlink:
			return virtualFileInfo{
				name:    filepath.Base(rel),
				size:    int64(len(e.LinkTarget)),
				mode:    os.ModeSymlink | os.FileMode(orDefaultPerm(e.Mode, 0o777)),
				modTime: unixOrNow(e.ModTimeUTC),
				isDir:   false,
			}, nil
		case overlayKindFile:
			return virtualFileInfo{
				name:    filepath.Base(rel),
				size:    e.Size,
				mode:    os.FileMode(orDefaultPerm(e.Mode, defaultFileMode)),
				modTime: unixOrNow(e.ModTimeUTC),
				isDir:   false,
			}, nil
		}
	}
	return os.Lstat(a.basePath(rel))
}

func (a *OverlayAdapter) ReadFile(rel string) ([]byte, error) {
	rel = cleanRelPath(rel)
	if rel == "" {
		return nil, syscall.EISDIR
	}

	a.mu.RLock()
	e, ok := a.index.Entries[rel]
	a.mu.RUnlock()
	if ok {
		switch e.Kind {
		case overlayKindDelete:
			return nil, os.ErrNotExist
		case overlayKindDir:
			return nil, syscall.EISDIR
		case overlayKindSymlink:
			targetRel := resolveSymlinkRel(rel, e.LinkTarget)
			if targetRel == "" {
				return nil, syscall.ENOENT
			}
			return a.ReadFile(targetRel)
		case overlayKindFile:
			return os.ReadFile(a.objectPath(e.ObjectSHA))
		}
	}
	return os.ReadFile(a.basePath(rel))
}

func (a *OverlayAdapter) WriteFile(rel string, data []byte, mode os.FileMode) error {
	rel = cleanRelPath(rel)
	if rel == "" {
		return syscall.EISDIR
	}
	if mode.Perm() == 0 {
		mode = defaultFileMode
	}

	sha := sha256Hex(data)
	objPath := a.objectPath(sha)
	if _, err := os.Stat(objPath); os.IsNotExist(err) {
		tmp := objPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, objPath); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	} else if err != nil {
		return err
	}

	baseSHA, _ := fileSHA256(a.basePath(rel))
	a.mu.Lock()
	a.index.Entries[rel] = overlayEntry{
		Kind:       overlayKindFile,
		ObjectSHA:  sha,
		Mode:       uint32(mode.Perm()),
		Size:       int64(len(data)),
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) WriteSymlink(rel, target string) error {
	rel = cleanRelPath(rel)
	if rel == "" {
		return syscall.EINVAL
	}
	baseSHA, _ := fileSHA256(a.basePath(rel))
	a.mu.Lock()
	a.index.Entries[rel] = overlayEntry{
		Kind:       overlayKindSymlink,
		LinkTarget: target,
		Mode:       0o777,
		Size:       int64(len(target)),
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) Readlink(rel string) (string, error) {
	rel = cleanRelPath(rel)
	a.mu.RLock()
	e, ok := a.index.Entries[rel]
	a.mu.RUnlock()
	if ok {
		switch e.Kind {
		case overlayKindDelete:
			return "", os.ErrNotExist
		case overlayKindSymlink:
			return e.LinkTarget, nil
		default:
			return "", syscall.EINVAL
		}
	}
	return os.Readlink(a.basePath(rel))
}

func (a *OverlayAdapter) Chmod(rel string, mode os.FileMode) error {
	rel = cleanRelPath(rel)
	fi, err := a.Stat(rel)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode().IsDir():
		baseSHA, _ := fileSHA256(a.basePath(rel))
		a.mu.Lock()
		a.index.Entries[rel] = overlayEntry{
			Kind:       overlayKindDir,
			Mode:       uint32(mode.Perm()),
			Size:       0,
			ModTimeUTC: time.Now().UTC().Unix(),
			BaseSHA256: baseSHA,
		}
		a.mu.Unlock()
		return a.persist()
	case fi.Mode()&os.ModeSymlink != 0:
		a.mu.Lock()
		e, ok := a.index.Entries[rel]
		if ok && e.Kind == overlayKindSymlink {
			e.Mode = uint32(mode.Perm())
			e.ModTimeUTC = time.Now().UTC().Unix()
			a.index.Entries[rel] = e
			a.mu.Unlock()
			return a.persist()
		}
		a.mu.Unlock()
		return syscall.ENOTSUP
	default:
		b, err := a.ReadFile(rel)
		if err != nil {
			return err
		}
		return a.WriteFile(rel, b, mode.Perm())
	}
}

func (a *OverlayAdapter) Truncate(rel string, size int64) error {
	rel = cleanRelPath(rel)
	if size < 0 {
		return syscall.EINVAL
	}
	fi, err := a.Stat(rel)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return syscall.EISDIR
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return syscall.ENOTSUP
	}
	b, err := a.ReadFile(rel)
	if err != nil {
		return err
	}
	switch {
	case int64(len(b)) > size:
		b = b[:size]
	case int64(len(b)) < size:
		nb := make([]byte, size)
		copy(nb, b)
		b = nb
	}
	return a.WriteFile(rel, b, fi.Mode().Perm())
}

func (a *OverlayAdapter) SetMtime(rel string, mtime time.Time) error {
	rel = cleanRelPath(rel)
	a.mu.Lock()
	e, ok := a.index.Entries[rel]
	if ok {
		e.ModTimeUTC = mtime.UTC().Unix()
		a.index.Entries[rel] = e
		a.mu.Unlock()
		return a.persist()
	}
	a.mu.Unlock()

	fi, err := a.Stat(rel)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		baseSHA, _ := fileSHA256(a.basePath(rel))
		a.mu.Lock()
		a.index.Entries[rel] = overlayEntry{
			Kind:       overlayKindDir,
			Mode:       uint32(fi.Mode().Perm()),
			Size:       0,
			ModTimeUTC: mtime.UTC().Unix(),
			BaseSHA256: baseSHA,
		}
		a.mu.Unlock()
		return a.persist()
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := a.Readlink(rel)
		if err != nil {
			return err
		}
		baseSHA, _ := fileSHA256(a.basePath(rel))
		a.mu.Lock()
		a.index.Entries[rel] = overlayEntry{
			Kind:       overlayKindSymlink,
			LinkTarget: target,
			Mode:       uint32(fi.Mode().Perm()),
			Size:       int64(len(target)),
			ModTimeUTC: mtime.UTC().Unix(),
			BaseSHA256: baseSHA,
		}
		a.mu.Unlock()
		return a.persist()
	}
	b, err := a.ReadFile(rel)
	if err != nil {
		return err
	}
	if err := a.WriteFile(rel, b, fi.Mode().Perm()); err != nil {
		return err
	}
	a.mu.Lock()
	e = a.index.Entries[rel]
	e.ModTimeUTC = mtime.UTC().Unix()
	a.index.Entries[rel] = e
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) CreateEmptyFile(rel string, mode os.FileMode) error {
	exists, isDir, err := a.Exists(rel)
	if err != nil {
		return err
	}
	if exists {
		if isDir {
			return syscall.EISDIR
		}
		return os.ErrExist
	}
	return a.WriteFile(rel, []byte{}, mode)
}

func (a *OverlayAdapter) Mkdir(rel string, mode os.FileMode) error {
	rel = cleanRelPath(rel)
	if rel == "" {
		return os.ErrExist
	}
	exists, _, err := a.Exists(rel)
	if err != nil {
		return err
	}
	if exists {
		return os.ErrExist
	}
	if mode.Perm() == 0 {
		mode = defaultDirMode
	}

	baseSHA, _ := fileSHA256(a.basePath(rel))
	a.mu.Lock()
	a.index.Entries[rel] = overlayEntry{
		Kind:       overlayKindDir,
		Mode:       uint32(mode.Perm()),
		Size:       0,
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) DeleteFile(rel string) error {
	rel = cleanRelPath(rel)
	exists, isDir, err := a.Exists(rel)
	if err != nil {
		return err
	}
	if !exists {
		return os.ErrNotExist
	}
	if isDir {
		return syscall.EISDIR
	}

	baseSHA, _ := fileSHA256(a.basePath(rel))
	a.mu.Lock()
	a.index.Entries[rel] = overlayEntry{
		Kind:       overlayKindDelete,
		Mode:       0,
		Size:       0,
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) RemoveDir(rel string) error {
	rel = cleanRelPath(rel)
	if rel == "" {
		return syscall.EBUSY
	}
	exists, isDir, err := a.Exists(rel)
	if err != nil {
		return err
	}
	if !exists {
		return os.ErrNotExist
	}
	if !isDir {
		return syscall.ENOTDIR
	}
	children, err := a.ReadDir(rel)
	if err != nil {
		return err
	}
	if len(children) != 0 {
		return syscall.ENOTEMPTY
	}

	baseSHA, _ := fileSHA256(a.basePath(rel))
	a.mu.Lock()
	a.index.Entries[rel] = overlayEntry{
		Kind:       overlayKindDelete,
		Mode:       0,
		Size:       0,
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) Rename(oldRel, newRel string) error {
	oldRel = cleanRelPath(oldRel)
	newRel = cleanRelPath(newRel)
	if oldRel == "" || newRel == "" {
		return syscall.EINVAL
	}
	if oldRel == newRel {
		return nil
	}
	oldInfo, err := a.Stat(oldRel)
	if err != nil {
		return err
	}
	oldIsDir := oldInfo.IsDir()

	if exists, newIsDir, err := a.Exists(newRel); err != nil {
		return err
	} else if exists {
		if oldIsDir && !newIsDir {
			return syscall.ENOTDIR
		}
		if !oldIsDir && newIsDir {
			return syscall.EISDIR
		}
		if newIsDir {
			if err := a.RemoveDir(newRel); err != nil {
				return err
			}
		} else {
			if err := a.DeleteFile(newRel); err != nil {
				return err
			}
		}
	}

	if oldIsDir {
		return a.renameDir(oldRel, newRel)
	}
	return a.renameFile(oldRel, newRel)
}

func (a *OverlayAdapter) ReadDir(rel string) ([]OverlayDirEntry, error) {
	rel = cleanRelPath(rel)
	if rel != "" {
		exists, isDir, err := a.Exists(rel)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, os.ErrNotExist
		}
		if !isDir {
			return nil, syscall.ENOTDIR
		}
	}

	result := map[string]OverlayDirEntry{}
	baseDir := a.basePath(rel)
	baseEntries, err := os.ReadDir(baseDir)
	if err == nil {
		for _, de := range baseEntries {
			result[de.Name()] = OverlayDirEntry{Name: de.Name(), IsDir: de.IsDir()}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	a.mu.RLock()
	keys := make([]string, 0, len(a.index.Entries))
	for k := range a.index.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parent := path.Dir(k)
		if parent == "." {
			parent = ""
		}
		if parent != rel {
			continue
		}
		name := path.Base(k)
		e := a.index.Entries[k]
		switch e.Kind {
		case overlayKindDelete:
			delete(result, name)
		case overlayKindDir:
			result[name] = OverlayDirEntry{Name: name, IsDir: true}
		case overlayKindSymlink:
			result[name] = OverlayDirEntry{Name: name, IsDir: false}
		case overlayKindFile:
			result[name] = OverlayDirEntry{Name: name, IsDir: false}
		}
	}
	a.mu.RUnlock()

	names := make([]string, 0, len(result))
	for name := range result {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]OverlayDirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, result[name])
	}
	return out, nil
}

func (a *OverlayAdapter) renameFile(oldRel, newRel string) error {
	data, err := a.ReadFile(oldRel)
	if err != nil {
		return err
	}
	fi, err := a.Stat(oldRel)
	if err != nil {
		return err
	}
	if err := a.WriteFile(newRel, data, fi.Mode().Perm()); err != nil {
		return err
	}
	return a.DeleteFile(oldRel)
}

func (a *OverlayAdapter) renameDir(oldRel, newRel string) error {
	if err := a.cloneTree(oldRel, newRel); err != nil {
		return err
	}
	a.mu.Lock()
	for k := range a.index.Entries {
		if k == oldRel || strings.HasPrefix(k, oldRel+"/") {
			delete(a.index.Entries, k)
		}
	}
	baseSHA, _ := fileSHA256(a.basePath(oldRel))
	a.index.Entries[oldRel] = overlayEntry{
		Kind:       overlayKindDelete,
		ModTimeUTC: time.Now().UTC().Unix(),
		BaseSHA256: baseSHA,
	}
	a.mu.Unlock()
	return a.persist()
}

func (a *OverlayAdapter) cloneTree(oldRel, newRel string) error {
	oldInfo, err := a.Stat(oldRel)
	if err != nil {
		return err
	}
	if !oldInfo.IsDir() {
		return syscall.ENOTDIR
	}
	if err := a.Mkdir(newRel, oldInfo.Mode().Perm()); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	children, err := a.ReadDir(oldRel)
	if err != nil {
		return err
	}
	for _, child := range children {
		src := cleanRelPath(path.Join(oldRel, child.Name))
		dst := cleanRelPath(path.Join(newRel, child.Name))
		fi, err := a.Stat(src)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if err := a.cloneTree(src, dst); err != nil {
				return err
			}
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := a.Readlink(src)
			if err != nil {
				return err
			}
			if err := a.WriteSymlink(dst, target); err != nil {
				return err
			}
			continue
		}
		b, err := a.ReadFile(src)
		if err != nil {
			return err
		}
		if err := a.WriteFile(dst, b, fi.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func (a *OverlayAdapter) loadIndex() error {
	b, err := os.ReadFile(a.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return a.persist()
		}
		return err
	}
	var idx overlayIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return err
	}
	if idx.Version == 0 {
		idx.Version = overlayIndexVersion
	}
	if idx.Entries == nil {
		idx.Entries = map[string]overlayEntry{}
	}
	for k, v := range idx.Entries {
		norm := cleanRelPath(k)
		if norm == "" {
			delete(idx.Entries, k)
			continue
		}
		if norm != k {
			delete(idx.Entries, k)
			idx.Entries[norm] = v
		}
	}
	a.mu.Lock()
	a.index = idx
	a.mu.Unlock()
	return nil
}

func (a *OverlayAdapter) persist() error {
	a.mu.RLock()
	b, err := json.MarshalIndent(a.index, "", "  ")
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := a.indexPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.indexPath)
}

func (a *OverlayAdapter) basePath(rel string) string {
	rel = cleanRelPath(rel)
	if rel == "" {
		return a.baseRoot
	}
	return filepath.Join(a.baseRoot, filepath.FromSlash(rel))
}

func (a *OverlayAdapter) objectPath(sha string) string {
	return filepath.Join(a.objectsDir, sha)
}

type virtualFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (f virtualFileInfo) Name() string       { return f.name }
func (f virtualFileInfo) Size() int64        { return f.size }
func (f virtualFileInfo) Mode() os.FileMode  { return f.mode }
func (f virtualFileInfo) ModTime() time.Time { return f.modTime }
func (f virtualFileInfo) IsDir() bool        { return f.isDir }
func (f virtualFileInfo) Sys() any           { return nil }

func fileSHA256(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cleanRelPath(rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return ""
	}
	rel = filepath.Clean(rel)
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	if rel == "." {
		return ""
	}
	return path.Clean(filepath.ToSlash(rel))
}

func resolveSymlinkRel(fromRel, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	target = filepath.ToSlash(target)
	if strings.HasPrefix(target, "/") {
		return cleanRelPath(strings.TrimPrefix(target, "/"))
	}
	baseDir := path.Dir(cleanRelPath(fromRel))
	if baseDir == "." {
		baseDir = ""
	}
	return cleanRelPath(path.Join(baseDir, target))
}

func sanitizeSessionID(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".")
}

func unixOrNow(sec int64) time.Time {
	if sec <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(sec, 0).UTC()
}

func orDefaultPerm(mode uint32, def os.FileMode) uint32 {
	if mode == 0 {
		return uint32(def.Perm())
	}
	return mode
}

func overlayErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errors.Is(err, os.ErrNotExist) {
		return syscall.ENOENT
	}
	if errors.Is(err, os.ErrExist) {
		return syscall.EEXIST
	}
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		return fs.ToErrno(pe.Err)
	}
	return fs.ToErrno(err)
}
