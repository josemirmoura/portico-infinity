package sensitive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// VisitProtectedSubtree checks ONLY the tree that a recursive file operation
// will touch. Unlike ProtectedAliases, it has no arbitrary global entry-count
// ceiling: it streams directory entries in fixed-size batches and reports
// protected files immediately to visit, without retaining an unbounded list.
//
// The traversal is rooted at an open directory descriptor. It never follows
// symlink leaf entries and rejects nested mount points. Any I/O error aborts
// the preflight before a caller may begin a destructive operation. The caller
// must still coordinate subsequent filesystem mutations: this is a
// preflight, not a transactional guarantee against concurrent host writes.
func VisitProtectedSubtree(hostRoot, target string, visit func(string) error) error {
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return errors.New("sensitive subtree target must be an absolute clean path")
	}
	if visit == nil {
		return errors.New("sensitive subtree visitor is required")
	}
	physical := hostPath(hostRoot, target)
	initial, err := os.Lstat(physical)
	if err != nil {
		return err
	}
	// Recursive file/remove of an ordinary file or a symbolic link is
	// covered by the normal exact-path check in the Broker. Do not open a
	// symlinked root as a directory or follow it into its referent.
	if !initial.IsDir() || initial.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	root, err := os.OpenRoot(physical)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(initial, opened) {
		return errors.New("sensitive subtree root changed during preflight")
	}
	rootKey, _, ok := inode(opened)
	if !ok {
		return errors.New("cannot identify sensitive subtree root inode")
	}
	var walk func(string) error
	walk = func(rel string) error {
		dir, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			entries, err := dir.ReadDir(256)
			if err != nil && err != io.EOF {
				return err
			}
			for _, entry := range entries {
				child := filepath.Join(rel, entry.Name())
				info, statErr := root.Lstat(child)
				if statErr != nil {
					return statErr
				}
				if info.Mode()&os.ModeSymlink != 0 {
					// A link is unlinked without following its referent,
					// but a link named .env is itself a protected name.
					// Do not let recursive removal bypass exact-path
					// sensitive work approval through a symlink leaf.
					if ProtectedName(child) {
						if err := visit(filepath.Join(target, child)); err != nil {
							return err
						}
					}
					continue
				}
				key, links, known := inode(info)
				if !known || key.dev != rootKey.dev {
					return fmt.Errorf("unverifiable inode or nested mount in sensitive subtree: %s", child)
				}
				if info.IsDir() {
					if err := walk(child); err != nil {
						return err
					}
					continue
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("unexpected special file in sensitive subtree: %s", child)
				}
				if ProtectedName(child) || links > 1 {
					if err := visit(filepath.Join(target, child)); err != nil {
						return err
					}
				}
			}
			if err == io.EOF {
				return nil
			}
		}
	}
	return walk(".")
}
