package broker

import (
    "context"
    "encoding/json"
    "os"
    "os/user"
    "path/filepath"
    "strconv"
    "syscall"
    "testing"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestInfinityMkdirForShellPolicyOwnerAndReplay(t *testing.T) {
    if os.Geteuid() == 0 { t.Skip("uses current non-root CI user; privileged securefs tests cover root case") }
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    b, _, _, root := testBroker(t)
    current, err := user.Current()
    if err != nil { t.Fatal(err) }
    b.Policy.Shell.Enabled = true
    b.Policy.Shell.RunAs = current.Username
    b.Policy.Shell.CWDRoots = []string{root}
    target := filepath.Join(root, "shell-workspace")
    args, _ := json.Marshal(map[string]any{"for_shell": true})
    req := wire.Request{ID: "shell-mkdir", Subject: "alice", Tool: "file.mkdir",
        Resource: target, InvocationID: "shell-mkdir-once", Args: args}
    response := b.Handle(context.Background(), req)
    if !response.OK { t.Fatalf("shell directory creation refused: %+v", response) }
    info, err := os.Lstat(target)
    if err != nil { t.Fatal(err) }
    stat := info.Sys().(*syscall.Stat_t)
    expectedUID, _ := strconv.Atoi(current.Uid)
    expectedGID, _ := strconv.Atoi(current.Gid)
    if int(stat.Uid) != expectedUID || int(stat.Gid) != expectedGID || info.Mode().Perm() != 0750 {
        t.Fatalf("wrong workspace permissions: %v uid=%d gid=%d", info.Mode(), stat.Uid, stat.Gid)
    }
    req.ID = "cached"
    if cached := b.Handle(context.Background(), req); !cached.OK {
        t.Fatalf("idempotent retry rejected: %+v", cached)
    }
    req.ID = "conflict"
    req.Args = []byte(`{"for_shell": false}`)
    if conflict := b.Handle(context.Background(), req); conflict.OK {
        t.Fatalf("changed for_shell flag reused same invocation: %+v", conflict)
    }
}

func TestInfinityMkdirForShellDeniesUnapprovedAndExistingPaths(t *testing.T) {
    if os.Geteuid() == 0 { t.Skip("uses current non-root CI user") }
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    b, _, _, root := testBroker(t)
    current, err := user.Current()
    if err != nil { t.Fatal(err) }
    b.Policy.Shell.Enabled = true
    b.Policy.Shell.RunAs = current.Username
    b.Policy.Shell.CWDRoots = []string{filepath.Join(root, "allowed")}
    if err := os.Mkdir(filepath.Join(root, "allowed"), 0755); err != nil { t.Fatal(err) }
    args := []byte(`{"for_shell":true}`)
    cases := []struct { name, path string }{
        {"outside-shell", filepath.Join(root, "not-allowed")},
        {"preexisting", filepath.Join(root, "allowed")},
        {"missing-parent", filepath.Join(root, "allowed", "missing", "child")},
        {"outside-file", filepath.Join(filepath.Dir(root), "outside")},
    }
    for i, tc := range cases {
        req := wire.Request{ID: tc.name, Subject: "alice", Tool: "file.mkdir",
            Resource: tc.path, InvocationID: "shell-deny-"+strconv.Itoa(i), Args: args}
        if resp := b.Handle(context.Background(), req); resp.OK {
            t.Errorf("%s unexpectedly allowed: %+v", tc.name, resp)
        }
    }
    b.Policy.Shell.Enabled = false
    req := wire.Request{ID:"shell-disabled",Subject:"alice",Tool:"file.mkdir",
        Resource:filepath.Join(root,"shell-disabled"),InvocationID:"deny-disabled",Args:args}
    if resp := b.Handle(context.Background(), req); resp.OK { t.Fatal("shell disabled but workspace created") }

    // Community is unaffected: false remains ordinary mkdir, true is denied.
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "")
    b.Policy.Shell.Enabled = true
    req = wire.Request{ID:"community",Subject:"alice",Tool:"file.mkdir",
        Resource:filepath.Join(root,"community"),InvocationID:"deny-community",Args:args}
    if resp := b.Handle(context.Background(), req); resp.OK { t.Fatal("community shell-workspace owner override allowed") }
    req.Args = []byte(`{"for_shell":false}`)
    req.InvocationID = "community-standard"
    if resp := b.Handle(context.Background(), req); !resp.OK { t.Fatalf("normal community mkdir broken: %+v", resp) }
}
