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

func TestWorkerDurableProgressAndNoSecondDispatch(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    original := bytes.Repeat([]byte("synthetic-model-block"), 130000)
    if err := os.WriteFile(filepath.Join(host, src), original, 0600); err != nil { t.Fatal(err) }
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
    copied, err := os.ReadFile(filepath.Join(host, dst))
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(original, copied) { t.Fatal("copied binary differs") }
}

func TestWorkerCancelledWithoutPublishing(t *testing.T) {
    dir, host, src, dst, id := fixture(t)
    if err := os.WriteFile(filepath.Join(host, src), bytes.Repeat([]byte("x"), 4<<20), 0600); err != nil {
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
    if _, err := os.Stat(filepath.Join(host, dst)); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("cancelled job published destination")
    }
    if _, err := Run(context.Background(), dir, host, id); err == nil {
        t.Fatal("cancelled job must not retry without operator reconciliation")
    }
}
