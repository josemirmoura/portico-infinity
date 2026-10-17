package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

// A real Broker request, not just a securefs unit test. The protected-file
// preflight MUST complete before any tree removal effect, even above 50k.
func TestInfinityDelete001LargeTreeThroughBroker(t *testing.T) {
	if testing.Short() {
		t.Skip("large disposable filesystem E2E")
	}
	b, root, state := sensitiveTestBroker(t)
	defer state.Close()
	b.Policy.Filesystem.Actions = append(b.Policy.Filesystem.Actions, "remove_recursive")

	project := filepath.Join(root, "large-project")
	sibling := filepath.Join(root, "sibling")
	for _, dir := range []string{project, sibling} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	secretSibling := filepath.Join(sibling, ".env")
	if err := os.WriteFile(secretSibling, []byte("do-not-touch"), 0600); err != nil {
		t.Fatal(err)
	}
	const count = 50019
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(project, fmt.Sprintf("file-%06d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(project, ".env")
	if err := os.WriteFile(secret, []byte("project-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{"recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	request := func(invocation string) wire.Response {
		return b.Handle(context.Background(), wire.Request{
			ID: invocation, Subject: "alice", Tool: "file.remove",
			InvocationID: invocation, Resource: project, Args: args,
		})
	}
	if denied := request("delete001-secret-denied"); denied.OK {
		t.Fatal("protected secret in large tree must deny removal")
	}
	// A denied preflight cannot destroy any entries, not only the .env.
	if entries, err := os.ReadDir(project); err != nil || len(entries) != count+1 {
		t.Fatalf("preflight denial had partial effects: count=%d err=%v", len(entries), err)
	}
	if err := os.Remove(secret); err != nil {
		t.Fatal(err)
	}
	if res := request("delete001-unprotected-allowed"); !res.OK {
		t.Fatalf("clean 50k+ target rejected: %+v", res)
	}
	if _, err := os.Stat(project); !os.IsNotExist(err) {
		t.Fatalf("large tree still present after successful Broker removal: %v", err)
	}
	if data, err := os.ReadFile(secretSibling); err != nil || string(data) != "do-not-touch" {
		t.Fatalf("unrelated protected sibling was modified: %q %v", data, err)
	}
}
