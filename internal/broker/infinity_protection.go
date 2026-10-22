package broker

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

// infinityProtectedRoot is set only by the opt-privileged Infinity runtime.
// It prevents self-modification of the control-plane checkout by typed
// privileged filesystem tools, including actions on ancestor directories.
//
// This does not sandbox arbitrary non-root shell commands. The installer
// must use an execution identity unable to write the control-plane checkout.
func infinityProtectedRoot() (string, bool) {
    raw := os.Getenv("VPS_AGENT_INFINITY_PROTECTED_ROOT")
    if raw == "" {
        return "", false
    }
    if !filepath.IsAbs(raw) || filepath.Clean(raw) != raw ||
       raw != "/srv/portico-infinity" {
        return "", true // configuration is unsafe; all mutations fail closed
    }
    return raw, false
}

func infinityWithin(root, target string) bool {
    if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
        return false
    }
    rel, err := filepath.Rel(root, target)
    return err == nil && (rel == "." ||
        (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
}

func infinityTouchesProtected(path, root string) bool {
    if path == "" || !filepath.IsAbs(path) {
        return true
    }
    return infinityWithin(root, path) || infinityWithin(path, root)
}

// guardInfinityControlPath applies before dispatch to every typed filesystem
// mutation. The internal policy and deployment source must not be editable
// by their own AI client, even with a static /opt writable root. The checkout is outside /opt.
func guardInfinityControlPath(req wire.Request) *wire.Response {
    root, bad := infinityProtectedRoot()
    if root == "" && !bad {
        return nil // Legacy Community behavior remains unchanged.
    }
    var paths []string
    switch req.Tool {
    case "file.mkdir", "file.write", "file.write_test", "file.patch",
         "file.remove", "file.chmod", "file.chown":
        paths = []string{req.Resource}
    case "file.move":
        paths = []string{req.Resource}
        fallthrough
    case "file.copy", "file.copy.start":
        var args struct {
            Destination string `json:"destination"`
        }
        if err := json.Unmarshal(req.Args, &args); err != nil || args.Destination == "" {
            response := deny(req.ID, "invalid_args", "destination is required")
            return &response
        }
        paths = append(paths, args.Destination)
    default:
        return nil
    }
    if bad {
        response := deny(req.ID, "infinity_policy_invalid", "Infinity protected root configuration invalid")
        return &response
    }
    for _, path := range paths {
        if infinityTouchesProtected(path, root) {
            response := deny(req.ID, "infinity_control_protected",
                "Infinity control-plane path and its ancestors are not writable by MCP")
            return &response
        }
    }
    return nil
}
