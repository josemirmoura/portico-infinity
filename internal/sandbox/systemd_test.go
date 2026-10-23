package sandbox

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildSystemdRunArgsBlockedNetwork(t *testing.T) {
	args, err := BuildSystemdRunArgs(Spec{
		Unit: "vps-agent-job-123", User: "vps-agent-exec",
		Command: "echo ok", CWD: "/srv/app",
		ReadOnlyPaths: []string{"/srv/app"}, ReadWritePaths: []string{"/srv/app"}, InaccessiblePaths: []string{"/etc"}, Runtime: time.Minute,
		NetworkMode: "blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--property=NoNewPrivileges=yes",
		"--property=ProtectSystem=strict",
		"--property=PrivateNetwork=yes",
		"--property=ReadWritePaths=/srv/app",
		"--property=InaccessiblePaths=/etc",
		"--property=ProtectProc=invisible",
		"--property=PrivateDevices=yes",
	}
	for _, w := range want {
		if !slices.Contains(args, w) {
			t.Errorf("missing %q in %v", w, args)
		}
	}
	got := strings.Join(args[len(args)-4:], " ")
	if got != "-- /usr/bin/sh -c echo ok" {
		t.Fatalf("unexpected command tail %q", got)
	}
}

func TestBuildSystemdRunArgsPreservesExitStatus(t *testing.T) {
    args, err := BuildSystemdRunArgs(Spec{
        Unit: "shell-terminal-proof", User: "vps-agent-exec", Command: "true",
        CWD: "/opt/workspace", Runtime: 9 * time.Second,
        NetworkMode: "blocked",
    })
    if err != nil { t.Fatal(err) }
    for _, expected := range []string{
        "--no-block",
        "--property=Type=oneshot",
        "--property=RemainAfterExit=yes",
        "--property=TimeoutStartSec=10",
    } {
        if !slices.Contains(args, expected) {
            t.Fatalf("missing %q from %v", expected, args)
        }
    }
    if slices.Contains(args, "--collect") {
        t.Fatal("collected transient units lose terminal exit evidence before broker reconciliation")
    }
}

func TestBuildSystemdRunArgsAllowlistFailsClosed(t *testing.T) {
	_, err := BuildSystemdRunArgs(Spec{
		Unit: "job", Command: "true", CWD: "/tmp", NetworkMode: "allowlist",
	})
	if err == nil {
		t.Fatal("network allowlist must fail closed until implemented")
	}
}


func TestBuildSystemdRunArgsFilesystemWhitelist(t *testing.T) {
	args, err := BuildSystemdRunArgs(Spec{
		Unit: "job-whitelist", User: "root", Command: "printf ok", CWD: "/opt/app",
		ReadOnlyPaths: []string{"/opt/app", "/srv/read"},
		ReadWritePaths: []string{"/opt/app"},
		IsolateFilesystem: true,
		Runtime: time.Minute, NetworkMode: "blocked",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--property=TemporaryFileSystem=/:ro",
		"--property=CapabilityBoundingSet=",
		"--property=BindReadOnlyPaths=/usr",
		"--property=BindPaths=/opt/app",
		"--property=BindReadOnlyPaths=/srv/read",
	}
	for _, w := range want {
		if !slices.Contains(args, w) {
			t.Errorf("missing %q in %v", w, args)
		}
	}
	if slices.Contains(args, "--property=BindReadOnlyPaths=/opt/app") {
		t.Fatal("writable path was also bound read-only")
	}
	if slices.Contains(args, "--property=PrivateTmp=yes") {
		t.Fatal("PrivateTmp would hide authorized /tmp roots in whitelist mode")
	}
}
