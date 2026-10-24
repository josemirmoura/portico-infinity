package broker

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)
func TestComposeCleanupRealSystemdSmoke(t *testing.T){
    if os.Getenv("VPS_AGENT_SYSTEMD_JOB_SMOKE")!="1"{
        t.Skip("requires disposable root-owned real systemd runner")
    }
    if os.Geteuid()!=0{t.Skip("requires disposable privileged runner")}
    if _,err:=exec.LookPath("systemd-run");err!=nil{t.Skip("systemd-run unavailable")}
    ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second)
    defer cancel()
    unitID:=fmt.Sprintf("vps-agent-compose-%032x",os.Getpid())
    unit:=unitID+".service"
    result,err:=exec.CommandContext(ctx,"systemd-run",
        "--unit="+unitID,"--no-block","--quiet",
        "--property=Type=oneshot","--property=RemainAfterExit=yes",
        "--","/usr/bin/true").CombinedOutput()
    if err!=nil{t.Fatalf("start synthetic Compose-labelled systemd unit: %v %s",err,result)}
    t.Cleanup(func(){
        _=exec.Command("systemctl","stop",unit).Run()
        _=exec.Command("systemctl","reset-failed",unit).Run()
    })
    db,err:=state.Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    if err:=db.CreateJob(ctx,state.JobRecord{
        ID:unitID,UnitName:unit,Subject:"owner",Tool:"compose.pull",
        Resource:"/opt/synthetic-compose-project",State:"running",
        CreatedAt:time.Now().Add(-time.Minute),Deadline:time.Now().Add(time.Minute),
    });err!=nil{t.Fatal(err)}
    broker:=&Broker{State:db,ComposeRetirer:SystemdComposeRetirer{}}
    deadline:=time.Now().Add(15*time.Second)
    complete:=0
    for time.Now().Before(deadline){
        complete,_,err=broker.ReconcileComposeJobs(ctx,8)
        if err!=nil{t.Fatal(err)}
        if complete>0{break}
        time.Sleep(100*time.Millisecond)
    }
    saved,err:=db.GetJob(ctx,unitID)
    if err!=nil||saved.State!="done"||saved.ExitCode==nil||*saved.ExitCode!=0{
        t.Fatalf("Compose terminal proof lost: %+v err=%v",saved,err)
    }
    retired:=false
    for time.Now().Before(deadline){
        done,_,err:=broker.SweepComposeCleanup(ctx,8)
        if err!=nil{t.Fatal(err)}
        if done==1 {retired=true;break}
        time.Sleep(100*time.Millisecond)
    }
    outstanding,err:=db.PendingComposeUnitCleanup(ctx,8)
    if err!=nil||!retired||len(outstanding)!=0{
        t.Fatalf("unit retirement not proven: retired=%v queue=%+v err=%v",retired,outstanding,err)
    }
    saved,err=db.GetJob(ctx,unitID)
    if err!=nil||saved.State!="done"||saved.ExitCode==nil||*saved.ExitCode!=0{
        t.Fatalf("retirement rewrote Compose outcome: %+v err=%v",saved,err)
    }
}
