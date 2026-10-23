package securefs

import (
    "os"
    "path/filepath"
    "strings"
    "syscall"
    "testing"
)

func TestCreateShellWorkspaceOneLeafPreservesExistingData(t *testing.T) {
    if os.Geteuid() == 0 { t.Skip("running as root is covered by broker privileged tests") }
    root := t.TempDir()
    m, err := New([]string{root}, []string{root}, 1024)
    if err != nil { t.Fatal(err) }
    existing := filepath.Join(root, "existing")
    if err := os.Mkdir(existing, 0700); err != nil { t.Fatal(err) }
    preserved := filepath.Join(existing, "keep.txt")
    if err := os.WriteFile(preserved, []byte("do-not-touch"), 0600); err != nil { t.Fatal(err) }
    metaBefore, err := os.Lstat(existing)
    if err != nil { t.Fatal(err) }
    target := filepath.Join(root, "workspace")
    if err := m.CreateShellWorkspace(target, os.Geteuid(), os.Getegid()); err != nil { t.Fatal(err) }
    info, err := os.Lstat(target)
    if err != nil || !info.IsDir() { t.Fatalf("workspace=%v err=%v", info, err) }
    stat := info.Sys().(*syscall.Stat_t)
    if int(stat.Uid) != os.Geteuid() || int(stat.Gid) != os.Getegid() || info.Mode().Perm() != 0750 {
        t.Fatalf("invalid workspace owner or mode: %+v %v", stat, info.Mode())
    }
    // Existing paths are not retargeted or repurposed.
    if err := m.CreateShellWorkspace(target, os.Geteuid(), os.Getegid()); err == nil {
        t.Fatal("existing workspace must not be silently changed")
    }
    if err := m.CreateShellWorkspace(existing, os.Geteuid(), os.Getegid()); err == nil {
        t.Fatal("existing tree must not be reused")
    }
    metaAfter, err := os.Lstat(existing)
    if err != nil { t.Fatal(err) }
    if !os.SameFile(metaBefore, metaAfter) || metaBefore.Mode() != metaAfter.Mode() {
        t.Fatal("preexisting directory changed")
    }
    bytes, err := os.ReadFile(preserved)
    if err != nil || string(bytes) != "do-not-touch" { t.Fatalf("existing file lost: %q %v", bytes, err) }
}

func TestCreateShellWorkspaceFailClosedPaths(t *testing.T) {
    if os.Geteuid() == 0 { t.Skip("non-root test") }
    root := t.TempDir()
    outside := t.TempDir()
    m, err := New([]string{root}, []string{root}, 1024)
    if err != nil { t.Fatal(err) }
    if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil { t.Fatal(err) }
    for name, path := range map[string]string{
        "root": root,
        "outside": filepath.Join(outside, "escape"),
        "traversal": root + "/../outside",
        "alias": filepath.Join(root, "alias", "sub"),
        "missing parent": filepath.Join(root, "missing", "new"),
    } {
        t.Run(name, func(t *testing.T) {
            if err := m.CreateShellWorkspace(path, os.Geteuid(), os.Getegid()); err == nil {
                t.Fatalf("unexpected workspace creation at %q", path)
            }
        })
    }
    if err := m.CreateShellWorkspace(filepath.Join(root, "root-user"), 0, 0); err == nil {
        t.Fatal("shell workspace must never be owned by root")
    }
    target := filepath.Join(root, "no-ghost")
    if err := m.CreateShellWorkspace(target, os.Geteuid(), -1); err == nil || !strings.Contains(err.Error(), "non-root") {
        t.Fatalf("invalid gid unexpectedly permitted: %v", err)
    }
}
