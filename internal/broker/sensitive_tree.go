package broker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
