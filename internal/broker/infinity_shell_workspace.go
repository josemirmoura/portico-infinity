package broker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
)

// infinityShellWorkspaceRoots limits a non-root Infinity shell to the one
// workspace selected by its cwd. Generic file tools retain the full configured
// physical filesystem ceiling (default /opt).
//
// A shell cannot safely mask secrets by scanning an entire large ceiling on
// every launch. Mount exactly the selected tree that was scanned, independent
// of whether the operator chose /opt or another validated physical root.
// Community and privileged tools remain unchanged.
func infinityShellWorkspaceRoots(cwd string, isolated bool, readRoots, writeRoots []string) ([]string, []string, error) {
	protectedRoot, invalid := infinityProtectedRoot()
	if invalid {
		return nil, nil, errors.New("invalid Infinity protected root configuration")
	}
	if protectedRoot == "" || !isolated {
		return readRoots, writeRoots, nil
	}
	physical := os.Getenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT")
	if !filepath.IsAbs(physical) || physical == "/" || filepath.Clean(physical) != physical {
		return nil, nil, errors.New("Infinity shell physical scope must be an explicit canonical directory")
	}
	// Reject a misconfigured ceiling that could encompass (or sit inside)
	// Infinity's protected controller, regardless of file-policy claims.
	if pathWithinRoot(physical, protectedRoot) || pathWithinRoot(protectedRoot, physical) {
		return nil, nil, errors.New("Infinity shell physical scope overlaps the protected controller")
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd ||
		cwd == physical || !pathWithinRoot(physical, cwd) {
		return nil, nil, errors.New("Infinity shell requires a project directory strictly below the configured physical scope")
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
		if part == physical {
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
