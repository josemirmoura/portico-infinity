package sensitive

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	// MaxWorkspaceMasks keeps systemd's InaccessiblePaths arguments bounded.
	// The number of ordinary files in a workspace has no fixed ceiling.
	MaxWorkspaceMasks = 1024
	MaxWorkspaceMaskBytes = 64 << 10
)

// ProtectedAliasesStreaming scans only the shell workspace roots, in batches
// of 256 names. It never materializes directory contents, follows symlinks, or
// crosses bind mounts, even when a mount shares its source filesystem.
//
// Unlike the legacy Community scanner, the number of ordinary entries is not
// capped. Only the list of sensitive masks passed to systemd is bounded.
// Failure is always fail-closed: no shell job may start with incomplete masks.
func ProtectedAliasesStreaming(hostRoot string, roots []string) ([]string, error) {
	masks := make([]string, 0, 16)
	seen := make(map[string]struct{})
	bytes := 0
	resolve := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV)
	visit := func(path string) error {
		if _, ok := seen[path]; ok {
			return nil
		}
		// Each mask becomes a separate systemd command argument. Bound both
		// count and combined size while allowing arbitrarily many safe files.
		if len(masks) >= MaxWorkspaceMasks || bytes+len(path)+32 > MaxWorkspaceMaskBytes {
			return fmt.Errorf("workspace requires too many sensitive masks: %w", ErrScanLimit)
		}
		seen[path] = struct{}{}
		masks = append(masks, path)
		bytes += len(path) + 32
		return nil
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
			return nil, errors.New("shell workspace root must be a normalized absolute path")
		}
		if ProtectedName(root) {
			if err := visit(root); err != nil {
				return nil, err
			}
		}
		fd, err := openAbsoluteDirWithoutSymlinks(hostPath(hostRoot, root))
		if err != nil {
			return nil, fmt.Errorf("cannot safely open shell workspace: %w", err)
		}
		// walkProtectedDirectory owns fd. Its O_NOFOLLOW/Openat2/NO_XDEV
		// checks guard traversal against symlink and mount escapes.
		if err := walkProtectedDirectory(fd, root, resolve, visit); err != nil {
			return nil, fmt.Errorf("cannot safely scan shell workspace: %w", err)
		}
	}
	return masks, nil
}
