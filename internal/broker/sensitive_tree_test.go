package broker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestProtectedFileBlocksRecursiveTreeRemoval(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()

	project := filepath.Join(root, "tree")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(project, ".env")
	if err := os.WriteFile(protected, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{"recursive": true})
	resp := b.Handle(ctx, wire.Request{
		ID: "tree-remove",
		Subject: "alice",
		Tool: "file.remove",
		Resource: project,
		InvocationID: "tree-remove-1",
		Args: args,
	})
	if resp.OK {
		t.Fatal("tree removal unexpectedly crossed the protected-file boundary")
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("protected file changed after denied tree removal: %v", err)
	}
}

func TestProtectedFileBlocksDirectoryMove(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()

	project := filepath.Join(root, "tree")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(project, ".env")
	if err := os.WriteFile(protected, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "tree-moved")
	args, _ := json.Marshal(map[string]any{"destination": destination})
	resp := b.Handle(ctx, wire.Request{
		ID: "tree-move",
		Subject: "alice",
		Tool: "file.move",
		Resource: project,
		InvocationID: "tree-move-1",
		Args: args,
	})
	if resp.OK {
		t.Fatal("directory move unexpectedly crossed the protected-file boundary")
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("protected file changed after denied directory move: %v", err)
	}
}


func TestRecursiveTreePreflightIgnoresProtectedSibling(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()
	project := filepath.Join(root, "remove-me")
	sibling := filepath.Join(root, "unrelated")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0750); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(sibling, ".env")
	if err := os.WriteFile(secret, []byte("secret in unrelated project"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"recursive": true})
	result := b.Handle(ctx, wire.Request{
		ID: "scoped-tree-remove", Subject: "alice",
		Tool: "file.remove", Resource: project,
		InvocationID: "scoped-tree-remove-1", Args: args,
	})
	if !result.OK {
		t.Fatalf("unrelated sibling blocked tree operation: %+v", result)
	}
	if _, err := os.Stat(project); !os.IsNotExist(err) {
		t.Fatalf("target should be removed, stat error=%v", err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("protected sibling was affected: %v", err)
	}
}

func TestSensitiveTreePreflightRejectsTargetSymlink(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()
	project := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	err := b.requireSensitiveSubtreeWork(ctx, "alice", alias, []string{root})
	if err == nil {
		t.Fatal("preflight must reject symlink target")
	}
	if _, err := os.Stat(project); err != nil {
		t.Fatal(err)
	}
}
