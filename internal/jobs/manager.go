package jobs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/hostexec"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
)

type Job struct {
	ID       string    `json:"id"`
	Unit     string    `json:"unit"`
	State    string    `json:"state"`
	Deadline time.Time `json:"deadline"`
}

type Runner interface {
	Start(context.Context, sandbox.Spec) (Job, error)
	Status(context.Context, Job) (string, error)
	Tail(context.Context, Job, int) (string, error)
	Cancel(context.Context, Job) error
}

type SystemdRunner struct{}

// TerminalStatus is based on the systemd result, not just ActiveState.
// An unavailable or collected unit is always ambiguous until independently
// reconciled; it must never be considered a successful shell command.
type TerminalStatus struct {
	State    string
	Detail   string
	ExitCode *int
}

type DetailedStatusRunner interface {
	DetailedStatus(context.Context, Job) (TerminalStatus, error)
}

type TerminalCollector interface {
	CollectFinished(context.Context, Job)
}

// DurableTerminalRetirer is a typed host operation invoked only for a shell
// job with a proven terminal exit and a matching durable SQLite receipt.
type DurableTerminalRetirer interface {
    RetireTerminalUnit(context.Context,Job) error
}

// safeRetirableShellUnit prevents the cleanup journal from becoming an
// arbitrary systemctl-stop API. Shell units have deterministic Broker names.
func safeRetirableShellUnit(unit string)bool {
    const prefix="vps-agent-job-"
    if !strings.HasPrefix(unit,prefix)||len(unit)!=len(prefix)+16{return false}
    for _,ch:=range unit[len(prefix):]{
        if !((ch>='0'&&ch<='9')||(ch>='a'&&ch<='f')){return false}
    }
    return true
}

func shellUnitLifecycle(raw string)(load,active,sub string){
    for _,line:=range strings.Split(raw,"\n"){
        k,v,ok:=strings.Cut(line,"=")
        if !ok{continue}
        switch strings.TrimSpace(k){
        case "LoadState":load=strings.TrimSpace(v)
        case "ActiveState":active=strings.TrimSpace(v)
        case "SubState":sub=strings.TrimSpace(v)
        }
    }
    return
}

func (SystemdRunner) RetireTerminalUnit(ctx context.Context,job Job)error {
    if !safeRetirableShellUnit(job.Unit){
        return errors.New("cleanup requires deterministic Broker shell unit name")
    }
    inspect:=func()(string,string,string,error){
        out,err:=hostexec.CommandContext(ctx,"systemctl","show",job.Unit,
            "--no-pager","--property=LoadState,ActiveState,SubState").CombinedOutput()
        if err!=nil{return "","","",fmt.Errorf("systemd unit inspection failed: %w",err)}
        a,b,c:=shellUnitLifecycle(string(out))
        if a==""{return "","","",errors.New("systemd returned no LoadState")}
        return a,b,c,nil
    }
    load,active,sub,err:=inspect()
    if err!=nil{return err}
    if load=="not-found"{return nil} // Already retired by a prior attempt.
    if load!="loaded"{
        return fmt.Errorf("unexpected shell unit load state %q",load)
    }
    // A later unit with the same name that is actively running is never
    // stopped by recovery. It requires manual inspection.
    if !((active=="active"&&sub=="exited")||
         (active=="failed")||
         (active=="inactive"&&sub=="dead")){
        return fmt.Errorf("shell unit still active or state uncertain: %s/%s",active,sub)
    }
    if err:=hostexec.CommandContext(ctx,"systemctl","stop",job.Unit).Run();err!=nil {
        return fmt.Errorf("shell unit stop failed: %w",err)
    }
    if err:=hostexec.CommandContext(ctx,"systemctl","reset-failed",job.Unit).Run();err!=nil {
        return fmt.Errorf("shell unit reset-failed failed: %w",err)
    }
    load,active,sub,err=inspect()
    if err!=nil{return err}
    if load!="not-found"{
        return fmt.Errorf("shell unit not yet unloaded (%s/%s/%s)",load,active,sub)
    }
    return nil
}

func classifyShellUnit(raw string) (TerminalStatus, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if fields["LoadState"] != "loaded" {
		return TerminalStatus{}, fmt.Errorf("shell unit unavailable (LoadState=%q): operator reconciliation required", fields["LoadState"])
	}
	active, sub, result := fields["ActiveState"], fields["SubState"], fields["Result"]
	var exitCode *int
	if rawCode := fields["ExecMainStatus"]; rawCode != "" {
		parsed, err := strconv.Atoi(rawCode)
		if err != nil {
			return TerminalStatus{}, fmt.Errorf("invalid systemd exit code %q", rawCode)
		}
		exitCode = &parsed
	}
	switch active {
	case "activating", "reloading", "deactivating":
		return TerminalStatus{State: "running"}, nil
	case "active":
		if sub != "exited" {
			return TerminalStatus{State: "running"}, nil
		}
		if result == "success" && fields["ExecMainCode"] == "1" &&
			exitCode != nil && *exitCode == 0 {
			return TerminalStatus{State: "done", ExitCode: exitCode}, nil
		}
		return TerminalStatus{}, errors.New("shell unit exited without trustworthy success status: operator reconciliation required")
	case "failed":
		if result == "" || result == "success" {
			return TerminalStatus{}, errors.New("failed shell unit without a trustworthy failure result")
		}
		return TerminalStatus{State: "failed", Detail: "systemd result: " + result, ExitCode: exitCode}, nil
	case "inactive":
		// Type=oneshot with RemainAfterExit=yes retains successful units in
		// active/exited until persisted; inactive may mean external stop.
		return TerminalStatus{}, errors.New("inactive shell unit without durable terminal proof: operator reconciliation required")
	default:
		return TerminalStatus{}, fmt.Errorf("unknown shell unit ActiveState %q: operator reconciliation required", active)
	}
}

func (SystemdRunner) DetailedStatus(ctx context.Context, job Job) (TerminalStatus, error) {
	out, err := hostexec.CommandContext(ctx, "systemctl", "show", job.Unit,
		"--no-pager", "--property=LoadState,ActiveState,SubState,Result,ExecMainCode,ExecMainStatus").CombinedOutput()
	if err != nil {
		return TerminalStatus{}, fmt.Errorf("systemctl show shell unit: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return classifyShellUnit(string(out))
}

func (SystemdRunner) CollectFinished(ctx context.Context, job Job) {
	// Called only after SQLite commits a trustworthy terminal result.
	// Cleanup is best effort: losing this status must not rewrite the
	// durable terminal checkpoint or silently rerun a command.
	_ = hostexec.CommandContext(ctx, "systemctl", "stop", job.Unit).Run()
	_ = hostexec.CommandContext(ctx, "systemctl", "reset-failed", job.Unit).Run()
}

func (SystemdRunner) Start(ctx context.Context, spec sandbox.Spec) (Job, error) {
	args, err := sandbox.BuildSystemdRunArgs(spec)
	if err != nil {
		return Job{}, err
	}
	out, err := hostexec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return Job{}, fmt.Errorf("systemd-run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return Job{
		ID: spec.Unit, Unit: spec.Unit, State: "running",
		Deadline: time.Now().Add(spec.Runtime),
	}, nil
}

func (SystemdRunner) Status(ctx context.Context, job Job) (string, error) {
	out, err := hostexec.CommandContext(ctx, "systemctl", "show", job.Unit, "--property=ActiveState", "--value").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("systemctl show: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (SystemdRunner) Tail(ctx context.Context, job Job, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	if lines > 1000 {
		lines = 1000
	}
	out, err := hostexec.CommandContext(ctx, "journalctl", "-u", job.Unit, "-n", fmt.Sprint(lines), "--no-pager").CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}

func (SystemdRunner) Cancel(ctx context.Context, job Job) error {
	if job.Unit == "" {
		return errors.New("unit is required")
	}
	out, err := hostexec.CommandContext(ctx, "systemctl", "stop", job.Unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl stop: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
