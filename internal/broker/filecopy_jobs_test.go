package broker

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/filecopyworker"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

func TestFilecopyIdentityIsStableButSubjectScoped(t *testing.T) {
    a := filecopyID("user-a", "stable-id")
    b := filecopyID("user-a", "stable-id")
    c := filecopyID("user-b", "stable-id")
    if a != b || a == c || !strings.HasPrefix(a, "vps-agent-filecopy-") {
        t.Fatal("job identity must be stable and principal-specific")
    }
}

func TestFilecopyDisabledWithoutIndependentOperatorOptIn(t *testing.T) {
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_JOBS", "0")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "1099511627776")
    req := wire.Request{ID:"no-worker",Subject:"operator",Tool:"file.copy.start",
        Resource:"/opt/models/a.gguf",InvocationID:"untrusted-operation"}
    b:=&Broker{}
    resp:=b.startFilecopy(context.Background(),req)
    if resp.OK || resp.Error == nil || resp.Error.Code!="permission_denied" {
        t.Fatalf("filecopy must be disabled, got %#v",resp)
    }
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_JOBS", "1")
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "")
    if filecopyOperatorQuota() != 0 { t.Fatal("legacy Community must never enable filecopy worker") }
    t.Setenv("VPS_AGENT_INFINITY_PROTECTED_ROOT", "/srv/portico-infinity")
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "0")
    if filecopyOperatorQuota() != 0 { t.Fatal("zero quota must disable worker") }
    t.Setenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES", "1099511627777")
    if filecopyOperatorQuota() != 0 { t.Fatal("oversized quota must disable worker") }
}

func TestFilecopyDispatchRejectsUnsafeOperatorScopeBeforeHostWork(t *testing.T) {
    id := filecopyID("operator", "scope-check")
    for _, root := range []string{"/", "/etc", "/srv", "/var/lib", "/mnt/../etc", "/mnt/"} {
        err := dispatchFilecopyUnit(context.Background(), id, root)
        if err == nil || !strings.Contains(err.Error(), "unsafe physical scope") {
            t.Fatalf("operator-scope bypass for %q: %v", root, err)
        }
    }
}

func TestWorkerUnitIDRequiresExactPrefixAndHex(t *testing.T) {
    a:=filecopyID("alice","op")
    for _, bad := range []string{"/", "portico-infinity.service", "vps-agent-filecopy-123",
        "vps-agent-compose-"+strings.Repeat("a",32)} {
        if a==bad { t.Fatal("unrelated host service identity accepted") }
    }
}

func TestReconcileFilecopyAfterTransientUnitDisappears(t *testing.T) {
    id := filecopyID("owner", "before-host-reboot")
    unitMissing := "LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\nExecMainStatus=0\n"
    cases := []struct {
        name string
        progress filecopyworker.Progress
        progressErr error
        want string
        reject bool
    }{
        {"running-stale", filecopyworker.Progress{JobID:id,Phase:"running",BytesDone:64<<20,BytesTotal:2<<30},nil,"reconcile_required",false},
        {"queued-dispatch-uncertain",filecopyworker.Progress{JobID:id,Phase:"queued"},nil,"reconcile_required",false},
        {"already-uncertain",filecopyworker.Progress{JobID:id,Phase:"reconcile_required"},nil,"reconcile_required",false},
        {"completed-with-durable-sha",filecopyworker.Progress{JobID:id,Phase:"done",BytesDone:256<<20,BytesTotal:256<<20,SHA256:strings.Repeat("a",64)},nil,"done",false},
        {"cancelled-before-reboot",filecopyworker.Progress{JobID:id,Phase:"cancelled"},nil,"cancelled",false},
        {"failed-before-reboot",filecopyworker.Progress{JobID:id,Phase:"failed"},nil,"failed",false},
        {"missing-checkpoint",filecopyworker.Progress{JobID:id,Phase:"running"},errors.New("not found"),"",true},
        {"wrong-job",filecopyworker.Progress{JobID:filecopyID("other","wrong"),Phase:"running"},nil,"reconcile_required",false},
        {"no-job",filecopyworker.Progress{Phase:"running"},nil,"",true},
        {"bad-terminal-hash",filecopyworker.Progress{JobID:id,Phase:"done",BytesDone:1,BytesTotal:1,SHA256:"abc"},nil,"",true},
        {"nonhex-terminal-hash",filecopyworker.Progress{JobID:id,Phase:"done",BytesDone:1,BytesTotal:1,SHA256:strings.Repeat("z",64)},nil,"",true},
        {"bad-terminal-counts",filecopyworker.Progress{JobID:id,Phase:"done",BytesDone:5,BytesTotal:6,SHA256:strings.Repeat("a",64)},nil,"",true},
        {"unknown-phase",filecopyworker.Progress{JobID:id,Phase:"reset"},nil,"",true},
    }
    for _,tc:=range cases {
        t.Run(tc.name,func(t *testing.T){
            status, detail, code, err := classifyFilecopyUnit(unitMissing,tc.progress,tc.progressErr)
            if tc.reject {
                if err==nil {t.Fatalf("untrusted lost-unit status accepted: %q",status)}
                return
            }
            if err!=nil||status!=tc.want||code!=nil||detail=="" {
                t.Fatalf("status=%q detail=%q code=%v err=%v want=%q",status,detail,code,err,tc.want)
            }
        })
    }
}

func TestTransientFilecopyRebootCannotBeMisreadAsSuccess(t *testing.T) {
    id:=filecopyID("owner","reboot-ambiguous")
    raw:="LoadState=not-found\nActiveState=inactive\nResult=success\nExecMainStatus=0\n"
    progress:=filecopyworker.Progress{JobID:id,Phase:"running",BytesDone:64<<20,BytesTotal:256<<20}
    state,_,_,err:=classifyFilecopyUnit(raw,progress,nil)
    if err!=nil||state!="reconcile_required" {t.Fatalf("false done after missing unit: %s %v",state,err)}
    malformed:="LoadState=loaded\nLoadState=not-found\nActiveState=inactive\n"
    if _,_,_,err:=classifyFilecopyUnit(malformed,progress,nil);err==nil {
       t.Fatal("duplicate LoadState must fail closed")
    }
    if _,_,_,err:=classifyFilecopyUnit("ActiveState=inactive\n",progress,nil);err==nil {
       t.Fatal("missing LoadState must fail closed")
    }
}
