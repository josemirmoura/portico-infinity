//go:build linux

package securefs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ensureNoMountCross rejects crossing a mount or bind mount, including binds
// from a different directory on the SAME filesystem (Dev equality is not
// sufficient). All paths are resolved beneath the already-open authorized
// root FD. A final symlink may be unlinked, but it is never followed.
func ensureNoMountCross(rootFD int, rel string) error {
	fd, err := unix.Openat2(rootFD, rel, &unix.OpenHow{
		Flags: uint64(unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC),
		Resolve: uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS |
			unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV),
	})
	if err != nil {
		return fmt.Errorf("path crosses a symlink or mount boundary (%s): %w", rel, err)
	}
	return unix.Close(fd)
}
