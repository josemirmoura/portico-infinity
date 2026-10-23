package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/state"
)

func TestSystemdRunnerSmoke(t *testing.T) {
	if os.Getenv("VPS_AGENT_SYSTEMD_JOB_SMOKE") != "1" {
		t.Skip("set VPS_AGENT_SYSTEMD_JOB_SMOKE=1 to run privileged systemd job smoke")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("systemd-run unavailable")
	}

	unit := fmt.Sprintf("vps-agent-ci-job-%d", os.Getpid())
	r := SystemdRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	job, err := r.Start(ctx, sandbox.Spec{
		Unit: unit, User: "vps-agent-exec",
		Command: "echo transient-job-proof; sleep 20",
		CWD: "/tmp", Runtime: 25 * time.Second,
		MemoryMaxBytes: 64 << 20, TasksMax: 16,
		NetworkMode: "blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("systemctl", "stop", unit).Run()
		_ = exec.Command("systemctl", "reset-failed", unit).Run()
	})

	deadline := time.Now().Add(8 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		status, err = r.Status(ctx, job)
		if err == nil && (status == "active" || status == "activating") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != "active" && status != "activating" {
		t.Fatalf("transient unit did not become active: status=%q err=%v", status, err)
	}

	var logs string
	for time.Now().Before(deadline) {
		logs, _ = r.Tail(ctx, job, 50)
		if strings.Contains(logs, "transient-job-proof") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(logs, "transient-job-proof") {
		t.Fatalf("expected command output in journal, got %q", logs)
	}

	if err := r.Cancel(ctx, job); err != nil {
		t.Fatal(err)
	}
}

func TestShellSystemdTerminalStatusSmoke(t *testing.T) {
    if os.Getenv("VPS_AGENT_SYSTEMD_JOB_SMOKE") != "1" {
        t.Skip("requires privileged systemd smoke environment")
    }
    if _, err := exec.LookPath("systemd-run"); err != nil {
        t.Skip("systemd-run unavailable")
    }
    scenarios := []struct {
        name string
        cmd string
        cwd string
        state string
        exit int
    }{
        {name: "done", cmd: "printf shell-terminal-ok\\n", cwd: "/tmp", state: "done", exit: 0},
        {name: "exit17", cmd: "exit 17", cwd: "/tmp", state: "failed", exit: 17},
        {name: "chdir200", cmd: "true", cwd: t.TempDir(), state: "failed", exit: 200},
    }
    for i, tc := range scenarios {
        t.Run(tc.name, func(t *testing.T) {
            unit := fmt.Sprintf("vps-agent-ci-terminal-%d-%d", os.Getpid(), i)
            runner := SystemdRunner{}
            ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
            defer cancel()
            job, err := runner.Start(ctx, sandbox.Spec{
                Unit: unit, User: "vps-agent-exec", Command: tc.cmd,
                CWD: tc.cwd, Runtime: 10*time.Second,
                MemoryMaxBytes: 64 << 20, TasksMax: 16, NetworkMode: "blocked",
            })
            if err != nil { t.Fatal(err) }
            t.Cleanup(func() {
                _ = exec.Command("systemctl", "stop", unit).Run()
                _ = exec.Command("systemctl", "reset-failed", unit).Run()
            })
            var status TerminalStatus
            deadline := time.Now().Add(12*time.Second)
            for time.Now().Before(deadline) {
                status, err = runner.DetailedStatus(ctx, job)
                if err == nil && (status.State == "done" || status.State == "failed") {
                    break
                }
                time.Sleep(100*time.Millisecond)
            }
            if err != nil || status.State != tc.state || status.ExitCode == nil || *status.ExitCode != tc.exit {
                logs, _ := runner.Tail(ctx, job, 20)
                t.Fatalf("shell state=%+v err=%v want state=%s exit=%d; logs=%s",
                    status, err, tc.state, tc.exit, logs)
            }
        })
    }
}

func TestUnpolledShellReconcileRealSystemdSmoke(t *testing.T) {
    if os.Getenv("VPS_AGENT_SYSTEMD_JOB_SMOKE") != "1" {
        t.Skip("requires root-owned disposable systemd runner")
    }
    if _, err := exec.LookPath("systemd-run"); err != nil { t.Skip("systemd-run unavailable") }
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    runner := SystemdRunner{}
    unit := fmt.Sprintf("vps-agent-ci-janitor-%d", os.Getpid())
    job, err := runner.Start(ctx, sandbox.Spec{
        Unit:unit, User:"vps-agent-exec", Command:"true", CWD:"/tmp",
        Runtime:15*time.Second, NetworkMode:"blocked", MemoryMaxBytes:64<<20, TasksMax:16,
    })
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() {
        _ = exec.Command("systemctl", "stop", unit).Run()
        _ = exec.Command("systemctl", "reset-failed", unit).Run()
    })
    db, err := state.Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if err := db.CreateJob(ctx,state.JobRecord{
        ID:job.ID,Subject:"owner",Tool:"shell.exec",Resource:"/tmp",
        UnitName:unit,State:"running",CreatedAt:time.Now().Add(-time.Minute),
        Deadline:time.Now().Add(time.Minute),
    }); err != nil { t.Fatal(err) }
    m := &Manager{State:db, Runner:runner}
    var found bool
    deadline := time.Now().Add(10*time.Second)
    for time.Now().Before(deadline) {
        result, err := m.ReconcileShellJobs(ctx,64)
        if err != nil { t.Fatal(err) }
        if result.Completed > 0 { found = true; break }
        time.Sleep(100*time.Millisecond)
    }
    rec, err := db.GetJob(ctx,job.ID)
    if err != nil || !found || rec.State!="done" || rec.ExitCode==nil || *rec.ExitCode!=0 {
        logs,_ := runner.Tail(ctx,job,20)
        t.Fatalf("real systemd unpolled job state=%+v err=%v found=%v logs=%s",rec,err,found,logs)
    }
}
