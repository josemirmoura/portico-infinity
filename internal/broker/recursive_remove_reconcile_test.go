package broker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestFailedRecursiveRemoveRemainsUnresolvedAndCannotReplayBlindly(t *testing.T) {
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()

	args, err := json.Marshal(map[string]any{"recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	req := wire.Request{
		ID: "first", InvocationID: "recursive-unresolved-1", Subject: "alice",
		Tool: "file.remove", Resource: filepath.Join(root, "candidate"),
		Args: args,
	}
	calls := 0
	fn := func() (any, error) {
		calls++
		return nil, errors.New("simulated failure after external filesystem effects")
	}
	response := b.fileMutation(context.Background(), req, map[string]any{"recursive": true}, fn)
	if response.OK {
		t.Fatal("failed recursive removal was reported as success")
	}
	req.ID = "retry"
	second := b.fileMutation(context.Background(), req, map[string]any{"recursive": true}, fn)
	if second.OK || calls != 1 {
		t.Fatalf("unresolved operation was blindly retried: calls=%d response=%+v", calls, second)
	}
}

func TestFailedNonrecursiveFileMutationRetainsOriginalRetrySemantics(t *testing.T) {
	b, root, store := sensitiveTestBroker(t)
	defer store.Close()

	req := wire.Request{
		ID: "first", InvocationID: "nonrecursive-retry-1", Subject: "alice",
		Tool: "file.remove", Resource: filepath.Join(root, "candidate"),
		Args: []byte(`{"recursive":false}`),
	}
	calls := 0
	fn := func() (any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("no external effect")
		}
		return map[string]any{"removed": true}, nil
	}
	response := b.fileMutation(context.Background(), req, map[string]any{"recursive": false}, fn)
	if response.OK {
		t.Fatal("failed operation was reported as success")
	}
	req.ID = "retry"
	second := b.fileMutation(context.Background(), req, map[string]any{"recursive": false}, fn)
	if !second.OK || calls != 2 {
		t.Fatalf("nonrecursive retry unexpectedly denied: calls=%d response=%+v", calls, second)
	}
}
