package jobs

import (
    "context"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

func addOldJob(t *testing.T, db *state.Store, id, tool string, deadline time.Time) {
    t.Helper()
    if err := db.CreateJob(context.Background(), state.JobRecord{
        ID:id, UnitName:id, Subject:"alice", Tool:tool, Resource:"/opt/workspace",
        State:"running", CreatedAt:time.Now().Add(-2*time.Minute), Deadline:deadline,
    }); err != nil { t.Fatal(err) }
}

func TestUnpolledShellJobReconcilesAndCollectsOnce(t *testing.T) {
    db, err := state.Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    code := 0
    runner := &terminalFake{result: TerminalStatus{State:"done", ExitCode:&code}}
    m := &Manager{State:db, Runner:runner}
    addOldJob(t,db,"finished-without-poll","shell.exec",time.Now().Add(time.Minute))
    got, err := m.ReconcileShellJobs(context.Background(), 64)
    if err != nil || got.Inspected != 1 || got.Completed != 1 || got.RequiresOperator != 0 {
        t.Fatalf("sweep=%+v err=%v",got,err)
    }
    rec, err := db.GetJob(context.Background(),"finished-without-poll")
    if err != nil || rec.State != "done" || rec.ExitCode == nil || *rec.ExitCode != 0 || runner.collects != 1 {
        t.Fatalf("record=%+v err=%v collections=%d", rec, err, runner.collects)
    }
    got, err = m.ReconcileShellJobs(context.Background(), 64)
    if err != nil || got.Inspected != 0 || runner.collects != 1 {
        t.Fatalf("repeat=%+v err=%v collections=%d", got,err,runner.collects)
    }
}

func TestExpiredUnknownShellJobNeedsOperatorNoFakeSuccess(t *testing.T) {
    db, err := state.Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    runner := &terminalFake{failure:context.DeadlineExceeded}
    m := &Manager{State:db, Runner:runner}
    addOldJob(t,db,"vanished-before-status","shell.exec",time.Now().Add(-2*time.Minute))
    got, err := m.ReconcileShellJobs(context.Background(), 64)
    if err != nil || got.Inspected != 1 || got.RequiresOperator != 1 || got.Completed != 0 {
        t.Fatalf("sweep=%+v err=%v",got,err)
    }
    rec, err := m.Status(context.Background(),"alice","vanished-before-status")
    if err != nil || rec.State != "reconcile_required" || rec.ExitCode != nil || runner.collects != 0 {
        t.Fatalf("ambiguous job falsely classified: rec=%+v err=%v collections=%d", rec,err,runner.collects)
    }
    active, err := db.ListActiveJobs(context.Background())
    if err != nil || len(active) != 0 { t.Fatalf("ambiguous record still active: %+v err=%v", active,err) }
}

func TestReconcilerRespectsYoungForeignAndUnexpiredJobs(t *testing.T) {
    db, err := state.Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    runner := &terminalFake{failure:context.DeadlineExceeded}
    m := &Manager{State:db, Runner:runner}
    addOldJob(t,db,"compose-job","compose.up",time.Now().Add(-time.Hour))
    addOldJob(t,db,"unexpired-shell","shell.exec",time.Now().Add(time.Hour))
    if err := db.CreateJob(context.Background(), state.JobRecord{
        ID:"young-shell",UnitName:"young-shell",Subject:"alice",Tool:"shell.exec",
        State:"running",Deadline:time.Now().Add(-2*time.Hour),
    }); err != nil { t.Fatal(err) }
    got, err := m.ReconcileShellJobs(context.Background(), 64)
    if err != nil || got.Inspected != 1 || got.Completed != 0 || got.RequiresOperator != 0 {
        t.Fatalf("unrelated or fresh job changed: %+v err=%v",got,err)
    }
    active, err := db.ListActiveJobs(context.Background())
    if err != nil || len(active) != 3 {
        t.Fatalf("expected intact jobs: active=%+v err=%v",active,err)
    }
}
