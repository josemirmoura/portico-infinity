package jobs

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "os/user"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
    "testing"
    "time"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/sandbox"
    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/securefs"
)

// The systemd smoke workflow runs with sudo on an expendable GitHub runner.
// It exercises the exact file.mkdir(for_shell) ownership semantics followed
// by a real confined non-root systemd shell, not just a mocked policy check.
func TestShellWorkspaceRealSystemdSmoke(t *testing.T) {
    if os.Getenv("VPS_AGENT_SYSTEMD_JOB_SMOKE") != "1" {
        t.Skip("requires disposable privileged systemd runner")
    }
    if os.Geteuid() != 0 { t.Skip("needs root to prepare non-root owner") }
    if _, err := exec.LookPath("systemd-run"); err != nil { t.Skip("systemd-run unavailable") }
    account, err := user.Lookup("vps-agent-exec")
    if err != nil { t.Skip("disposable CI user vps-agent-exec not configured") }
    uid, _ := strconv.Atoi(account.Uid)
    gid, _ := strconv.Atoi(account.Gid)
    if uid <= 0 { t.Fatal("executor must be non-root") }

    parent := t.TempDir()
    if err := os.Chmod(parent, 0755); err != nil { t.Fatal(err) }
    workspace := filepath.Join(parent, "new-workspace")
    manager, err := securefs.New([]string{parent}, []string{parent}, 1024)
    if err != nil { t.Fatal(err) }
    if err := manager.CreateShellWorkspace(workspace, uid, gid); err != nil { t.Fatal(err) }

    unit := fmt.Sprintf("vps-agent-ci-workspace-%d", os.Getpid())
    runner := SystemdRunner{}
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    job, err := runner.Start(ctx, sandbox.Spec{
        Unit: unit, User: account.Username, CWD: workspace,
        Command: "umask 077; printf 'CI_WORKSPACE_WRITE_OK\\n' > owner-marker.txt; id -u",
        ReadWritePaths: []string{workspace}, IsolateFilesystem: true,
        Runtime: 15*time.Second, MemoryMaxBytes: 64 << 20, TasksMax: 16,
        NetworkMode: "blocked",
    })
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() {
        _ = exec.Command("systemctl", "stop", unit).Run()
        _ = exec.Command("systemctl", "reset-failed", unit).Run()
    })
    deadline := time.Now().Add(12*time.Second)
    var terminal TerminalStatus
    for time.Now().Before(deadline) {
        terminal, err = runner.DetailedStatus(ctx, job)
        if err == nil && (terminal.State == "done" || terminal.State == "failed") { break }
        time.Sleep(100*time.Millisecond)
    }
    if err != nil || terminal.State != "done" || terminal.ExitCode == nil || *terminal.ExitCode != 0 {
        journal, _ := runner.Tail(ctx, job, 40)
        t.Fatalf("non-root workspace shell failed: state=%+v err=%v journal=%s", terminal, err, journal)
    }
    marker := filepath.Join(workspace, "owner-marker.txt")
    got, err := os.ReadFile(marker)
    if err != nil || strings.TrimSpace(string(got)) != "CI_WORKSPACE_WRITE_OK" {
        t.Fatalf("marker unreadable or corrupted: %q err=%v", got, err)
    }
    info, err := os.Stat(marker)
    if err != nil { t.Fatal(err) }
    if stat := info.Sys().(*syscall.Stat_t); int(stat.Uid) != uid {
        t.Fatalf("shell created file as UID %d, expected %d", stat.Uid, uid)
    }
}
