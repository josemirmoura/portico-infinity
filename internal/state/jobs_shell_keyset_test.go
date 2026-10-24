package state

import (
    "context"
    "fmt"
    "testing"
    "time"
)

func TestActiveShellJobsKeysetBoundedAndFiltersForeign(t *testing.T) {
    db, err := Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    ctx := context.Background()
    old := time.Now().Add(-time.Hour).Truncate(time.Second)
    future := time.Now().Add(time.Hour)
    // Many foreign entries must never fill a shell reconciliation page.
    for i:=0; i<320; i++ {
        id:=fmt.Sprintf("foreign-%04d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id, Subject:"owner", Tool:"compose.up", State:"running",
            Deadline:future,CreatedAt:old,
        }); err!=nil {t.Fatal(err)}
    }
    for i:=0;i<135;i++ {
        id:=fmt.Sprintf("shell-%04d",i)
        if err:=db.CreateJob(ctx,JobRecord{
            ID:id,Subject:"owner",Tool:"shell.exec",State:"running",
            Deadline:future,CreatedAt:old,
        });err!=nil {t.Fatal(err)}
    }
    if err:=db.CreateJob(ctx,JobRecord{
        ID:"shell-too-new",Subject:"owner",Tool:"shell.exec",
        State:"running",Deadline:future,CreatedAt:time.Now(),
    });err!=nil {t.Fatal(err)}
    if err:=db.CreateJob(ctx,JobRecord{
        ID:"shell-terminal",Subject:"owner",Tool:"shell.exec",
        State:"done",Deadline:future,CreatedAt:old,
    });err!=nil {t.Fatal(err)}
    before:=time.Now().Add(-20*time.Second)
    var cursor time.Time
    var cursorID string
    seen:=map[string]bool{}
    sizes:=[]int{}
    for {
        page,err:=db.ListActiveShellJobsPage(ctx,cursor,cursorID,before,32)
        if err!=nil {t.Fatal(err)}
        sizes=append(sizes,len(page))
        if len(page)==0 {break}
        if len(page)>32 {t.Fatalf("unbounded page length=%d",len(page))}
        for _,record:=range page {
            if seen[record.ID] {t.Fatalf("duplicate job %s",record.ID)}
            if record.Tool!="shell.exec" || record.State!="running" ||
              record.ID=="shell-too-new" || record.ID=="shell-terminal" {
                t.Fatalf("foreign or young job in page: %+v",record)
            }
            seen[record.ID]=true
            cursor, cursorID=record.CreatedAt,record.ID
        }
    }
    if len(seen)!=135 || len(sizes)!=6 {
        t.Fatalf("incomplete keyset: jobs=%d pages=%v",len(seen),sizes)
    }
    if len(sizes)==0 || sizes[0]!=32 || sizes[4]!=7 || sizes[5]!=0 {
        t.Fatalf("unexpected page sizes: %v",sizes)
    }
    for _,invalid:=range []int{-1,0,65} {
        if _,err:=db.ListActiveShellJobsPage(ctx,time.Time{},"",before,invalid);err==nil {
            t.Errorf("unsafe page limit %d permitted",invalid)
        }
    }
}

func TestActiveShellJobsKeysetPersistentStore(t *testing.T) {
    file:=t.TempDir()+"/store.db"
    ctx:=context.Background()
    db,err:=Open(file)
    if err!=nil {t.Fatal(err)}
    created:=time.Now().Add(-time.Minute)
    if err:=db.CreateJob(ctx,JobRecord{
        ID:"persisted-shell",Subject:"owner",Tool:"shell.exec",State:"running",
        Deadline:time.Now().Add(time.Hour),CreatedAt:created,
    });err!=nil {t.Fatal(err)}
    if err:=db.Close();err!=nil {t.Fatal(err)}
    reopened,err:=Open(file)
    if err!=nil {t.Fatal(err)}
    defer reopened.Close()
    page,err:=reopened.ListActiveShellJobsPage(ctx,time.Time{},"",time.Now(),8)
    if err!=nil || len(page)!=1 || page[0].ID!="persisted-shell" {
        t.Fatalf("migrated db failed keyset read: %+v err=%v",page,err)
    }
}
