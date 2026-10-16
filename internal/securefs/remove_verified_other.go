//go:build !linux

package securefs

import "errors"

var ErrPartialRemoval = errors.New("recursive removal partially completed; operator reconciliation required")

// Fail closed on operating systems without openat2/STATX_MNT_ID.
func (m *Manager) RemoveRecursiveVerified(_ string, _ func(string, bool) error) error {
	return errors.New("verified recursive removal requires Linux openat2 and statx mount IDs")
}
