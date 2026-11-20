package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PhysicalRoots reads the explicitly configured Infinity ceilings. The plural
// setting is opt-in; an invalid value must never fall back to the old ceiling.
func PhysicalRoots() ([]string, error) {
	if raw, present := os.LookupEnv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS"); present {
		var roots []string
		if err := json.Unmarshal([]byte(raw), &roots); err != nil {
			return nil, fmt.Errorf("decode physical scope roots: %w", err)
		}
		if len(roots) == 0 {
			return nil, errors.New("physical scope roots must not be empty")
		}
		seen := map[string]bool{}
		for _, root := range roots {
			if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || seen[root] {
				return nil, fmt.Errorf("invalid physical scope root %q", root)
			}
			seen[root] = true
		}
		for i, root := range roots {
			for j, other := range roots {
				if i != j && withinAnyRoot([]string{root}, other) {
					return nil, fmt.Errorf("overlapping physical scope roots %q and %q", root, other)
				}
			}
		}
		return roots, nil
	}
	root := os.Getenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT")
	if root == "" {
		return nil, errors.New("physical scope root is not configured")
	}
	return []string{root}, nil
}

func (c *Config) ValidatePhysicalRoots(roots []string, wholeHost bool) error {
	if len(roots) == 0 {
		return errors.New("physical scope roots are empty")
	}
	if wholeHost {
		if len(roots) != 1 {
			return errors.New("multi-root scope cannot enable whole-host mode")
		}
		return nil // Legacy single-root whole-host behavior remains opt-in.
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || (root == "/" && !wholeHost) {
			return fmt.Errorf("invalid physical scope root %q", root)
		}
	}
	check := func(kind string, paths []string) error {
		for _, path := range paths {
			if path != "" && !withinAnyRoot(roots, path) {
				return fmt.Errorf("%s path %q is outside physical scope roots", kind, path)
			}
		}
		return nil
	}
	for _, item := range []struct { name string; paths []string }{
		{"filesystem.read", c.Filesystem.Read}, {"filesystem.write", c.Filesystem.Write},
		{"shell.cwd_roots", c.Shell.CWDRoots}, {"compose.inspect", c.Compose.Inspect},
		{"compose.manage", c.Compose.Manage},
	} {
		if err := check(item.name, item.paths); err != nil { return err }
	}
	return nil
}
