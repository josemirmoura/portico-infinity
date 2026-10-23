package securefs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBinaryStreamAtomicSHAAndProgress(t *testing.T) {
	root := t.TempDir()
	m, err := New([]string{root}, []string{root}, DefaultMaxBytes)
	if err != nil { t.Fatal(err) }
	src := filepath.Join(root, "model.gguf")
	dst := filepath.Join(root, "model-copy.gguf")
	content := bytes.Repeat([]byte("synthetic-gguf-chunk"), 200000)
	if err := os.WriteFile(src, content, 0640); err != nil { t.Fatal(err) }
	wantHash := sha256.Sum256(content)
	want := hex.EncodeToString(wantHash[:])
	var last int64
	hash, err := m.CopyFileStream(context.Background(), src, dst, 1<<30, want, func(done, total int64) error {
		if done < last || total != int64(len(content)) { t.Errorf("bad copy progress %d/%d", done, total) }
		last = done
		return nil
	})
	if err != nil { t.Fatal(err) }
	if hash != want || last != int64(len(content)) { t.Fatalf("hash/progress mismatch: %s %d", hash, last) }
	copied, err := os.ReadFile(dst)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(copied, content) { t.Fatal("copied bytes differ") }
	info, err := os.Stat(dst)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0640 { t.Fatalf("mode not preserved: %o", info.Mode().Perm()) }
}

func TestBinaryStreamDenyOverwriteMismatchAndCancel(t *testing.T) {
	root := t.TempDir()
	m, err := New([]string{root}, []string{root}, DefaultMaxBytes)
	if err != nil { t.Fatal(err) }
	src, dst := filepath.Join(root, "source.gguf"), filepath.Join(root, "output.gguf")
	if err := os.WriteFile(src, bytes.Repeat([]byte("a"), 3<<20), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(dst, []byte("original"), 0600); err != nil { t.Fatal(err) }
	if _, err := m.CopyFileStream(context.Background(), src, dst, 1<<30, "", nil); err == nil {
		t.Fatal("overwrite was unexpectedly allowed")
	}
	original, err := os.ReadFile(dst)
	if err != nil || string(original) != "original" { t.Fatal("existing destination modified") }

	if err := os.Remove(dst); err != nil { t.Fatal(err) }
	if _, err := m.CopyFileStream(context.Background(), src, dst, 1<<30, hex.EncodeToString(bytes.Repeat([]byte{0}, 32)), nil); err == nil {
		t.Fatal("incorrect SHA-256 must fail")
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) { t.Fatal("mismatch published destination") }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := m.CopyFileStream(ctx, src, dst, 1<<30, "", func(done, _ int64) error {
		if done >= 1<<20 { cancel() }
		return nil
	}); !errors.Is(err, context.Canceled) { t.Fatalf("cancel must stop copy: %v", err) }
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) { t.Fatal("cancel published destination") }
	if _, err := m.CopyFileStream(context.Background(), src, dst, 1, "", nil); err == nil {
		t.Fatal("configured quota was bypassed")
	}
	link := filepath.Join(root, "symlink.gguf")
	if err := os.Symlink(src, link); err != nil { t.Fatal(err) }
	if _, err := m.CopyFileStream(context.Background(), link, dst, 1<<30, "", nil); err == nil {
		t.Fatal("symlink source must be rejected")
	}
}


func TestBinaryStreamSourcePathSwapDuringFinalProgressNeverPublishes(t *testing.T) {
    for _, swap := range []string{"replacement", "symlink"} {
        t.Run(swap, func(t *testing.T) {
            root := t.TempDir()
            m, err := New([]string{root}, []string{root}, DefaultMaxBytes)
            if err != nil { t.Fatal(err) }
            src := filepath.Join(root, "source.gguf")
            retired := filepath.Join(root, "retired.gguf")
            dst := filepath.Join(root, "destination.gguf")
            // Three 1 MiB chunks, with path swap in the final progress hook.
            if err := os.WriteFile(src, bytes.Repeat([]byte("a"), 3<<20), 0600); err != nil {
                t.Fatal(err)
            }
            swapped := false
            _, err = m.CopyFileStream(context.Background(), src, dst, 10<<20, "",
                func(done, total int64) error {
                    if done != total { return nil }
                    swapped = true
                    if err := os.Rename(src, retired); err != nil { return err }
                    if swap == "symlink" { return os.Symlink(retired, src) }
                    return os.WriteFile(src, bytes.Repeat([]byte("b"), 3<<20), 0600)
                })
            if !swapped { t.Fatal("source path swap was never executed during copy") }
            if err == nil { t.Fatal("replaced source pathname unexpectedly published destination") }
            if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
                t.Fatalf("destination must not be published after source swap: %v", statErr)
            }
            temporary, err := filepath.Glob(filepath.Join(root, ".destination.gguf.binary-copy-*"))
            if err != nil { t.Fatal(err) }
            if len(temporary) != 0 { t.Fatalf("temporary copy files left behind: %v", temporary) }
            if _, err := os.Stat(retired); err != nil {
                t.Fatalf("original pinned source must be left untouched: %v", err)
            }
        })
    }
}


func TestBinaryStreamPreCommitFenceFailClosed(t *testing.T) {
    for _, tc := range []struct { name string; deny error }{
        {"revoked", context.Canceled},
        {"fence-unavailable", errors.New("operator fence unavailable")},
    } {
        t.Run(tc.name, func(t *testing.T) {
            root := t.TempDir()
            m, err := New([]string{root}, []string{root}, DefaultMaxBytes)
            if err != nil { t.Fatal(err) }
            src, dst := filepath.Join(root, "source.gguf"), filepath.Join(root, "published.gguf")
            if err := os.WriteFile(src, bytes.Repeat([]byte("z"), 2<<20), 0600); err != nil { t.Fatal(err) }
            progressReachedEnd := false
            fenceCalled := 0
            _, err = m.CopyFileStreamWithPreCommitFence(context.Background(), src, dst, 3<<20, "",
                func(done, total int64) error {
                    if done == total { progressReachedEnd = true }
                    return nil
                }, func() error {
                    fenceCalled++
                    if !progressReachedEnd { return errors.New("fence invoked before final copy chunk") }
                    return tc.deny
                })
            if !errors.Is(err, tc.deny) { t.Fatalf("pre-publication fence not enforced: %v", err) }
            if fenceCalled != 1 { t.Fatalf("fence call count = %d", fenceCalled) }
            if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
                t.Fatalf("destination published despite denied fence: %v", statErr)
            }
            temps, err := filepath.Glob(filepath.Join(root, ".published.gguf.binary-copy-*"))
            if err != nil { t.Fatal(err) }
            if len(temps) != 0 { t.Fatalf("leftover unpublished temp files: %v", temps) }
        })
    }
}

func TestBinaryStreamPreCommitFenceMustBeProvided(t *testing.T) {
    root := t.TempDir()
    m, err := New([]string{root}, []string{root}, DefaultMaxBytes)
    if err != nil { t.Fatal(err) }
    _, err = m.CopyFileStreamWithPreCommitFence(context.Background(),
        filepath.Join(root, "source"), filepath.Join(root, "dst"), 1<<20, "", nil, nil)
    if err == nil { t.Fatal("nil revocation fence accepted") }
}
