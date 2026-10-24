package broker

import (
    "context"
    "fmt"
    "path/filepath"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/jobs"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)
func TestComposeUnresolvedPreviousOperationFencesAllSubjects(t *testing.T){
    b,_,store,root:=testBroker(t)
    b.Jobs=&jobs.Manager{State:store}
    ctx:=context.Background()
    if err:=store.CreateJob(ctx,state.JobRecord{
        ID:"previous",UnitName:"vps-agent-compose-0123456789abcdef0123456789abcdef.service",
        Subject:"bob",Tool:"compose.down",Resource:root,
        State:"reconcile_required",Deadline:time.Now().Add(-time.Hour),
    });err!=nil{t.Fatal(err)}
    fake:=&fakeComposeAsyncStarter{}
    req:=wire.Request{ID:"try",Subject:"alice",Tool:"compose.up",
        Resource:root,InvocationID:"new-safe-operation"}
    result:=b.composeMutationAsync(ctx,req,"up",fake)
    if result.OK||result.Error==nil||result.Error.Code!="resource_busy"||fake.starts!=0{
        t.Fatalf("unresolved project allowed duplicate operation: %+v starts=%d",result,fake.starts)
    }
    // A denied operation must be safely retryable after independent manual
    // reconciliation; no pending idempotency intent may remain.
    if err:=store.AbortOperation(ctx,req.InvocationID);err==nil{
        t.Fatal("busy rejection orphaned a pending operation")
    }
    // Another real project is not fenced by this resource's old operation.
    req.Resource=filepath.Join(root,"other")
    req.InvocationID="other-project"
    allowed:=b.composeMutationAsync(ctx,req,"up",fake)
    if !allowed.OK||fake.starts!=1 {
        t.Fatalf("unrelated project was fenced: %+v starts=%d",allowed,fake.starts)
    }
}
func TestComposeNoncanonicalPathRejectedBeforeDispatch(t *testing.T){
    b,_,_,root:=testBroker(t)
    fake:=&fakeComposeAsyncStarter{}
    paths:=[]string{filepath.Join(root,"..",filepath.Base(root))+"/",
        "relative/project",root+"/./"}
    for i,path:=range paths{
        result:=b.composeMutationAsync(context.Background(),wire.Request{
            ID:"noncanonical",Subject:"alice",Tool:"compose.pull",
            Resource:path,InvocationID:fmt.Sprintf("noncanonical-%d",i),
        },"pull",fake)
        if result.OK||fake.starts!=0{
            t.Fatalf("noncanonical path accepted: %q, response=%+v",path,result)
        }
    }
}
