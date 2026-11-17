//go:build windows

package compose

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// BUG-799 r3: the takeover lock is an OS advisory byte-range lock on a lock
// file that is created once and never deleted by the protocol. On Windows
// this is LockFileEx, exclusive and fail-immediately, over byte 0 (one byte,
// which may lie beyond EOF). The lock belongs to the file HANDLE, so two
// opens in one process conflict exactly like two processes, and the kernel
// releases it when the handle closes or the process dies -- a crashed holder
// can never leave a lock behind.

// tryLockFile takes the exclusive lock without blocking. held=false with a
// nil error means another handle holds it.
func tryLockFile(f *os.File) (held bool, err error) {
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// unlockFile releases the lock taken by tryLockFile.
func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
