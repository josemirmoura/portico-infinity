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
	protectedRoot, invalid := infinityProtectedRoot()
	if invalid {
		return fmt.Errorf("invalid Infinity protected root configuration")
	}
	if protectedRoot == "" {
		// Community compatibility: preserve its existing global scan and
		// bounded-entry behavior. This change is Infinity-only.
		paths, err := sensitive.ProtectedAliases(
			os.Getenv("VPS_AGENT_HOST_ROOT"), roots, sensitive.DefaultScanLimit)
		if err != nil {
			return err
		}
		target = filepath.Clean(target)
		for _, path := range paths {
			if pathWithinRoot(target, path) && access[filepath.Clean(path)] != "work" {
				return fmt.Errorf("protected path %s requires a separate work approval before the tree operation", path)
			}
		}
		return nil
	}
	// Infinity scans only the subdirectory being moved or removed.
	// Streaming avoids a global 50k-entry limit and unbounded metadata lists.
	return sensitive.VisitProtectedSubtree(os.Getenv("VPS_AGENT_HOST_ROOT"), filepath.Clean(target), func(path string) error {
		if access[filepath.Clean(path)] != "work" {
			return fmt.Errorf("protected path %s requires a separate work approval before the tree operation", path)
		}
		return nil
	})
}
