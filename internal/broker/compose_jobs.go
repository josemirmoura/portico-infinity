package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/wire"
)

const composeJobRuntime = 2 * time.Hour

// composeAsyncStarter is deliberately separate from DockerManager. Only the
// production DockerCLI implements it; existing test doubles preserve legacy
// synchronous behavior without gaining authority to launch host units.
type composeAsyncStarter interface {
	StartComposeJob(context.Context, string, string, string) error
}

func composeJobID(subject, invocationID string) string {
	digest := sha256.Sum256([]byte(subject + "\x00" + invocationID))
	return "vps-agent-compose-" + hex.EncodeToString(digest[:16])
}

func composeArgs(action string) ([]string, error) {
	switch action {
	case "pull":
		return []string{"pull"}, nil
	case "up":
		return []string{"up", "-d", "--remove-orphans"}, nil
	case "down":
		return []string{"down"}, nil
	default:
		return nil, fmt.Errorf("unsupported compose action %q", action)
	}
}

// StartComposeJob queues a hardcoded Docker Compose command as a host systemd
// transient unit. It never accepts a shell command from the MCP caller. No
// Docker socket or root shell is exposed through the Gateway or its tools.
// Units are kept (no --collect) so systemctl can report the final exit status
// after clients disconnect or the Broker is restarted.
func (DockerCLI) StartComposeJob(ctx context.Context, dir, action, unit string) error {
	dir, composeFile, err := canonicalComposeProject(dir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(unit, "vps-agent-compose-") {
		return errors.New("invalid compose job unit prefix")
	}
	if len(unit) > 75 || strings.ContainsAny(unit, "/ \t\n") {
		return errors.New("invalid compose job unit name")
	}
	actionArgs, err := composeArgs(action)
	if err != nil {
		return err
	}
	args := []string{
		"--unit=" + unit, "--no-block", "--quiet",
		"--description=Portico authorized Docker Compose task",
		"--working-directory=" + dir,
		"--property=Type=oneshot",
		"--property=RemainAfterExit=yes",
		"--property=NoNewPrivileges=yes",
		"--property=UMask=0077",
		"--property=RuntimeMaxSec=7200",
		"--property=MemoryMax=1073741824",
		"--property=TasksMax=128",
		"--",
		"docker", "compose", "-f", composeFile, "--project-directory", dir,
	}
	args = append(args, actionArgs...)
	out, err := hostexec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run compose: %s: %w", boundedOutput(out), err)
	}
	return nil
}

// classifyComposeUnit fails closed on missing status. In particular, a
// missing/garbage-collected unit MUST NOT be mistaken for a successful job.
func classifyComposeUnit(raw string) (string, string, *int, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	active := fields["ActiveState"]
	substate := fields["SubState"]
	result := fields["Result"]
	load := fields["LoadState"]
	if load != "loaded" {
		return "", "", nil, fmt.Errorf("compose unit unavailable (LoadState=%q); reconcile by operator", load)
	}
	exit := (*int)(nil)
	if val := fields["ExecMainStatus"]; val != "" {
		number, err := strconv.Atoi(val)
		if err != nil {
			return "", "", nil, fmt.Errorf("invalid Compose exit status %q", val)
		}
		exit = &number
	}
	switch active {
	case "active":
		if substate == "exited" {
			if result == "success" && exit != nil && *exit == 0 {
				return "done", "", exit, nil
			}
			return "", "", nil, errors.New("exited Compose unit without trustworthy success status")
		}
		return "running", "", nil, nil
	case "activating", "reloading", "deactivating":
		return "running", "", nil, nil
	case "failed":
		return "failed", "systemd result: " + result, exit, nil
	case "inactive":
		if result == "success" && exit != nil && *exit == 0 {
			return "done", "", exit, nil
		}
		if result != "" && result != "success" {
			return "failed", "systemd result: " + result, exit, nil
		}
		return "", "", nil, errors.New("inactive Compose unit without trustworthy exit result")
	default:
		return "", "", nil, fmt.Errorf("unknown Compose unit state %q; reconcile", active)
	}
}

func (b *Broker) composeJobStatus(ctx context.Context, record state.JobRecord) (state.JobRecord, error) {
	switch record.State {
	case "done", "failed", "cancelled":
		return record, nil
	}
	out, err := hostexec.CommandContext(ctx, "systemctl", "show", record.UnitName,
		"--no-pager", "--property=LoadState,ActiveState,SubState,Result,ExecMainStatus").CombinedOutput()
	if err != nil {
		return record, fmt.Errorf("cannot verify Compose unit: %s: %w", boundedOutput(out), err)
	}
	status, detail, exitCode, err := classifyComposeUnit(string(out))
	if err != nil {
		return record, err
	}
	if status != record.State {
		if err := b.State.UpdateJobState(ctx, record.ID, status, detail, exitCode); err != nil {
			return record, fmt.Errorf("persist Compose job status: %w", err)
		}
		return b.State.GetJob(ctx, record.ID)
	}
	return record, nil
}

func (b *Broker) composeMutationAsync(ctx context.Context, req wire.Request, action string, starter composeAsyncStarter) wire.Response {
	if b.State == nil {
		return deny(req.ID, "state_required", "Compose jobs require durable state")
	}
	if b.Jobs == nil {
		return deny(req.ID, "jobs_unavailable", "Compose jobs require the job service")
	}
	requestHash, _ := state.HashRequest(map[string]any{
		"subject": req.Subject, "tool": req.Tool, "project_dir": req.Resource, "action": action,
	})
	decision, cached, err := b.State.BeginOperation(ctx, req.InvocationID, req.Subject, req.Tool, requestHash)
	if err != nil {
		return deny(req.ID, "state_error", err.Error())
	}
	jobID := composeJobID(req.Subject, req.InvocationID)
	switch decision {
	case state.OperationCached:
		return wire.Response{ID: req.ID, OK: true, Result: cached}
	case state.OperationConflict:
		return deny(req.ID, "idempotency_conflict", "operation id reused with different request")
	case state.OperationReconcile:
		// If a previous attempt dispatched a unit but lost its IPC reply, the
		// existing job is discoverable by its deterministic ID. Do not restart it.
		record, err := b.State.GetJob(ctx, jobID)
		if err != nil || record.Subject != req.Subject || record.Tool != req.Tool ||
			filepath.Clean(record.Resource) != filepath.Clean(req.Resource) {
			return deny(req.ID, "reconcile_required", "Compose dispatch uncertain; operator reconciliation required")
		}
		return ok(req.ID, map[string]any{"job_id": jobID, "state": record.State,
			"project_dir": req.Resource, "action": action, "recovered": true})
	}

	// The Broker serializes mutation dispatch through auditMutationMu. Check
	// all subjects so a second OAuth principal cannot mutate the same Compose
	// project while a previous Docker job is running.
	// If no external job has been dispatched, an operation rejected as busy
	// must remain retryable with the SAME operation_id. Only an uncertain
	// systemd dispatch is intentionally left pending for reconciliation.
	abortBeforeDispatch := func() {
		_ = b.State.AbortOperation(context.Background(), req.InvocationID)
	}
	active, err := b.State.ListActiveJobs(ctx)
	if err != nil {
		abortBeforeDispatch()
		return deny(req.ID, "state_error", err.Error())
	}
	for _, record := range active {
		if !strings.HasPrefix(record.Tool, "compose.") ||
			filepath.Clean(record.Resource) != filepath.Clean(req.Resource) {
			continue
		}
		current, statusErr := b.composeJobStatus(ctx, record)
		if statusErr != nil {
			abortBeforeDispatch()
			return deny(req.ID, "resource_busy", "earlier Compose job status uncertain; inspect before proceeding")
		}
		if current.State != "done" && current.State != "failed" && current.State != "cancelled" {
			abortBeforeDispatch()
			return deny(req.ID, "resource_busy", "another Compose job is running for this project")
		}
	}

	unit := jobID + ".service"
	record := state.JobRecord{
		ID: jobID, UnitName: unit, Subject: req.Subject, Tool: req.Tool,
		Resource: req.Resource, State: "starting", Deadline: time.Now().Add(composeJobRuntime),
	}
	if err := b.State.CreateJob(ctx, record); err != nil {
		return deny(req.ID, "state_error", "cannot persist Compose job before dispatch: "+err.Error())
	}
	if err := starter.StartComposeJob(ctx, req.Resource, action, jobID); err != nil {
		// Starting may have reached systemd before its acknowledgement failed.
		// Keep the operation journal pending and refuse automatic re-execution.
		// Keep the job pending: systemd may have accepted the unit even if its
		// acknowledgement was lost. A different operation ID must stay blocked.
		_ = b.State.UpdateJobState(context.Background(), jobID, "starting", "dispatch acknowledgement uncertain", nil)
		return deny(req.ID, "reconcile_required", "Compose dispatch failed or uncertain: "+err.Error())
	}
	if err := b.State.UpdateJobState(ctx, jobID, "running", "", nil); err != nil {
		return deny(req.ID, "reconcile_required", "Compose dispatched but job status was not persisted")
	}
	result, _ := json.Marshal(map[string]any{
		"job_id": jobID, "state": "running", "project_dir": req.Resource,
		"action": action, "status_tool": "job.status", "log_tool": "job.tail",
	})
	if err := b.State.CompleteOperation(ctx, req.InvocationID, result); err != nil {
		return deny(req.ID, "reconcile_required", "Compose dispatched but journal update failed")
	}
	return wire.Response{ID: req.ID, OK: true, Result: result}
}
