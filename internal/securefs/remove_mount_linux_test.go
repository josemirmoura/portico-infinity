//go:build linux

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
	"golang.org/x/sys/unix"
)

// A bind mount can point to another directory on the same device, making a
// st_dev-only check insufficient. Simulate insertion AFTER sensitive preflight
// and BEFORE recursive deletion; the source must survive.
func TestRecursiveRemoveRejectsBindMountInsertedAfterPreflight(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "project")
	source := filepath.Join(base, "live-source")
	mountpoint := filepath.Join(target, "mounted")
	for _, p := range []string{mountpoint, source} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	srcFile := filepath.Join(source, "KEEP.txt")
	if err := os.WriteFile(srcFile, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sensitive.WalkProtectedSubtree("", target, []string{base}, func(string) error { return nil }); err != nil {
		t.Fatalf("initial protected-file preflight should pass: %v", err)
	}
	if err := unix.Mount(source, mountpoint, "", unix.MS_BIND, ""); err != nil {
		if errors.Is(err, unix.EPERM) && os.Getenv("PORTICO_REQUIRE_MOUNT_TEST") != "1" {
			t.Skipf("bind mounts unavailable in unprivileged test environment: %v", err)
		}
		t.Fatalf("cannot set up required bind-mount adversarial test: %v", err)
	}
	defer func() {
		if err := unix.Unmount(mountpoint, 0); err != nil {
			t.Errorf("cannot restore test bind mount: %v", err)
		}
	}()
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	err = m.Remove(target, true)
	if err == nil {
		t.Fatal("recursive deletion traversed a bind mount introduced after preflight")
	}
	if data, err := os.ReadFile(srcFile); err != nil || string(data) != "preserve me" {
		t.Fatalf("bind-mount source altered by recursive deletion: content=%q err=%v", data, err)
	}
}


func TestRecursiveRemoveRejectsBindMountInsertedDuringDeletion(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "project")
	mountpoint := filepath.Join(target, "subdir")
	source := filepath.Join(base, "source")
	for _, p := range []string{mountpoint, source} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	srcFile := filepath.Join(source, "KEEP.txt")
	if err := os.WriteFile(srcFile, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	var mounted bool
	defer func() {
		if mounted {
			if err := unix.Unmount(mountpoint, 0); err != nil {
				t.Errorf("cannot clean up test bind mount: %v", err)
			}
		}
	}()
	err = m.RemoveWithEntryGuard(target, true, func(path string, _ os.FileInfo) error {
		if path != mountpoint {
			return nil
		}
		injected = true
		if mountErr := unix.Mount(source, mountpoint, "", unix.MS_BIND, ""); mountErr != nil {
			if errors.Is(mountErr, unix.EPERM) && os.Getenv("PORTICO_REQUIRE_MOUNT_TEST") != "1" {
				t.Skipf("mount injection requires CAP_SYS_ADMIN: %v", mountErr)
			}
			return mountErr
		}
		mounted = true
		return nil
	})
	if !injected || !mounted || err == nil {
		t.Fatalf("mount inserted during deletion was not blocked: injected=%v mounted=%v err=%v", injected, mounted, err)
	}
	if data, readErr := os.ReadFile(srcFile); readErr != nil || string(data) != "preserve" {
		t.Fatalf("live source was damaged: %q %v", data, readErr)
	}
}
