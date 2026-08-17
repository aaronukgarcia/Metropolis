package rail

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestIntermodalTonnesConservation is engine.rail.md AC-3's false-pass guard:
// a known tonnage moved through the transfer point must conserve tonnes, with
// in and out summed INDEPENDENTLY (never a conservation-OK flag).
func TestIntermodalTonnesConservation(t *testing.T) {
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}

	// No sea leg appears here: sea's 3kt minimum exceeds road's 25t and rail's
	// 1,000t per-movement maxes, so a conserving sea↔road/sea↔rail handoff is
	// unrepresentable (SEC-125) — the rail↔road pair is the valid surface.
	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road: %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeRoad, freight.ModeRail, 25); err != nil {
		t.Fatalf("road→rail: %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road: %v", err)
	}

	acct := r.IntermodalAccount()
	var inTotal, outTotal, dwellTotal int64
	for _, v := range acct.InTonnes {
		inTotal += v
	}
	for _, v := range acct.OutTonnes {
		outTotal += v
	}
	for _, v := range acct.DwellTonnes {
		dwellTotal += v
	}
	if inTotal != outTotal+dwellTotal {
		t.Fatalf("conservation violated: in %d != out %d + dwell %d", inTotal, outTotal, dwellTotal)
	}
	if inTotal != 75 {
		t.Fatalf("expected 75 tonnes through the transfer point, got in %d", inTotal)
	}
}

func TestIntermodalTransferRejected(t *testing.T) {
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}
	cases := []struct {
		name   string
		from   freight.Mode
		to     freight.Mode
		tonnes int64
	}{
		{"negative", freight.ModeSea, freight.ModeRail, -5},
		{"zero", freight.ModeSea, freight.ModeRail, 0},
		{"sameMode", freight.ModeRail, freight.ModeRail, 100},
		{"unknownFrom", "hovercraft", freight.ModeRail, 100},
		{"unknownTo", freight.ModeSea, "teleport", 100},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := r.IntermodalTransfer(c.from, c.to, c.tonnes)
			if !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
				t.Fatalf("IntermodalTransfer(%s,%s,%d): want ErrRailTransferRejected, got %v", c.from, c.to, c.tonnes, err)
			}
		})
	}
}

// TestIntermodalTransferModalCap is engine.rail.md AC-3 + engine.freight.md
// AC-13: the intermodal point enforces the road/rail/sea per-movement caps —
// exactly at a cap is accepted, one tonne over is rejected (never clamped).
// The enforceable cap boundary is road's 25t: rail's 1,000t cap is never the
// binding constraint of a valid handoff (rail↔road binds at road's 25t, and a
// rail↔sea handoff is unrepresentable — sea's 3kt minimum exceeds rail's cap,
// SEC-125). The sea minimum is covered by TestIntermodalTransferModalMin.
func TestIntermodalTransferModalCap(t *testing.T) {
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}

	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road 25 (at road cap): %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 26); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("rail→road 26 (over road cap): want ErrRailTransferRejected, got %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeRoad, freight.ModeRail, 25); err != nil {
		t.Fatalf("road→rail 25 (at road cap): %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeRoad, freight.ModeRail, 26); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("road→rail 26 (over road cap): want ErrRailTransferRejected, got %v", err)
	}
}

// TestIntermodalTransferModalMin is the SEC-125 regression: the intermodal
// transfer point must reject tonnage below a mode's minTonnesPerMovement
// (sea's 3,000 t coaster floor) — pre-fix, IntermodalTransfer(ModeSea,
// ModeRail, 1000) returned nil and recorded a physically-impossible
// below-coaster-floor sea leg while engine.freight rejected the same tonnage.
// The sea mode's minimum is positive; any transfer touching sea below 3,000 t
// is rejected with ErrRailTransferRejected, never silently accepted.
func TestIntermodalTransferModalMin(t *testing.T) {
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}

	// Below sea minimum on the FROM leg (the exact SEC-125 repro): 1000 is at
	// the rail cap, so only the sea minimum can reject it.
	if _, err := r.IntermodalTransfer(freight.ModeSea, freight.ModeRail, 1000); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("sea→rail 1000 (below sea min): want ErrRailTransferRejected, got %v", err)
	}
	// Below sea minimum on the TO leg.
	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeSea, 1000); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("rail→sea 1000 (below sea min): want ErrRailTransferRejected, got %v", err)
	}
	// A mode with no minimum (road/rail) is unaffected: rail→road 25 at the
	// road cap still succeeds.
	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road 25 (road min 0): %v", err)
	}
}

// TestIntermodalTransferMaxInt64Boundary is the Destructive-REJECTED AC-4
// probe, now fixed: MaxInt64 far exceeds every mode's per-movement cap, so the
// two transfers are rejected and the account stays untouched — no duplicated
// out[rail]=MaxInt64 AND out[road]=MaxInt64 for one MaxInt64 accepted.
func TestIntermodalTransferMaxInt64Boundary(t *testing.T) {
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}

	if _, err := r.IntermodalTransfer(freight.ModeSea, freight.ModeRail, math.MaxInt64); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("sea→rail MaxInt64: want ErrRailTransferRejected, got %v", err)
	}
	if _, err := r.IntermodalTransfer(freight.ModeSea, freight.ModeRoad, math.MaxInt64); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("sea→road MaxInt64: want ErrRailTransferRejected, got %v", err)
	}

	acct := r.IntermodalAccount()
	if len(acct.InTonnes) != 0 || len(acct.OutTonnes) != 0 || len(acct.DwellTonnes) != 0 {
		t.Fatalf("rejected MaxInt64 transfers mutated the account: %+v", acct)
	}
}

// TestIntermodalTransferRejectsSaturation is the Destructive-REJECTED
// conservation-at-saturation probe (AC-3 in == out + dwell, AC-4): a transfer
// that would saturate EITHER the in-ledger or the out-ledger is rejected with
// ErrRailTransferRejected and no partial update — never a half-recorded handoff
// whose accepted != delivered gap is neither dwelled nor rejected (that
// fabricates tonnes: the old code reported Accepted=1000, Delivered=0, Dwell=0
// against out[rail]=MaxInt64, breaking in == out + dwell). White-box — it seeds
// a ledger to math.MaxInt64 directly, since reaching it via capped transfers
// is infeasible. The rail↔road pair (25t, at road cap) is used so the transfer
// clears the max/min cap checks and actually reaches the saturation path —
// sea↔rail would now be rejected at the sea-minimum check first (SEC-125).
func TestIntermodalTransferRejectsSaturation(t *testing.T) {
	// Saturated OUT-ledger: out[road] = MaxInt64, transfer rail→road 25. Must
	// be rejected, mutating nothing.
	r, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}
	r.mu.Lock()
	r.out[freight.ModeRoad] = math.MaxInt64
	r.mu.Unlock()

	if _, err := r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("rail→road into saturated out-ledger: want ErrRailTransferRejected, got %v", err)
	}
	acct := r.IntermodalAccount()
	if acct.OutTonnes[freight.ModeRoad] != math.MaxInt64 {
		t.Fatalf("out-ledger mutated by a rejected transfer: %d", acct.OutTonnes[freight.ModeRoad])
	}
	if len(acct.InTonnes) != 0 {
		t.Fatalf("in-ledger mutated by a rejected transfer: %v", acct.InTonnes)
	}

	// Saturated IN-ledger.
	r2, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}
	r2.mu.Lock()
	r2.in[freight.ModeRail] = math.MaxInt64
	r2.mu.Unlock()

	if _, err := r2.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("rail→road from saturated in-ledger: want ErrRailTransferRejected, got %v", err)
	}
	acct2 := r2.IntermodalAccount()
	if acct2.InTonnes[freight.ModeRail] != math.MaxInt64 {
		t.Fatalf("in-ledger mutated by a rejected transfer: %d", acct2.InTonnes[freight.ModeRail])
	}
	if len(acct2.OutTonnes) != 0 {
		t.Fatalf("out-ledger mutated by a rejected transfer: %v", acct2.OutTonnes)
	}

	// Partial saturation: in-ledger at MaxInt64-5, +25 saturates by 20. The
	// verdict's "fabricates tonnes" case — must be rejected, not partially
	// applied.
	r3, err := NewRailAPI("rail-test")
	if err != nil {
		t.Fatalf("NewRailAPI: %v", err)
	}
	r3.mu.Lock()
	r3.in[freight.ModeRail] = math.MaxInt64 - 5
	r3.mu.Unlock()

	if _, err := r3.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); !errors.Is(err, &errs.E{Code: ErrRailTransferRejected}) {
		t.Fatalf("rail→road that would partially saturate in-ledger: want ErrRailTransferRejected, got %v", err)
	}
	acct3 := r3.IntermodalAccount()
	if acct3.InTonnes[freight.ModeRail] != math.MaxInt64-5 {
		t.Fatalf("in-ledger mutated by a rejected partial-saturation transfer: %d", acct3.InTonnes[freight.ModeRail])
	}
	if len(acct3.OutTonnes) != 0 {
		t.Fatalf("out-ledger mutated by a rejected partial-saturation transfer: %v", acct3.OutTonnes)
	}
}

func TestRailDeterminism(t *testing.T) {
	run := func() string {
		r, err := NewRailAPI("rail-test")
		if err != nil {
			t.Fatalf("NewRailAPI: %v", err)
		}
		_, _ = r.IntermodalTransfer(freight.ModeSea, freight.ModeRail, 1000)
		_, _ = r.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25)
		b, _ := json.Marshal(r.IntermodalAccount())
		return string(b)
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("determinism violated:\n%s\n--- vs ---\n%s", a, b)
	}
}
