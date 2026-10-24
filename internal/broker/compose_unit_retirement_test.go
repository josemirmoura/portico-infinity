package broker

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

type fakeComposeRetirer struct{
    fail bool
    calls int
}
func(f *fakeComposeRetirer)RetireComposeUnit(_ context.Context,_ string)error{
    f.calls++
    if f.fail{return errors.New("synthetic systemd failure")}
    return nil
}

func TestComposeCleanupFailRestartAndRecoverWithoutReplay(t *testing.T){
    ctx:=context.Background()
    db,err:=state.Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    unit:="vps-agent-compose-0123456789abcdef0123456789abcdef.service"
    if err:=db.CreateJob(ctx,state.JobRecord{
        ID:"retire-compose",UnitName:unit,Subject:"alice",Tool:"compose.up",
        Resource:"/opt/project",State:"running",Deadline:time.Now().Add(time.Hour),
    });err!=nil{t.Fatal(err)}
    zero:=0
    if changed,err:=db.CompleteComposeJobAndQueueCleanup(ctx,
        "retire-compose","done","",&zero);err!=nil||!changed{t.Fatalf("proof: %v %v",changed,err)}
    failed:=&fakeComposeRetirer{fail:true}
    broker1:=&Broker{State:db,ComposeRetirer:failed}
    complete,pending,err:=broker1.SweepComposeCleanup(ctx,8)
    if err!=nil||complete!=0||pending!=1||failed.calls!=1{
        t.Fatalf("first cleanup=%d/%d calls=%d err=%v",complete,pending,failed.calls,err)
    }
    good:=&fakeComposeRetirer{}
    broker2:=&Broker{State:db,ComposeRetirer:good}
    complete,pending,err=broker2.SweepComposeCleanup(ctx,8)
    if err!=nil||complete!=1||pending!=0||good.calls!=1{
        t.Fatalf("restart retry=%d/%d calls=%d err=%v",complete,pending,good.calls,err)
    }
    complete,pending,err=broker2.SweepComposeCleanup(ctx,8)
    if err!=nil||complete!=0||pending!=0||good.calls!=1{
        t.Fatalf("double cleanup=%d/%d calls=%d err=%v",complete,pending,good.calls,err)
    }
    saved,err:=db.GetJob(ctx,"retire-compose")
    if err!=nil||saved.State!="done"||saved.ExitCode==nil||*saved.ExitCode!=0{
        t.Fatalf("terminal result lost: %+v err=%v",saved,err)
    }
}
func TestComposeRetirementUnitNameIsStrict(t *testing.T){
    for _,unit:=range []string{
        "vps-agent-compose-0123456789abcdef0123456789abcdef.service",
        "vps-agent-compose-aabbccddeeff00112233445566778899.service",
    }{if !validComposeUnitName(unit){t.Fatalf("valid name denied: %s",unit)}}
    for _,unit:=range []string{
        "", "docker.service","vps-agent-compose-short.service",
        "vps-agent-compose-aabbccddeeff0011223344556677889z.service",
        "vps-agent-compose-aabbccddeeff00112233445566778899",
        "vps-agent-compose-aabbccddeeff00112233445566778899;rm -rf /.service",
    }{if validComposeUnitName(unit){t.Fatalf("unsafe name accepted: %s",unit)}}
}
