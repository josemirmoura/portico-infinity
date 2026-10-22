package broker

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "syscall"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/filecopyworker"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/securefs"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
    "golang.org/x/sys/unix"
)

const (
    filecopyHostBinary = "/usr/local/libexec/portico-infinity/portico-filecopy-worker"
    filecopyBrokerState = "/var/lib/vps-agent/filecopy-jobs"
    filecopyJobTimeout = 12 * time.Hour
)

func filecopyID(subject, invocation string) string {
    sum := sha256.Sum256([]byte(subject+"\x00"+invocation))
    return "vps-agent-filecopy-"+hex.EncodeToString(sum[:16])
}

// Operator opt-in is strictly additional to normal policy permissions.
func filecopyOperatorQuota() int64 {
    if os.Getenv("VPS_AGENT_INFINITY_PROTECTED_ROOT") == "" ||
        os.Getenv("VPS_AGENT_INFINITY_FILECOPY_JOBS") != "1" {
        return 0
    }
    n, err := strconv.ParseInt(os.Getenv("VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES"), 10, 64)
    if err != nil || n <= 0 || n > securefs.MaxBinaryCopyBytes { return 0 }
    return n
}

func verifyHostFilecopyBinary() error {
    // A root-owned executable is not trustworthy when an unprivileged user
    // can replace it through a writable/symlinked ancestor directory.
    for _, ancestor := range []string{
        "/usr", "/usr/local", "/usr/local/libexec",
        "/usr/local/libexec/portico-infinity",
    } {
        info, err := os.Lstat(hostexec.SystemPath(ancestor))
        if err != nil { return fmt.Errorf("unsafe host worker ancestor: %w", err) }
        st, ok := info.Sys().(*syscall.Stat_t)
        if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
            st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
            return errors.New("worker installation directory is not root-owned and protected")
        }
    }
    path := hostexec.SystemPath(filecopyHostBinary)
    info, err := os.Lstat(path)
    if err != nil { return fmt.Errorf("operator must install filecopy worker: %w", err) }
    st, ok := info.Sys().(*syscall.Stat_t)
    if !ok || !info.Mode().IsRegular() || st.Uid != 0 ||
        info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 ||
        st.Nlink != 1 {
        return errors.New("host filecopy worker is not an immutable root-owned executable")
    }
    return nil
}

func persistFilecopyManifest(m filecopyworker.Manifest) error {
    if err := m.Validate(m.JobID); err != nil { return err }
    if err := os.Mkdir(filecopyBrokerState, 0700); err != nil && !errors.Is(err, os.ErrExist) {
        return err
    }
    if err := filecopyworker.CheckDir(filecopyBrokerState); err != nil { return err }
    root, err := os.OpenRoot(filecopyBrokerState)
    if err != nil { return err }
    defer root.Close()
    file, err := root.OpenFile(m.JobID+".request.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
    if err != nil { return err }
    complete := false
    defer func() {
        _ = file.Close()
        if !complete { _ = root.Remove(m.JobID+".request.json") }
    }()
    data, err := json.Marshal(m)
    if err != nil || len(data) > filecopyworker.MaxManifestBytes {
        return errors.New("filecopy manifest is not representable within safe limit")
    }
    if _, err := file.Write(data); err != nil { return err }
    if err := file.Sync(); err != nil { return err }
    if err := file.Close(); err != nil { return err }
    fd, err := unix.Open(filecopyBrokerState, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
    if err != nil { return err }
    syncErr := unix.Fsync(fd)
    _ = unix.Close(fd)
    if syncErr != nil { return syncErr }
    complete = true
    return nil
}

func dispatchFilecopyUnit(ctx context.Context, id, root string) error {
    if !filecopyworker.ValidID(id) { return errors.New("invalid filecopy unit identity") }
    // Root is derived only from the operator-owned broker environment.
    // Revalidate before interpolating into a typed systemd property.
    probe := filecopyworker.Manifest{JobID:id, Subject:"operator", ScopeRoot:root,
        Source:root+"/filecopy-source", Destination:root+"/filecopy-destination", MaxBytes:1}
    if err := probe.Validate(id); err != nil { return errors.New("unsafe physical scope for filecopy dispatch") }
    if err := verifyHostFilecopyBinary(); err != nil { return err }
    args := []string{
        "--unit="+id, "--no-block", "--quiet",
        "--description=Portico typed binary filecopy task",
        "--property=Type=oneshot",
        "--property=RemainAfterExit=yes",
        "--property=NoNewPrivileges=yes",
        "--property=UMask=0077",
        "--property=TimeoutStartSec=43200",
        "--property=MemoryMax=1073741824",
        "--property=TasksMax=64",
        "--property=ProtectSystem=strict",
        "--property=ReadWritePaths="+root+" "+filecopyworker.StateDir,
        "--property=ProtectHome=yes",
        "--property=PrivateDevices=yes",
        "--property=PrivateNetwork=yes",
        "--", filecopyHostBinary, id,
    }
    out, err := hostexec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
    if err != nil {
        return fmt.Errorf("typed systemd worker dispatch uncertain: %s: %w", boundedOutput(out), err)
    }
    return nil
}

// startFilecopy enforces permissions synchronously and leaves all long-running
// I/O to a root-owned, independently installed systemd worker.
func (b *Broker) startFilecopy(ctx context.Context, req wire.Request) wire.Response {
    quota := filecopyOperatorQuota()
    if quota == 0 { return deny(req.ID, "permission_denied", "binary filecopy worker not enabled by independent operator") }
    if b.State == nil || b.Jobs == nil { return deny(req.ID, "state_required", "durable job store required") }
    if !b.Policy.CanFilesystem("copy") { return deny(req.ID, "permission_denied", "filesystem copy disabled") }
    if req.InvocationID == "" { return deny(req.ID, "invocation_required", "filecopy.start requires operation_id") }
    var in struct {
        Destination string `json:"destination"`
        MaxBytes int64 `json:"max_bytes"`
        ExpectedSHA256 string `json:"expected_sha256"`
    }
    if err := json.Unmarshal(req.Args,&in); err != nil {
        return deny(req.ID, "invalid_args", "invalid binary copy request")
    }
    if in.MaxBytes <= 0 || in.MaxBytes > quota {
        return deny(req.ID, "quota_denied", "filecopy quota exceeds operator limit")
    }
    selectedRoot := os.Getenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT")
    if selectedRoot == "" {
        return deny(req.ID, "permission_denied", "filecopy requires an operator-configured physical scope")
    }
    manifest := filecopyworker.Manifest{
        JobID:filecopyID(req.Subject,req.InvocationID),
        Subject:req.Subject,Source:req.Resource,Destination:in.Destination,
        MaxBytes:in.MaxBytes,ExpectedSHA256:in.ExpectedSHA256,
        ScopeRoot:selectedRoot,
    }
    if err := manifest.Validate(manifest.JobID); err != nil {
        return deny(req.ID, "invalid_args", err.Error())
    }
    revoked, revocationErr := filecopyworker.IsSubjectRevoked(filecopyBrokerState, req.Subject)
    if revocationErr != nil {
        return deny(req.ID, "reconcile_required", "operator-owned revocation fence unavailable; no filecopy dispatched")
    }
    if revoked {
        return deny(req.ID, "permission_denied", "subject's binary filecopy permission was revoked by operator")
    }
    readRoots, writeRoots, err := b.effectiveFileRoots(ctx, req.Subject)
    if err != nil { return deny(req.ID, "filesystem_unavailable", err.Error()) }
    if !pathWithinAnyRoot(readRoots, req.Resource) || !pathWithinAnyRoot(writeRoots, in.Destination) {
        return deny(req.ID, "permission_denied", "source or destination outside authorized roots")
    }
    // Filecopy can run for hours. Temporary approvals must NOT be converted
    // into a privileged background copy that outlives their TTL or revocation.
    // Require both paths inside permanent policy roots, not merely an
    // active short-lived root delegation.
    if !pathWithinAnyRoot(b.Policy.Filesystem.Read, req.Resource) ||
        !pathWithinAnyRoot(b.Policy.Filesystem.Write, in.Destination) {
        return deny(req.ID, "permission_denied", "long binary copy requires permanent read/write policy roots")
    }
    fs, err := b.effectiveFS(ctx, req.Subject)
    if err != nil { return deny(req.ID, "filesystem_unavailable", err.Error()) }
    entry, err := fs.Stat(req.Resource, false)
    if err != nil {
        return deny(req.ID, "source_invalid", "cannot verify source before filecopy dispatch")
    }
    if entry.IsDir || entry.Symlink || entry.Size > in.MaxBytes {
        return deny(req.ID, "source_invalid", "source is not a permitted regular file within quota")
    }
    // Operator must install the host binary independently. Do not stage any
    // intent when the worker executable cannot be verified.
    if err := verifyHostFilecopyBinary(); err != nil {
        return deny(req.ID, "worker_unavailable", err.Error())
    }
    requestHash, err := state.HashRequest(map[string]any{
        "subject":req.Subject,"tool":req.Tool,"source":req.Resource,
        "destination":in.Destination,"max_bytes":in.MaxBytes,
        "expected_sha256":in.ExpectedSHA256,"scope_root":selectedRoot,
    })
    if err != nil { return deny(req.ID, "hash_error", err.Error()) }
    decision, cached, err := b.State.BeginOperation(ctx,req.InvocationID,req.Subject,req.Tool,requestHash)
    if err != nil { return deny(req.ID, "state_error", err.Error()) }
    switch decision {
    case state.OperationCached:
        return wire.Response{ID:req.ID,OK:true,Result:cached}
    case state.OperationConflict:
        return deny(req.ID,"idempotency_conflict","operation id reused with different filecopy")
    case state.OperationReconcile:
        // The acknowledgement can be lost after systemd accepted the job.
        // Return its deterministic ID for safe status inspection, never
        // dispatch another unit or claim the operation completed.
        existing, lookupErr := b.State.GetJob(ctx, manifest.JobID)
        if lookupErr == nil && existing.Subject == req.Subject &&
            existing.Tool == req.Tool &&
            filepath.Clean(existing.Resource) == filepath.Clean(in.Destination) {
            return ok(req.ID, map[string]any{
                "job_id":manifest.JobID, "state":existing.State,
                "recovered":true, "reconcile_required":true,
                "status_tool":"job.status",
            })
        }
        return deny(req.ID,"reconcile_required","prior filecopy dispatch outcome uncertain; operator reconciliation required")
    }
    abortBeforeDispatch := func() { _ = b.State.AbortOperation(context.Background(), req.InvocationID) }
    active, err := b.State.ListActiveJobs(ctx)
    if err != nil {
        abortBeforeDispatch()
        return deny(req.ID,"state_error",err.Error())
    }
    for _, rec := range active {
        if rec.Tool!="file.copy.start" { continue }
        // Resource locks must include both source and destination to prevent
        // a second filecopy into the same destination while one runs.
        if filepath.Clean(rec.Resource)==filepath.Clean(in.Destination) {
            abortBeforeDispatch()
            return deny(req.ID,"resource_busy","destination has an active binary copy")
        }
    }
    if err := persistFilecopyManifest(manifest); err != nil {
        abortBeforeDispatch()
        return deny(req.ID,"manifest_error",err.Error())
    }
    id := manifest.JobID
    rec := state.JobRecord{
        ID:id,UnitName:id+".service",Subject:req.Subject,
        Tool:req.Tool,Resource:in.Destination,State:"starting",
        Deadline:time.Now().Add(filecopyJobTimeout),
    }
    if err := b.State.CreateJob(ctx,rec); err != nil {
        // The manifest may already be durable. Keep the operation pending
        // rather than abort and risk reusing this identifier against the
        // existing manifest. No dispatch has been attempted.
        return deny(req.ID,"reconcile_required",
            "manifest persisted but SQLite job creation failed; operator reconciliation required")
    }
    if err := dispatchFilecopyUnit(ctx,id,selectedRoot); err != nil {
        _ = b.State.UpdateJobState(context.Background(),id,"starting","dispatch acknowledgement uncertain",nil)
        return deny(req.ID,"reconcile_required","filecopy dispatch uncertain: "+err.Error())
    }
    if err := b.State.UpdateJobState(ctx,id,"running","",nil); err != nil {
        return deny(req.ID,"reconcile_required","worker dispatched but SQLite status was not persisted")
    }
    reply,_ := json.Marshal(map[string]any{
        "job_id":id,"state":"running","bytes_done":int64(0),
        "source":req.Resource,"destination":in.Destination,
        "status_tool":"job.status","cancel_tool":"job.cancel",
    })
    if err := b.State.CompleteOperation(ctx,req.InvocationID,reply); err != nil {
        return deny(req.ID,"reconcile_required","worker dispatched but invocation journal uncertain")
    }
    return wire.Response{ID:req.ID,OK:true,Result:reply}
}

// Read only the worker-created progress metadata, not file contents.
func readFilecopyProgress(id string) (filecopyworker.Progress,error) {
    var progress filecopyworker.Progress
    if !filecopyworker.ValidID(id) { return progress, errors.New("invalid binary-copy job id") }
    if err := filecopyworker.CheckDir(filecopyBrokerState); err != nil { return progress,err }
    root,err:=os.OpenRoot(filecopyBrokerState)
    if err!=nil { return progress,err }
    defer root.Close()
    file,err:=root.Open(id+".progress.json")
    if err!=nil { return progress,err }
    defer file.Close()
    data,err:=io.ReadAll(io.LimitReader(file, 4097))
    if err!=nil { return progress,err }
    if len(data)>4096 { return progress,errors.New("progress record too large") }
    if err:=json.Unmarshal(data,&progress);err!=nil { return progress,err }
    if progress.JobID!=id { return progress,errors.New("progress identity mismatch") }
    return progress,nil
}

// classifyFilecopyUnit reconciles systemd termination with durable worker
// checkpoints. In particular, stopped oneshots may be inactive/success even
// though they persisted phase=cancelled, not phase=done.
func classifyFilecopyUnit(raw string, progress filecopyworker.Progress, progressErr error) (string, string, *int, error) {
    // Systemd may record a stopped oneshot as inactive/dead Result=success.
    // The worker's durably persisted cancelled state takes precedence only
    // AFTER systemd confirms the unit has terminated.
    state, detail, code, err := classifyComposeUnit(raw)
    if err != nil { return "", "", nil, err }
    if progressErr == nil && progress.Phase == "cancelled" &&
        (state == "done" || state == "failed") {
        return "cancelled", "worker persisted cancellation", nil, nil
    }
    return state, detail, code, nil
}

func (b *Broker) filecopyStatus(ctx context.Context, record state.JobRecord) (map[string]any,error) {
    if !filecopyworker.ValidID(record.ID) || record.UnitName != record.ID+".service" ||
        record.Tool != "file.copy.start" {
        return nil, errors.New("invalid typed filecopy job identity")
    }
    out,err:=hostexec.CommandContext(ctx,"systemctl","show",record.UnitName,
        "--no-pager","--property=LoadState,ActiveState,SubState,Result,ExecMainStatus").CombinedOutput()
    if err!=nil { return nil,fmt.Errorf("cannot verify filecopy unit: %w",err) }
    progress,progressErr:=readFilecopyProgress(record.ID)
    // Unlike ordinary successful oneshot jobs, a systemctl stop can report
    // inactive/dead/Result=success even when the worker exited by cancellation.
    // The worker's durable terminal checkpoint is authoritative for that case.
    unitState,detail,code,err:=classifyFilecopyUnit(string(out), progress, progressErr)
    if err!=nil { return nil,fmt.Errorf("filecopy unit uncertain: %w",err) }
    if unitState=="done" {
        if progressErr!=nil || progress.Phase!="done" || progress.SHA256=="" {
            return nil,errors.New("worker finished but verified final copy metadata is unavailable: reconcile")
        }
    }
    if unitState=="failed" {
        if progressErr!=nil {
            return nil,errors.New("filecopy unit failed without durable worker progress: operator reconciliation required")
        }
        // A crashed worker can leave phase=running even AFTER the atomic
        // destination rename. Failure is trustworthy only when the worker
        // durably recorded its own terminal failed/cancelled phase.
        switch progress.Phase {
        case "failed":
            // Keep systemd failed classification.
        case "cancelled":
            unitState="cancelled"
        default:
            return nil,errors.New("filecopy publication outcome uncertain: operator reconciliation required")
        }
    }
    if unitState!=record.State && unitState!="running" {
        if err:=b.State.UpdateJobState(ctx,record.ID,unitState,detail,code);err!=nil { return nil,err }
        record,err=b.State.GetJob(ctx,record.ID)
        if err!=nil { return nil,err }
    }
    result:=map[string]any{"job_id":record.ID,"state":unitState,
        "tool":record.Tool,"exit_code":record.ExitCode,
        "updated_at":record.UpdatedAt,
    }
    if progressErr==nil {
        result["bytes_done"]=progress.BytesDone
        result["bytes_total"]=progress.BytesTotal
        result["phase"]=progress.Phase
        if progress.Phase=="done" { result["sha256"]=progress.SHA256 }
    }
    return result,nil
}


// Cancel ONLY a typed unit owned by this OAuth subject. Never report a
// successful cancel before checking whether publication already completed.
// The general shell/Compose cancel path remains unchanged.
func (b *Broker) cancelFilecopy(ctx context.Context, req wire.Request, record state.JobRecord) wire.Response {
    if record.Subject != req.Subject {
        return deny(req.ID, "job_error", "job belongs to another subject")
    }
    if !filecopyworker.ValidID(record.ID) || record.UnitName != record.ID+".service" {
        return deny(req.ID, "job_error", "filecopy unit identity is invalid")
    }
    previous, err := b.filecopyStatus(ctx, record)
    if err != nil {
        return deny(req.ID, "reconcile_required", err.Error())
    }
    phase, _ := previous["state"].(string)
    if phase == "done" || phase == "failed" || phase == "cancelled" {
        return deny(req.ID, "job_already_terminal", "filecopy job already terminal")
    }
    out, err := hostexec.CommandContext(ctx, "systemctl", "stop", record.UnitName).CombinedOutput()
    if err != nil {
        return deny(req.ID, "reconcile_required",
            "filecopy stop acknowledgement uncertain: "+boundedOutput(out))
    }
    current, err := b.State.GetJob(ctx, record.ID)
    if err != nil {
        return deny(req.ID, "reconcile_required", "cannot reread job after stopping worker")
    }
    result, err := b.filecopyStatus(ctx, current)
    if err != nil {
        return deny(req.ID, "reconcile_required", err.Error())
    }
    stateName, _ := result["state"].(string)
    switch stateName {
    case "cancelled":
        return ok(req.ID, map[string]any{"job_id":record.ID, "cancelled":true})
    case "done":
        return deny(req.ID, "job_already_terminal", "filecopy completed before cancellation")
    default:
        return deny(req.ID, "reconcile_required", "filecopy stop not conclusively acknowledged as cancelled")
    }
}
