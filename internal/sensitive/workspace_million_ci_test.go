//go:build linux

package sensitive

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Opt-in isolated stress test. Creates 1,000,000 ZERO-BYTE files in an
// ephemeral runner to measure the real streaming secret masker. Never run on
// an operator workspace, production /opt, or a mounted dataset directory.
func TestInfinityMillionOrdinaryWorkspaceEntriesStreaming(t *testing.T) {
	if os.Getenv("PORTICO_CI_MILLION_ENTRY_FIXTURE") != "synthetic-only" {
		t.Skip("requires explicitly opted-in ephemeral synthetic stress fixture")
	}
	if os.Getenv("CI") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("million-entry fixture is restricted to GitHub Actions CI")
	}
	base := t.TempDir()
	if filepath.IsAbs(os.Getenv("VPS_AGENT_PHYSICAL_SCOPE_ROOT")) {
		t.Fatal("refusing fixture when a host physical scope is configured")
	}
	project := filepath.Join(base, "opt", "project")
	sibling := filepath.Join(base, "opt", "sibling")
	for _, p := range []string{project, sibling} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const shards, perShard = 100, 10000
	start := time.Now()
	for shard := 0; shard < shards; shard++ {
		dir := filepath.Join(project, fmt.Sprintf("shard-%03d", shard))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < perShard; n++ {
			name := fmt.Sprintf("part-%04d", n)
			entryFD, openErr := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0600)
			if openErr != nil {
				_ = unix.Close(fd)
				t.Fatalf("shard=%d entry=%d: %v", shard, n, openErr)
			}
			if err := unix.Close(entryFD); err != nil {
				_ = unix.Close(fd)
				t.Fatal(err)
			}
		}
		if err := unix.Close(fd); err != nil {
			t.Fatal(err)
		}
	}
	createElapsed := time.Since(start)
	secret := filepath.Join(project, "shard-037", ".env")
	hardlink := filepath.Join(project, "shard-080", "model-index")
	if err := os.WriteFile(secret, []byte("disposable-synthetic-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, ".env"), []byte("unrelated-synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env.sample"), []byte("allowed-template"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	start = time.Now()
	masks, err := ProtectedAliasesStreaming(base, []string{"/opt/project"})
	scanElapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(masks)
	expected := []string{
		"/opt/project/shard-037/.env",
		"/opt/project/shard-080/model-index",
	}
	if !slices.Equal(masks, expected) {
		t.Fatalf("expected only real protected paths, got: %v", masks)
	}
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	t.Logf("INFINITY_SCALE_MILLION_ENTRIES: PASS count=%d create=%s scan=%s masks=%d heap_live_before_mib=%d heap_live_after_mib=%d heap_sys_mib=%d",
		shards*perShard, createElapsed.Round(time.Millisecond), scanElapsed.Round(time.Millisecond), len(masks),
		memBefore.Alloc/(1<<20), memAfter.Alloc/(1<<20), memAfter.HeapSys/(1<<20))
}
