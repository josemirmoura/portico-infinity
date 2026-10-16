package broker

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
)

func TestInfinityShellOnlyMountsWorkspaceThatWasScanned(t *testing.T) {
	host := t.TempDir()
	t.Setenv("VPS_AGENT_HOST_ROOT", host)
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	workspace := filepath.Join(host, "opt", "project")
	sibling := filepath.Join(host, "opt", "other")
	if err := os.MkdirAll(workspace, 0755); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(sibling, 0755); err != nil { t.Fatal(err) }
	for _, path := range []string{
		filepath.Join(workspace, ".env"),
		filepath.Join(sibling, ".env"),
		filepath.Join(sibling, "one"),
		filepath.Join(sibling, "two"),
		filepath.Join(sibling, "three"),
	} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil { t.Fatal(err) }
	}
	// A small scan budget illustrates the production case: global /opt
	// exhausts its node budget even when the chosen workspace is tiny.
	if _, err := sensitive.ProtectedAliases(host, []string{"/opt"}, 3); !errors.Is(err, sensitive.ErrScanLimit) {
		t.Fatalf("expected full /opt scan to fail closed, got %v", err)
	}
	ro, rw, err := infinityShellWorkspaceRoots("/opt/project", true, []string{"/opt"}, []string{"/opt"})
	if err != nil { t.Fatal(err) }
	if len(ro) != 0 || !reflect.DeepEqual(rw, []string{"/opt/project"}) {
		t.Fatalf("unexpected shell filesystem mounts: ro=%v rw=%v", ro, rw)
	}
	masks, err := sensitive.ProtectedAliases(host, append(ro, rw...), 3)
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(masks, []string{"/opt/project/.env"}) {
		t.Fatalf("workspace secret masking mismatch: %v", masks)
	}
	args, err := sandbox.BuildSystemdRunArgs(sandbox.Spec{
		Unit: "infinity-workspace-test", Command: "id -u", CWD: "/opt/project",
		ReadOnlyPaths: ro, ReadWritePaths: rw, InaccessiblePaths: masks,
		IsolateFilesystem: true,
	})
	if err != nil { t.Fatal(err) }
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--property=BindPaths=/opt/project",
		"--property=InaccessiblePaths=-/opt/project/.env",
	} {
		if !strings.Contains(joined, want) { t.Fatalf("missing %s: %s", want, joined) }
	}
	if strings.Contains(joined, "--property=BindPaths=/opt\n") {
		t.Fatalf("unexpected broad /opt mount: %s", joined)
	}
}

func TestInfinityShellWorkspaceRejectsUnsafeOrOutOfScopePaths(t *testing.T) {
	host := t.TempDir()
	t.Setenv("VPS_AGENT_HOST_ROOT", host)
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	if err := os.MkdirAll(filepath.Join(host, "opt", "project"), 0755); err != nil { t.Fatal(err) }
	if err := os.Symlink("project", filepath.Join(host, "opt", "link")); err != nil { t.Fatal(err) }
	for _, cwd := range []string{
		"/opt", "/etc", "/opt/../etc", "/opt/link", "/opt/project/../link", "/opt/missing", "opt/project",
	} {
		t.Run(cwd, func(t *testing.T) {
			if _, _, err := infinityShellWorkspaceRoots(cwd, true, []string{"/opt"}, []string{"/opt"}); err == nil {
				t.Fatalf("unsafe or overly broad workspace %q accepted", cwd)
			}
		})
	}
	if _, _, err := infinityShellWorkspaceRoots("/opt/project", true, []string{"/opt/other"}, []string{"/opt/other"}); err == nil {
		t.Fatal("workspace outside authorized roots accepted")
	}
	ro, rw, err := infinityShellWorkspaceRoots("/opt/project", true, []string{"/opt"}, nil)
	if err != nil || !reflect.DeepEqual(ro, []string{"/opt/project"}) || len(rw) != 0 {
		t.Fatalf("read-only workspace unexpectedly writable: ro=%v rw=%v err=%v", ro, rw, err)
	}
}

func TestInfinityShellWorkspaceDoesNotAlterCommunity(t *testing.T) {
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "")
	read := []string{"/opt/ro"}
	write := []string{"/opt/rw"}
	ro, rw, err := infinityShellWorkspaceRoots("/opt", true, read, write)
	if err != nil || !reflect.DeepEqual(ro, read) || !reflect.DeepEqual(rw, write) {
		t.Fatalf("Community changed: ro=%v rw=%v err=%v", ro, rw, err)
	}
}
