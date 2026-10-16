package broker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
)

// infinityShellWorkspaceRoots limits a non-root Infinity shell to the one
// workspace selected by its cwd. Generic file tools retain the full /opt scope.
//
// A shell cannot safely mask secrets by scanning all of /opt on every launch:
// installations routinely exceed DefaultScanLimit, and mounting more of /opt
// than we have scanned would expose any protected material in those siblings.
// Instead, mount exactly the tree that was scanned. This boundary is specific
// to the isolated Infinity shell; Community and privileged tools are unchanged.
func infinityShellWorkspaceRoots(cwd string, isolated bool, readRoots, writeRoots []string) ([]string, []string, error) {
	protectedRoot, invalid := infinityProtectedRoot()
	if invalid {
		return nil, nil, errors.New("invalid Infinity protected root configuration")
	}
	if protectedRoot == "" || !isolated {
		return readRoots, writeRoots, nil
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd ||
		cwd == "/opt" || !pathWithinRoot("/opt", cwd) {
		return nil, nil, errors.New("Infinity shell requires a project directory strictly below /opt")
	}

	// Refuse an existing symlink at any workspace component. The transient
	// systemd bind mount consumes a pathname, so following a link here could
	// unexpectedly mount a host path outside the authorized workspace.
	for part := cwd; ; part = filepath.Dir(part) {
		info, err := os.Lstat(hostexec.Path(part))
		if err != nil {
			return nil, nil, fmt.Errorf("cannot inspect Infinity shell workspace: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("Infinity shell workspace contains a non-directory or symlink")
		}
		if part == "/opt" {
			break
		}
	}
	if pathWithinAnyRoot(writeRoots, cwd) {
		return nil, []string{cwd}, nil
	}
	if pathWithinAnyRoot(readRoots, cwd) {
		return []string{cwd}, nil, nil
	}
	return nil, nil, errors.New("Infinity shell workspace is outside authorized filesystem roots")
}
