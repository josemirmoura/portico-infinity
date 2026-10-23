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
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/opt")
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
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/opt")
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

func TestInfinityShellUsesAlternateOperatorScope(t *testing.T) {
	host := t.TempDir()
	t.Setenv("VPS_AGENT_HOST_ROOT", host)
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/mnt/portico-ci")
	for _, path := range []string{
		filepath.Join(host, "mnt", "portico-ci", "project"),
		filepath.Join(host, "mnt", "portico-ci", "sibling"),
		filepath.Join(host, "opt", "old-project"),
	} {
		if err := os.MkdirAll(path, 0755); err != nil { t.Fatal(err) }
	}
	if err := os.WriteFile(filepath.Join(host, "mnt", "portico-ci", "project", ".env"), []byte("protected"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(host, "mnt", "portico-ci", "sibling", ".env"), []byte("sibling"), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink("project", filepath.Join(host, "mnt", "portico-ci", "link")); err != nil { t.Fatal(err) }

	ro, rw, err := infinityShellWorkspaceRoots("/mnt/portico-ci/project", true,
		[]string{"/mnt/portico-ci"}, []string{"/mnt/portico-ci"})
	if err != nil { t.Fatal(err) }
	if len(ro) != 0 || !reflect.DeepEqual(rw, []string{"/mnt/portico-ci/project"}) {
		t.Fatalf("alternate root not honored: ro=%v rw=%v", ro, rw)
	}
	masks, err := sensitive.ProtectedAliases(host, rw, 50)
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(masks, []string{"/mnt/portico-ci/project/.env"}) {
		t.Fatalf("cross-workspace secrets exposed: %v", masks)
	}
	args, err := sandbox.BuildSystemdRunArgs(sandbox.Spec{
		Unit: "infinity-custom-scope-test", Command: "id -u", CWD: "/mnt/portico-ci/project",
		ReadOnlyPaths: ro, ReadWritePaths: rw, InaccessiblePaths: masks, IsolateFilesystem: true,
	})
	if err != nil { t.Fatal(err) }
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--property=BindPaths=/mnt/portico-ci/project") ||
		!strings.Contains(joined, "--property=InaccessiblePaths=-/mnt/portico-ci/project/.env") {
		t.Fatalf("custom root not bound and masked safely: %s", joined)
	}
	for _, cwd := range []string{
		"/mnt/portico-ci", "/opt/old-project", "/mnt/portico-ci/link",
		"/mnt/portico-ci/project/../sibling", "/mnt/portico-ci/missing",
		"/srv/portico-infinity", "mnt/portico-ci/project",
	} {
		if _, _, err := infinityShellWorkspaceRoots(cwd, true,
			[]string{"/mnt/portico-ci"}, []string{"/mnt/portico-ci"}); err == nil {
			t.Errorf("invalid alternate-root shell directory accepted: %q", cwd)
		}
	}
}

func TestInfinityShellRejectsInvalidOrOverlappingPhysicalScope(t *testing.T) {
	host := t.TempDir()
	t.Setenv("VPS_AGENT_HOST_ROOT", host)
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	for _, scope := range []string{"", "/", "/opt/..", "/srv", "/srv/portico-infinity", "/srv/portico-infinity/data"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", scope)
			if _, _, err := infinityShellWorkspaceRoots("/srv/portico-infinity/data/project", true,
				[]string{"/srv"}, []string{"/srv"}); err == nil {
				t.Fatalf("invalid or protected root %q was accepted", scope)
			}
		})
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
