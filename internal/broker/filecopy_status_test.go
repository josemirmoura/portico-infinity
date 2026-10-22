package broker

import (
    "errors"
    "testing"

    "github.com/josemirmoura/mcp-vps-agent-gateway/internal/filecopyworker"
)

func TestFilecopyCancellationHonorsDurableCheckpoint(t *testing.T) {
    inactiveSuccess := "LoadState=loaded\nActiveState=inactive\nSubState=dead\nResult=success\nExecMainStatus=0\n"
    failedExit := "LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\nExecMainStatus=1\n"
    activeRunning := "LoadState=loaded\nActiveState=activating\nSubState=start\nResult=success\n"
    goneUnit := "LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\nExecMainStatus=0\n"

    testcases := []struct {
        name string
        unit string
        phase string
        progressErr error
        want string
        err bool
    }{
        {"cancelled oneshot inactive success", inactiveSuccess, "cancelled", nil, "cancelled", false},
        {"cancelled oneshot nonzero exit", failedExit, "cancelled", nil, "cancelled", false},
        {"running still running despite checkpoint", activeRunning, "cancelled", nil, "running", false},
        {"genuine completed oneshot", inactiveSuccess, "done", nil, "done", false},
        {"failed worker remains failed", failedExit, "failed", nil, "failed", false},
        {"missing checkpoint cannot prove cancelled", inactiveSuccess, "cancelled", errors.New("missing"), "done", false},
        {"missing systemd unit is not cancelled", goneUnit, "cancelled", nil, "", true},
    }
    for _,tc:=range testcases {
        t.Run(tc.name,func(t *testing.T) {
            phase,_,_,err:=classifyFilecopyUnit(tc.unit,filecopyworker.Progress{Phase:tc.phase},tc.progressErr)
            if (err!=nil)!=tc.err || phase!=tc.want {
                t.Fatalf("got state=%q err=%v; want state=%q err=%v",phase,err,tc.want,tc.err)
            }
        })
    }
}
