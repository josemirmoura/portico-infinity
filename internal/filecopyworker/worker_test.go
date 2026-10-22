package filecopyworker

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func fixture(t *testing.T) (dir, hostRoot, src, dst, jobID string) {
    t.Helper()
    root := t.TempDir()
    dir = filepath.Join(root, "state")
    if err := os.Mkdir(dir, 0700); err != nil { t.Fatal(err) }
    hostRoot = filepath.Join(root, "host")
    if err := os.MkdirAll(filepath.Join(hostRoot, "opt", "models"), 0700); err != nil { t.Fatal(err) }
    src, dst = "/opt/models/source.gguf", "/opt/models/dest.gguf"
    jobID = "vps-agent-filecopy-" + "c4b9e2d010fadcbe" + "1029384756abcdef"
    return
}

func prepare(t *testing.T, dir string, m Manifest) {
    t.Helper()
    payload, err := json.Marshal(m)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir, m.JobID+".request.json"), payload, 0600); err != nil { t.Fatal(err) }
}

func readProgress(t *testing.T, dir, id string) Progress {
    t.Helper()
    raw, err := os.ReadFile(filepath.Join(dir, id+".progress.json"))
    if err != nil { t.Fatal(err) }
    var progress Progress
    if err := json.Unmarshal(raw, &progress); err != nil { t.Fatal(err) }
    return progress
}

func TestWorkerValidatesScopesAndManifest(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    _ = host
    m := Manifest{JobID: id, Subject: "user", Source: src, Destination: dst, MaxBytes: 1<<30}
    if err := m.Validate(id); err != nil { t.Fatal(err) }
    invalid := []Manifest{
        {JobID: id, Subject: "user", Source: "/etc/passwd", Destination: dst, MaxBytes: 1<<30},
        {JobID: id, Subject: "user", Source: src, Destination: "/srv/portico-infinity/state/state.db", MaxBytes: 1<<30},
        {JobID: id, Subject: "user", Source: "/opt/models/.env", Destination: dst, MaxBytes: 1<<30},
        {JobID: id, Subject: "user", Source: src, Destination: src, MaxBytes: 1<<30},
        {JobID: id, Subject: "", Source: src, Destination: dst, MaxBytes: 1<<30},
        {JobID: id, Subject: "user", Source: src, Destination: dst, MaxBytes: 0},
        {JobID: id, Subject: "user", Source: src, Destination: dst, MaxBytes: (1<<40)+1},
    }
    for _, v := range invalid {
        if v.Validate(id) == nil { t.Fatalf("unsafe manifest accepted: %+v", v) }
    }
    prepare(t, dir, m)
    if _, err := readPinnedManifest(dir, id); err != nil { t.Fatal(err) }
    if err := os.Chmod(filepath.Join(dir, id+".request.json"), 0666); err != nil { t.Fatal(err) }
    if _, err := readPinnedManifest(dir, id); err == nil { t.Fatal("world-readable manifest accepted") }
}

func TestWorkerSelectedRootAndIsolation(t *testing.T) {
    _, host, _, _, id := fixture(t)
    scope := "/mnt"
    source := "/mnt/models/origin.gguf"
    destination := "/mnt/models/replica.gguf"
    valid := Manifest{JobID:id, Subject:"operator", ScopeRoot:scope,
        Source:source, Destination:destination, MaxBytes:1 << 30}
    if err := valid.Validate(id); err != nil { t.Fatalf("valid alternative scope rejected: %v", err) }
    for _, invalid := range []Manifest{
        {JobID:id, Subject:"operator", ScopeRoot:"/srv", Source:"/srv/file", Destination:"/srv/other", MaxBytes:1 << 30},
        {JobID:id, Subject:"operator", ScopeRoot:"/etc", Source:"/etc/file", Destination:"/etc/other", MaxBytes:1 << 30},
        {JobID:id, Subject:"operator", ScopeRoot:"/", Source:"/mnt/file", Destination:"/mnt/other", MaxBytes:1 << 30},
        {JobID:id, Subject:"operator", ScopeRoot:"/mnt/../opt", Source:source, Destination:destination, MaxBytes:1 << 30},
        {JobID:id, Subject:"operator", ScopeRoot:scope, Source:"/opt/models/origin.gguf", Destination:destination, MaxBytes:1 << 30},
        {JobID:id, Subject:"operator", ScopeRoot:scope, Source:source, Destination:"/mnt/models/.env", MaxBytes:1 << 30},
    } {
        if err := invalid.Validate(id); err == nil { t.Fatalf("unsafe root or path accepted: %+v", invalid) }
    }

    if err := os.MkdirAll(filepath.Join(host, "mnt", "models"), 0700); err != nil { t.Fatal(err) }
    original := bytes.Repeat([]byte("alternate-operator-ceiling"), 10000)
    if err := os.WriteFile(filepath.Join(host, "mnt", "models", "origin.gguf"), original, 0600); err != nil {
        t.Fatal(err)
    }
    dir := filepath.Join(t.TempDir(), "state")
    if err := os.Mkdir(dir, 0700); err != nil { t.Fatal(err) }
    prepare(t, dir, valid)
    sum := sha256.Sum256(original)
    expected := hex.EncodeToString(sum[:])
    sha, err := Run(context.Background(), dir, host, id)
    if err != nil { t.Fatal(err) }
    if sha != expected { t.Fatal("alternate root copy hash mismatch") }
    data, err := os.ReadFile(filepath.Join(host, "mnt", "models", "replica.gguf"))
    if err != nil || !bytes.Equal(original, data) { t.Fatal("alternate root copy bytes differ", err) }
}

func TestWorkerDurableProgressAndNoSecondDispatch(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    original := bytes.Repeat([]byte("synthetic-model-block"), 130000)
    if err := os.WriteFile(filepath.Join(host, strings.TrimPrefix(src, "/")), original, 0600); err != nil { t.Fatal(err) }
    dig := sha256.Sum256(original)
    want := hex.EncodeToString(dig[:])
    prepare(t, dir, Manifest{JobID: id, Subject: "oauth-subject", Source: src, Destination: dst,
        MaxBytes: 1<<30, ExpectedSHA256: want})
    got, err := Run(context.Background(), dir, host, id)
    if err != nil { t.Fatal(err) }
    if got != want { t.Fatalf("SHA mismatch %s %s", got, want) }
    progress := readProgress(t, dir, id)
    if progress.Phase != "done" || progress.BytesDone != int64(len(original)) ||
        progress.BytesTotal != int64(len(original)) || progress.SHA256 != want {
        t.Fatalf("invalid progress: %+v", progress)
    }
    if _, err := Run(context.Background(), dir, host, id); err == nil {
        t.Fatal("completed job was dispatched twice")
    }
    copied, err := os.ReadFile(filepath.Join(host, strings.TrimPrefix(dst, "/")))
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(original, copied) { t.Fatal("copied binary differs") }
}

func TestOperatorFenceIsScopedAndPreventsWorkerStart(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    original := bytes.Repeat([]byte("model-block"), 400000)
    if err := os.WriteFile(filepath.Join(host, strings.TrimPrefix(src, "/")), original, 0600); err != nil {
        t.Fatal(err)
    }
    manifest := Manifest{JobID:id, Subject:"oauth-user-a", Source:src, Destination:dst, MaxBytes:1<<30}
    prepare(t,dir,manifest)
    marker, err := SubjectRevocationMarker("oauth-user-a")
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir,marker), []byte("revoked\n"),0600); err != nil { t.Fatal(err) }
    revoked, err := IsSubjectRevoked(dir,"oauth-user-a")
    if err != nil || !revoked { t.Fatal("operator fence not detected",err) }
    unaffected, err := IsSubjectRevoked(dir,"oauth-user-b")
    if err != nil || unaffected { t.Fatal("unrelated subject unexpectedly revoked",err) }
    if _,err := Run(context.Background(),dir,host,id); !errors.Is(err,context.Canceled) {
        t.Fatalf("revoked principal was allowed to run: %v",err)
    }
    if p:=readProgress(t,dir,id);p.Phase!="cancelled" { t.Fatalf("phase=%q",p.Phase) }
    if _,err := os.Stat(filepath.Join(host,strings.TrimPrefix(dst,"/"))); !errors.Is(err,os.ErrNotExist) {
        t.Fatal("revoked principal published a file")
    }
}

func TestRevocationFenceRejectsUnsafeSymlink(t *testing.T) {
    dir,_,_,_,_ := fixture(t)
    marker,err:=SubjectRevocationMarker("user")
    if err != nil { t.Fatal(err) }
    if err:=os.Symlink(filepath.Join(dir,"outside"), filepath.Join(dir,marker));err != nil { t.Fatal(err) }
    if _,err:=IsSubjectRevoked(dir,"user");err == nil { t.Fatal("symlink marker was trusted") }
}

func TestMidStreamRevocationFailsBeforeFilePublication(t *testing.T) {
    dir,host,src,dst,id:=fixture(t)
    source:=bytes.Repeat([]byte("model-block"), 800000)
    if err:=os.WriteFile(filepath.Join(host,strings.TrimPrefix(src,"/")),source,0600);err != nil { t.Fatal(err) }
    prepare(t,dir,Manifest{JobID:id,Subject:"revoked-midstream",Source:src,Destination:dst,MaxBytes:1<<30})
    marker,err:=SubjectRevocationMarker("revoked-midstream")
    if err != nil { t.Fatal(err) }
    checks:=0
    check:=func(d,s string)(bool,error) {
        checks++
        if checks==3 {
            if err:=os.WriteFile(filepath.Join(d,marker),[]byte("revoked\n"),0600);err != nil {
                return false,err
            }
        }
        return IsSubjectRevoked(d,s)
    }
    if _,err:=runWithRevocationCheck(context.Background(),dir,host,id,check);!errors.Is(err,context.Canceled) {
        t.Fatalf("mid-stream revoked worker did not cancel: %v",err)
    }
    if checks<3 { t.Fatalf("stream checked revocation only %d times",checks) }
    if p:=readProgress(t,dir,id);p.Phase!="cancelled" {
        t.Fatalf("worker reported %s, expected cancelled",p.Phase)
    }
    if _,err:=os.Stat(filepath.Join(host,strings.TrimPrefix(dst,"/")));!errors.Is(err,os.ErrNotExist) {
        t.Fatal("revoked long copy published destination")
    }
}

func TestUnreadableRevocationFenceIsNotReportedAsCancellation(t *testing.T) {
    dir,host,src,dst,id:=fixture(t)
    source:=bytes.Repeat([]byte("model-block"), 800000)
    if err:=os.WriteFile(filepath.Join(host,strings.TrimPrefix(src,"/")),source,0600);err != nil {t.Fatal(err)}
    prepare(t,dir,Manifest{JobID:id,Subject:"blocked-io",Source:src,Destination:dst,MaxBytes:1<<30})
    probes:=0
    probe:=func(d,s string)(bool,error) {
        probes++
        if probes==3 {return false,errors.New("revocation state unavailable")}
        return IsSubjectRevoked(d,s)
    }
    if _,err:=runWithRevocationCheck(context.Background(),dir,host,id,probe);err == nil {
        t.Fatal("unavailable revocation fence unexpectedly allowed copy")
    }
    if probes<3 {t.Fatal("revocation probe was not exercised midstream")}
    if p:=readProgress(t,dir,id);p.Phase!="reconcile_required" {
        t.Fatalf("expected fail-closed reconciliation, got %s",p.Phase)
    }
    if _,err:=os.Stat(filepath.Join(host,strings.TrimPrefix(dst,"/")));!errors.Is(err,os.ErrNotExist) {
        t.Fatal("copy was published after revocation probe failure")
    }
}

func TestWorkerCancelledWithoutPublishing(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    if err := os.WriteFile(filepath.Join(host, strings.TrimPrefix(src, "/")), bytes.Repeat([]byte("x"), 4<<20), 0600); err != nil {
        t.Fatal(err)
    }
    prepare(t, dir, Manifest{JobID: id, Subject: "user", Source: src, Destination: dst, MaxBytes: 1<<30})
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    if _, err := Run(ctx, dir, host, id); !errors.Is(err, context.Canceled) {
        t.Fatalf("cancelled job must report context cancellation: %v", err)
    }
    progress := readProgress(t, dir, id)
    if progress.Phase != "cancelled" { t.Fatalf("cancelled job phase: %s", progress.Phase) }
    if _, err := os.Stat(filepath.Join(host, strings.TrimPrefix(dst, "/"))); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("cancelled job published destination")
    }
    if _, err := Run(context.Background(), dir, host, id); err == nil {
        t.Fatal("cancelled job must not retry without operator reconciliation")
    }
}
