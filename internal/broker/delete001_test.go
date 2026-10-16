package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestDelete001RecursiveRemovalOfLargeAuthorizedTree(t *testing.T) {
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()
	b.Policy.Filesystem.Actions = append(b.Policy.Filesystem.Actions, "remove_recursive")
	tree := filepath.Join(root, "large-lab")
	if err := os.Mkdir(tree, 0700); err != nil { t.Fatal(err) }
	outsideTree := filepath.Join(root, "sibling-project")
	if err := os.Mkdir(outsideTree, 0700); err != nil { t.Fatal(err) }
	siblingSecret := filepath.Join(outsideTree, ".env")
	if err := os.WriteFile(siblingSecret, []byte("untouched"), 0600); err != nil { t.Fatal(err) }
	for i := 0; i < sensitive.DefaultScanLimit+4; i++ {
		p := filepath.Join(tree, fmt.Sprintf("file-%05d", i))
		if err := os.WriteFile(p, nil, 0600); err != nil { t.Fatal(err) }
	}
	args, _ := json.Marshal(map[string]bool{"recursive": true})
	r := b.Handle(context.Background(), wire.Request{
		ID: "delete-001-large", Subject: "alice", Tool: "file.remove",
		Resource: tree, InvocationID: "delete-001-large-unique", Args: args,
	})
	if !r.OK {
		t.Fatalf("large authorized tree was blocked: %+v", r)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("tree still exists after approved recursive deletion: %v", err)
	}
	secret, err := os.ReadFile(siblingSecret)
	if err != nil || string(secret) != "untouched" {
		t.Fatalf("a sibling secret was changed by unrelated recursive deletion: %q %v", secret, err)
	}
}

func TestDelete001DenyNestedSecretsBeforeAnyDeletion(t *testing.T) {
	t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()
	b.Policy.Filesystem.Actions = append(b.Policy.Filesystem.Actions, "remove_recursive")
	tree := filepath.Join(root, "target")
	if err := os.Mkdir(tree, 0700); err != nil { t.Fatal(err) }
	ordinary := filepath.Join(tree, "ordinary.txt")
	protected := filepath.Join(tree, ".env")
	if err := os.WriteFile(ordinary, []byte("keep"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(protected, []byte("secret"), 0600); err != nil { t.Fatal(err) }
	args, _ := json.Marshal(map[string]bool{"recursive": true})
	r := b.Handle(context.Background(), wire.Request{
		ID: "delete-001-denied", Subject: "alice", Tool: "file.remove",
		Resource: tree, InvocationID: "delete-001-denied-unique", Args: args,
	})
	if r.OK || r.Error == nil || r.Error.Code != "sensitive_path_locked" {
		t.Fatalf("recursive deletion of protected tree was not rejected: %+v", r)
	}
	for _, p := range []string{ordinary, protected} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("denied deletion altered tree at %s: %v", p, err)
		}
	}
}
