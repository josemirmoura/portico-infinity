package broker

import (
    "context"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
)

type ComposeUnitRetirer interface{
    RetireComposeUnit(context.Context,string)error
}
type SystemdComposeRetirer struct{}

func validComposeUnitName(name string)bool{
    const prefix="vps-agent-compose-"
    const suffix=".service"
    if !strings.HasPrefix(name,prefix)||!strings.HasSuffix(name,suffix)||
       len(name)!=len(prefix)+32+len(suffix){return false}
    for _,r:=range name[len(prefix):len(name)-len(suffix)]{
        if !((r>='0'&&r<='9')||(r>='a'&&r<='f')){return false}
    }
    return true
}
func composeUnitLifecycle(raw string)(load,active,sub string){
    for _,line:=range strings.Split(raw,"\n"){
        k,v,ok:=strings.Cut(line,"=")
        if !ok{continue}
        switch strings.TrimSpace(k){
        case "LoadState":load=strings.TrimSpace(v)
        case "ActiveState":active=strings.TrimSpace(v)
        case "SubState":sub=strings.TrimSpace(v)
        }
    }
    return
}
func(SystemdComposeRetirer)RetireComposeUnit(ctx context.Context,unit string)error{
    if !validComposeUnitName(unit){
        return errors.New("Compose unit name is not a deterministic Broker unit")
    }
    inspect:=func()(string,string,string,error){
        out,err:=hostexec.CommandContext(ctx,"systemctl","show",unit,"--no-pager",
            "--property=LoadState,ActiveState,SubState").CombinedOutput()
        if err!=nil{return "","","",fmt.Errorf("Compose unit inspection failed: %w",err)}
        load,active,sub:=composeUnitLifecycle(string(out))
        if load==""{return "","","",errors.New("Compose unit has no trustworthy LoadState")}
        return load,active,sub,nil
    }
    load,active,sub,err:=inspect()
    if err!=nil{return err}
    if load=="not-found"{return nil} // already retired after earlier acknowledgement loss
    if load!="loaded" {return fmt.Errorf("Compose unit load state %q is not trusted",load)}
    // Never stop a running Compose CLI; this function only retires an idle
    // transient unit after its terminal result is durably checkpointed.
    if !((active=="active"&&sub=="exited")||active=="failed"||
        (active=="inactive"&&sub=="dead")){
        return fmt.Errorf("Compose unit may still be running (%s/%s)",active,sub)
    }
    if err:=hostexec.CommandContext(ctx,"systemctl","stop",unit).Run();err!=nil{
        return fmt.Errorf("Compose unit stop failed: %w",err)
    }
    if err:=hostexec.CommandContext(ctx,"systemctl","reset-failed",unit).Run();err!=nil{
        return fmt.Errorf("Compose unit reset-failed failed: %w",err)
    }
    load,active,sub,err=inspect()
    if err!=nil{return err}
    if load!="not-found"{
        return fmt.Errorf("Compose unit not retired (%s/%s/%s)",load,active,sub)
    }
    return nil
}

// ReconcileComposeJobs checks a bounded page of previously dispatched tasks.
// No user MCP call is required; the Broker never starts a Compose command
// here. An expired/ambiguous job becomes reconcile_required, preserving its
// project-wide dispatch fence until operator action.
func(b *Broker)ReconcileComposeJobs(ctx context.Context,limit int)(int,int,error){
    if b==nil||b.ComposeRetirer==nil{return 0,0,nil}
    if b.State==nil{return 0,0,errors.New("Compose reconciler has no store")}
    if limit<=0||limit>64{limit=64}
    b.composeReconcileMu.Lock()
    defer b.composeReconcileMu.Unlock()
    now:=time.Now()
    cutoff:=now.Add(-20*time.Second)
    records,err:=b.State.ListActiveComposeJobsPage(ctx,
        b.composeReconcileCreated,b.composeReconcileID,cutoff,limit)
    if err!=nil{return 0,0,err}
    if len(records)==0&&!b.composeReconcileCreated.IsZero(){
        b.composeReconcileCreated=time.Time{}
        b.composeReconcileID=""
        records,err=b.State.ListActiveComposeJobsPage(ctx,time.Time{},"",cutoff,limit)
        if err!=nil{return 0,0,err}
    }
    completed,uncertain:=0,0
    for _,rec:=range records{
        if ctx.Err()!=nil{return completed,uncertain,ctx.Err()}
        b.composeReconcileCreated=rec.CreatedAt
        b.composeReconcileID=rec.ID
        switch rec.State {
        case "starting","running","active","activating","reloading","deactivating","cancelling":
        default:continue
        }
        current,statusErr:=b.composeJobStatus(ctx,rec)
        if statusErr==nil&&(current.State=="done"||current.State=="failed"){
            completed++
            continue
        }
        if now.Before(rec.Deadline.Add(time.Minute)){continue}
        changed,err:=b.State.UpdateActiveJobState(ctx,rec.ID,
            "reconcile_required","Compose exit evidence missing; operator reconciliation required",nil)
        if err!=nil{return completed,uncertain,err}
        if changed{uncertain++}
    }
    return completed,uncertain,nil
}

func(b *Broker)SweepComposeCleanup(ctx context.Context,limit int)(int,int,error){
    if b==nil||b.ComposeRetirer==nil{return 0,0,nil}
    if b.State==nil{return 0,0,errors.New("Compose cleanup has no store")}
    if limit<=0||limit>64{limit=64}
    queue,err:=b.State.PendingComposeUnitCleanup(ctx,limit)
    if err!=nil{return 0,0,err}
    done,failed:=0,0
    for _,receipt:=range queue{
        if ctx.Err()!=nil{return done,failed,ctx.Err()}
        if err:=b.ComposeRetirer.RetireComposeUnit(ctx,receipt.Unit);err!=nil{
            failed++
            if dbErr:=b.State.FailComposeUnitCleanup(ctx,receipt.JobID,receipt.Unit);dbErr!=nil{
                return done,failed,dbErr
            }
            continue
        }
        if err:=b.State.FinishComposeUnitCleanup(ctx,receipt.JobID,receipt.Unit);err!=nil{
            return done,failed,err
        }
        done++
    }
    return done,failed,nil
}
