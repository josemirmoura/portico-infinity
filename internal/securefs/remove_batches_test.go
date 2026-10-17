package securefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecursiveRemoveStreamsMultipleDirectoryBatches(t *testing.T) {
	base := t.TempDir()
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "large")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1531; i++ {
		path := filepath.Join(target, fmt.Sprintf("entry-%06d", i))
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		subdir := filepath.Join(target, fmt.Sprintf("nested-%d", i))
		if err := os.Mkdir(subdir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subdir, "leaf"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	neighbor := filepath.Join(base, "neighbor.txt")
	if err := os.WriteFile(neighbor, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(target, true); err != nil {
		t.Fatalf("batched removal failed: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target remains after recursive removal: %v", err)
	}
	if data, err := os.ReadFile(neighbor); err != nil || string(data) != "preserved" {
		t.Fatalf("unrelated sibling was modified: %q %v", data, err)
	}
}

func TestRecursiveRemoveReportsPartialFailureAndKeepsProtectedOutsideTarget(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test relies on unprivileged filesystem permissions")
	}
	base := t.TempDir()
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "tree")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	cannotOpen := filepath.Join(target, "no-access")
	if err := os.Mkdir(cannotOpen, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(cannotOpen, 0700)
	// The exact deletion order is FS-dependent. Irrespective of how many
	// siblings were removed, an error is mandatory and never signifies success.
	if err := m.Remove(target, true); err == nil ||
		(!strings.Contains(err.Error(), "permission") && !strings.Contains(err.Error(), "denied")) {
		t.Fatalf("expected an explicit deletion error, got %v", err)
	}
}


func TestRecursiveRemoveMoreThanFiftyThousandEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("large destructive filesystem acceptance")
	}
	base := t.TempDir()
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "large-tree")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	const entries = 50053
	for i := 0; i < entries; i++ {
		name := filepath.Join(target, fmt.Sprintf("entry-%06d", i))
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Remove(target, true); err != nil {
		t.Fatalf("cannot remove 50k+ entries: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("large tree still exists after successful return: %v", err)
	}
}

func TestRecursiveRemoveRejectsFileReplacementDuringAuthorization(t *testing.T) {
	base := t.TempDir()
	m, err := New([]string{base}, []string{base}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "project")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(target, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(base, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("new version"), 0600); err != nil {
		t.Fatal(err)
	}
	replaced := false
	err = m.RemoveWithEntryGuard(target, true, func(path string, info os.FileInfo) error {
		if path == victim {
			replaced = true
			if err := os.Rename(replacement, victim); err != nil {
				return err
			}
		}
		return nil
	})
	if !replaced || err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("changed inode was deleted without detection: replaced=%v err=%v", replaced, err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "new version" {
		t.Fatalf("replacement inode should remain intact: %q %v", data, err)
	}
}
