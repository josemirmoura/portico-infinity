package broker

import (
    "context"
    "os"
    "os/user"
    "strconv"
    "strings"
    "testing"
)

func TestInfinityHostAccountRecordValidation(t *testing.T) {
    tests := []struct {
        label, name, record string
        uid, gid int
        ok bool
    }{
        {"host-service-account","portico-infinity-exec","portico-infinity-exec:x:999:987::/nonexistent:/usr/sbin/nologin\n",999,987,true},
        {"host-root-denied","root","root:x:0:0::/root:/bin/bash\n",0,0,false},
        {"unexpected-name","portico-infinity-exec","other:x:999:987::/nonexistent:/usr/sbin/nologin\n",0,0,false},
        {"negative-uid","portico-infinity-exec","portico-infinity-exec:x:-1:987::/nonexistent:/usr/sbin/nologin\n",0,0,false},
        {"nonnumeric-gid","portico-infinity-exec","portico-infinity-exec:x:999:admin::/nonexistent:/usr/sbin/nologin\n",0,0,false},
        {"injected-line","portico-infinity-exec","portico-infinity-exec:x:999:987::/nonexistent:/usr/sbin/nologin\nroot:x:0:0::/root:/bin/bash\n",0,0,false},
        {"embedded-cr","portico-infinity-exec","portico-infinity-exec:x:999:987::/nonexistent:/bin/fal\rse\n",0,0,false},
        {"empty","portico-infinity-exec","",0,0,false},
        {"oversized","portico-infinity-exec",strings.Repeat("x",5000),0,0,false},
    }
    for _,tc := range tests {t.Run(tc.label,func(t *testing.T){
        uid,gid,err:=parseHostShellAccountRecord(tc.name,[]byte(tc.record))
        if tc.ok {
            if err!=nil || uid!=tc.uid || gid!=tc.gid {t.Fatalf("ids=%d:%d err=%v",uid,gid,err)}
        } else if err==nil {t.Fatalf("unsafe account accepted: %d:%d",uid,gid)}
    })}
}

func TestInfinityHostAccountRejectsMaliciousOrMissingPolicyUser(t *testing.T) {
    t.Setenv("VPS_AGENT_HOST_ROOT", "")
    t.Setenv("VPS_AGENT_HOST_NSENTER", "")
    for _,name:=range []string{"", "--help", "fake user", "fake\nuser", "root", "x" + strings.Repeat("a", 129), "portico-infinity-definitely-nonexistent-9487"} {
        _,_,err:=resolveHostShellAccount(context.Background(),name)
        if err==nil {t.Fatalf("invalid or unavailable host shell account was accepted: %q",name)}
    }
}

func TestInfinityHostAccountResolvesActualNonRootIdentity(t *testing.T) {
    if os.Geteuid()==0 {t.Skip("requires a non-root CI identity")}
    t.Setenv("VPS_AGENT_HOST_ROOT", "")
    t.Setenv("VPS_AGENT_HOST_NSENTER", "")
    current,err:=user.Current()
    if err!=nil {t.Fatal(err)}
    uid,err:=strconv.Atoi(current.Uid)
    if err!=nil {t.Fatal(err)}
    gid,err:=strconv.Atoi(current.Gid)
    if err!=nil {t.Fatal(err)}
    gotUID,gotGID,err:=resolveHostShellAccount(context.Background(),current.Username)
    if err!=nil {t.Fatal(err)}
    if gotUID!=uid || gotGID!=gid {t.Fatalf("host account=%d:%d expected %d:%d",gotUID,gotGID,uid,gid)}
}
