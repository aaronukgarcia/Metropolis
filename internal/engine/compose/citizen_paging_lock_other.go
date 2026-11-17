//go:build !unix && !windows

package compose

import (
	"errors"
	"os"
)

// No advisory-lock primitive is wired for this GOOS: fail closed (the
// takeover refuses with a config error) rather than take an unguarded
// remove-then-create.
var errNoAdvisoryLock = errors.New("no OS advisory file lock available on this GOOS")

func tryLockFile(*os.File) (bool, error) { return false, errNoAdvisoryLock }

func unlockFile(*os.File) error { return errNoAdvisoryLock }
