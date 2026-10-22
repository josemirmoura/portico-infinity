// Package filecopyworker runs an operator-installed typed binary copy task.
// It receives NO shell commands and exposes NO ambient host administration API.
// The Broker must authenticate the OAuth subject and persist an immutable
// root-owned manifest + SQLite job record before systemd dispatch.
package filecopyworker

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "syscall"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/securefs"
    "golang.org/x/sys/unix"
)

const (
    StateDir = "/srv/portico-infinity/state/filecopy-jobs"
    MaxManifestBytes = 4096
    MaxSubjectBytes = 1024
    progressStep = 64 << 20
)

var jobPattern = regexp.MustCompile(`^vps-agent-filecopy-[0-9a-f]{32}$`)

type Manifest struct {
    JobID string `json:"job_id"`
    Subject string `json:"subject"`
    Source string `json:"source"`
    Destination string `json:"destination"`
    ExpectedSHA256 string `json:"expected_sha256,omitempty"`
    MaxBytes int64 `json:"max_bytes"`
    ScopeRoot string `json:"scope_root,omitempty"` // operator-controlled, never from MCP input
}

type Progress struct {
    JobID string `json:"job_id"`
    Phase string `json:"phase"`
    BytesDone int64 `json:"bytes_done"`
    BytesTotal int64 `json:"bytes_total"`
    SHA256 string `json:"sha256,omitempty"`
    UpdatedAt string `json:"updated_at"`
}

func ValidID(id string) bool { return jobPattern.MatchString(id) }

var safeScopePattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

func validWorkerScope(root string) bool {
    if root == "/" || root == "" || !filepath.IsAbs(root) ||
        filepath.Clean(root) != root || !safeScopePattern.MatchString(root) {
        return false
    }
    for _, reserved := range []string{
        "/srv", "/etc", "/usr", "/var", "/root", "/proc", "/sys", "/dev",
        "/run", "/boot", "/home", "/tmp", "/bin", "/sbin", "/lib", "/lib64",
    } {
        if root == reserved || strings.HasPrefix(root, reserved+"/") { return false }
    }
    return true
}

func (m Manifest) selectedScope() string {
    if m.ScopeRoot == "" { return "/opt" } // Backward compatibility for existing manifests
    return m.ScopeRoot
}

func allowedScopedPath(root, path string) bool {
    if !validWorkerScope(root) || !filepath.IsAbs(path) ||
        filepath.Clean(path) != path || path == root ||
        !strings.HasPrefix(path, root+"/") {
        return false
    }
    for _, part := range strings.Split(strings.TrimPrefix(path, root+"/"), "/") {
        if part == "" || part == "." || part == ".." {
            return false
        }
        if part == ".env" || strings.HasPrefix(part, ".env.") {
            return false
        }
    }
    return true
}

func (m Manifest) Validate(id string) error {
    if !ValidID(id) || m.JobID != id {
        return errors.New("invalid filecopy job identity")
    }
    if len(m.Subject) == 0 || len(m.Subject) > MaxSubjectBytes {
        return errors.New("invalid filecopy owner identity")
    }
    root := m.selectedScope()
    if !allowedScopedPath(root, m.Source) || !allowedScopedPath(root, m.Destination) {
        return errors.New("filecopy paths must be clean, nonsensitive and below selected scope")
    }
    if m.Source == m.Destination {
        return errors.New("filecopy source must differ from destination")
    }
    if m.MaxBytes <= 0 || m.MaxBytes > securefs.MaxBinaryCopyBytes {
        return errors.New("invalid filecopy quota")
    }
    if m.ExpectedSHA256 != "" {
        if len(m.ExpectedSHA256) != 64 {
            return errors.New("expected hash is not a SHA-256 digest")
        }
        if _, err := hex.DecodeString(m.ExpectedSHA256); err != nil {
            return errors.New("invalid expected SHA-256")
        }
    }
    return nil
}

// CheckDir fails closed on a writable or symlinked state folder. Its final
// filesystem is deliberately independent of /opt; the installed entrypoint
// always supplies StateDir, not a path from a model or MCP request.
func CheckDir(dir string) error {
    info, err := os.Lstat(dir)
    if err != nil { return err }
    if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
        info.Mode().Perm()&0077 != 0 {
        return errors.New("unsafe filecopy state directory")
    }
    st, ok := info.Sys().(*syscall.Stat_t)
    if !ok || int(st.Uid) != os.Geteuid() {
        return errors.New("filecopy state directory must be owned by the worker identity")
    }
    return nil
}

func readPinnedManifest(dir, id string) (Manifest, error) {
    var manifest Manifest
    if !ValidID(id) { return manifest, errors.New("invalid job id") }
    if err := CheckDir(dir); err != nil { return manifest, err }
    path := filepath.Join(dir, id+".request.json")
    fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
    if err != nil { return manifest, err }
    file := os.NewFile(uintptr(fd), path)
    if file == nil {
        _ = unix.Close(fd)
        return manifest, errors.New("cannot pin job manifest")
    }
    defer file.Close()
    stat, err := file.Stat()
    if err != nil { return manifest, err }
    si, ok := stat.Sys().(*syscall.Stat_t)
    if !ok || !stat.Mode().IsRegular() || si.Nlink != 1 ||
        int(si.Uid) != os.Geteuid() || stat.Mode().Perm()&0077 != 0 ||
        stat.Size() > MaxManifestBytes {
        return manifest, errors.New("job manifest owner/type/size is unsafe")
    }
    raw, err := io.ReadAll(io.LimitReader(file, MaxManifestBytes+1))
    if err != nil { return manifest, err }
    if len(raw) > MaxManifestBytes { return manifest, errors.New("job manifest too large") }
    decoder := json.NewDecoder(strings.NewReader(string(raw)))
    decoder.DisallowUnknownFields()
    if err := decoder.Decode(&manifest); err != nil { return manifest, err }
    var extra any
    if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
        return manifest, errors.New("extra manifest JSON values")
    }
    return manifest, manifest.Validate(id)
}

func privateAtomicJSON(dir, name string, value any) error {
    raw, err := json.Marshal(value)
    if err != nil { return err }
    var nonce [8]byte
    if _, err := rand.Read(nonce[:]); err != nil { return err }
    root, err := os.OpenRoot(dir)
    if err != nil { return err }
    defer root.Close()
    temp := "."+name+".tmp-"+hex.EncodeToString(nonce[:])
    f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
    if err != nil { return err }
    published := false
    defer func() {
        _ = f.Close()
        if !published { _ = root.Remove(temp) }
    }()
    if _, err := f.Write(raw); err != nil { return err }
    if err := f.Sync(); err != nil { return err }
    if err := f.Close(); err != nil { return err }
    if err := root.Rename(temp, name); err != nil { return err }
    published = true
    dirfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
    if err != nil { return err }
    defer unix.Close(dirfd)
    return unix.Fsync(dirfd)
}

func status(dir, id string, phase string, done, total int64, sha string) error {
    return privateAtomicJSON(dir, id+".progress.json", Progress{
        JobID: id, Phase: phase, BytesDone: done, BytesTotal: total,
        SHA256: sha, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
    })
}

// Run performs a single authorized manifest task. hostRoot is empty in
// production; the non-empty mapping is used only by synthetic unit tests.
// The exclusive flock prevents concurrent workers sharing a job ID.
// A post-commit I/O error is reported as reconciliation, never re-executed.
func Run(ctx context.Context, dir, hostRoot, id string) (string, error) {
    manifest, err := readPinnedManifest(dir, id)
    if err != nil { return "", err }
    lockPath := filepath.Join(dir, id+".lock")
    lockfd, err := unix.Open(lockPath, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
    if err != nil { return "", err }
    defer unix.Close(lockfd)
    if err := unix.Flock(lockfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return "", errors.New("filecopy job already running or lock uncertain")
    }
    defer unix.Flock(lockfd, unix.LOCK_UN)

    // A completed/cancelled or previously uncertain task is NEVER retried
    // simply because the systemd unit was restarted.
    progressRoot, err := os.OpenRoot(dir)
    if err != nil { return "", err }
    existing, openErr := progressRoot.Open(id+".progress.json")
    if openErr == nil {
        data, readErr := io.ReadAll(io.LimitReader(existing, MaxManifestBytes+1))
        closeErr := existing.Close()
        if readErr != nil { _ = progressRoot.Close(); return "", readErr }
        if closeErr != nil { _ = progressRoot.Close(); return "", closeErr }
        if len(data) > MaxManifestBytes {
            _ = progressRoot.Close()
            return "", errors.New("filecopy progress too large: operator reconciliation required")
        }
        var previous Progress
        if json.Unmarshal(data, &previous) != nil || previous.JobID != id {
            _ = progressRoot.Close()
            return "", errors.New("filecopy progress is corrupt: operator reconciliation required")
        }
        if previous.Phase != "queued" {
            _ = progressRoot.Close()
            return "", errors.New("filecopy already dispatched: no automatic retry")
        }
    } else if !errors.Is(openErr, os.ErrNotExist) {
        _ = progressRoot.Close()
        return "", openErr
    }
    if err := progressRoot.Close(); err != nil { return "", err }
    if err := status(dir, id, "running", 0, 0, ""); err != nil { return "", err }
    selected := manifest.selectedScope()
    fs, err := securefs.NewWithHostRoot([]string{selected}, []string{selected}, securefs.DefaultMaxBytes, hostRoot)
    if err != nil { return "", err }
    var lastDone, lastTotal, recorded int64
    lastReport := time.Now()
    var progressErr error
    sha, copyErr := fs.CopyFileStream(ctx, manifest.Source, manifest.Destination,
        manifest.MaxBytes, manifest.ExpectedSHA256, func(done, total int64) error {
            lastDone, lastTotal = done, total
            if progressErr != nil { return progressErr }
            if done-recorded < progressStep && time.Since(lastReport) < time.Second && done != total {
                return nil
            }
            progressErr = status(dir, id, "running", done, total, "")
            if progressErr == nil { recorded, lastReport = done, time.Now() }
            return progressErr
        })
    if progressErr != nil {
        // The destination may already have been atomically published.
        _ = status(dir, id, "reconcile_required", lastDone, lastTotal, "")
        return "", fmt.Errorf("progress durability failed, reconcile before reuse: %w", progressErr)
    }
    if copyErr != nil {
        phase := "failed"
        if errors.Is(copyErr, context.Canceled) { phase = "cancelled" }
        if errors.Is(copyErr, securefs.ErrCopyReconcile) { phase = "reconcile_required" }
        if err := status(dir, id, phase, lastDone, lastTotal, ""); err != nil {
            return "", fmt.Errorf("copy failed and final status could not be persisted: %w", err)
        }
        return "", copyErr
    }
    if err := status(dir, id, "done", lastDone, lastTotal, sha); err != nil {
        return "", fmt.Errorf("filecopy destination may be complete; final status uncertain: %w", err)
    }
    return sha, nil
}
