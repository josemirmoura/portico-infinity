package broker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

func TestDeleteEntryGuardBlocksSecretInsertedAfterPreflight(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()

	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := b.requireSensitiveSubtreeWork(ctx, "alice", project, []string{root}); err != nil {
		t.Fatalf("empty project preflight failed: %v", err)
	}
	// A separate writer created this secret after the preflight completed.
	secret := filepath.Join(project, ".env")
	if err := os.WriteFile(secret, []byte("not for AI"), 0600); err != nil {
		t.Fatal(err)
	}
	err := b.FS.RemoveWithEntryGuard(project, true, func(path string, info os.FileInfo) error {
		return b.authorizeSensitiveDeletionEntry(ctx, "alice", path, info)
	})
	if err == nil || !strings.Contains(err.Error(), "work approval") {
		t.Fatalf("newly inserted secret was not denied: %v", err)
	}
	if data, readErr := os.ReadFile(secret); readErr != nil || string(data) != "not for AI" {
		t.Fatalf("late secret was deleted or modified: %q %v", data, readErr)
	}
}

func TestDeleteEntryGuardBlocksHardlinkInsertedAfterPreflight(t *testing.T) {
	ctx := context.Background()
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := b.requireSensitiveSubtreeWork(ctx, "alice", project, []string{root}); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "secret-original.txt")
	if err := os.WriteFile(original, []byte("external record"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "ordinary-looking.txt")
	if err := os.Link(original, alias); err != nil {
		t.Skipf("cannot exercise hardlinks: %v", err)
	}
	err := b.FS.RemoveWithEntryGuard(project, true, func(path string, info os.FileInfo) error {
		return b.authorizeSensitiveDeletionEntry(ctx, "alice", path, info)
	})
	if err == nil {
		t.Fatal("late hardlink alias was not denied")
	}
	if _, statErr := os.Stat(alias); statErr != nil {
		t.Fatalf("hardlink was changed despite denial: %v", statErr)
	}
	if data, readErr := os.ReadFile(original); readErr != nil || string(data) != "external record" {
		t.Fatalf("external record was modified: %q %v", data, readErr)
	}
}
