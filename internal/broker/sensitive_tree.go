package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	paths, err := sensitive.ProtectedAliases(
		os.Getenv("VPS_AGENT_HOST_ROOT"),
		[]string{target},
		sensitive.DefaultScanLimit,
	)
	if err != nil {
		return err
	}
	access, err := b.activeSensitiveAccess(ctx, subject)
	if err != nil {
		return err
	}
	target = filepath.Clean(target)
	for _, path := range paths {
		if !pathWithinRoot(target, path) {
			continue
		}
		if access[filepath.Clean(path)] != "work" {
			return fmt.Errorf("protected path %s requires a separate work approval before the tree operation", path)
		}
	}
	return nil
}
