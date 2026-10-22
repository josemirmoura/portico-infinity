// portico-filecopy-worker is an operator-installed standalone systemd worker.
// The operator must copy this verified binary into a root-owned host path;
// the Broker cannot execute arbitrary commands or modify that protected path.
package main

import (
    "context"
    "errors"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/filecopyworker"
)

func main() {
    if err := run(); err != nil {
        // No file contents, credentials or OAuth subject are printed.
        fmt.Fprintln(os.Stderr, "filecopy worker failed:", err)
        os.Exit(1)
    }
}

func run() error {
    if os.Geteuid() != 0 {
        return errors.New("filecopy worker must run from independent root-owned systemd unit")
    }
    if len(os.Args) != 2 || !filecopyworker.ValidID(os.Args[1]) {
        return errors.New("usage: portico-filecopy-worker <validated-job-id>")
    }
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()
    _, err := filecopyworker.Run(ctx, filecopyworker.StateDir, "", os.Args[1])
    return err
}
