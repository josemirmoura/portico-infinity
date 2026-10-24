package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

type Manager struct {
	State  *state.Store
	Runner Runner
	// Enabled only for the Infinity broker. Community legacy behavior remains
	// unchanged until its own explicitly reviewed migration.
	DurableShellCleanup bool
	// Cursor is process-local and reconstructed conservatively after a Broker
	// restart. The keyset scan itself is always bounded in SQLite.
	reconcileMu sync.Mutex
	reconcileCreated time.Time
	reconcileID string
}

func (m *Manager) Start(ctx context.Context, subject, tool, resource, grantID string, spec sandbox.Spec) (state.JobRecord, error) {
	if m.State == nil || m.Runner == nil {
		return state.JobRecord{}, errors.New("job manager is not configured")
	}
	if spec.Unit == "" {
		return state.JobRecord{}, errors.New("job unit is required")
	}
	now := time.Now()
	deadline := now.Add(spec.Runtime)
	if grantID != "" {
		grant, err := m.State.GetGrant(ctx, grantID)
		if err != nil {
			return state.JobRecord{}, fmt.Errorf("load job grant: %w", err)
		}
		if grant.Subject != subject || grant.Revoked || !grant.ExpiresAt.After(now) {
			return state.JobRecord{}, errors.New("job grant is invalid, revoked, expired, or belongs to another subject")
		}
		if grant.ExpiresAt.Before(deadline) {
			deadline = grant.ExpiresAt
		}
		spec.Runtime = time.Until(deadline)
		if spec.Runtime <= 0 {
			return state.JobRecord{}, errors.New("job deadline has already expired")
		}
	}
	rec := state.JobRecord{
		ID: spec.Unit, Subject: subject, Tool: tool, Resource: resource,
		UnitName: spec.Unit, GrantID: grantID, State: "starting", Deadline: deadline,
	}
	if err := m.State.CreateJob(ctx, rec); err != nil {
		return state.JobRecord{}, err
	}
	started, err := m.Runner.Start(ctx, spec)
	if err != nil {
		_ = m.State.UpdateJobState(context.Background(), rec.ID, "failed", err.Error(), nil)
		return state.JobRecord{}, err
	}
	rec.State = started.State
	rec.Deadline = started.Deadline
	if err := m.State.UpdateJobState(ctx, rec.ID, rec.State, "", nil); err != nil {
		return state.JobRecord{}, fmt.Errorf("job started but state update failed: %w", err)
	}
	return m.State.GetJob(ctx, rec.ID)
}

func (m *Manager) Status(ctx context.Context, subject, id string) (state.JobRecord, error) {
	rec, err := m.State.GetJob(ctx, id)
	if err != nil {
		return state.JobRecord{}, err
	}
	if rec.Subject != subject {
		return state.JobRecord{}, errors.New("job belongs to another subject")
	}
	if rec.State == "done" || rec.State == "failed" || rec.State == "cancelled" || rec.State == "reconcile_required" {
		return rec, nil
	}
	job := Job{ID: rec.ID, Unit: rec.UnitName, State: rec.State, Deadline: rec.Deadline}
	if runner, ok := m.Runner.(DetailedStatusRunner); ok {
		terminal, statusErr := runner.DetailedStatus(ctx, job)
		if statusErr != nil {
			// Do not mutate the durable checkpoint when the unit disappears
			// after a crash or its final state cannot be verified.
			return rec, fmt.Errorf("reconcile_required: %w", statusErr)
		}
		if terminal.State == "" {
			return rec, errors.New("reconcile_required: shell job missing a trustworthy state")
		}
		if terminal.State != rec.State || terminal.ExitCode != nil || terminal.Detail != "" {
            var changed bool
            var err error
            if m.DurableShellCleanup && (terminal.State=="done" || terminal.State=="failed") {
                // The outbox and job checkpoint must commit atomically.
                changed,err=m.State.CompleteShellJobAndQueueCleanup(
                    ctx,rec.ID,terminal.State,terminal.Detail,terminal.ExitCode)
            } else {
                changed,err=m.State.UpdateActiveJobState(ctx,rec.ID,
                    terminal.State,terminal.Detail,terminal.ExitCode)
            }
			if err != nil { return rec, fmt.Errorf("reconcile_required: cannot persist shell job status: %w", err) }
			rec, statusErr = m.State.GetJob(ctx, rec.ID)
			if statusErr != nil { return rec, statusErr }
			if changed && (rec.State == "done" || rec.State == "failed") {
                if !m.DurableShellCleanup {
				    if collector, ok := m.Runner.(TerminalCollector); ok { collector.CollectFinished(ctx, job) }
                }
			}
		}
		return rec, nil
	}
	// Other test runners and legacy adapters retain the existing interface.
	status, err := m.Runner.Status(ctx, job)
	if err != nil {
		return rec, err
	}
	if status != "" && status != rec.State {
		if err := m.State.UpdateJobState(ctx, rec.ID, status, "", nil); err != nil {
			return rec, err
		}
		return m.State.GetJob(ctx, rec.ID)
	}
	return rec, nil
}


type ShellReconcileResult struct {
    Inspected int
    Completed int
    RequiresOperator int
}

// ReconcileShellJobs handles shell units whose MCP clients never poll status.
// The Broker invokes this in Infinity only. Never claim success from a
// missing or expired unit, and do not retry an untrusted previous operation.
func (m *Manager) ReconcileShellJobs(ctx context.Context, limit int) (ShellReconcileResult, error) {
    var summary ShellReconcileResult
    if m == nil || m.State == nil || m.Runner == nil {
        return summary, errors.New("job reconciler is not configured")
    }
    if limit <= 0 || limit > 64 { limit = 64 }
    m.reconcileMu.Lock()
    defer m.reconcileMu.Unlock()
    now := time.Now()
    cutoff := now.Add(-20 * time.Second)
    records, err := m.State.ListActiveShellJobsPage(ctx,
        m.reconcileCreated, m.reconcileID, cutoff, limit)
    if err != nil { return summary, err }
    if len(records) == 0 && !m.reconcileCreated.IsZero() {
        // End of keyset: wrap and advance through older, still-running jobs
        // without letting them starve new arrivals forever.
        m.reconcileCreated = time.Time{}
        m.reconcileID = ""
        records, err = m.State.ListActiveShellJobsPage(ctx,
            time.Time{}, "", cutoff, limit)
        if err != nil { return summary, err }
    }
    for _, record := range records {
        if ctx.Err() != nil { return summary, ctx.Err() }
        // Advance even when the job stays running, so a backlog of live
        // commands cannot indefinitely starve jobs later in the ledger.
        m.reconcileCreated = record.CreatedAt
        m.reconcileID = record.ID
        // The record may have become terminal between SELECT and GetJob.
        switch record.State {
        case "starting", "running", "active", "activating", "reloading", "deactivating", "cancelling":
        default:
            continue
        }
        summary.Inspected++
        current, checkErr := m.Status(ctx, record.Subject, record.ID)
        if checkErr == nil && (current.State == "done" || current.State == "failed") {
            summary.Completed++
            continue
        }
        if now.Before(record.Deadline.Add(time.Minute)) { continue }
        // A job still untrusted past its deadline requires operator review,
        // not automated replay or a fabricated zero exit code.
        changed, err := m.State.UpdateActiveJobState(ctx, record.ID,
            "reconcile_required", "expired shell result needs operator reconciliation", nil)
        if err != nil { return summary, err }
        if changed { summary.RequiresOperator++ }
    }
    return summary, nil
}


// SweepShellUnitCleanup retries only pending, durable terminal receipts. It
// never runs a command or broad systemctl discovery. A failed host cleanup
// remains pending and rotates behind other receipts for a later tick.
func (m *Manager) SweepShellUnitCleanup(ctx context.Context, limit int)(int,int,error){
    if m==nil || !m.DurableShellCleanup {return 0,0,nil}
    if m.State==nil{return 0,0,errors.New("cleanup state unavailable")}
    retirer,ok:=m.Runner.(DurableTerminalRetirer)
    if !ok{return 0,0,errors.New("durable cleanup runner unavailable")}
    if limit<=0||limit>64{limit=64}
    items,err:=m.State.PendingShellUnitCleanup(ctx,limit)
    if err!=nil{return 0,0,err}
    completed,failed:=0,0
    for _,item:=range items{
        if ctx.Err()!=nil{return completed,failed,ctx.Err()}
        job:=Job{ID:item.JobID,Unit:item.Unit,State:"done"}
        err:=retirer.RetireTerminalUnit(ctx,job)
        if err!=nil{
            failed++
            // Avoid persisting raw systemctl output that could expose
            // diagnostic details; the operator can read the journal.
            if dbErr:=m.State.FailShellUnitCleanup(ctx,item.JobID,item.Unit,
                "cleanup failed; inspect systemd journal");dbErr!=nil{
                return completed,failed,dbErr
            }
            continue
        }
        if err:=m.State.FinishShellUnitCleanup(ctx,item.JobID,item.Unit);err!=nil{
            return completed,failed,err
        }
        completed++
    }
    return completed,failed,nil
}

func (m *Manager) Tail(ctx context.Context, subject, id string, lines int) (string, error) {
	rec, err := m.State.GetJob(ctx, id)
	if err != nil {
		return "", err
	}
	if rec.Subject != subject {
		return "", errors.New("job belongs to another subject")
	}
	return m.Runner.Tail(ctx, Job{ID: rec.ID, Unit: rec.UnitName, State: rec.State, Deadline: rec.Deadline}, lines)
}

func (m *Manager) Cancel(ctx context.Context, subject, id string) error {
	rec, err := m.State.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if rec.Subject != subject {
		return errors.New("job belongs to another subject")
	}
	if rec.State == "done" || rec.State == "failed" || rec.State == "cancelled" ||
		rec.State == "reconcile_required" {
		return errors.New("job_already_terminal: refusing cancellation of a terminal or unresolved job")
	}
	if err := m.Runner.Cancel(ctx, Job{ID: rec.ID, Unit: rec.UnitName, State: rec.State, Deadline: rec.Deadline}); err != nil {
		return err
	}
	changed, err := m.State.UpdateActiveJobState(ctx, rec.ID, "cancelled", "cancelled by request", nil)
	if err != nil { return err }
	if !changed {
		return errors.New("reconcile_required: job changed state concurrently with cancellation")
	}
	return nil
}
