//go:build unix

package compose

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// BUG-799 r3: the takeover lock is an OS advisory flock(2) lock on a lock
// file that is created once and never deleted by the protocol. flock locks
// belong to the open file description, so two separate opens (even in one
// process) conflict exactly like two processes, and the kernel releases the
// lock when the last descriptor closes or the process dies -- a crashed
// holder can never leave a lock behind.

// tryLockFile takes the exclusive lock without blocking. held=false with a
// nil error means another open file description holds it.
func tryLockFile(f *os.File) (held bool, err error) {
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EWOULDBLOCK) {
			return false, nil
		}
		return false, err
	}
}

// unlockFile releases the lock taken by tryLockFile.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
