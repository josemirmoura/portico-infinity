package sensitive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVisitProtectedSubtreeBeyondGlobalScanLimit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "large")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DefaultScanLimit+8; i++ {
		name := filepath.Join(target, fmt.Sprintf("item-%05d", i))
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Global collection remains bounded, as do shell secret masks.
	if _, err := ProtectedAliases("", []string{root}, DefaultScanLimit); !errors.Is(err, ErrScanLimit) {
		t.Fatalf("expected legacy global scanner to fail closed, got %v", err)
	}
	visited := 0
	if err := VisitProtectedSubtree("", target, func(p string) error {
		visited++
		return nil
	}); err != nil {
		t.Fatalf("large unprotected directory must be scannable: %v", err)
	}
	if visited != 0 {
		t.Fatalf("unexpected protected paths: %d", visited)
	}
	secret := filepath.Join(target, ".env")
	if err := os.WriteFile(secret, []byte("synthetic-only"), 0600); err != nil {
		t.Fatal(err)
	}
	found := false
	if err := VisitProtectedSubtree("", target, func(p string) error {
		if p == secret {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("protected .env hidden in a large tree was missed")
	}
}

func TestVisitProtectedSubtreeSymlinkAndHardlink(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "tree")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(tree, 0700); err != nil { t.Fatal(err) }
	if err := os.Mkdir(outside, 0700); err != nil { t.Fatal(err) }
	outsideSecret := filepath.Join(outside, ".env")
	if err := os.WriteFile(outsideSecret, []byte("outside"), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink(outside, filepath.Join(tree, "link-to-outside")); err != nil { t.Fatal(err) }
	// The symlink is an unlinkable leaf; its referent must not be scanned.
	var paths []string
	if err := VisitProtectedSubtree("", tree, func(p string) error { paths = append(paths, p); return nil }); err != nil { t.Fatal(err) }
	if len(paths) != 0 { t.Fatalf("scanner followed a symlink: %v", paths) }
	alias := filepath.Join(tree, "ordinary.txt")
	if err := os.Link(outsideSecret, alias); err != nil { t.Fatal(err) }
	if err := VisitProtectedSubtree("", tree, func(p string) error {
		paths = append(paths, p)
		return nil
	}); err != nil { t.Fatal(err) }
	if len(paths) != 1 || paths[0] != alias {
		t.Fatalf("hardlink not classified as protected: %v", paths)
	}
}

func TestVisitProtectedSubtreeVisitorFailureAndUnverifiablePathsFailClosed(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	if err := os.Mkdir(tree, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(tree, ".env"), []byte("synthetic-only"), 0600); err != nil { t.Fatal(err) }
	denied := errors.New("no sensitive work approval")
	if err := VisitProtectedSubtree("", tree, func(string) error { return denied }); !errors.Is(err, denied) {
		t.Fatalf("visitor rejection must abort: %v", err)
	}
	for _, candidate := range []string{"tree", "/does-not-exist", root + "/../" + filepath.Base(root) + "/tree"} {
		if err := VisitProtectedSubtree("", candidate, func(string) error { return nil }); err == nil {
			t.Fatalf("invalid or unsafe target accepted: %q", candidate)
		}
	}
	missing := filepath.Join(root, "missing")
	if err := VisitProtectedSubtree("", missing, func(string) error { return nil }); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing tree must fail closed, got: %v", err)
	}
	if err := VisitProtectedSubtree("", tree, nil); err == nil || !strings.Contains(err.Error(), "visitor") {
		t.Fatalf("nil visitor accepted: %v", err)
	}
}


func TestVisitProtectedSubtreeRejectsProtectedSymlinkNameWithoutFollowingTarget(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "tree")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(target, 0700); err != nil { t.Fatal(err) }
	if err := os.Mkdir(outside, 0700); err != nil { t.Fatal(err) }
	external := filepath.Join(outside, "safe.txt")
	if err := os.WriteFile(external, []byte("must remain"), 0600); err != nil { t.Fatal(err) }
	link := filepath.Join(target, ".env")
	if err := os.Symlink(external, link); err != nil { t.Fatal(err) }

	var found []string
	err := VisitProtectedSubtree("", target, func(p string) error {
		found = append(found, p)
		return nil
	})
	if err != nil { t.Fatal(err) }
	if len(found) != 1 || found[0] != link {
		t.Fatalf("protected symlink path missed: %v", found)
	}
	b, err := os.ReadFile(external)
	if err != nil || string(b) != "must remain" {
		t.Fatalf("preflight followed or changed target: %q, %v", b, err)
	}
}
