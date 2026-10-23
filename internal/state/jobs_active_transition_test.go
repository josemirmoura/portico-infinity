package state

import (
    "context"
    "testing"
    "time"
)

func TestUpdateActiveJobStateNeverOverwritesTerminalOrReconcileRequired(t *testing.T) {
    db, err := Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    ctx := context.Background()
    for _, tc := range []struct { name, terminal string }{
        {"success","done"},{"failed","failed"},{"cancelled","cancelled"},{"operator","reconcile_required"},
    } {
        t.Run(tc.name,func(t *testing.T){
            id:="shell-cas-"+tc.name
            if err := db.CreateJob(ctx,JobRecord{
                ID:id,Subject:"alice",Tool:"shell.exec",UnitName:id,
                State:"running",Deadline:time.Now().Add(time.Minute),
            }); err != nil {t.Fatal(err)}
            exit:=0
            changed,err:=db.UpdateActiveJobState(ctx,id,tc.terminal,"saved",&exit)
            if err!=nil || !changed {t.Fatalf("first update=%v err=%v",changed,err)}
            changed,err=db.UpdateActiveJobState(ctx,id,"running","late",&exit)
            if err!=nil || changed {t.Fatalf("late overwrite allowed=%v err=%v",changed,err)}
            saved,err:=db.GetJob(ctx,id)
            if err!=nil || saved.State!=tc.terminal || saved.Detail!="saved" {
                t.Fatalf("checkpoint overwritten: %+v err=%v",saved,err)
            }
        })
    }
}
