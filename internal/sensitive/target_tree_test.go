package sensitive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSubtreeScanIgnoresUnrelatedSiblings(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "selected")
	sibling := filepath.Join(root, "sibling")
	for _, d := range []string{target, sibling} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, ".env"), []byte("do not inspect"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "extra"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProtectedAliases("", []string{root}, 2); !errors.Is(err, ErrScanLimit) {
		t.Fatalf("expected legacy whole-root scanner to exhaust limit, got %v", err)
	}
	calls := 0
	err := WalkProtectedSubtree("", target, []string{root}, func(string) error {
		calls++
		return nil
	})
	if err != nil || calls != 0 {
		t.Fatalf("targeted scan inspected siblings: calls=%d err=%v", calls, err)
	}
	if _, err := os.Stat(filepath.Join(sibling, ".env")); err != nil {
		t.Fatalf("scan must not mutate sibling: %v", err)
	}
}

func TestSubtreeScanHandlesMoreThanFiftyThousandEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("large filesystem regression")
	}
	root := t.TempDir()
	target := filepath.Join(root, "large")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50050; i++ {
		file := filepath.Join(target, fmt.Sprintf("file-%06d", i))
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(target, ".env")
	if err := os.WriteFile(secret, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	found := 0
	err := WalkProtectedSubtree("", target, []string{root}, func(path string) error {
		if path != secret {
			t.Errorf("unexpected classified path %q", path)
		}
		found++
		return nil
	})
	if err != nil || found != 1 {
		t.Fatalf("large tree scan failed: found=%d err=%v", found, err)
	}
	// Preflight must never perform a deletion.
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("preflight mutated secret: %v", err)
	}
}

func TestSubtreeScanProtectsNestedSecretAndHardlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target", "nested")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(target, ".env.production")
	if err := os.WriteFile(secret, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(target, "innocent.dat")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hardlinks not available: %v", err)
	}
	seen := make(map[string]bool)
	err := WalkProtectedSubtree("", filepath.Dir(target), []string{root}, func(path string) error {
		seen[path] = true
		return nil
	})
	if err != nil || !seen[secret] || !seen[link] {
		t.Fatalf("missed protected alias: %v, err=%v", seen, err)
	}
	deny := errors.New("protected path requires work approval")
	if err := WalkProtectedSubtree("", filepath.Dir(target), []string{root}, func(string) error { return deny }); !errors.Is(err, deny) {
		t.Fatalf("expected denial during preflight, got %v", err)
	}
}

func TestSubtreeScanRejectsTraversalSymlinksAndOutOfScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sub, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	visit := func(string) error { return nil }
	for _, target := range []string{
		filepath.Join(root, "out-of-scope"),
		filepath.Join(sub, "..", "..", "elsewhere"),
		filepath.Join(sub, "outside-link", "other"),
	} {
		// First candidate is allowed but absent; second traverses; third follows
		// a symlink. All must fail closed before the mutation phase.
		if err := WalkProtectedSubtree("", target, []string{root}, visit); err == nil {
			t.Fatalf("unsafe/missing target accepted: %q", target)
		}
	}
	if err := WalkProtectedSubtree("", sub, []string{outside}, visit); err == nil {
		t.Fatal("target outside grant accepted")
	}
}

func TestSubtreeScanHostRootMapping(t *testing.T) {
	host := t.TempDir()
	project := filepath.Join(host, "opt", "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var found string
	err := WalkProtectedSubtree(host, "/opt/project", []string{"/opt"}, func(path string) error {
		found = path
		return nil
	})
	if err != nil || found != "/opt/project/.env" {
		t.Fatalf("host mapping mismatch: path=%s err=%v", found, err)
	}
}
