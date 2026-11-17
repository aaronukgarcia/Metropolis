//go:build !windows

package compose

import "os"

// openClaimForRead: POSIX unlink never conflicts with open readers.
func openClaimForRead(path string) (*os.File, error) { return os.Open(path) }
