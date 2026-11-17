//go:build unix

package compose

import (
	"errors"

	"golang.org/x/sys/unix"
)

// linkUnsupported reports whether an os.Link failure means the filesystem
// has no hard links at all (BUG-799 r5: publishClaim fails closed,
// MET-G824). link(2) answers EPERM on filesystems that do not support hard
// links (vfat, many FUSE mounts), EOPNOTSUPP/ENOTSUP or ENOSYS elsewhere.
// We always link our own freshly created regular file, so EPERM's other
// meaning (protected_hardlinks on a file we do not own) cannot arise here.
func linkUnsupported(err error) bool {
	return errors.Is(err, unix.EPERM) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS)
}
