package state

import (
    "context"
    "fmt"
    "path/filepath"
    "testing"
    "time"
)
func TestFirstBlockingComposeJobIsProjectScopedAndFailClosed(t *testing.T){
    db,err:=Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    ctx:=context.Background()
    dir:=t.TempDir()
    other:=filepath.Join(dir,"other")
    now:=time.Now()
    for i:=0;i<600;i++ {
        id:=fmt.Sprintf("shell-%04d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,UnitName:id,Subject:"alice",Tool:"shell.exec",Resource:dir,
            State:"running",Deadline:now.Add(time.Hour),
        });err!=nil{t.Fatal(err)}
    }
    for i:=0;i<150;i++{
        id:=fmt.Sprintf("other-compose-%04d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,UnitName:id,Subject:"bob",Tool:"compose.pull",Resource:other,
            State:"running",Deadline:now.Add(time.Hour),
        });err!=nil{t.Fatal(err)}
    }
    if rec,exists,err:=db.FirstBlockingComposeJob(ctx,dir);err!=nil||exists {
        t.Fatalf("foreign project blocked: exists=%v rec=%+v err=%v",exists,rec,err)
    }
    id:="uncertain"
    if err:=db.CreateJob(ctx,JobRecord{
        ID:id,UnitName:"vps-agent-compose-0123456789abcdef0123456789abcdef.service",
        Subject:"bob",Tool:"compose.up",Resource:dir,State:"reconcile_required",
        Deadline:now.Add(-time.Hour),
    });err!=nil{t.Fatal(err)}
    rec,exists,err:=db.FirstBlockingComposeJob(ctx,dir)
    if err!=nil||!exists||rec.ID!=id||rec.State!="reconcile_required"{
        t.Fatalf("unresolved Compose not fenced: exists=%v rec=%+v err=%v",exists,rec,err)
    }
    if err:=db.UpdateJobState(ctx,id,"done","operator-confirmed",nil);err!=nil{t.Fatal(err)}
    rec,exists,err=db.FirstBlockingComposeJob(ctx,dir)
    if err!=nil||exists{t.Fatalf("done job not released: %+v exists=%v err=%v",rec,exists,err)}
}
