package broker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/jobs"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

type fakeComposeAsyncStarter struct {
	starts int
	fail   error
	unit   string
	dir    string
	action string
}

func (f *fakeComposeAsyncStarter) StartComposeJob(_ context.Context, dir, action, unit string) error {
	f.starts++
	f.dir, f.action, f.unit = dir, action, unit
	return f.fail
}

func TestComposeJobStatusClassification(t *testing.T) {
	tests := []struct {
		name string
		raw string
		want string
		exit int
		fail bool
	}{
		{"running", "LoadState=loaded\nActiveState=active\nResult=success\nExecMainStatus=0", "running", 0, false},
		{"oneshot-done", "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\nExecMainStatus=0", "done", 0, false},
		{"oneshot-uncertain", "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\nExecMainStatus=", "", 0, true},
		{"done", "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=0", "done", 0, false},
		{"failed", "LoadState=loaded\nActiveState=failed\nResult=exit-code\nExecMainStatus=28", "failed", 28, false},
		{"timeout", "LoadState=loaded\nActiveState=inactive\nResult=timeout\nExecMainStatus=1", "failed", 1, false},
		{"missing", "LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0", "", 0, true},
		{"uncertain", "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=", "", 0, true},
		{"corrupt", "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=abc", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, exit, err := classifyComposeUnit(tc.raw)
			if tc.fail {
				if err == nil {
					t.Fatalf("expected reconciliation error, got %q", status)
				}
				return
			}
			if err != nil || status != tc.want {
				t.Fatalf("status=%q err=%v want=%q", status, err, tc.want)
			}
			if tc.want == "done" || tc.want == "failed" {
				if exit == nil || *exit != tc.exit {
					t.Fatalf("exit=%v want=%d", exit, tc.exit)
				}
			}
		})
	}
}

func TestComposeJobIDDoesNotExposeSubjects(t *testing.T) {
	id1 := composeJobID("user@example.com", "same-operation")
	id2 := composeJobID("other@example.com", "same-operation")
	if id1 == id2 || strings.Contains(id1, "user") || !strings.HasPrefix(id1, "vps-agent-compose-") {
		t.Fatalf("unsafe/non-unique job IDs: %s, %s", id1, id2)
	}
	if composeJobID("user@example.com", "same-operation") != id1 {
		t.Fatal("job ID is not deterministic across reconnect")
	}
}

func TestComposeArgsTypedAllowlist(t *testing.T) {
	for _, action := range []string{"pull", "up", "down"} {
		args, err := composeArgs(action)
		if err != nil || len(args) == 0 {
			t.Fatalf("action %q rejected: %v", action, err)
		}
	}
	for _, action := range []string{"rm", "exec", "pull; rm -rf /", "", "build", "run"} {
		if _, err := composeArgs(action); err == nil {
			t.Fatalf("arbitrary Compose action allowed: %q", action)
		}
	}
}

func TestComposeAsyncJobIsDurableIdempotentAndScoped(t *testing.T) {
	b, _, store, root := testBroker(t)
	b.Jobs = &jobs.Manager{State: store}
	starter := &fakeComposeAsyncStarter{}
	req := wire.Request{
		ID: "call-1", Subject: "alice", Tool: "compose.pull",
		Resource: root, InvocationID: "compose-stable-id",
	}
	resp := b.composeMutationAsync(context.Background(), req, "pull", starter)
	if !resp.OK {
		t.Fatalf("queue failed: %+v", resp)
	}
	var queued map[string]any
	if err := json.Unmarshal(resp.Result, &queued); err != nil {
		t.Fatal(err)
	}
	jobID, ok := queued["job_id"].(string)
	if !ok || jobID == "" || queued["state"] != "running" {
		t.Fatalf("unexpected queued result: %+v", queued)
	}
	if starter.starts != 1 || starter.unit != jobID || starter.action != "pull" || starter.dir != root {
		t.Fatalf("unexpected starter invocation: %+v", starter)
	}
	rec, err := store.GetJob(context.Background(), jobID)
	if err != nil || rec.State != "running" || rec.Subject != "alice" {
		t.Fatalf("job not persisted: %+v err=%v", rec, err)
	}

	// Retrying the original invocation returns the same durable result,
	// without launching another Docker task.
	req.ID = "call-2"
	resp = b.composeMutationAsync(context.Background(), req, "pull", starter)
	if !resp.OK || starter.starts != 1 {
		t.Fatalf("duplicate started another Docker task: %+v starts=%d", resp, starter.starts)
	}

	// A second operation must fail closed while the first status is active
	// or cannot be reconciled to systemd. No duplicate pull is allowed.
	req.ID, req.InvocationID = "call-3", "another-operation"
	resp = b.composeMutationAsync(context.Background(), req, "pull", starter)
	if resp.OK || resp.Error == nil || resp.Error.Code != "resource_busy" || starter.starts != 1 {
		t.Fatalf("concurrent Compose action not blocked: %+v starts=%d", resp, starter.starts)
	}
	// A busy rejection occurs BEFORE any effect was dispatched. It must
	// release the operation intent for safe retry with the original ID.
	if err := store.AbortOperation(context.Background(), "another-operation"); err == nil {
		t.Fatal("busy pre-dispatch left an orphan pending operation")
	}

	// No other subject can read this job even if it guesses the job ID.
	foreign := b.Handle(context.Background(), wire.Request{
		ID: "job-from-bob", Tool: "job.status", Subject: "bob", Resource: jobID,
	})
	if foreign.OK || foreign.Error == nil || foreign.Error.Code != "job_error" {
		t.Fatalf("foreign job status leaked: %+v", foreign)
	}
}

func TestComposeAsyncDispatchFailureNeverRetriesBlindly(t *testing.T) {
	b, _, store, root := testBroker(t)
	b.Jobs = &jobs.Manager{State: store}
	starter := &fakeComposeAsyncStarter{fail: errors.New("lost systemd acknowledgment")}
	req := wire.Request{
		ID: "call-1", Subject: "alice", Tool: "compose.up",
		Resource: root, InvocationID: "uncertain-compose-start",
	}
	resp := b.composeMutationAsync(context.Background(), req, "up", starter)
	if resp.OK || starter.starts != 1 {
		t.Fatalf("failed dispatch not reported: %+v", resp)
	}
	rec, err := store.GetJob(context.Background(), composeJobID(req.Subject, req.InvocationID))
	if err != nil || rec.State != "starting" {
		t.Fatalf("uncertain dispatch must stay pending, got %+v err=%v", rec, err)
	}
	req.ID = "call-2"
	resp = b.composeMutationAsync(context.Background(), req, "up", starter)
	if !resp.OK || starter.starts != 1 {
		t.Fatalf("same operation should recover job ID, not relaunch: %+v", resp)
	}
	req.ID, req.InvocationID = "call-3", "other"
	resp = b.composeMutationAsync(context.Background(), req, "up", starter)
	if resp.OK || resp.Error == nil || resp.Error.Code != "resource_busy" || starter.starts != 1 {
		t.Fatalf("new operation bypassed uncertain dispatch: %+v", resp)
	}
	if err := store.AbortOperation(context.Background(), "other"); err == nil {
		t.Fatal("busy pre-dispatch left an orphan pending operation")
	}
}

func TestComposeAsyncJobConflictDoesNotStartWork(t *testing.T) {
	b, _, store, root := testBroker(t)
	b.Jobs = &jobs.Manager{State: store}
	starter := &fakeComposeAsyncStarter{}
	req := wire.Request{
		ID: "call-1", Subject: "alice", Tool: "compose.pull",
		Resource: root, InvocationID: "same-id",
	}
	if resp := b.composeMutationAsync(context.Background(), req, "pull", starter); !resp.OK {
		t.Fatal(resp)
	}
	req.ID, req.Resource = "call-2", filepath.Join(root, "other")
	resp := b.composeMutationAsync(context.Background(), req, "pull", starter)
	if resp.OK || resp.Error == nil || resp.Error.Code != "idempotency_conflict" || starter.starts != 1 {
		t.Fatalf("operation ID allowed conflicting action: %+v", resp)
	}
}
