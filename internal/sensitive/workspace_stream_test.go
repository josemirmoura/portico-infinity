package sensitive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A single representative 50k+ fixture proves the old arbitrary scan budget
// does not prevent legitimate shells on LLM/dataset workspaces.
func TestWorkspaceStreamingBeyond50000AndMasksHardlinks(t *testing.T) {
	host := t.TempDir()
	base := filepath.Join(host, "opt")
	workspace := filepath.Join(base, "project")
	sibling := filepath.Join(base, "unrelated")
	for _, p := range []string{workspace, sibling} {
		if err := os.MkdirAll(p, 0700); err != nil { t.Fatal(err) }
	}
	for i := 0; i < 50033; i++ {
		p := filepath.Join(workspace, fmt.Sprintf("model-part-%06d", i))
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil { t.Fatal(err) }
		if err := f.Close(); err != nil { t.Fatal(err) }
	}
	secret := filepath.Join(workspace, ".env")
	alias := filepath.Join(workspace, "model-index")
	if err := os.WriteFile(secret, []byte("synthetic"), 0600); err != nil { t.Fatal(err) }
	if err := os.Link(secret, alias); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(sibling, ".env"), []byte("synthetic sibling"), 0600); err != nil { t.Fatal(err) }

	// Preserve legacy behavior for the Community.
	_, err := ProtectedAliases(host, []string{"/opt/project"}, DefaultScanLimit)
	if !errors.Is(err, ErrScanLimit) { t.Fatalf("legacy scanner must retain its bounded behavior: %v", err) }

	got, err := ProtectedAliasesStreaming(host, []string{"/opt/project"})
	if err != nil { t.Fatal(err) }
	slices.Sort(got)
	want := []string{"/opt/project/.env", "/opt/project/model-index"}
	if !slices.Equal(got, want) { t.Fatalf("unexpected masks: got %v, want %v", got, want) }
}

func TestWorkspaceStreamingFailsClosedOnMaskBudgetAndSymlink(t *testing.T) {
	host := t.TempDir()
	ws := filepath.Join(host, "opt", "project")
	if err := os.MkdirAll(ws, 0700); err != nil { t.Fatal(err) }
	for i := 0; i <= MaxWorkspaceMasks; i++ {
		p := filepath.Join(ws, fmt.Sprintf(".env.%06d", i))
		if err := os.WriteFile(p, nil, 0600); err != nil { t.Fatal(err) }
	}
	if _, err := ProtectedAliasesStreaming(host, []string{"/opt/project"}); !errors.Is(err, ErrScanLimit) {
		t.Fatalf("excessive masks must fail closed: %v", err)
	}
	if err := os.Symlink(ws, filepath.Join(host, "opt", "link")); err != nil { t.Fatal(err) }
	if _, err := ProtectedAliasesStreaming(host, []string{"/opt/link"}); err == nil {
		t.Fatal("symlink workspace root must be rejected")
	}
	for _, root := range []string{"/", "/opt/../etc", "opt/project"} {
		if _, err := ProtectedAliasesStreaming(host, []string{root}); err == nil {
			t.Fatalf("invalid workspace %q accepted", root)
		}
	}
}
