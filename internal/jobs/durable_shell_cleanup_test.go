package jobs

import (
    "context"
    "errors"
    "path/filepath"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

type durableCleanupFake struct{
    terminalFake
    retireCalls int
    failRetire bool
}
func(f *durableCleanupFake)RetireTerminalUnit(_ context.Context,job Job)error{
    f.retireCalls++
    if f.failRetire{return errors.New("synthetic systemd stop failure")}
    return nil
}

func TestDurableCleanupSurvivesFailureRestartAndPreservesTerminal(t *testing.T){
    ctx:=context.Background()
    path:=filepath.Join(t.TempDir(),"jobs.db")
    db,err:=state.Open(path)
    if err!=nil{t.Fatal(err)}
    exit:=17
    runner:=&durableCleanupFake{terminalFake:terminalFake{result:TerminalStatus{
        State:"failed",Detail:"exit-code",ExitCode:&exit,
    }},failRetire:true}
    manager:=&Manager{State:db,Runner:runner,DurableShellCleanup:true}
    rec,err:=manager.Start(ctx,"owner","shell.exec","/opt/synthetic","",sandbox.Spec{
        Unit:"vps-agent-job-1234567890abcdef",Command:"exit 17",CWD:"/opt/synthetic",Runtime:time.Minute,
    })
    if err!=nil{t.Fatal(err)}
    saved,err:=manager.Status(ctx,"owner",rec.ID)
    if err!=nil||saved.State!="failed"||saved.ExitCode==nil||*saved.ExitCode!=17{
        t.Fatalf("terminal result=%+v err=%v",saved,err)
    }
    pending,err:=db.PendingShellUnitCleanup(ctx,4)
    if err!=nil||len(pending)!=1{t.Fatalf("lost pending receipt: %+v err=%v",pending,err)}
    completed,failed,err:=manager.SweepShellUnitCleanup(ctx,4)
    if err!=nil||completed!=0||failed!=1 {t.Fatalf("failed cleanup %+v %+v %v",completed,failed,err)}
    if err:=db.Close();err!=nil{t.Fatal(err)}
    reopened,err:=state.Open(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    afterCrash:=&durableCleanupFake{terminalFake:terminalFake{result:TerminalStatus{State:"running"}}}
    manager=&Manager{State:reopened,Runner:afterCrash,DurableShellCleanup:true}
    completed,failed,err=manager.SweepShellUnitCleanup(ctx,4)
    if err!=nil||completed!=1||failed!=0||afterCrash.retireCalls!=1{
        t.Fatalf("restart retry failed: completed=%d failed=%d err=%v calls=%d",completed,failed,err,afterCrash.retireCalls)
    }
    completed,failed,err=manager.SweepShellUnitCleanup(ctx,4)
    if err!=nil||completed!=0||failed!=0||afterCrash.retireCalls!=1{
        t.Fatalf("cleanup replayed: completed=%d failed=%d err=%v calls=%d",completed,failed,err,afterCrash.retireCalls)
    }
    final,err:=reopened.GetJob(ctx,rec.ID)
    if err!=nil||final.State!="failed"||final.ExitCode==nil||*final.ExitCode!=17{
        t.Fatalf("cleanup changed terminal checkpoint: %+v err=%v",final,err)
    }
    if runner.collects!=0 {t.Fatal("old non-durable collector invoked in Infinity")}
}

func TestCleanupOnlyAfterTerminalProofAndCommunityIsolation(t *testing.T) {
    ctx:=context.Background()
    db,err:=state.Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    code:=0
    runner:=&durableCleanupFake{terminalFake:terminalFake{result:TerminalStatus{State:"running"}}}
    manager:=&Manager{State:db,Runner:runner,DurableShellCleanup:true}
    rec,err:=manager.Start(ctx,"owner","shell.exec","/opt/synthetic","",sandbox.Spec{
        Unit:"vps-agent-job-aabbccddeeff0099",Command:"true",CWD:"/opt/synthetic",Runtime:time.Minute,
    })
    if err!=nil{t.Fatal(err)}
    _,err=manager.Status(ctx,"owner",rec.ID)
    if err!=nil{t.Fatal(err)}
    pending,err:=db.PendingShellUnitCleanup(ctx,3)
    if err!=nil||len(pending)!=0{t.Fatalf("cleaned running job: %+v",pending)}
    runner.result=TerminalStatus{State:"done",ExitCode:&code}
    _,err=manager.Status(ctx,"owner",rec.ID)
    if err!=nil{t.Fatal(err)}
    pending,err=db.PendingShellUnitCleanup(ctx,3)
    if err!=nil||len(pending)!=1{t.Fatalf("terminal not queued: %+v err=%v",pending,err)}
    db2,err:=state.Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db2.Close()
    legacy:=&durableCleanupFake{terminalFake:terminalFake{result:TerminalStatus{State:"done",ExitCode:&code}}}
    m2:=&Manager{State:db2,Runner:legacy} // disabled by default
    rec2,err:=m2.Start(ctx,"owner","shell.exec","/opt/synthetic","",sandbox.Spec{
        Unit:"legacy-test",Command:"true",CWD:"/opt/synthetic",Runtime:time.Minute,
    })
    if err!=nil{t.Fatal(err)}
    _,err=m2.Status(ctx,"owner",rec2.ID)
    if err!=nil{t.Fatal(err)}
    if legacy.collects!=1 {t.Fatal("Community legacy callback changed")}
    pending,err=db2.PendingShellUnitCleanup(ctx,3)
    if err!=nil||len(pending)!=0{t.Fatal("Community created cleanup receipt")}
}
func TestDeterministicShellUnitValidation(t *testing.T){
    for _,name:=range []string{
        "vps-agent-job-0123456789abcdef","vps-agent-job-aabbccddeeff0099",
    }{if !safeRetirableShellUnit(name){t.Fatalf("valid unit denied: %q",name)}}
    for _,name:=range []string{
        "", "nginx.service","vps-agent-compose-0123456789abcdef",
        "vps-agent-job-0123456789abcdef.service","vps-agent-job-0123456789abcdeg",
        "vps-agent-job-0123456789ABCDEf","vps-agent-job-aaaa;sudo rm -rf /",
    }{if safeRetirableShellUnit(name){t.Fatalf("unsafe unit accepted: %q",name)}}
}
