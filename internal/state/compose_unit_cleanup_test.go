package state

import (
    "context"
    "fmt"
    "path/filepath"
    "testing"
    "time"
)
func TestComposeOutboxAtomicityRestartAndRetry(t *testing.T){
    ctx:=context.Background()
    dbFile:=filepath.Join(t.TempDir(),"state.db")
    db,err:=Open(dbFile)
    if err!=nil{t.Fatal(err)}
    unit:="vps-agent-compose-0123456789abcdef0123456789abcdef.service"
    if err:=db.CreateJob(ctx,JobRecord{
        ID:"compose-proof",UnitName:unit,Subject:"alice",Tool:"compose.up",
        Resource:"/opt/test",State:"running",Deadline:time.Now().Add(time.Hour),
    });err!=nil{t.Fatal(err)}
    failCode:=17
    changed,err:=db.CompleteComposeJobAndQueueCleanup(ctx,"compose-proof","failed","exit-code",&failCode)
    if err!=nil||!changed{t.Fatalf("atomic write failed: %v %v",changed,err)}
    changed,err=db.CompleteComposeJobAndQueueCleanup(ctx,"compose-proof","done","late",&failCode)
    if err!=nil||changed{t.Fatalf("terminal state overwritten: %v %v",changed,err)}
    if err:=db.Close();err!=nil{t.Fatal(err)}
    db,err=Open(dbFile)
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    saved,err:=db.GetJob(ctx,"compose-proof")
    if err!=nil||saved.State!="failed"||saved.ExitCode==nil||*saved.ExitCode!=17{
        t.Fatalf("outcome lost: %+v err=%v",saved,err)
    }
    queued,err:=db.PendingComposeUnitCleanup(ctx,8)
    if err!=nil||len(queued)!=1||queued[0].Unit!=unit{t.Fatalf("pending lost: %+v err=%v",queued,err)}
    if err:=db.FailComposeUnitCleanup(ctx,"compose-proof",unit);err!=nil{t.Fatal(err)}
    queued,err=db.PendingComposeUnitCleanup(ctx,8)
    if err!=nil||len(queued)!=1||queued[0].Attempts!=1{t.Fatalf("retry not durable: %+v err=%v",queued,err)}
    if err:=db.FinishComposeUnitCleanup(ctx,"compose-proof",unit);err!=nil{t.Fatal(err)}
    queued,err=db.PendingComposeUnitCleanup(ctx,8)
    if err!=nil||len(queued)!=0{t.Fatalf("completed receipt still pending: %+v %v",queued,err)}
    if err:=db.FinishComposeUnitCleanup(ctx,"compose-proof",unit);err==nil{
        t.Fatal("duplicate retirement acknowledged")
    }
}
func TestComposeCleanupNeverIncludesUnprovenForeignOrNonterminal(t *testing.T){
    ctx:=context.Background()
    db,err:=Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    for _,tool:=range []string{"compose.pull","file.copy.start","shell.exec"}{
        id:=fmt.Sprintf("job-%s",tool)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,Subject:"owner",Tool:tool,UnitName:"vps-agent-compose-aabbccddeeff00112233445566778899.service",
            State:"running",Deadline:time.Now().Add(time.Minute),
        });err!=nil{t.Fatal(err)}
    }
    zero:=0
    if _,err:=db.CompleteComposeJobAndQueueCleanup(ctx,"job-compose.pull","done","",nil);err==nil{
        t.Fatal("unverified Compose command was retired")
    }
    for _,id:=range []string{"job-file.copy.start","job-shell.exec"}{
        if _,err:=db.CompleteComposeJobAndQueueCleanup(ctx,id,"done","",&zero);err==nil{
            t.Fatalf("non-Compose job queued: %s",id)
        }
    }
    page,err:=db.PendingComposeUnitCleanup(ctx,8)
    if err!=nil||len(page)!=0{t.Fatalf("unsafe unit queued: %+v err=%v",page,err)}
}
func TestComposeActivePageIsBoundedAndSkipsOtherJobTypes(t *testing.T){
    ctx:=context.Background()
    db,err:=Open(":memory:")
    if err!=nil{t.Fatal(err)}
    defer db.Close()
    old:=time.Now().Add(-time.Hour).Truncate(time.Second)
    for i:=0;i<200;i++{
        id:=fmt.Sprintf("shell-%03d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,Tool:"shell.exec",Subject:"owner",State:"running",
            Deadline:time.Now().Add(time.Hour),CreatedAt:old,
        });err!=nil{t.Fatal(err)}
    }
    for i:=0;i<91;i++{
        id:=fmt.Sprintf("compose-%03d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,Tool:"compose.down",Subject:"owner",State:"running",
            Deadline:time.Now().Add(time.Hour),CreatedAt:old,
        });err!=nil{t.Fatal(err)}
    }
    var cursor time.Time
    var id string
    seen:=map[string]bool{}
    for page:=0;page<4;page++{
        rows,err:=db.ListActiveComposeJobsPage(ctx,cursor,id,time.Now().Add(-20*time.Second),32)
        if err!=nil{t.Fatal(err)}
        if page==3 && len(rows)!=0{t.Fatalf("unbounded page: %d",len(rows))}
        for _,row:=range rows{
            if row.Tool!="compose.down"||seen[row.ID]{t.Fatalf("foreign/duplicate row: %+v",row)}
            seen[row.ID]=true
            cursor,id=row.CreatedAt,row.ID
        }
    }
    if len(seen)!=91{t.Fatalf("unprocessed Compose entries: %d",len(seen))}
}
