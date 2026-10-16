package broker

import (
    "encoding/json"
    "testing"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestInfinityProtectedControlPlane(t *testing.T) {
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    cases := []struct {
        name, tool, resource, destination string
        blocked bool
    }{
        {"write own policy", "file.write", "/srv/portico-infinity/config/policy.yaml", "", true},
        {"patch own compose", "file.patch", "/srv/portico-infinity/compose.yaml", "", true},
        {"chmod own directory", "file.chmod", "/srv/portico-infinity", "", true},
        {"chown ancestor", "file.chown", "/srv", "", true},
        {"remove ancestor", "file.remove", "/srv", "", true},
        {"move own directory", "file.move", "/srv/portico-infinity", "/opt/elsewhere", true},
        {"move unrelated to own directory", "file.move", "/opt/newproject", "/srv/portico-infinity", true},
        {"copy into control plane", "file.copy", "/opt/src", "/srv/portico-infinity/file", true},
        {"copy to ancestor", "file.copy", "/opt/src", "/srv", true},
        {"unrelated write", "file.write", "/opt/newproject/src/code.go", "", false},
        {"unrelated chmod", "file.chmod", "/opt/newproject", "", false},
        {"create project", "file.mkdir", "/opt/newproject", "", false},
        {"opt ancestor not control plane", "file.chmod", "/opt", "", false},
        {"copy unrelated", "file.copy", "/opt/a", "/opt/newproject/b", false},
        {"prefix confusion", "file.write", "/srv/portico-infinity-elsewhere/file", "", false},
        {"normalize traversal", "file.write", "/srv/newproject/../portico-infinity/file", "", true},
        {"relative path", "file.write", "portico-infinity/file", "", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            req := wire.Request{ID: "test", Tool: tc.tool, Resource: tc.resource}
            if tc.destination != "" {
                req.Args, _ = json.Marshal(map[string]string{"destination": tc.destination})
            }
            denied := guardInfinityControlPath(req) != nil
            if denied != tc.blocked {
                t.Fatalf("blocked=%t, want %t; %#v", denied, tc.blocked, tc)
            }
        })
    }
}

func TestInfinityControlPlaneInvalidSettingsFailClosed(t *testing.T) {
    for _, root := range []string{"/", "/opt", "/opt/portico-infinity", "srv/portico-infinity", "/etc", "/srv/../opt"} {
        t.Run(root, func(t *testing.T) {
            t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", root)
            req := wire.Request{ID: "test", Tool: "file.write", Resource: "/opt/anything"}
            if guardInfinityControlPath(req) == nil {
                t.Fatalf("invalid control-plane root %q accepted", root)
            }
        })
    }
}

func TestInfinityControlPlaneGuardNotForcedOnCommunity(t *testing.T) {
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "")
    req := wire.Request{ID: "test", Tool: "file.write", Resource: "/srv/portico-infinity/config/policy.yaml"}
    if guardInfinityControlPath(req) != nil {
        t.Fatal("legacy behavior was changed")
    }
}
