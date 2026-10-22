package filecopyworker

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

// Expensive, opt-in E2E of the actual worker with a sparse source >2 GiB.
// This test runs only on disposable CI runners; never on a user's model data.
// A real 2 GiB destination is written, hashed and removed by t.TempDir.
func TestWorkerMultiGiBSparseCopy(t *testing.T) {
    if os.Getenv("INFINITY_MULTIGIB_E2E") != "1" {
        t.Skip("set INFINITY_MULTIGIB_E2E=1 in disposable CI only")
    }
    dir, host, src, dst, id := fixture(t)
    const largeSize int64 = (2 << 30) + 4096
    physicalSource := filepath.Join(host, strings.TrimPrefix(src, "/"))
    f, err := os.OpenFile(physicalSource, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil { t.Fatal(err) }
    if err := f.Truncate(largeSize); err != nil {
        _ = f.Close()
        t.Fatal(err)
    }
    if err := f.Close(); err != nil { t.Fatal(err) }

    want, err := hashFileStreaming(physicalSource)
    if err != nil { t.Fatal(err) }
    prepare(t, dir, Manifest{
        JobID: id, Subject: "synthetic-operator", Source: src, Destination: dst,
        MaxBytes: 3 << 30, ExpectedSHA256: want,
    })
    started := time.Now()
    got, err := Run(context.Background(), dir, host, id)
    if err != nil { t.Fatal(err) }
    physicalDestination := filepath.Join(host, strings.TrimPrefix(dst, "/"))
    targetStat, err := os.Stat(physicalDestination)
    if err != nil { t.Fatal(err) }
    if targetStat.Size() != largeSize {
        t.Fatalf("large destination size mismatch: %d != %d", targetStat.Size(), largeSize)
    }
    destinationHash, err := hashFileStreaming(physicalDestination)
    if err != nil { t.Fatal(err) }
    progress := readProgress(t, dir, id)
    if got != want || destinationHash != want || progress.SHA256 != want ||
        progress.Phase != "done" || progress.BytesDone != largeSize ||
        progress.BytesTotal != largeSize {
        t.Fatalf("multigib SHA/progress incorrect: expected=%s got=%s destination=%s phase=%s done=%d total=%d",
            want, got, destinationHash, progress.Phase, progress.BytesDone, progress.BytesTotal)
    }
    if _, err := Run(context.Background(), dir, host, id); err == nil {
        t.Fatal("worker replay unexpectedly accepted completed multigib job")
    }
    t.Logf("synthetic multigib E2E passed: bytes=%d elapsed=%s SHA-256=%s",
        largeSize, time.Since(started).Round(time.Millisecond), got)
}

func TestWorkerLargeSparseCancellation(t *testing.T) {
    if os.Getenv("INFINITY_MULTIGIB_E2E") != "1" {
        t.Skip("set INFINITY_MULTIGIB_E2E=1 in disposable CI only")
    }
    dir, host, src, dst, id := fixture(t)
    physicalSource := filepath.Join(host, strings.TrimPrefix(src, "/"))
    f, err := os.OpenFile(physicalSource, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil { t.Fatal(err) }
    if err := f.Truncate(2 << 30); err != nil {
        _ = f.Close()
        t.Fatal(err)
    }
    if err := f.Close(); err != nil { t.Fatal(err) }

    prepare(t, dir, Manifest{
        JobID: id, Subject: "synthetic-operator", Source: src, Destination: dst,
        MaxBytes: 3 << 30,
    })
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    // Cancel from a separate goroutine only after a large portion has flowed.
    // The worker persists progress approximately every 64 MiB; polling is
    // intentionally bounded, without direct mutation of worker state.
    finished := make(chan struct{})
    go func() {
        defer close(finished)
        deadline := time.NewTimer(90 * time.Second)
        defer deadline.Stop()
        ticker := time.NewTicker(50 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-deadline.C:
                cancel()
                return
            case <-ticker.C:
                p := filepath.Join(dir, id+".progress.json")
                raw, readErr := os.ReadFile(p)
                if readErr != nil { continue }
                // Progress field is JSON; use typed parsing to avoid false positives.
                var progress Progress
                if jsonErr := json.Unmarshal(raw, &progress); jsonErr == nil &&
                    progress.BytesDone >= (128 << 20) {
                    cancel()
                    return
                }
            case <-ctx.Done():
                return
            }
        }
    }()
    _, err = Run(ctx, dir, host, id)
    cancel()
    <-finished
    if !errors.Is(err, context.Canceled) {
        t.Fatalf("cancelled sparse copy must stop without publishing: %v", err)
    }
    progress := readProgress(t, dir, id)
    if progress.Phase != "cancelled" || progress.BytesDone < (128 << 20) {
        t.Fatalf("cancel must be observed after >=128MiB progress: phase=%s bytes=%d",
            progress.Phase, progress.BytesDone)
    }
    if _, err := os.Stat(filepath.Join(host, strings.TrimPrefix(dst, "/")));
        !errors.Is(err, os.ErrNotExist) {
        t.Fatal("cancelled large copy published destination")
    }
    if _, err := Run(context.Background(), dir, host, id); err == nil {
        t.Fatal("cancelled job was automatically resumed without reconciliation")
    }
}

func hashFileStreaming(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil { return "", err }
    defer f.Close()
    h := sha256.New()
    if _, err := io.Copy(h, f); err != nil { return "", err }
    return hex.EncodeToString(h.Sum(nil)), nil
}
