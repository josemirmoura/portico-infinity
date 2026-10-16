package integration

import (
    "context"
    "net/http/httptest"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/broker"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/gateway"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/ipc"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/policy"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/securefs"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestInfinityScopedToolsOverRealMCP exercises the actual MCP HTTP gateway,
// Unix-socket IPC, Broker, secured filesystem and durable audit machinery.
// The filesystem scope is a disposable analogue of /opt; nothing is changed
// on a real /opt, /srv or on any user host.
func TestInfinityScopedToolsOverRealMCP(t *testing.T) {
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    t.Setenv("VPS_AGENT_HOST_ROOT", "")
    root := t.TempDir()
    socket := filepath.Join(t.TempDir(), "broker.sock")

    store, err := state.Open(":memory:")
    if err != nil {
        t.Fatal(err)
    }
    defer store.Close()

    fs, err := securefs.New([]string{root}, []string{root}, 1<<20)
    if err != nil {
        t.Fatal(err)
    }
    cfg := &policy.Config{
        Version: 1, Mode: "scoped",
        Features: policy.Features{FullModeEnabled: false},
        Filesystem: policy.FilesystemPolicy{
            Read: []string{root}, Write: []string{root},
            Actions: []string{"list", "stat", "read", "mkdir", "write",
                "patch", "copy", "move", "remove", "hash", "chmod", "chown"},
        },
        Network: policy.NetworkPolicy{Mode: "blocked"},
        Privilege: policy.PrivilegePolicy{Admin: "broker-only"},
        Replay: policy.ReplayPolicy{RequireIdempotencyForSafeWrites: true},
    }
    if err := cfg.Validate(); err != nil {
        t.Fatal(err)
    }
    b := &broker.Broker{
        Policy: cfg, FS: fs, State: store, ExpectedSubject: "infinity-operator",
        InstanceID: "infinity-disposable", InstanceName: "portico-infinity",
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    done := make(chan error, 1)
    go func() { done <- ipc.NewServer(socket, b).Serve(ctx) }()
    clientIPC := ipc.Client{Socket: socket, Timeout: 2 * time.Second}
    until := time.Now().Add(4 * time.Second)
    ready := false
    for time.Now().Before(until) {
        _, err := clientIPC.Call(ctx, wire.Request{
            ID: "startup-probe", Subject: "infinity-operator", Tool: "system.info",
        })
        if err == nil {
            ready = true
            break
        }
        time.Sleep(20 * time.Millisecond)
    }
    if !ready {
        t.Fatal("broker unix socket did not become ready")
    }

    server := httptest.NewServer(gateway.Handler(
        gateway.BrokerExecutor{Client: clientIPC},
        gateway.AuthConfig{Mode: "none", StaticSubject: "infinity-operator"},
    ))
    defer server.Close()
    client := mcp.NewClient(&mcp.Implementation{Name: "infinity-integration", Version: "v1"}, nil)
    session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
    if err != nil {
        t.Fatal(err)
    }
    defer session.Close()

    call := func(name string, args map[string]any, allowed bool) {
        t.Helper()
        result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
        if err != nil {
            t.Fatalf("%s protocol error: %v", name, err)
        }
        if result.IsError == allowed {
            t.Fatalf("%s allowed=%t, IsError=%t, result=%+v", name, allowed, result.IsError, result)
        }
    }

    target := filepath.Join(root, "sample.txt")
    call("file.write", map[string]any{"path": target, "content": "hello infinity"}, true)
    call("file.read", map[string]any{"path": target}, true)
    call("file.chmod", map[string]any{"path": target, "mode": 384}, true) // 0600
    info, err := os.Stat(target)
    if err != nil || info.Mode().Perm() != 0o600 {
        t.Fatalf("chmod failed, info=%v err=%v", info, err)
    }
    call("file.chown", map[string]any{"path": target, "uid": os.Getuid(), "gid": os.Getgid()}, true)
    call("file.write", map[string]any{"path": filepath.Join(root, "..", "outside.txt"), "content": "denied"}, false)
    call("file.write", map[string]any{"path": "/srv/portico-infinity/config/policy.yaml", "content": "denied"}, false)
    call("package.install", map[string]any{"name": "curl"}, false)
    call("shell.exec_admin", map[string]any{"command": "id", "cwd": root, "grant_id": "invalid"}, false)
    call("file.remove", map[string]any{"path": target}, true)
    if _, err := os.Stat(target); !os.IsNotExist(err) {
        t.Fatalf("remove did not finish: %v", err)
    }

    bad, err := clientIPC.Call(ctx, wire.Request{ID: "subject-forged",
        Subject: "another-operator", Tool: "file.list", Resource: root})
    if err != nil || bad.OK || bad.Error == nil || bad.Error.Code != "identity_mismatch" {
        t.Fatalf("unauthorized MCP subject not blocked: response=%+v err=%v", bad, err)
    }
    if err := store.VerifyAudit(ctx); err != nil {
        t.Fatalf("Broker audit hash chain failed: %v", err)
    }

    cancel()
    select {
    case err := <-done:
        if err != nil {
            t.Fatalf("Broker IPC shutdown failed: %v", err)
        }
    case <-time.After(3*time.Second):
        t.Fatal("Broker IPC did not shut down")
    }
}
