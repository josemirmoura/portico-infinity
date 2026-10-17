package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
)

func (b *Broker) requireSensitiveSubtreeWork(ctx context.Context, subject, target string, roots []string) error {
	// Only the subtree affected by a recursive mutation needs preflight.
	// Scanning all authorized roots (e.g. /opt) needlessly traverses sibling
	// projects and rejects safe operations once unrelated entries exceed 50k.
	target = filepath.Clean(target)
	if !pathWithinAnyRoot(roots, target) {
		return fmt.Errorf("tree operation outside authorized writable roots")
	}
	info, err := os.Lstat(hostexec.Path(target))
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tree operation target cannot be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("sensitive subtree preflight requires a directory")
	}
	access, err := b.activeSensitiveAccess(ctx, subject)
	if err != nil {
		return err
	}
	// The subtree selected by the caller is checked using dirfds.
	// Do not enumerate siblings in a broad /opt policy root.
	return sensitive.WalkProtectedSubtree(
		os.Getenv("VPS_AGENT_HOST_ROOT"), target, roots,
		func(path string) error {
			if access[filepath.Clean(path)] != "work" {
				return fmt.Errorf("protected path %s requires a separate work approval", path)
			}
			return nil
		},
	)
}


// authorizeSensitiveDeletionEntry rechecks any sensitive inode actually
// encountered during deletion. The earlier preflight is not sufficient when
// other processes can add files between the scan and the mutation.
func (b *Broker) authorizeSensitiveDeletionEntry(ctx context.Context, subject, path string, info os.FileInfo) error {
	protected := sensitive.ProtectedName(path)
	if info.Mode().IsRegular() {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot verify hardlink count for %s", path)
		}
		protected = protected || st.Nlink > 1
	}
	if !protected {
		return nil
	}
	access, err := b.activeSensitiveAccess(ctx, subject)
	if err != nil {
		return err
	}
	if access[filepath.Clean(path)] != "work" {
		return fmt.Errorf("protected entry %s requires separate work approval before deletion", path)
	}
	return nil
}
