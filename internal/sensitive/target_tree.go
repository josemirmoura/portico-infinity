package sensitive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// WalkProtectedSubtree checks only a destructive operation's target, not all
// sibling directories in a broad grant such as /opt. It walks directory FDs
// without following symlinks or crossing bind mounts, streams entries in small
// batches and reports each sensitive path to a caller-supplied access checker.
//
// This is a preflight guard, not a filesystem transaction. The mutating phase
// must still defend against concurrent changes and report partial outcomes.
func WalkProtectedSubtree(hostRoot, target string, allowedRoots []string, visit func(string) error) error {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return errors.New("target must be an absolute normalized path")
	}
	if visit == nil {
		return errors.New("protected subtree visitor is required")
	}
	var selected, relative string
	for _, candidate := range allowedRoots {
		if !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate {
			continue
		}
		rel, err := filepath.Rel(candidate, target)
		if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(candidate) > len(selected) {
			selected, relative = candidate, rel
		}
	}
	if selected == "" {
		return errors.New("target is not below an authorized write root")
	}

	rootFD, err := openAbsoluteDirWithoutSymlinks(hostPath(hostRoot, selected))
	if err != nil {
		return fmt.Errorf("open authorized root: %w", err)
	}
	defer unix.Close(rootFD)

	resolve := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV)
	parentFD, err := unix.Openat2(rootFD, filepath.Dir(relative), directoryOpenHow(resolve))
	if err != nil {
		return fmt.Errorf("unsafe target ancestor: %w", err)
	}
	defer unix.Close(parentFD)

	name := filepath.Base(relative)
	var st unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := inspectTreeEntry(parentFD, name, target, &st, resolve, visit); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil
	}

	fd, err := unix.Openat2(parentFD, name, directoryOpenHow(resolve))
	if err != nil {
		return fmt.Errorf("unsafe target directory: %w", err)
	}
	return walkProtectedDirectory(fd, target, resolve, visit)
}

func directoryOpenHow(resolve uint64) *unix.OpenHow {
	return &unix.OpenHow{
		Flags: uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: resolve,
	}
}

// fd ownership transfers to this function, including when the scan fails.
func walkProtectedDirectory(fd int, canonical string, resolve uint64, visit func(string) error) error {
	dir := os.NewFile(uintptr(fd), canonical)
	if dir == nil {
		_ = unix.Close(fd)
		return errors.New("cannot open directory handle")
	}
	defer dir.Close()

	for {
		names, err := dir.Readdirnames(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("enumerate %s: %w", canonical, err)
		}
		for _, name := range names {
			path := filepath.Join(canonical, name)
			var st unix.Stat_t
			if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			if err := inspectTreeEntry(fd, name, path, &st, resolve, visit); err != nil {
				return err
			}
			if st.Mode&unix.S_IFMT != unix.S_IFDIR {
				continue
			}
			childFD, err := unix.Openat2(fd, name, directoryOpenHow(resolve))
			if err != nil {
				return fmt.Errorf("unsafe subdirectory %s: %w", path, err)
			}
			if err := walkProtectedDirectory(childFD, path, resolve, visit); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if len(names) == 0 {
			return fmt.Errorf("directory enumeration made no progress: %s", canonical)
		}
	}
}

func inspectTreeEntry(parentFD int, name, canonical string, st *unix.Stat_t, resolve uint64, visit func(string) error) error {
	mode := st.Mode & unix.S_IFMT
	if mode != unix.S_IFLNK {
		// Bind mounts may have the same device number as their sources. Probe
		// each non-symlink entry with NO_XDEV, even when it is a regular file.
		fd, err := unix.Openat2(parentFD, name, &unix.OpenHow{
			Flags: uint64(unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW),
			Resolve: resolve,
		})
		if err != nil {
			return fmt.Errorf("unsafe entry %s: %w", canonical, err)
		}
		_ = unix.Close(fd)
	}
	switch mode {
	case unix.S_IFREG, unix.S_IFDIR, unix.S_IFLNK:
	default:
		return fmt.Errorf("unsupported special entry in destructive tree: %s", canonical)
	}
	if ProtectedName(canonical) || (mode == unix.S_IFREG && st.Nlink > 1) {
		if err := visit(canonical); err != nil {
			return err
		}
	}
	return nil
}

func openAbsoluteDirWithoutSymlinks(path string) (int, error) {
	if !filepath.IsAbs(path) {
		return -1, errors.New("authorized root must be absolute")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, segment := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		next, openErr := unix.Openat(fd, segment,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("authorized root has symlink/unavailable ancestor: %w", openErr)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, nil
}
