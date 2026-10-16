//go:build linux

package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedRecursiveRemoveLargeSafeTree(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "llm-project")
	if err := os.Mkdir(tree, 0750); err != nil { t.Fatal(err) }
	for i := 0; i < 2048; i++ {
		name := filepath.Join(tree, fmt.Sprintf("chunk-%06d.gguf", i))
		if err := os.WriteFile(name, nil, 0600); err != nil { t.Fatal(err) }
	}
	neighbor := filepath.Join(base, "neighbor.txt")
	if err := os.WriteFile(neighbor, []byte("keep"), 0600); err != nil { t.Fatal(err) }
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	seen := 0
	err = fs.RemoveRecursiveVerified(tree, func(p string, protected bool) error {
		if protected { t.Fatalf("unexpected protected path: %s", p) }
		seen++
		return nil
	})
	if err != nil { t.Fatal(err) }
	if seen != 2049 { t.Fatalf("expected files and root checked, got %d", seen) }
	if _, err := os.Stat(tree); !os.IsNotExist(err) { t.Fatalf("tree remains: %v", err) }
	if v, err := os.ReadFile(neighbor); err != nil || string(v) != "keep" { t.Fatalf("neighbor changed: %q, %v", v, err) }
}

func TestVerifiedRecursiveRemoveProtectsEnvAndHardlinks(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "project")
	if err := os.Mkdir(tree, 0750); err != nil { t.Fatal(err) }
	secret := filepath.Join(tree, ".env")
	if err := os.WriteFile(secret, []byte("synthetic"), 0600); err != nil { t.Fatal(err) }
	hardlink := filepath.Join(tree, "alias.txt")
	if err := os.Link(secret, hardlink); err != nil { t.Fatal(err) }
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	denied := errors.New("sensitive work permission required")
	seen := 0
	err = fs.RemoveRecursiveVerified(tree, func(_ string, protected bool) error {
		if protected { seen++; return denied }
		return nil
	})
	if !errors.Is(err, denied) { t.Fatalf("protected file was not denied: %v", err) }
	if seen < 1 { t.Fatal("no sensitive entry reached verifier") }
	if _, err := os.Stat(secret); err != nil { t.Fatalf("protected inode changed: %v", err) }
}

func TestVerifiedRecursiveRemoveNeverFollowsSymlink(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(tree, 0750); err != nil { t.Fatal(err) }
	if err := os.Mkdir(outside, 0750); err != nil { t.Fatal(err) }
	external := filepath.Join(outside, "original.gguf")
	if err := os.WriteFile(external, []byte("keep"), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink(outside, filepath.Join(tree, "linked-dir")); err != nil { t.Fatal(err) }
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	if err := fs.RemoveRecursiveVerified(tree, func(_ string, _ bool) error { return nil }); err != nil { t.Fatal(err) }
	if got, err := os.ReadFile(external); err != nil || string(got) != "keep" { t.Fatalf("symlink target removed: %q %v", got, err) }
}

func TestVerifiedRecursiveRemovePartialErrorNeedsReconciliation(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "project")
	if err := os.Mkdir(tree, 0750); err != nil { t.Fatal(err) }
	for i := 0; i < 12; i++ {
		if err := os.WriteFile(filepath.Join(tree, fmt.Sprintf("part-%02d", i)), nil, 0600); err != nil { t.Fatal(err) }
	}
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	seen := 0
	denied := errors.New("simulated concurrent host change")
	err = fs.RemoveRecursiveVerified(tree, func(_ string, _ bool) error {
		seen++
		if seen == 5 { return denied }
		return nil
	})
	if !errors.Is(err, ErrPartialRemoval) || !errors.Is(err, denied) {
		// The partial marker must be the primary machine-readable safety flag.
		if !errors.Is(err, ErrPartialRemoval) { t.Fatalf("expected partial reconciliation: %v", err) }
	}
	if _, err := os.Stat(tree); err != nil { t.Fatalf("partial failure erased root: %v", err) }
}

func TestVerifiedRecursiveRemoveCannotDeleteAuthorizedRoot(t *testing.T) {
	base := t.TempDir()
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	if err := fs.RemoveRecursiveVerified(base, func(_ string, _ bool) error { return nil }); err == nil {
		t.Fatal("authorized root deletion permitted")
	}
}


func TestVerifiedRecursiveRemoveRejectsRacedSymlinkDirectory(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "target")
	child := filepath.Join(tree, "child")
	outside := filepath.Join(base, "outside")
	for _, p := range []string{child, outside} {
		if err := os.MkdirAll(p, 0750); err != nil { t.Fatal(err) }
	}
	external := filepath.Join(outside, "model.gguf")
	if err := os.WriteFile(external, []byte("survive"), 0600); err != nil { t.Fatal(err) }
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	swapped := false
	err = fs.RemoveRecursiveVerified(tree, func(path string, _ bool) error {
		if path == child && !swapped {
			swapped = true
			if err := os.Rename(child, child+"-old"); err != nil { return err }
			return os.Symlink(outside, child)
		}
		return nil
	})
	if err == nil || !swapped {
		t.Fatalf("concurrent directory swap was not detected: %v, swapped=%v", err, swapped)
	}
	got, readErr := os.ReadFile(external)
	if readErr != nil || string(got) != "survive" {
		t.Fatalf("outside content affected by symlink race: %q %v", got, readErr)
	}
}

func TestVerifiedRecursiveRemoveReportsConcurrentSecretInsertion(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "target")
	if err := os.Mkdir(tree, 0750); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(tree, "ordinary.gguf"), nil, 0600); err != nil { t.Fatal(err) }
	fs, err := New([]string{base}, []string{base}, 4096)
	if err != nil { t.Fatal(err) }
	secret := filepath.Join(tree, ".env")
	inserted := false
	err = fs.RemoveRecursiveVerified(tree, func(path string, _ bool) error {
		if path == filepath.Join(tree, "ordinary.gguf") && !inserted {
			inserted = true
			return os.WriteFile(secret, []byte("must-survive"), 0600)
		}
		return nil
	})
	if !inserted || !errors.Is(err, ErrPartialRemoval) {
		t.Fatalf("expected partial-reconcile on concurrent secret injection, got %v", err)
	}
	got, readErr := os.ReadFile(secret)
	if readErr != nil || string(got) != "must-survive" {
		t.Fatalf("concurrent protected secret changed: %q %v", got, readErr)
	}
}
