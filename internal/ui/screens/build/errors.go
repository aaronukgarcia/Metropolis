package build

import "fmt"

// Registry error codes for ui.screen.build (FEAT-015). Range: V200-V299,
// claimed here per data/errors.json's "ranges.reserved" table — the third
// V-layer claim after ui.screen.proj (V000-V099) and ui.screen.trade
// (V100-V199). Checked against the table AND `grep -rn "MET-V2" internal/
// cmd/` before claiming, per BUG-008's lesson that the reserved-range
// table alone is not always current — no prior MET-V2xx code existed.
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7).
const (
	// ErrMalformedPatch: an "f3.build" patch failed to decode — invalid
	// JSON, an unrecognised schemaVersion, or an oversized wire payload.
	// Logged and dropped (SF-7/BLD-8); the screen's data keeps its
	// last-known-good state, never partially applied.
	ErrMalformedPatch = "MET-V200"

	// ErrUnknownSubscription: ApplyDelta was called with a protocol.Delta
	// whose SubscriptionID was never bound via BindSubscription, or was
	// bound and since unsubscribed (SF-7's "delta for an unknown/stale
	// subscription is dropped and logged ... never applied or causing a
	// panic").
	ErrUnknownSubscription = "MET-V201"

	// ErrUnknownZoneType: a ZonePaint action named a zone slug absent from
	// the current f3.build zones view (BLD-2 — refused loudly, never a
	// silently-dropped subset of the painted run).
	ErrUnknownZoneType = "MET-V203"

	// ErrUnknownStructure: a Demolish action named a cell the f3.build
	// demolition view does not currently report compensation for (BLD-4 —
	// the cost-showing step cannot be skipped: no reported cost, no
	// demolition command).
	ErrUnknownStructure = "MET-V204"

	// ErrUnknownBuilding: a BuildOn action named a building ID absent from
	// the current f3.build catalogue view (BLD-7 — refused loudly, never a
	// silently-created order).
	ErrUnknownBuilding = "MET-V205"
)

// ErrScreenCopied (MET-V202) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following ui.screen.
// map's/ui.screen.trade's/ui.screen.proj's precedent of keeping a
// copy-guard's own error constant local to copyguard.go rather than this
// file.

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch (wire.go) can report; both feed MET-V200's
// {cause} template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-V200 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.trade's convention.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
