//go:build !linux

package sensitive

import (
	"errors"
	"os"
)

var errMountVerificationUnavailable = errors.New("recursive sensitive subtree operations require Linux statx mount-id verification")

func rejectMountBoundary(_, _ string) error {
	return errMountVerificationUnavailable
}

func rejectMountEntry(_ *os.File, _ string) error {
	return errMountVerificationUnavailable
}
