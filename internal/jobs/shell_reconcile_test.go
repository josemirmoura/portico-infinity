package jobs

import (
    "context"
    "fmt"
    "strings"
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


type cancelRaceRunner struct {
    fakeRunner
    store *state.Store
}
func (f *cancelRaceRunner) Cancel(ctx context.Context, job Job) error {
    code:=0
    return f.store.UpdateJobState(ctx,job.ID,"done","completed during cancel",&code)
}

func TestCancellationCannotOverwriteTerminalResult(t *testing.T) {
    ctx:=context.Background()
    db,err:=state.Open(":memory:")
    if err!=nil { t.Fatal(err) }
    defer db.Close()
    addOldJob(t,db,"shell-cancel-terminal","shell.exec",time.Now().Add(time.Minute))
    code:=0
    if err:=db.UpdateJobState(ctx,"shell-cancel-terminal","done","completed",&code); err!=nil {t.Fatal(err)}
    runner:=&fakeRunner{}
    m:=&Manager{State:db,Runner:runner}
    if err:=m.Cancel(ctx,"alice","shell-cancel-terminal"); err==nil {
        t.Fatal("terminal job was cancellable")
    }
    if runner.cancelled {t.Fatal("terminal systemd unit was contacted")}
    r,err:=db.GetJob(ctx,"shell-cancel-terminal")
    if err!=nil || r.State!="done" || r.ExitCode==nil || *r.ExitCode!=0 {t.Fatalf("record=%+v err=%v",r,err)}

    addOldJob(t,db,"shell-cancel-race","shell.exec",time.Now().Add(time.Minute))
    m.Runner=&cancelRaceRunner{store:db}
    if err:=m.Cancel(ctx,"alice","shell-cancel-race"); err==nil {
        t.Fatal("late concurrent completion was overwritten")
    }
    r,err=db.GetJob(ctx,"shell-cancel-race")
    if err!=nil || r.State!="done" || r.ExitCode==nil || *r.ExitCode!=0 {
        t.Fatalf("concurrent terminal state corrupted: %+v err=%v",r,err)
    }
}


type rotatingStatusRunner struct {
    fakeRunner
    cleanups int
}
func (r *rotatingStatusRunner) DetailedStatus(_ context.Context, job Job) (TerminalStatus, error) {
    if strings.HasPrefix(job.Unit,"a-stuck-") {
        return TerminalStatus{State:"running"},nil
    }
    code:=0
    return TerminalStatus{State:"done",ExitCode:&code},nil
}
func (r *rotatingStatusRunner) CollectFinished(context.Context,Job) {r.cleanups++}

func TestShellReconcileRotatesUnderBacklogOfLiveJobs(t *testing.T) {
    db,err:=state.Open(":memory:")
    if err!=nil {t.Fatal(err)}
    defer db.Close()
    ctx:=context.Background()
    created:=time.Now().Add(-time.Hour).Truncate(time.Second)
    for i:=0;i<100;i++ {
        id:=fmt.Sprintf("a-stuck-%03d",i)
        if i>=80 {id=fmt.Sprintf("z-complete-%03d",i-80)}
        if err:=db.CreateJob(ctx,state.JobRecord{
            ID:id,UnitName:id,Subject:"owner",Tool:"shell.exec",
            State:"running",Deadline:time.Now().Add(time.Hour),
            CreatedAt:created,
        });err!=nil {t.Fatal(err)}
    }
    // Foreign jobs share the same creation timestamp and must not occupy
    // any of the bounded slots for shell reconciliation.
    for i:=0;i<150;i++ {
        id:=fmt.Sprintf("compose-%03d",i)
        if err:=db.CreateJob(ctx,state.JobRecord{
            ID:id,UnitName:id,Subject:"owner",Tool:"compose.up",
            State:"running",Deadline:time.Now().Add(time.Hour),
            CreatedAt:created,
        });err!=nil {t.Fatal(err)}
    }
    runner:=&rotatingStatusRunner{}
    manager:=&Manager{State:db,Runner:runner}
    first,err:=manager.ReconcileShellJobs(ctx,64)
    if err!=nil || first.Inspected!=64 || first.Completed!=0 {
        t.Fatalf("first sweep=%+v err=%v",first,err)
    }
    second,err:=manager.ReconcileShellJobs(ctx,64)
    if err!=nil || second.Inspected!=36 || second.Completed!=20 || runner.cleanups!=20 {
        t.Fatalf("later completed jobs starved: %+v err=%v cleanups=%d",second,err,runner.cleanups)
    }
    third,err:=manager.ReconcileShellJobs(ctx,64)
    if err!=nil || third.Inspected!=64 || third.Completed!=0 || runner.cleanups!=20 {
        t.Fatalf("wrap did not resume older active jobs: %+v err=%v cleanups=%d",
            third,err,runner.cleanups)
    }
    // SQLite still contains foreign Compose records untouched by this
    // reconciler. All completed shell checkpoints remain durable.
    for i:=0;i<20;i++ {
        id:=fmt.Sprintf("z-complete-%03d",i)
        record,err:=db.GetJob(ctx,id)
        if err!=nil || record.State!="done" || record.ExitCode==nil || *record.ExitCode!=0 {
            t.Fatalf("lost completion %s: %+v err=%v",id,record,err)
        }
    }
    foreign,err:=db.GetJob(ctx,"compose-100")
    if err!=nil || foreign.State!="running" {
        t.Fatalf("foreign Compose job mutated: %+v err=%v",foreign,err)
    }
}
