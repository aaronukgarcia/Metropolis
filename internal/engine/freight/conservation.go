package freight

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// ConservationAccount is one completed tick's mass-conservation accounting
// (AC-10): five — six with Imported — independently-sourced per-commodity
// terms, each read from its OWN ledger/accessor, never inferred as a
// remainder:
//
//   - Produced:            each chain stage's own output ledger (AC-5);
//   - ConsumedDownstream:  each chain stage's own input ledger (the next
//     stage's draw, AC-10's "downstream stage's own Draw" requirement);
//   - Exported:            the departure ledger's own tracked departures
//     (AC-9's export accessor);
//   - Imported:            the import ledger (AC-9's import accessor) —
//     outside AC-10's closed-chain identity, included so the COMPLETE
//     stock identity is checkable;
//   - StorageDelta:        closing − opening from each storage site's own
//     stock accessor (AC-6);
//   - InTransitDelta:      the net in-transit change (Ship departures minus
//     arrivals) from the movement ledger (AC-7).
//
// For a closed chain (Imported == 0 for every commodity), AC-10's identity
// holds exactly:
//
//	Produced == ConsumedDownstream + Exported + StorageDelta + InTransitDelta
//
// The COMPLETE identity (which also holds when imports occur) adds the
// Imported inflow:
//
//	Produced + Imported == ConsumedDownstream + Exported + StorageDelta + InTransitDelta
type ConservationAccount struct {
	Tick               int64
	Produced           map[Commodity]int64
	ConsumedDownstream map[Commodity]int64
	Exported           map[Commodity]int64
	Imported           map[Commodity]int64
	StorageDelta       map[Commodity]int64
	InTransitDelta     map[Commodity]int64
}

// ConservationAccount returns the most recently completed tick's accounting
// (captured at the tick boundary, before the ledgers reset). Each returned
// map is a defensive copy — mutating it never affects freight state.
func (f *FreightAPI) ConservationAccount() ConservationAccount {
	if err := f.checkNotCopied("ConservationAccount"); err != nil {
		return ConservationAccount{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return copyAccount(f.lastAccount)
}

// computeAccountLocked builds the account for the tick being closed, before
// the ledgers reset. The caller holds f.mu. StorageDelta is closing −
// opening, read from the site accessor (not inferred), over the union of
// the opening and closing commodity sets.
func (f *FreightAPI) computeAccountLocked() ConservationAccount {
	closing := f.totalStockLocked()
	a := ConservationAccount{
		Tick:               f.tick,
		Produced:           copyTonneMap(f.produced),
		ConsumedDownstream: copyTonneMap(f.consumed),
		Exported:           copyTonneMap(f.exported),
		Imported:           copyTonneMap(f.imported),
		StorageDelta:       make(map[Commodity]int64, len(closing)+len(f.storageOpening)),
		InTransitDelta:     copyTonneMap(f.inTransitDelta),
	}
	for c, cl := range closing {
		a.StorageDelta[c] = num.SatSub(cl, f.storageOpening[c])
	}
	for c, op := range f.storageOpening {
		if _, ok := closing[c]; !ok {
			a.StorageDelta[c] = num.SatSub(0, op)
		}
	}
	return a
}

// IsBalanced reports whether the account satisfies the COMPLETE conservation
// identity (Produced + Imported == ConsumedDownstream + Exported +
// StorageDelta + InTransitDelta) for every commodity in the supplied set
// (pass only the tonne-unit commodities to check the tonnes identity). Every
// term is read from its own map, never derived as a remainder.
func (a ConservationAccount) IsBalanced(commodities []Commodity) bool {
	for _, c := range commodities {
		lhs := num.SatAdd(a.Produced[c], a.Imported[c])
		rhs := num.SatAdd(a.ConsumedDownstream[c], a.Exported[c])
		rhs = num.SatAdd(rhs, a.StorageDelta[c])
		rhs = num.SatAdd(rhs, a.InTransitDelta[c])
		if lhs != rhs {
			return false
		}
	}
	return true
}

// Commodities returns the sorted union of every commodity appearing in any
// of the account's term maps (deterministic iteration, GR#21).
func (a ConservationAccount) Commodities() []Commodity {
	seen := make(map[Commodity]bool)
	for c := range a.Produced {
		seen[c] = true
	}
	for c := range a.ConsumedDownstream {
		seen[c] = true
	}
	for c := range a.Exported {
		seen[c] = true
	}
	for c := range a.Imported {
		seen[c] = true
	}
	for c := range a.StorageDelta {
		seen[c] = true
	}
	for c := range a.InTransitDelta {
		seen[c] = true
	}
	out := make([]Commodity, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// tonneCommodities returns the loaded commodities whose unit is "tonne" —
// the set AC-10's tonnes-conservation identity covers (power/fuel are
// excluded).
func (f *FreightAPI) tonneCommodities() []Commodity {
	out := make([]Commodity, 0)
	for c, cc := range f.cfg.commodities {
		if cc.Unit == UnitTonne {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// VerifyConservation checks the most recently completed tick's conservation
// identity for every tonne-unit commodity and returns the first violating
// commodity (or "" if balanced). It is the interim local check standing in
// for AC-11's engine.invariant registration (which is blocked — see doc.go);
// AC-11 will supersede this with the registered invariant once the
// code.json edge lands.
func (f *FreightAPI) VerifyConservation() Commodity {
	if err := f.checkNotCopied("VerifyConservation"); err != nil {
		return ""
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, c := range f.tonneCommodities() {
		acct := f.lastAccount
		lhs := num.SatAdd(acct.Produced[c], acct.Imported[c])
		rhs := num.SatAdd(acct.ConsumedDownstream[c], acct.Exported[c])
		rhs = num.SatAdd(rhs, acct.StorageDelta[c])
		rhs = num.SatAdd(rhs, acct.InTransitDelta[c])
		if lhs != rhs {
			return c
		}
	}
	return ""
}

// copyAccount returns a defensive copy of a's term maps.
func copyAccount(a ConservationAccount) ConservationAccount {
	a.Produced = copyTonneMap(a.Produced)
	a.ConsumedDownstream = copyTonneMap(a.ConsumedDownstream)
	a.Exported = copyTonneMap(a.Exported)
	a.Imported = copyTonneMap(a.Imported)
	a.StorageDelta = copyTonneMap(a.StorageDelta)
	a.InTransitDelta = copyTonneMap(a.InTransitDelta)
	return a
}

func copyTonneMap(m map[Commodity]int64) map[Commodity]int64 {
	out := make(map[Commodity]int64, len(m))
	for c, t := range m {
		out[c] = t
	}
	return out
}
