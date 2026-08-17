package rail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// RailAPI is the stub intermodal-transfer surface (see doc.go). It implements
// freight.RailIntermodal — the consumer-driven seam feat.containerport defines
// — so the deep-sea terminal consumes the sea↔rail↔road transfer point's
// tonnes-conservation contract rather than keeping a parallel ledger.
//
// The zero value is not usable; construct via [NewRailAPI]. A *RailAPI is safe
// for concurrent use: every mutable field is guarded by mu, and checkNotCopied
// rejects a struct-copied value (SEC-020 family).
type RailAPI struct {
	mu            sync.RWMutex
	correlationID string

	// Per-mode tonnage ledgers for the intermodal transfer point: in is the
	// tonnes handed INTO the point at a mode's leg, out is the tonnes handed
	// OUT, dwell is any buffered-but-not-yet-dispatched tonnes (zero in this
	// stub, since transfers are instantaneous — the full MOD-060 build will
	// add real buffer timing). Conservation: total in == total out + total
	// dwell.
	in    map[freight.Mode]int64
	out   map[freight.Mode]int64
	dwell map[freight.Mode]int64

	// modalCaps is the per-mode per-movement max tonnage read from
	// data/freight.json (road 25 t / rail 1,000 t / sea 40,000 t);
	// modalMinCaps is the per-mode per-movement minimum (sea's 3,000 t
	// coaster floor; zero for road/rail — SEC-125). The intermodal transfer
	// point enforces the SAME caps engine.freight loads (GR#3 — no second
	// driftable copy), so a transfer can never exceed the smaller of its
	// source and destination modes' per-movement maxes, nor fall below the
	// larger of their per-movement minimums.
	modalCaps    map[freight.Mode]int64
	modalMinCaps map[freight.Mode]int64

	self atomic.Pointer[RailAPI]
}

// Compile-time proof the stub satisfies feat.containerport's seam (GR#20 —
// engine.rail consumes engine.freight's contract via dependency inversion).
var _ freight.RailIntermodal = (*RailAPI)(nil)

// NewRailAPI returns a ready *RailAPI with empty ledgers and the road/rail/sea
// per-movement modal caps (max AND min) loaded from data/freight.json
// (enforced on every intermodal transfer — AC-3/AC-13). correlationID is
// attached to every error the returned surface constructs (GR#1). It fails
// with ErrRailDataInvalid when the modal caps cannot be read — never a
// caps-less surface that would silently accept an over-cap or below-min
// transfer.
func NewRailAPI(correlationID string) (*RailAPI, error) {
	maxCaps, minCaps, err := loadModalCaps(correlationID)
	if err != nil {
		return nil, err
	}
	r := &RailAPI{
		correlationID: correlationID,
		modalCaps:     maxCaps,
		modalMinCaps:  minCaps,
		in:            make(map[freight.Mode]int64),
		out:           make(map[freight.Mode]int64),
		dwell:         make(map[freight.Mode]int64),
	}
	r.self.Store(r)
	return r, nil
}

// modalCapsFile is data/freight.json's filename, relative to the resolved data
// directory (see data.ResolveDataDir) — the same file engine.freight loads, so
// the intermodal point enforces the SAME modal caps rather than a parallel,
// driftable copy (GR#3/GR#15).
const modalCapsFile = "freight.json"

// rawModalCaps is just the modalCaps slice of data/freight.json's wire shape,
// decoded only to read each mode's max AND min tonnage per movement (the min
// is sea's 3kt coaster floor; absent/zero for road/rail — SEC-125).
type rawModalCaps struct {
	ModalCaps map[string]struct {
		MinTonnesPerMovement int64 `json:"minTonnesPerMovement"`
		MaxTonnesPerMovement int64 `json:"maxTonnesPerMovement"`
	} `json:"modalCaps"`
}

// loadModalCaps reads the road/rail/sea per-movement max and min tonnage from
// data/freight.json, mirroring engine.freight's modal-cap validation so the
// stub cannot drift to a second, looser copy of the caps (GR#3 — SEC-125).
// Every failure is a registry-sourced *errs.E (GR#7) — the rail stub never
// falls back to a caps-less or defaulted-cap surface.
func loadModalCaps(correlationID string) (map[freight.Mode]int64, map[freight.Mode]int64, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, modalCapsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, errs.Wrap(ErrRailDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawModalCaps
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, errs.Wrap(ErrRailDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	maxCaps := make(map[freight.Mode]int64, 3)
	minCaps := make(map[freight.Mode]int64, 3)
	for _, m := range []freight.Mode{freight.ModeRoad, freight.ModeRail, freight.ModeSea} {
		rc, ok := raw.ModalCaps[string(m)]
		if !ok || rc.MaxTonnesPerMovement <= 0 {
			return nil, nil, errs.New(ErrRailDataInvalid, correlationID, map[string]any{
				"path":  path,
				"field": "modalCaps." + string(m),
				"rule":  "required maxTonnesPerMovement > 0",
			})
		}
		if rc.MinTonnesPerMovement < 0 {
			return nil, nil, errs.New(ErrRailDataInvalid, correlationID, map[string]any{
				"path":  path,
				"field": "modalCaps." + string(m) + ".minTonnesPerMovement",
				"rule":  "must be >= 0",
			})
		}
		if rc.MinTonnesPerMovement > rc.MaxTonnesPerMovement {
			return nil, nil, errs.New(ErrRailDataInvalid, correlationID, map[string]any{
				"path":  path,
				"field": "modalCaps." + string(m) + ".minTonnesPerMovement",
				"rule":  "must be <= maxTonnesPerMovement",
			})
		}
		maxCaps[m] = rc.MaxTonnesPerMovement
		minCaps[m] = rc.MinTonnesPerMovement
	}
	return maxCaps, minCaps, nil
}

// checkNotCopied rejects a method call on a struct-copied *RailAPI (SEC-020
// family). Lock-free — a single atomic.Pointer.Load.
func (r *RailAPI) checkNotCopied(method string) error {
	if r.self.Load() != r {
		return errs.New(ErrRailCopiedValue, r.correlationID, map[string]any{"method": method})
	}
	return nil
}

// IntermodalTransfer moves tonnes through the intermodal transfer point from
// one mode to another, conserving tonnes: the tonnes handed in at `from` are
// recorded and an equal tonnage is handed out at `to` (in == out + dwell,
// engine.rail.md AC-3). A non-positive tonnage, an unknown mode, a from==to
// no-op, a tonnage exceeding the intermodal modal cap (the smaller of the
// source and destination modes' per-movement max from data/freight.json), or
// a tonnage below a mode's per-movement minimum (sea's 3kt coaster floor —
// SEC-125) is rejected with ErrRailTransferRejected — never silently ignored
// or clamped. The same applies to saturation (GR#16): if recording the
// tonnage would saturate either ledger, the transfer is rejected with no
// partial update, so the in == out + dwell identity survives every transfer
// (Accepted and Delivered are always equal, and Dwell is always 0).
func (r *RailAPI) IntermodalTransfer(from, to freight.Mode, tonnes int64) (freight.IntermodalTransferResult, error) {
	if err := r.checkNotCopied("IntermodalTransfer"); err != nil {
		return freight.IntermodalTransferResult{}, err
	}
	if tonnes <= 0 {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"tonnes": tonnes,
			"reason": "tonnes must be positive",
		})
	}
	if !knownMode(from) || !knownMode(to) {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"reason": "unknown transport mode",
		})
	}
	if from == to {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"reason": "from and to modes must differ",
		})
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Modal cap (engine.rail.md AC-3 + engine.freight.md AC-13): one handoff
	// must fit one movement of each mode it touches, so the tonnage may not
	// exceed the smaller of the source and destination per-movement maxes,
	// nor fall below the larger of their per-movement minimums (sea's 3kt
	// coaster floor — SEC-125). Over-cap and below-min tonnage are both
	// rejected outright (AC-13's "reject, don't clamp" rule), never silently
	// truncated to the cap.
	if cap := r.modalCapFor(from, to); tonnes > cap {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"tonnes": tonnes,
			"max":    cap,
			"reason": "tonnes exceeds the intermodal modal cap",
		})
	}
	if min := r.modalMinFor(from, to); tonnes < min {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"tonnes": tonnes,
			"min":    min,
			"reason": "tonnes below the intermodal modal minimum",
		})
	}

	// Record the transfer only when BOTH ledgers can take the full tonnage
	// exactly (GR#16): an intermodal handoff conserves tonnes (in == out +
	// dwell), so a ledger that would saturate would leave accepted != delivered
	// with nowhere to dwell the gap (AC-4). A transfer that would saturate
	// EITHER ledger is rejected with ErrRailTransferRejected and no partial
	// update — the in and out ledgers move together or not at all (GR#3,
	// atomic, never one-then-the-other).
	oldIn := r.in[from]
	newIn, satIn := num.SatAddChecked(oldIn, tonnes)
	oldOut := r.out[to]
	newOut, satOut := num.SatAddChecked(oldOut, tonnes)
	if satIn || satOut {
		return freight.IntermodalTransferResult{}, errs.New(ErrRailTransferRejected, r.correlationID, map[string]any{
			"from":   string(from),
			"to":     string(to),
			"tonnes": tonnes,
			"reason": "transfer would saturate an intermodal ledger (in or out) — rejected with no partial update",
		})
	}
	r.in[from] = newIn
	r.out[to] = newOut
	accepted := newIn - oldIn
	delivered := newOut - oldOut

	return freight.IntermodalTransferResult{Accepted: accepted, Delivered: delivered, Dwell: 0}, nil
}

// modalCapFor returns the intermodal modal cap for a from→to handoff: the
// smaller of the two modes' per-movement max tonnage. Both modes are already
// knownMode-validated by the caller, so both cap entries exist.
func (r *RailAPI) modalCapFor(from, to freight.Mode) int64 {
	cap := r.modalCaps[from]
	if c := r.modalCaps[to]; c < cap {
		cap = c
	}
	return cap
}

// modalMinFor returns the intermodal modal minimum for a from→to handoff: the
// LARGER of the two modes' per-movement minimum tonnage (both legs must meet
// their mode's minimum, so the binding minimum is the higher of the two —
// sea's 3,000 t coaster floor; zero for road/rail). A handoff is rejected when
// its tonnage is below this floor (SEC-125). Both modes are already
// knownMode-validated by the caller, so both entries exist.
func (r *RailAPI) modalMinFor(from, to freight.Mode) int64 {
	min := r.modalMinCaps[from]
	if m := r.modalMinCaps[to]; m > min {
		min = m
	}
	return min
}

// IntermodalAccount returns the queryable per-mode in/out/dwell conservation
// account (independently summable — engine.rail.md AC-3's false-pass guard).
// Each map is a defensive copy; mutating it never affects rail state.
func (r *RailAPI) IntermodalAccount() freight.IntermodalAccount {
	if err := r.checkNotCopied("IntermodalAccount"); err != nil {
		return freight.IntermodalAccount{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return freight.IntermodalAccount{
		InTonnes:    copyModeMap(r.in),
		OutTonnes:   copyModeMap(r.out),
		DwellTonnes: copyModeMap(r.dwell),
	}
}

// knownMode reports whether m is one of the three documented transport modes.
func knownMode(m freight.Mode) bool {
	switch m {
	case freight.ModeSea, freight.ModeRail, freight.ModeRoad:
		return true
	}
	return false
}

func copyModeMap(m map[freight.Mode]int64) map[freight.Mode]int64 {
	out := make(map[freight.Mode]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
