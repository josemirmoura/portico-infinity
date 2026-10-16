package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
)

func (b *Broker) requireSensitiveSubtreeWork(ctx context.Context, subject, target string, roots []string) error {
	if !pathWithinAnyRoot(roots, target) {
		return fmt.Errorf("sensitive subtree is outside authorized write roots")
	}
	access, err := b.activeSensitiveAccess(ctx, subject)
	if err != nil {
		return err
	}
	// Scan only the subtree being moved or removed. /opt can contain many
	// unrelated projects; scanning siblings incorrectly blocked authorized
	// operations once the global 50,000-entry scan budget was exhausted.
	// Stream results to avoid an unbounded list for large target trees.
	return sensitive.VisitProtectedSubtree(os.Getenv("VPS_AGENT_HOST_ROOT"), filepath.Clean(target), func(path string) error {
		if access[filepath.Clean(path)] != "work" {
			return fmt.Errorf("protected path %s requires a separate work approval before the tree operation", path)
		}
		return nil
	})
}
