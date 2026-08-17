package trade

import "fmt"

// Registry error codes for ui.screen.trade (FEAT-017). This package claims
// V100-V199 (see data/errors.json's "V100-V199" reserved-range entry) —
// the V error-registry layer is the second UI block opened by
// ui.screen.proj (V000-V099), and ui.screen.trade takes the next
// sub-range. Checked against the table AND `grep -rn "MET-V1" internal/
// cmd/` before claiming, per BUG-008's lesson that the reserved-range
// table alone is not always current — no prior MET-V1xx code existed.
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: an "f5.trade" patch failed to decode — invalid
	// JSON, an unrecognised schemaVersion, or an oversized wire payload.
	// Logged and dropped (SF-7/TRD-8); the screen's data keeps its
	// last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-V100"

	// ErrUnknownSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and since unsubscribed (SF-7's "delta for an unknown/stale
	// subscription is dropped and logged ... never applied or causing a
	// panic").
	ErrUnknownSubscription = "MET-V101"

	// ErrUnknownContract: a create/cancel action named a contract ID absent
	// from the current view (TRD-7's "never a silent rejection" — the
	// action is refused loudly rather than silently dropped).
	ErrUnknownContract = "MET-V103"

	// ErrUnknownCommodity: a set-buffer action named a commodity absent
	// from the current warehouse view (TRD-3 — refused loudly, never a
	// silently-created row).
	ErrUnknownCommodity = "MET-V104"
)

// ErrScreenCopied (MET-V102) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following ui.screen.
// map's/ui.screen.demo's/ui.screen.proj's precedent of keeping a
// copy-guard's own error constant local to copyguard.go rather than this
// file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V100's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-V100 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.demo's/ui.screen.proj's
// errPatchTooLarge/errUnsupportedSchemaVersion convention.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
