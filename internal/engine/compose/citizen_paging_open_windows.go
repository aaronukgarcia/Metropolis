//go:build windows

package compose

import (
	"os"

	"golang.org/x/sys/windows"
)

// openClaimForRead opens path read-only with FILE_SHARE_DELETE added to the
// share mode Go's os.Open uses (READ|WRITE only). BUG-799 r4: a plain opener's
// diagnostic read of claim.pid held it open WITHOUT delete sharing, so a
// reclaimer's DeleteFile failed ERROR_SHARING_VIOLATION for as long as any
// opener was reading; with several openers spinning there was never a moment
// without a reader, the reclaimer exhausted its retries and refused, and the
// plain openers (who cannot steal) refused too: zero holders. With delete
// sharing a reader no longer blocks the remove (the file just goes
// delete-pending until the reader closes, which the existing ACCESS_DENIED
// retry already covers).
func openClaimForRead(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
