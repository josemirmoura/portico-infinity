package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
)

// requireSensitiveSubtreeWork checks the specific tree affected by a recursive
// removal or directory move. Broad grants such as /opt must not force scanning
// unrelated sibling projects or hit the shell-mask's 50k-entry budget.
func (b *Broker) requireSensitiveSubtreeWork(ctx context.Context, subject, target string, roots []string) error {
	access, err := b.activeSensitiveAccess(ctx, subject)
	if err != nil {
		return err
	}
	return sensitive.WalkProtectedSubtree(
		os.Getenv("VPS_AGENT_HOST_ROOT"), target, roots,
		func(path string) error {
			if access[filepath.Clean(path)] != "work" {
				return fmt.Errorf("protected path %s requires a separate work approval before the tree operation", path)
			}
			return nil
		},
	)
}

// authorizeSensitiveDeletionEntry checks the actual inode just encountered
// during the destructive phase, not only the earlier preflight snapshot.
// A newly added .env or multiply-linked file must still require explicit
// operator work approval. Grants are checked again at the time of deletion.
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
