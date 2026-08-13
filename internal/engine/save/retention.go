package save

import "os"

// autosaveRetentionSlots is the §3 line-131 "rolling 10" retention
// depth for autosaves.
const autosaveRetentionSlots = 10

// pruneAutosaves removes every autosave bundle beyond the 10 most
// recent (by sequence number), called ONLY after a new autosave has
// already been promoted successfully (AC-4's ordering guarantee — see
// Autosave and writeBundle). A deletion failure on one stale entry does
// not stop the sweep of the others; the first error encountered (if
// any) is returned after every removable entry has been attempted, so a
// single locked/permission-denied old directory doesn't mask the fact
// that later ones were still cleaned up.
func (m *Manager) pruneAutosaves() error {
	seqs, err := listAutosaveSeqs(m.root)
	if err != nil {
		return err
	}
	if len(seqs) <= autosaveRetentionSlots {
		return nil
	}
	toRemove := seqs[:len(seqs)-autosaveRetentionSlots]
	var firstErr error
	for _, seq := range toRemove {
		if err := os.RemoveAll(autosaveDir(m.root, seq)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
