package securefs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/josemirmoura/mcp-vps-agent-gateway/internal/sensitive"
	"golang.org/x/sys/unix"
)

// MaxBinaryCopyBytes is an upper safety bound, NOT a default permission.
// Production policy must set a lower quota for the eventual async worker.
const MaxBinaryCopyBytes int64 = 1 << 40 // 1 TiB

// CopyFileStream is the bounded-memory copying primitive for a future typed,
// durable binary-copy worker. Unlike CopyFile, it supports configurable sizes
// above 512 MiB. It is NOT registered as an MCP tool; do not use synchronously
// for multigigabyte files over a short-lived client connection.
//
// A caller must first independently authorize both paths, the limit and job.
// Paths with protected names/hardlinks are rejected by default. Source and
// parent handles are pinned with openat2 to avoid symlink/bind-mount escapes.
// Progress callbacks are best-effort and not a durable checkpoint.
// The destination is only published after SHA verification and fsync.
func (m *Manager) CopyFileStream(
	ctx context.Context, src, dst string, maxBytes int64,
	expectedSHA256 string, progress func(done, total int64),
) (string, error) {
	if ctx == nil { return "", errors.New("context is required") }
	if maxBytes <= 0 || maxBytes > MaxBinaryCopyBytes {
		return "", errors.New("binary copy quota is invalid")
	}
	if expectedSHA256 != "" {
		if len(expectedSHA256) != 64 { return "", errors.New("expected sha256 must have 64 hexadecimal characters") }
		if _, err := hex.DecodeString(expectedSHA256); err != nil { return "", fmt.Errorf("invalid sha256: %w", err) }
	}
	for _, p := range []string{src, dst} {
		for _, part := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
			if sensitive.ProtectedName(part) {
				return "", errors.New("binary copy refuses sensitive path without a typed sensitive-access gate")
			}
		}
	}
	srcRootPath, srcRel, err := selectRoot(m.readRoots, src)
	if err != nil { return "", err }
	dstRootPath, dstRel, err := selectRoot(m.writeRoots, dst)
	if err != nil { return "", err }
	if srcRel == "." || dstRel == "." { return "", errors.New("binary copy cannot target authorized root") }

	// Open the physical roots component by component without following links.
	srcRootFD, err := openBinaryDir(m.physical(srcRootPath))
	if err != nil { return "", err }
	defer unix.Close(srcRootFD)
	dstRootFD, err := openBinaryDir(m.physical(dstRootPath))
	if err != nil { return "", err }
	defer unix.Close(dstRootFD)
	resolve := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV)
	flags := uint64(unix.O_RDONLY | unix.O_CLOEXEC)
	fd, err := unix.Openat2(srcRootFD, srcRel, &unix.OpenHow{Flags: flags, Resolve: resolve})
	if err != nil { return "", fmt.Errorf("unsafe binary-copy source: %w", err) }
	in := os.NewFile(uintptr(fd), src)
	if in == nil { unix.Close(fd); return "", errors.New("cannot pin binary-copy source") }
	defer in.Close()
	before, err := in.Stat()
	if err != nil { return "", err }
	if !before.Mode().IsRegular() { return "", errors.New("binary-copy source must be a regular file") }
	if before.Size() > maxBytes { return "", fmt.Errorf("binary source exceeds configured quota %d", maxBytes) }
	var first unix.Stat_t
	if err := unix.Fstat(fd, &first); err != nil { return "", err }
	if first.Nlink > 1 { return "", errors.New("binary source has ambiguous hardlinks") }

	// Pin destination parent without traversing symlinks or bind mounts.
	dir := filepath.Dir(dstRel)
	parentFD, err := unix.Openat2(dstRootFD, dir,
		&unix.OpenHow{Flags: uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC), Resolve: resolve})
	if err != nil { return "", fmt.Errorf("unsafe binary-copy destination parent: %w", err) }
	defer unix.Close(parentFD)
	// A large copy is intentionally create-only: never replace an existing
	// model/checkpoint. In particular, do not clobber a concurrent writer.
	base := filepath.Base(dstRel)
	var existing unix.Stat_t
	if err := unix.Fstatat(parentFD, base, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		return "", errors.New("binary-copy destination already exists")
	} else if !errors.Is(err, unix.ENOENT) {
		return "", fmt.Errorf("cannot verify binary-copy destination: %w", err)
	}
	var parentStat unix.Stat_t
	if err := unix.Fstat(parentFD, &parentStat); err != nil { return "", err }
	uid, gid := int(parentStat.Uid), int(parentStat.Gid)

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil { return "", err }
	tmpBase := "." + base + ".binary-copy-" + hex.EncodeToString(nonce[:])
	outFD, err := unix.Openat(parentFD, tmpBase, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil { return "", err }
	out := os.NewFile(uintptr(outFD), tmpBase)
	if out == nil { unix.Close(outFD); return "", errors.New("cannot open temporary binary-copy destination") }
	published := false
	defer func() {
		_ = out.Close()
		if !published { _ = unix.Unlinkat(parentFD, tmpBase, 0) }
	}()

	h := sha256.New()
	buf := make([]byte, 1<<20)
	var done int64
	for done < before.Size() {
		if err := ctx.Err(); err != nil { return "", err }
		toRead := len(buf)
		if remaining := before.Size()-done; remaining < int64(toRead) { toRead = int(remaining) }
		n, err := io.ReadFull(in, buf[:toRead])
		if err != nil { return "", fmt.Errorf("source changed or truncated during binary copy: %w", err) }
		for written := 0; written < n; {
			amount, err := out.Write(buf[written:n])
			if err != nil { return "", err }
			if amount == 0 { return "", io.ErrShortWrite }
			written += amount
		}
		_, _ = h.Write(buf[:n])
		done += int64(n)
		if progress != nil { progress(done, before.Size()) }
	}
	if err := ctx.Err(); err != nil { return "", err }
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil { return "", err }
	if first.Dev != after.Dev || first.Ino != after.Ino || first.Size != after.Size ||
		first.Mtim != after.Mtim || first.Ctim != after.Ctim {
		return "", errors.New("binary-copy source changed during transfer; destination not published")
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if expectedSHA256 != "" && !strings.EqualFold(expectedSHA256, sum) {
		return "", errors.New("binary-copy SHA-256 mismatch; destination not published")
	}
	if err := out.Chown(uid, gid); err != nil { return "", err }
	if err := out.Chmod(before.Mode().Perm()); err != nil { return "", err }
	if err := out.Sync(); err != nil { return "", err }
	if err := out.Close(); err != nil { return "", err }
	// Both paths are within the same pinned parent directory descriptor.
	if err := unix.Renameat2(parentFD, tmpBase, parentFD, base, unix.RENAME_NOREPLACE); err != nil { return "", err }
	published = true
	// Persist the destination directory rename before reporting success.
	if err := unix.Fsync(parentFD); err != nil {
		return "", fmt.Errorf("binary destination renamed but directory sync failed; reconciliation required: %w", err)
	}
	return sum, nil
}

func openBinaryDir(path string) (int, error) {
	if !filepath.IsAbs(path) { return -1, errors.New("binary root must be absolute") }
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil { return -1, err }
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "" { continue }
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if err != nil { return -1, fmt.Errorf("symlink/unavailable binary-copy root: %w", err) }
		fd = next
	}
	return fd, nil
}
