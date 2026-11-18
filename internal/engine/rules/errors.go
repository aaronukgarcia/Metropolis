package rules

// Registry error codes for engine.rules (FEAT-9995385612). Range
// G5610-G5619, claimed with tools/plan/add-error.js claim-range. Every
// code is registered in data/errors.json. The package only names them; the
// restore, save and host code that detects each condition constructs them.
const (
	// ErrSavedUnderNewerRules: a snapshot or journal frame carries a rule
	// version above Current. Refused before any state is applied; never
	// clamped; no walk-back to an older snapshot.
	ErrSavedUnderNewerRules = "MET-G5610"

	// ErrSavedUnderRetiredRules: a snapshot or journal frame is older than
	// OldestSupported.
	ErrSavedUnderRetiredRules = "MET-G5611"

	// ErrRuleVersionWentBackwards: journal frames whose versions decrease.
	// Fails the restore closed; does not walk back to an older snapshot.
	ErrRuleVersionWentBackwards = "MET-G5612"

	// ErrMalformedRuleStamp: a journal envelope or Meta stamp that is not a
	// positive integer, has no command, or is 0 (version 0 is never stamped).
	ErrMalformedRuleStamp = "MET-G5613"

	// WarnRestoredUnderOlderRules: WARN, logged not returned. A restore
	// replayed history below Current; the message is built from the
	// registry's Old and New text.
	WarnRestoredUnderOlderRules = "MET-G5614"

	// ErrUnknownRuleVersion: a version asked of the registry that has no
	// entry (Entry, Between).
	ErrUnknownRuleVersion = "MET-G5615"
)
