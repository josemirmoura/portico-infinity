package securefs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const DefaultMaxBytes int64 = 8 << 20
const MaxCopyBytes int64 = 512 << 20

type Manager struct {
	readRoots  []string
	writeRoots []string
	maxBytes   int64
	hostRoot   string
	// Test seam for deterministic fchown failure. Nil uses the actual syscall.
	mkdirChown func(*os.File, int, int) error
}

func New(readRoots, writeRoots []string, maxBytes int64) (*Manager, error) {
	return NewWithHostRoot(readRoots, writeRoots, maxBytes, "")
}

func NewWithHostRoot(readRoots, writeRoots []string, maxBytes int64, hostRoot string) (*Manager, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	normalize := func(in []string) ([]string, error) {
		out := make([]string, 0, len(in))
		for _, r := range in {
			abs, err := filepath.Abs(r)
			if err != nil {
				return nil, err
			}
			out = append(out, filepath.Clean(abs))
		}
		return out, nil
	}
	rr, err := normalize(readRoots)
	if err != nil {
		return nil, err
	}
	wr, err := normalize(writeRoots)
	if err != nil {
		return nil, err
	}
	if hostRoot != "" {
		if !filepath.IsAbs(hostRoot) {
			return nil, errors.New("host root must be absolute")
		}
		hostRoot = filepath.Clean(hostRoot)
		if hostRoot == "/" {
			hostRoot = ""
		}
	}
	m := &Manager{readRoots: rr, writeRoots: wr, maxBytes: maxBytes, hostRoot: hostRoot}
	seen := make(map[string]struct{}, len(rr)+len(wr))
	for _, configuredRoot := range append(append([]string(nil), rr...), wr...) {
		if _, ok := seen[configuredRoot]; ok {
			continue
		}
		seen[configuredRoot] = struct{}{}
		if _, err := nearestExistingDir(m.physical(configuredRoot)); err != nil {
			return nil, fmt.Errorf("unsafe authorized root %q: %w", configuredRoot, err)
		}
	}
	return m, nil
}

func (m *Manager) physical(canonical string) string {
	if m.hostRoot == "" {
		return canonical
	}
	clean := filepath.Clean(canonical)
	if clean == "/" {
		return m.hostRoot
	}
	return filepath.Join(m.hostRoot, strings.TrimPrefix(clean, string(filepath.Separator)))
}

func rejectTraversal(p string) error {
	if p == "" || !filepath.IsAbs(p) {
		return errors.New("path must be absolute")
	}
	for _, part := range strings.FieldsFunc(filepath.ToSlash(p), func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return errors.New("path traversal is forbidden")
		}
	}
	return nil
}

func selectRoot(roots []string, target string) (root, rel string, err error) {
	if err := rejectTraversal(target); err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	abs = filepath.Clean(abs)
	for _, candidate := range roots {
		r, err := filepath.Rel(candidate, abs)
		if err != nil || r == "." {
			if err == nil && r == "." {
				return candidate, ".", nil
			}
			continue
		}
		if r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
			continue
		}
		return candidate, r, nil
	}
	return "", "", errors.New("path outside authorized roots")
}


func selectAuthorizedRoot(roots []string, target string) (string, error) {
	if err := rejectTraversal(target); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	for _, candidate := range roots {
		rel, err := filepath.Rel(candidate, abs)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return candidate, nil
		}
	}
	return "", errors.New("path outside authorized roots")
}

func nearestExistingDir(p string) (string, error) {
	cur := filepath.Clean(p)
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("authorized path ancestor is a symlink: %s", cur)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("authorized path ancestor is not a directory: %s", cur)
			}
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			if filepath.Clean(resolved) != cur {
				return "", fmt.Errorf("authorized path ancestor resolves through symlink: %s", cur)
			}
			return cur, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing directory ancestor for %s", p)
		}
		cur = parent
	}
}

// MkdirAll creates an authorized directory tree while rejecting traversal and
// symlink components. It can create the configured writable root itself when
// that root does not yet exist.
func (m *Manager) MkdirAll(dirname string, perm os.FileMode) error {
	allowedRoot, err := selectAuthorizedRoot(m.writeRoots, dirname)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(dirname)
	if err != nil {
		return err
	}
	target = filepath.Clean(target)

	physicalAllowedRoot := m.physical(allowedRoot)
	physicalTarget := m.physical(target)
	anchor, err := nearestExistingDir(physicalAllowedRoot)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(anchor, physicalTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("directory creation escaped authorized anchor")
	}

	root, err := openRootNoSymlink(anchor)
	if err != nil {
		return err
	}
	defer root.Close()

	current := "."
	for _, part := range strings.FieldsFunc(filepath.ToSlash(rel), func(r rune) bool { return r == '/' }) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return errors.New("path traversal is forbidden")
		}
		if current == "." {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := root.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symlink component: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("path component is not a directory: %s", current)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// New directories must remain editable by the authorized host owner,
		// not silently become root-owned when the Broker runs as root.
		parentInfo, err := root.Stat(filepath.Dir(current))
		if err != nil {
			return err
		}
		parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("cannot verify parent owner for new directory")
		}
		if err := root.Mkdir(current, perm.Perm()); err != nil {
			return err
		}
		dirHandle, err := root.OpenFile(current, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("created directory %q but cannot open for owner verification; MANUAL RECOVERY may be required: %w", filepath.Join(anchor, current), err)
		}
		var ownerErr error
		if m.mkdirChown != nil {
			ownerErr = m.mkdirChown(dirHandle, int(parentStat.Uid), int(parentStat.Gid))
		} else {
			ownerErr = dirHandle.Chown(int(parentStat.Uid), int(parentStat.Gid))
		}
		if ownerErr != nil {
			// Never unlink by name after fchown fails. Another process can
			// replace current between an inode check and root.Remove(current);
			// removal would then delete the other process's directory.
			// Leave the created directory for supervised recovery, even when
			// it is empty. Do not claim cleanup without an atomic guarantee.
			_ = dirHandle.Close()
			return fmt.Errorf("created directory %q: fchown failed; a root-owned partial directory may remain; MANUAL RECOVERY required (no automatic deletion): %w", filepath.Join(anchor, current), ownerErr)
		}
		if err := dirHandle.Close(); err != nil {
			return fmt.Errorf("created directory %q: close failed after chown, inspect directory before retry: %w", filepath.Join(anchor, current), err)
		}
	}
	return nil
}


type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Mode    string    `json:"mode"`
	Perm    uint32    `json:"perm"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
	Symlink bool      `json:"symlink"`
}

func entryFromInfo(fullPath string, info os.FileInfo) Entry {
	return Entry{
		Name: info.Name(), Path: fullPath, Mode: info.Mode().String(),
		Perm: uint32(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime(),
		IsDir: info.IsDir(), Symlink: info.Mode()&os.ModeSymlink != 0,
	}
}

func (m *Manager) Stat(filename string, write bool) (Entry, error) {
	roots := m.readRoots
	if write {
		roots = m.writeRoots
	}
	rootPath, rel, err := selectRoot(roots, filename)
	if err != nil {
		return Entry{}, err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(filepath.Clean(filename), info), nil
}

func (m *Manager) List(dirname string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rootPath, rel, err := selectRoot(m.readRoots, dirname)
	if err != nil {
		return nil, err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	infos, err := f.Readdir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(infos) > limit {
		infos = infos[:limit]
	}
	out := make([]Entry, 0, len(infos))
	base := filepath.Clean(dirname)
	for _, info := range infos {
		out = append(out, entryFromInfo(filepath.Join(base, info.Name()), info))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) Hash(filename string) (string, error) {
	rootPath, rel, err := selectRoot(m.readRoots, filename)
	if err != nil {
		return "", err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("hash requires a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Manager) Patch(filename, oldText, newText, expectedSHA256 string) (string, error) {
	data, err := m.ReadFile(filename)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	current := hex.EncodeToString(sum[:])
	if expectedSHA256 != "" && !strings.EqualFold(expectedSHA256, current) {
		return "", fmt.Errorf("precondition failed: sha256 is %s", current)
	}
	if oldText == "" {
		return "", errors.New("old_text cannot be empty")
	}
	count := strings.Count(string(data), oldText)
	if count != 1 {
		return "", fmt.Errorf("old_text must occur exactly once; found %d", count)
	}
	next := strings.Replace(string(data), oldText, newText, 1)
	if err := m.WriteFileAtomic(filename, []byte(next)); err != nil {
		return "", err
	}
	nextSum := sha256.Sum256([]byte(next))
	return hex.EncodeToString(nextSum[:]), nil
}

func (m *Manager) CopyFile(src, dst string) error {
	srcRootPath, srcRel, err := selectRoot(m.readRoots, src)
	if err != nil {
		return err
	}
	dstRootPath, dstRel, err := selectRoot(m.writeRoots, dst)
	if err != nil {
		return err
	}
	if dstRel == "." {
		return errors.New("destination cannot be an authorized root directory")
	}
	srcRoot, err := openRootNoSymlink(m.physical(srcRootPath))
	if err != nil {
		return err
	}
	defer srcRoot.Close()
	in, err := srcRoot.Open(srcRel)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("copy currently supports regular files only")
	}
	if info.Size() > MaxCopyBytes {
		return fmt.Errorf("file exceeds copy limit of %d bytes", MaxCopyBytes)
	}

	dstRoot, err := openRootNoSymlink(m.physical(dstRootPath))
	if err != nil {
		return err
	}
	defer dstRoot.Close()
	dir := filepath.Dir(dstRel)
	base := filepath.Base(dstRel)
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := "." + base + ".copy-" + hex.EncodeToString(nonce[:])
	if dir != "." {
		tmp = filepath.Join(dir, tmp)
	}
	// Keep destination owner's UID/GID on replacement, or inherit the
	// verified destination-directory owner for newly copied files.
	uid, gid, err := atomicWriteOwner(dstRoot, dir, dstRel)
	if err != nil {
		return err
	}
	out, err := dstRoot.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = out.Close()
		if cleanup {
			_ = dstRoot.Remove(tmp)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chown(uid, gid); err != nil {
		return fmt.Errorf("cannot apply verified destination owner before copy rename: %w", err)
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := dstRoot.Rename(tmp, dstRel); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (m *Manager) Move(src, dst string) error {
	srcRootPath, srcRel, err := selectRoot(m.writeRoots, src)
	if err != nil {
		return err
	}
	dstRootPath, dstRel, err := selectRoot(m.writeRoots, dst)
	if err != nil {
		return err
	}
	if srcRel == "." || dstRel == "." {
		return errors.New("cannot move an authorized root directory")
	}
	if srcRootPath == dstRootPath {
		root, err := openRootNoSymlink(m.physical(srcRootPath))
		if err != nil {
			return err
		}
		defer root.Close()
		return root.Rename(srcRel, dstRel)
	}
	if err := m.CopyFile(src, dst); err != nil {
		return err
	}
	return m.Remove(src, false)
}

// removeTree streams bounded batches instead of loading every entry into
// memory. Each batch reopens the directory from the beginning: removing names
// while retaining a getdents cursor can otherwise skip entries. The inode is
// rechecked between batches to fail closed if the target directory is swapped.
//
// This operation is NOT atomic. If an external process changes the tree during
// deletion, it may fail after some items have been removed. The caller must
// report an uncertain/partial outcome rather than claim rollback.
func removeTree(root *os.Root, rel string, baseFD int) error {
	if err := ensureNoMountCross(baseFD, rel); err != nil {
		return fmt.Errorf("unsafe recursive removal target: %w", err)
	}
	info, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return root.Remove(rel)
	}
	initial, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot verify directory inode before recursive removal")
	}
	// Directory cursors may skip entries if those entries are removed while
	// enumerating. Sweep a directory with bounded 256-name batches and repeat
	// only if entries remain. Unlike reopening per batch, this avoids quadratic
	// scans when deleting model/dataset directories with >50,000 files.
	for {
		if err := ensureRemoveDirectoryIdentity(root, rel, baseFD, initial); err != nil {
			return err
		}
		dir, err := root.Open(rel)
		if err != nil {
			return err
		}
		opened, statErr := dir.Stat()
		if statErr != nil {
			_ = dir.Close()
			return statErr
		}
		openedID, ok := opened.Sys().(*syscall.Stat_t)
		if !ok || !opened.IsDir() || openedID.Dev != initial.Dev || openedID.Ino != initial.Ino {
			_ = dir.Close()
			return fmt.Errorf("directory identity changed while opening: %s", rel)
		}
		removed := 0
		for {
			names, readErr := dir.Readdirnames(256)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				_ = dir.Close()
				return readErr
			}
			if len(names) == 0 {
				if !errors.Is(readErr, io.EOF) {
					_ = dir.Close()
					return fmt.Errorf("directory enumeration made no progress: %s", rel)
				}
				break
			}
			if err := ensureRemoveDirectoryIdentity(root, rel, baseFD, initial); err != nil {
				_ = dir.Close()
				return err
			}
			for _, name := range names {
				child := filepath.Join(rel, name)
				if err := removeTree(root, child, baseFD); err != nil {
					_ = dir.Close()
					return fmt.Errorf("recursive removal may be partial at %s: %w", child, err)
				}
				removed++
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
		}
		if err := dir.Close(); err != nil {
			return err
		}
		if removed == 0 {
			if err := ensureRemoveDirectoryIdentity(root, rel, baseFD, initial); err != nil {
				return err
			}
			// ENOTEMPTY means a concurrent writer inserted a new entry. Fail
			// explicitly and leave the operation journal unreconciled.
			return root.Remove(rel)
		}
	}
}

func ensureRemoveDirectoryIdentity(root *os.Root, rel string, baseFD int, expected *syscall.Stat_t) error {
	if err := ensureNoMountCross(baseFD, rel); err != nil {
		return fmt.Errorf("mount boundary changed during removal of %s: %w", rel, err)
	}
	now, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	actual, ok := now.Sys().(*syscall.Stat_t)
	if !ok || !now.IsDir() || now.Mode()&os.ModeSymlink != 0 ||
		actual.Dev != expected.Dev || actual.Ino != expected.Ino {
		return fmt.Errorf("directory identity changed during recursive removal: %s", rel)
	}
	return nil
}

func (m *Manager) Remove(target string, recursive bool) error {
	rootPath, rel, err := selectRoot(m.writeRoots, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return errors.New("refusing to delete an authorized root itself")
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if info.IsDir() && !recursive {
		return root.Remove(rel)
	}
	if recursive {
		base, err := root.Open(".")
		if err != nil {
			return err
		}
		defer base.Close()
		return removeTree(root, rel, int(base.Fd()))
	}
	return root.Remove(rel)
}

func (m *Manager) Chmod(target string, mode os.FileMode) error {
	if mode.Perm() != mode {
		return errors.New("chmod mode must contain permission bits only")
	}
	rootPath, rel, err := selectRoot(m.writeRoots, target)
	if err != nil {
		return err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(mode)
}

func (m *Manager) Chown(target string, uid, gid int) error {
	if uid < -1 || gid < -1 {
		return errors.New("uid/gid must be -1 or non-negative")
	}
	rootPath, rel, err := selectRoot(m.writeRoots, target)
	if err != nil {
		return err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chown(uid, gid)
}

func (m *Manager) ReadFile(filename string) ([]byte, error) {
	rootPath, rel, err := selectRoot(m.readRoots, filename)
	if err != nil {
		return nil, err
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return nil, err
	}
	defer root.Close()

	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	lr := io.LimitReader(f, m.maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > m.maxBytes {
		return nil, fmt.Errorf("file exceeds %d-byte limit", m.maxBytes)
	}
	return data, nil
}

// atomicWriteOwner chooses the effective host owner for a new atomic file.
// Existing files keep their original UID/GID. For new files, only the
// directory owner is used. Ownership is never supplied by the model or by
// any MCP argument. All metadata lookups remain under os.Root confinement.
func atomicWriteOwner(root *os.Root, dir, rel string) (int, int, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return 0, 0, err
		}
		info, err = root.Stat(dir)
		if err != nil {
			return 0, 0, err
		}
		if !info.IsDir() {
			return 0, 0, errors.New("atomic write parent must be a directory")
		}
	} else if !info.Mode().IsRegular() {
		return 0, 0, errors.New("atomic write target must be a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("cannot verify host UID/GID for atomic write")
	}
	return int(stat.Uid), int(stat.Gid), nil
}

func (m *Manager) WriteFileAtomic(filename string, data []byte) error {
	if int64(len(data)) > m.maxBytes {
		return fmt.Errorf("content exceeds %d-byte limit", m.maxBytes)
	}
	rootPath, rel, err := selectRoot(m.writeRoots, filename)
	if err != nil {
		return err
	}
	if rel == "." {
		return errors.New("cannot overwrite root directory")
	}
	root, err := openRootNoSymlink(m.physical(rootPath))
	if err != nil {
		return err
	}
	defer root.Close()

	dir := filepath.Dir(rel)
	uid, gid, err := atomicWriteOwner(root, dir, rel)
	if err != nil {
		return err
	}
	base := filepath.Base(rel)
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmpBase := "." + base + ".tmp-" + hex.EncodeToString(nonce[:])
	tmpRel := tmpBase
	if dir != "." {
		tmpRel = filepath.Join(dir, tmpBase)
	}

	f, err := root.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = root.Remove(tmpRel)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return err
	}
	// Chown the exclusive temporary inode before atomic rename. If the Broker
	// cannot set the verified host owner, fail closed without replacing target.
	if err := f.Chown(uid, gid); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := root.Rename(tmpRel, rel); err != nil {
		return err
	}
	cleanup = false
	return nil
}
