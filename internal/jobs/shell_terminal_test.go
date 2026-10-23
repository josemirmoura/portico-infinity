package jobs

import (
    "context"
    "strings"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

func TestClassifyShellUnitRequiresTrustedTerminalEvidence(t *testing.T) {
    cases := []struct {
        name string
        raw string
        want string
        code int
        hasCode bool
        shouldErr bool
    }{
        {"running", "LoadState=loaded\nActiveState=activating\nSubState=start\nResult=success\nExecMainStatus=0\n", "running", 0, false, false},
        {"success", "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\nExecMainCode=1\nExecMainStatus=0\n", "done", 0, true, false},
        {"command failure", "LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=17\n", "failed", 17, true, false},
        {"chdir failure", "LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=200\n", "failed", 200, true, false},
        {"collected", "LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0\n", "", 0, false, true},
        {"inactive stopped unit", "LoadState=loaded\nActiveState=inactive\nResult=success\nExecMainStatus=0\n", "", 0, false, true},
        {"active not proven success", "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\nExecMainStatus=0\n", "", 0, false, true},
        {"failed without result", "LoadState=loaded\nActiveState=failed\nResult=\nExecMainStatus=0\n", "", 0, false, true},
        {"invalid exit", "LoadState=loaded\nActiveState=failed\nResult=exit-code\nExecMainStatus=nonsense\n", "", 0, false, true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := classifyShellUnit(tc.raw)
            if (err != nil) != tc.shouldErr {
                t.Fatalf("got=%+v error=%v; expected error=%v", got, err, tc.shouldErr)
            }
            if tc.shouldErr { return }
            if got.State != tc.want { t.Fatalf("state=%s, want=%s", got.State, tc.want) }
            if (got.ExitCode != nil) != tc.hasCode { t.Fatalf("exit code=%v want hasCode=%v", got.ExitCode, tc.hasCode) }
            if tc.hasCode && *got.ExitCode != tc.code { t.Fatalf("exit code=%d, want=%d", *got.ExitCode, tc.code) }
        })
    }
}

type terminalFake struct {
    fakeRunner
    result TerminalStatus
    failure error
    collects int
}
func (f *terminalFake) DetailedStatus(context.Context, Job) (TerminalStatus, error) {
    return f.result, f.failure
}
func (f *terminalFake) CollectFinished(context.Context, Job) { f.collects++ }

func TestShellTerminalStatusIsPersistedBeforeCollection(t *testing.T) {
    for _, tc := range []struct { state string; code int }{{"done", 0}, {"failed", 200}} {
        t.Run(tc.state, func(t *testing.T) {
            db, err := state.Open(":memory:")
            if err != nil { t.Fatal(err) }
            defer db.Close()
            code := tc.code
            runner := &terminalFake{result: TerminalStatus{State: tc.state, ExitCode: &code}}
            m := &Manager{State: db, Runner: runner}
            ctx := context.Background()
            rec, err := m.Start(ctx, "owner", "shell.exec", "/opt/test", "", sandbox.Spec{
                Unit: "terminal-test-"+tc.state, Command: "true", CWD: "/opt/test", Runtime: time.Minute,
            })
            if err != nil { t.Fatal(err) }
            got, err := m.Status(ctx, "owner", rec.ID)
            if err != nil { t.Fatal(err) }
            if got.State != tc.state || got.ExitCode == nil || *got.ExitCode != code {
                t.Fatalf("wrong persisted terminal result: %+v", got)
            }
            if runner.collects != 1 { t.Fatalf("collections=%d want 1", runner.collects) }
            persisted, err := db.GetJob(ctx, rec.ID)
            if err != nil || persisted.State != tc.state || persisted.ExitCode == nil || *persisted.ExitCode != code {
                t.Fatalf("persisted=%+v err=%v", persisted, err)
            }
            got, err = m.Status(ctx, "owner", rec.ID)
            if err != nil || got.State != tc.state || runner.collects != 1 {
                t.Fatalf("repeat: got=%+v err=%v collects=%d", got, err, runner.collects)
            }
        })
    }
}

func TestShellStatusUnknownDoesNotBecomeSuccessful(t *testing.T) {
    db, err := state.Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    runner := &terminalFake{failure: context.DeadlineExceeded}
    m := &Manager{State: db, Runner: runner}
    ctx := context.Background()
    rec, err := m.Start(ctx, "owner", "shell.exec", "/opt/test", "", sandbox.Spec{
        Unit: "terminal-unknown", Command: "true", CWD: "/opt/test", Runtime: time.Minute,
    })
    if err != nil { t.Fatal(err) }
    _, err = m.Status(ctx, "owner", rec.ID)
    if err == nil || !strings.Contains(err.Error(), "reconcile_required") {
        t.Fatalf("missing reconciliation error: %v", err)
    }
    saved, err := db.GetJob(ctx, rec.ID)
    if err != nil || saved.State != "running" || saved.ExitCode != nil || runner.collects != 0 {
        t.Fatalf("saved=%+v err=%v collects=%d", saved, err, runner.collects)
    }
}
