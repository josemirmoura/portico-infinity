//go:build linux

package sensitive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Only the privileged disposable CI container may run this integration test.
// A skipped mount capability is NOT recorded as an accepted security gate.
func TestDelete001SameDeviceBindMountDenied(t *testing.T) {
	if os.Getenv("PORTICO_BIND_MOUNT_REQUIRED") != "1" {
		t.Skip("run in disposable privileged Linux E2E with PORTICO_BIND_MOUNT_REQUIRED=1")
	}
	base := t.TempDir()
	tree := filepath.Join(base, "target")
	source := filepath.Join(base, "source")
	nested := filepath.Join(tree, "nested")
	for _, path := range []string{nested, source} {
		if err := os.MkdirAll(path, 0700); err != nil { t.Fatal(err) }
	}
	if err := os.WriteFile(filepath.Join(source, "must-survive.gguf"), []byte("untouched"), 0600); err != nil { t.Fatal(err) }
	if err := unix.Mount(source, nested, "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("privileged bind mount test is REQUIRED, never skip: %v", err)
	}
	defer func() {
		if err := unix.Unmount(nested, 0); err != nil { t.Errorf("unmount fixture: %v", err) }
	}()
	var parent, child unix.Stat_t
	if err := unix.Stat(source, &parent); err != nil { t.Fatal(err) }
	if err := unix.Stat(nested, &child); err != nil { t.Fatal(err) }
	if parent.Dev != child.Dev { t.Fatal("fixture is not a same-device bind mount") }
	err := VisitProtectedSubtree("", tree, func(_ string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "mount") {
		t.Fatalf("same-device bind mount was accepted during recursive scan: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(source, "must-survive.gguf"))
	if readErr != nil || string(data) != "untouched" {
		t.Fatalf("mount source was modified by preflight: %q, %v", data, readErr)
	}
}
