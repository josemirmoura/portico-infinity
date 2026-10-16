//go:build linux

package sensitive

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// mountID returns the kernel mount identity, which distinguishes a bind
// mount from its source even if both use the same device/inode.
func mountID(fd int, path string, flags int) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(fd, path, flags|unix.AT_NO_AUTOMOUNT,
		unix.STATX_MNT_ID, &stat); err != nil {
		return 0, fmt.Errorf("cannot verify mount identity: %w", err)
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return 0, fmt.Errorf("kernel filesystem did not supply mount identity")
	}
	return stat.Mnt_id, nil
}

func rejectMountBoundary(parent, child string) error {
	p, err := mountID(unix.AT_FDCWD, parent, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	c, err := mountID(unix.AT_FDCWD, child, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	if p != c {
		return fmt.Errorf("sensitive subtree mount crossing denied: %s", child)
	}
	return nil
}

func rejectMountEntry(parent *os.File, name string) error {
	p, err := mountID(int(parent.Fd()), "", unix.AT_EMPTY_PATH)
	if err != nil {
		return err
	}
	c, err := mountID(int(parent.Fd()), name, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return err
	}
	if p != c {
		return fmt.Errorf("sensitive subtree contains a nested mount: %s", name)
	}
	return nil
}
