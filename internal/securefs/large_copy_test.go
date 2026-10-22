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
	hash, err := m.CopyFileStream(context.Background(), src, dst, 1<<30, want, func(done, total int64) {
		if done < last || total != int64(len(content)) { t.Errorf("bad copy progress %d/%d", done, total) }
		last = done
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
	if _, err := m.CopyFileStream(ctx, src, dst, 1<<30, "", func(done, _ int64) {
		if done >= 1<<20 { cancel() }
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
