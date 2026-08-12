package world

// Registry error codes for engine.world (MOD-017). Range: E400-E499,
// declared in data/errors.json's "ranges.reserved" table. Every code
// below IS registered there with real severity/module/message/remedy
// fields (GR#7) — see that file's "E400-E499" reserved-range entry and
// its "codes" section. Checked against data/errors.json AND
// `grep -rn "MET-E4" internal/ cmd/` before this range was claimed, per
// BUG-008's lesson (engine.core's errors.go doc comment) that the
// reserved table alone is not always current.
const (
	// ErrTerrainImportInvalid: ImportTerrain received a source tile that
	// failed checksum/format validation (AC-14). Never a silent
	// best-effort import.
	ErrTerrainImportInvalid = "MET-E400"

	// ErrAnchorOutOfExtent: a data/georef.json anchor coordinate resolved
	// outside the downloaded Terrain 50 tile's real extent (AC-13, AC-14).
	ErrAnchorOutOfExtent = "MET-E401"

	// ErrTileNotOwned: a mutation command targeted a tile the caller does
	// not own (AC-10, AC-15).
	ErrTileNotOwned = "MET-E402"

	// ErrTileOutOfBounds: a mutation command targeted a TileCoord outside
	// the 30x30 expansion extent (AC-15).
	ErrTileOutOfBounds = "MET-E403"

	// ErrPurchaseRejected: PurchaseTile was called against an
	// already-owned tile or an out-of-extent coordinate (AC-10).
	ErrPurchaseRejected = "MET-E404"

	// ErrGeologyNotProspected: a mining-relevant geology query ran
	// against a tile that has not been prospected yet (AC-7, §32).
	ErrGeologyNotProspected = "MET-E405"

	// ErrWorldCopied: a WorldAPI method, or an internal mu-guarded helper
	// (ensureTile, tilePrice), was called on a World value that is not
	// the one NewWorld constructed — i.e. a struct copy (BUG-064: `cp :=
	// *w` is legal, unsafe-free, reflect-free Go, and defeats mu's
	// per-instance safety because the copy gets its OWN mu but ALIASES
	// the original's tiles map, a reference type). Mirrors
	// engine.core's Engine.checkNotCopied/ErrEngineCopied exactly
	// (SEC-014/SEC-016 family) — see World.self's doc comment
	// (grid.go).
	ErrWorldCopied = "MET-E406"
)
