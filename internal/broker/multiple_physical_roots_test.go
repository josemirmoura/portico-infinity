package broker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/securefs"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestDelegationNeverEscapesExplicitPhysicalRoots(t *testing.T) {
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", "/opt")
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS", `["/home","/opt"]`)
	b := &Broker{}
	for _, root := range []string{"/srv", "/etc", "/home2", "/opt2", "/"} {
		if _, _, err := b.normalizeDelegatedRoot(root, "read"); err == nil {
			t.Fatalf("root %q must be outside the physical ceiling", root)
		}
	}
}

func TestTwoRootsReadWriteAndSecretsBlocked(t *testing.T) {
	b, _, _, first := testBroker(t)
	second := t.TempDir()
	roots := []string{first, second}
	encoded, err := json.Marshal(roots)
	if err != nil { t.Fatal(err) }
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT", first)
	t.Setenv("VPS_AGENT_PHYSICAL_SCOPE_ROOTS", string(encoded))
	b.Policy.Filesystem.Read = roots
	b.Policy.Filesystem.Write = roots
	b.FS, err = securefs.New(roots, roots, 1024)
	if err != nil { t.Fatal(err) }
	for i, root := range roots {
		file := filepath.Join(root, "portico-test.txt")
		args, _ := json.Marshal(map[string]string{"content": "authorized"})
		response := b.Handle(context.Background(), wire.Request{
			ID: "write", Subject: "alice", Tool: "file.write",
			Resource: file, InvocationID: "write-root-" + string(rune('0'+i)), Args: args,
		})
		if !response.OK { t.Fatalf("write in %q denied: %+v", root, response.Error) }
		content, err := os.ReadFile(file)
		if err != nil || string(content) != "authorized" { t.Fatalf("wrong content: %q, %v", content, err) }
		response = b.Handle(context.Background(), wire.Request{
			ID: "read", Subject: "alice", Tool: "file.read", Resource: file,
		})
		if !response.OK { t.Fatalf("read in %q denied: %+v", root, response.Error) }
	}
	for _, outside := range []string{"/srv/portico-infinity/config/policy.yaml", "/etc/passwd", "/home2/outside"} {
		response := b.Handle(context.Background(), wire.Request{
			ID: "escape", Subject: "alice", Tool: "file.read", Resource: outside,
		})
		if response.OK { t.Fatalf("outside file %q was accessible", outside) }
	}
	ssh := filepath.Join(first, ".ssh")
	if err := os.Mkdir(ssh, 0700); err != nil { t.Fatal(err) }
	secret := filepath.Join(ssh, "id_ed25519")
	if err := os.WriteFile(secret, []byte("synthetic-key"), 0600); err != nil { t.Fatal(err) }
	response := b.Handle(context.Background(), wire.Request{
		ID: "secret", Subject: "alice", Tool: "file.read", Resource: secret,
	})
	if response.OK || response.Error == nil || response.Error.Code != "sensitive_path_locked" {
		t.Fatalf("identity material is accessible without separate grant: %+v", response)
	}
}
