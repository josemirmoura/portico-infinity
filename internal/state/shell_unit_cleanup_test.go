package state

import (
    "context"
    "path/filepath"
    "testing"
    "time"
)

func TestDurableShellCleanupAtomicityAndRestart(t *testing.T) {
    ctx:=context.Background()
    path:=filepath.Join(t.TempDir(),"jobs.db")
    db,err:=Open(path)
    if err!=nil{t.Fatal(err)}
    err=db.CreateJob(ctx,JobRecord{ID:"terminal",UnitName:"vps-agent-job-1234567890abcdef",
        Tool:"shell.exec",Subject:"alice",State:"running",Deadline:time.Now().Add(time.Minute)})
    if err!=nil{t.Fatal(err)}
    exit:=19
    changed,err:=db.CompleteShellJobAndQueueCleanup(ctx,"terminal","failed","exit-code",&exit)
    if err!=nil||!changed{t.Fatalf("atomic transition: changed=%v err=%v",changed,err)}
    changed,err=db.CompleteShellJobAndQueueCleanup(ctx,"terminal","done","late",&exit)
    if err!=nil||changed{t.Fatalf("terminal was overwritten: changed=%v err=%v",changed,err)}
    err=db.Close()
    if err!=nil{t.Fatal(err)}
    reopened,err:=Open(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    saved,err:=reopened.GetJob(ctx,"terminal")
    if err!=nil||saved.State!="failed"||saved.ExitCode==nil||*saved.ExitCode!=19 {
        t.Fatalf("checkpoint not durable: %+v err=%v",saved,err)
    }
    pending,err:=reopened.PendingShellUnitCleanup(ctx,10)
    if err!=nil||len(pending)!=1||pending[0].Unit!="vps-agent-job-1234567890abcdef" {
        t.Fatalf("outbox not durable: %+v err=%v",pending,err)
    }
    err=reopened.FailShellUnitCleanup(ctx,"terminal",pending[0].Unit,"synthetic failure")
    if err!=nil{t.Fatal(err)}
    pending,err=reopened.PendingShellUnitCleanup(ctx,10)
    if err!=nil||len(pending)!=1||pending[0].Attempts!=1 {t.Fatalf("retry lost: %+v err=%v",pending,err)}
    if err:=reopened.FinishShellUnitCleanup(ctx,"terminal",pending[0].Unit);err!=nil{t.Fatal(err)}
    pending,err=reopened.PendingShellUnitCleanup(ctx,10)
    if err!=nil||len(pending)!=0{t.Fatalf("cleanup still pending: %+v err=%v",pending,err)}
    if err:=reopened.FinishShellUnitCleanup(ctx,"terminal",pendingOrUnit());err==nil{
        t.Fatal("duplicate receipt acknowledged")
    }
    saved,err=reopened.GetJob(ctx,"terminal")
    if err!=nil||saved.State!="failed"||*saved.ExitCode!=19 {t.Fatalf("cleanup changed terminal result: %+v err=%v",saved,err)}
}
func pendingOrUnit()string{return "vps-agent-job-1234567890abcdef"}

func TestDurableShellCleanupRejectsUnprovenAndForeign(t *testing.T){
    ctx:=context.Background()
    db,err:=Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    for _,tc:=range []struct{name,tool,state string}{
        {"foreign","compose.up","running"},
        {"unproven","shell.exec","running"},
        {"terminal","shell.exec","done"},
    }{
        if err:=db.CreateJob(ctx,JobRecord{ID:tc.name,UnitName:"vps-agent-job-1234567890abcdef",
            Tool:tc.tool,Subject:"alice",State:tc.state,Deadline:time.Now().Add(time.Minute)});err!=nil{t.Fatal(err)}
    }
    zero:=0
    if _,err:=db.CompleteShellJobAndQueueCleanup(ctx,"foreign","done","",&zero);err==nil{t.Fatal("foreign unit accepted")}
    if _,err:=db.CompleteShellJobAndQueueCleanup(ctx,"unproven","done","",nil);err==nil{t.Fatal("missing proof accepted")}
    changed,err:=db.CompleteShellJobAndQueueCleanup(ctx,"terminal","done","",&zero)
    if err!=nil||changed{t.Fatalf("unexpected terminal replay: %v %v",changed,err)}
    pending,err:=db.PendingShellUnitCleanup(ctx,2)
    if err!=nil||len(pending)!=0{t.Fatalf("unsafe cleanup inserted: %+v %v",pending,err)}
}
