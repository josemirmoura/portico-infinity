//go:build linux

package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
	"golang.org/x/sys/unix"
)

// ErrPartialRemoval means that one or more entries have already been unlinked
// before a later failure. Retrying without operator reconciliation is unsafe.
var ErrPartialRemoval = errors.New("recursive removal partially completed; operator reconciliation required")

// RemoveRecursiveVerified uses descriptor-relative operations, bounded
// directory iteration and kernel mount IDs. Only the Infinity Broker calls
// this path; Community's existing file operation remains unchanged.
//
// check is invoked for EVERY entry directly before its attempted removal,
// with protected=true for .env names or multiply linked regular files.
// The caller must enforce exact-path sensitive.work approvals. This does
// not replace the separate all-or-nothing sensitive subtree preflight.
func (m *Manager) RemoveRecursiveVerified(target string, check func(string, bool) error) error {
	if check == nil {
		return errors.New("recursive removal requires a protected-path verifier")
	}
	rootPath, rel, err := selectRoot(m.writeRoots, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return errors.New("refusing to delete an authorized root")
	}
	rootFD, err := unix.Open(m.physical(rootPath), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(rootFD)
	parentRel := filepath.Dir(rel)
	parentFD := rootFD
	if parentRel != "." {
		parentFD, err = verifiedOpenDir(rootFD, parentRel)
		if err != nil {
			return err
		}
		defer unix.Close(parentFD)
	}
	removed := uint64(0)
	err = verifiedRemoveEntry(parentFD, filepath.Base(rel), filepath.Clean(target), check, &removed, 0)
	if err != nil && removed != 0 {
		return fmt.Errorf("%w (entries_unlinked=%d, target=%s): %w", ErrPartialRemoval, removed, target, err)
	}
	return err
}

func verifiedOpenDir(parentFD int, name string) (int, error) {
	return unix.Openat2(parentFD, name, &unix.OpenHow{
		Flags: uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
}

func verifiedStatx(fd int, name string, flags int) (unix.Statx_t, error) {
	var st unix.Statx_t
	err := unix.Statx(fd, name, flags|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &st)
	if err != nil {
		return st, err
	}
	if st.Mask&unix.STATX_MNT_ID == 0 || st.Mask&unix.STATX_BASIC_STATS != unix.STATX_BASIC_STATS {
		return st, errors.New("kernel did not provide complete inode and mount metadata")
	}
	return st, nil
}

func sameVerifiedEntry(first, next unix.Statx_t) bool {
	return first.Ino == next.Ino && first.Mnt_id == next.Mnt_id &&
		first.Mode == next.Mode && first.Dev_major == next.Dev_major &&
		first.Dev_minor == next.Dev_minor
}

func verifiedRemoveEntry(parentFD int, name, canonical string, check func(string, bool) error, removed *uint64, depth int) error {
	if depth > 1024 {
		return errors.New("recursive removal directory depth safety limit exceeded")
	}
	parent, err := verifiedStatx(parentFD, "", unix.AT_EMPTY_PATH)
	if err != nil {
		return err
	}
	info, err := verifiedStatx(parentFD, name, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	if parent.Mnt_id != info.Mnt_id {
		return fmt.Errorf("recursive removal refused nested mount: %s", canonical)
	}
	mode := uint32(info.Mode) & unix.S_IFMT
	secret := sensitive.ProtectedName(canonical) || (mode == unix.S_IFREG && info.Nlink > 1)
	if mode != unix.S_IFDIR && mode != unix.S_IFREG && mode != unix.S_IFLNK {
		return fmt.Errorf("recursive removal refused special file: %s", canonical)
	}
	if err := check(canonical, secret); err != nil {
		return err
	}
	if mode == unix.S_IFDIR {
		childFD, err := verifiedOpenDir(parentFD, name)
		if err != nil {
			return err
		}
		childStat, err := verifiedStatx(childFD, "", unix.AT_EMPTY_PATH)
		if err != nil {
			unix.Close(childFD)
			return err
		}
		if !sameVerifiedEntry(info, childStat) {
			unix.Close(childFD)
			return fmt.Errorf("directory inode changed before recursive removal: %s", canonical)
		}
		child := os.NewFile(uintptr(childFD), canonical)
		// Bounded memory even for directories with millions of entries.
		for {
			entries, readErr := child.ReadDir(256)
			if readErr != nil && readErr != io.EOF {
				child.Close()
				return readErr
			}
			for _, entry := range entries {
				childPath := filepath.Join(canonical, entry.Name())
				if err := verifiedRemoveEntry(childFD, entry.Name(), childPath, check, removed, depth+1); err != nil {
					child.Close()
					return err
				}
			}
			if readErr == io.EOF {
				break
			}
		}
		if err := child.Close(); err != nil {
			return err
		}
	}
	again, err := verifiedStatx(parentFD, name, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	if !sameVerifiedEntry(info, again) {
		return fmt.Errorf("file identity changed during recursive removal: %s", canonical)
	}
	flags := 0
	if mode == unix.S_IFDIR {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(parentFD, name, flags); err != nil {
		return err
	}
	*removed++
	return nil
}

