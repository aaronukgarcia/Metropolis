//go:build windows

package compose

import (
	"errors"

	"golang.org/x/sys/windows"
)

// linkUnsupported reports whether an os.Link failure means the volume has no
// hard links at all (BUG-799 r5: publishClaim fails closed, MET-G824).
// CreateHardLinkW answers ERROR_INVALID_FUNCTION on FAT/exFAT and
// ERROR_NOT_SUPPORTED on redirectors/filesystems without link support. Every
// other failure (ACCESS_DENIED on a delete-pending target, sharing
// violations) stays a transient, retried error.
func linkUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}
