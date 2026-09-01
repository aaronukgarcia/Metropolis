package compose

import (
	"bytes"
	"testing"
)

// FEAT-1972079941 AC-6 — independent destructive-round regression tests
// (GR#23) hardening the per-module save round-trip claim. These strengthen
// TestSaveRoundTrip_PerModuleStateIsByteIdentical (which proves loaded ==
// original) with two independent properties the original test alone does
// not assert:
//
//  1. Per-module TEETH: each round-tripped module's saved stream genuinely
//     reflects DRIVEN state — a fresh, undriven composition's stream
//     differs from the driven one — so the equality check is not passing
//     because every stream is trivially the post-Wire default. (Traffic is
//     the documented exception: it holds no persistent at-rest state — its
//     day-boundary reset leaves an empty save stream even after a full run
//     — so its round-trip is empty->empty. Recorded, not asserted-teeth.)
//  2. Every StateDigest input round-trips (FEAT-1972079943 closed the former
//     gap): the module-owned inputs (citizens/finance/refuse/crime) via their
//     participants, and compose's own conservation/liveness ledgers via the
//     compose ledger participant + the derived-mirror recompute in Load — so
//     the FULL StateDigest is byte-identical after a Save/Load.

// scratchTeethStreams drains each participant Source into a stable []byte
// keyed by shard Kind (identical shape to participantStreams — kept local
// so this file stands alone).
func scratchTeethStreams(t *testing.T, comp *Composition) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for _, p := range comp.Participants() {
		src := p.Source()
		var buf bytes.Buffer
		for {
			rec, ok, err := src()
			if err != nil {
				t.Fatalf("participant %q Source error: %v", p.Kind(), err)
			}
			if !ok {
				break
			}
			buf.WriteString(rec.Kind)
			buf.WriteByte(0)
			buf.Write(rec.Data)
			buf.WriteByte('\n')
		}
		out[p.Kind()] = buf.Bytes()
	}
	return out
}

// TestSaveRoundTrip_PerModuleHasTeeth proves, per module, that the saved
// stream is sensitive to driven state (driven != fresh) AND that a loaded
// composition reproduces the driven stream (loaded == driven). Together
// with the byte-equality assertion in the sibling test, this closes the
// "all streams are trivially equal defaults" escape.
func TestSaveRoundTrip_PerModuleHasTeeth(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	driven := scratchTeethStreams(t, compA)

	_, compFresh := buildComposition(t)
	fresh := scratchTeethStreams(t, compFresh)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := scratchTeethStreams(t, compB)

	// Modules that MUST be driven to non-trivial state by driveMultiDomain,
	// so a fresh composition's stream must differ from the driven one. Since
	// FEAT-1972079943 this includes the compose-owned ledger participant
	// ("compose"): moneyFlows / peopleDelta / vital* etc. all move off zero
	// under the driven sequence.
	teethModules := []string{"world", "citizens", "finance", "build", "refuse", "unlocks", "compose"}
	for _, kind := range teethModules {
		if bytes.Equal(driven[kind], fresh[kind]) {
			t.Errorf("module %q: driven stream is byte-identical to a FRESH composition — the round-trip has no teeth (state never moved off default)", kind)
		}
		if !bytes.Equal(driven[kind], loaded[kind]) {
			t.Errorf("module %q: loaded stream did NOT reproduce the driven stream (reload not faithful)", kind)
		}
	}

	// Every remaining participant MUST at least round-trip faithfully
	// (loaded == driven), whether or not the driven sequence moves it off
	// default. Traffic holds no persistent at-rest state (documented empty
	// stream). Crime is driven only indirectly (via the attract hook's
	// safetyTerm) and may or may not move off its post-Wire default within
	// the short driven window, so its teeth are recorded, not asserted.
	for _, kind := range []string{"traffic", "crime"} {
		if !bytes.Equal(driven[kind], loaded[kind]) {
			t.Errorf("module %q: loaded stream did NOT reproduce the driven stream", kind)
		}
		if bytes.Equal(driven[kind], fresh[kind]) {
			t.Logf("NOTE: module %q driven stream equals a FRESH composition (no at-rest state moved) — round-trips empty->empty / default->default", kind)
		} else {
			t.Logf("NOTE: module %q moved off default under the driven sequence (has teeth)", kind)
		}
	}
}

// TestSaveRoundTrip_EveryDigestInputRoundTrips proves, field by field, that
// EVERY input StateDigest hashes now survives Save→Load — the module-owned
// inputs (citizens/finance/refuse/crime observables) via their participants,
// and the composition root's OWN conservation/liveness ledgers via the compose
// ledger participant + the derived-mirror recompute in Load. This is the
// field-level companion to the headline digest-equality test: if the full
// digest ever regresses, this localises the culprit to a concrete field
// rather than reporting only "the 32 bytes differ".
func TestSaveRoundTrip_EveryDigestInputRoundTrips(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	sa, sb := compA.state, compB.state

	// Module-owned digest inputs — restored by their participants.
	if sa.citizens.PopulationHash(sa.cid) != sb.citizens.PopulationHash(sb.cid) {
		t.Error("citizens PopulationHash (a participant-owned digest input) did NOT round-trip")
	}
	for _, acct := range digestFinanceAccounts {
		if ledgerBalance(sa.finance, acct) != ledgerBalance(sb.finance, acct) {
			t.Errorf("finance account %v (participant-owned digest input) did NOT round-trip: A=%d B=%d", acct, ledgerBalance(sa.finance, acct), ledgerBalance(sb.finance, acct))
		}
	}
	if sa.finance.TaxRevenue() != sb.finance.TaxRevenue() ||
		sa.finance.WagesPosted() != sb.finance.WagesPosted() ||
		sa.finance.MoneyStock() != sb.finance.MoneyStock() {
		t.Error("finance aggregate digest inputs did NOT round-trip")
	}
	if sa.refuse.Contamination() != sb.refuse.Contamination() {
		t.Error("refuse Contamination (a participant-owned digest input) did NOT round-trip")
	}
	// Crime observables — the 8th participant, added by FEAT-1972079943.
	if sa.crime.ThreatLevel() != sb.crime.ThreatLevel() {
		t.Errorf("crime ThreatLevel (a participant-owned digest input) did NOT round-trip: A=%f B=%f", sa.crime.ThreatLevel(), sb.crime.ThreatLevel())
	}

	// Compose-own ledgers — now restored (durable fields serialized by the
	// compose ledger participant; treasury/citizenWealth recomputed from the
	// finance ledger in Load). Every one MUST match after load.
	if sa.treasury != sb.treasury {
		t.Errorf("compose treasury did NOT round-trip: A=%d B=%d", sa.treasury, sb.treasury)
	}
	if sa.citizenWealth != sb.citizenWealth {
		t.Errorf("compose citizenWealth did NOT round-trip: A=%d B=%d", sa.citizenWealth, sb.citizenWealth)
	}
	if sa.moneyFlows != sb.moneyFlows {
		t.Errorf("compose moneyFlows did NOT round-trip: A=%d B=%d", sa.moneyFlows, sb.moneyFlows)
	}
	if sa.netMigration != sb.netMigration {
		t.Errorf("compose netMigration did NOT round-trip: A=%d B=%d", sa.netMigration, sb.netMigration)
	}
	if sa.consumptionDelivered != sb.consumptionDelivered {
		t.Errorf("compose consumptionDelivered did NOT round-trip: A=%f B=%f", sa.consumptionDelivered, sb.consumptionDelivered)
	}
	if sa.vitalBirths != sb.vitalBirths || sa.vitalDeaths != sb.vitalDeaths {
		t.Errorf("compose vital counts did NOT round-trip: births A=%d B=%d deaths A=%d B=%d", sa.vitalBirths, sb.vitalBirths, sa.vitalDeaths, sb.vitalDeaths)
	}
	if sa.peopleOpening != sb.peopleOpening || sa.peopleDelta != sb.peopleDelta {
		t.Errorf("compose people ledger did NOT round-trip: opening A=%d B=%d delta A=%d B=%d", sa.peopleOpening, sb.peopleOpening, sa.peopleDelta, sb.peopleDelta)
	}
	if sa.moneyOpening != sb.moneyOpening || sa.moneyDelta != sb.moneyDelta {
		t.Errorf("compose money ledger did NOT round-trip: opening A=%d B=%d delta A=%d B=%d", sa.moneyOpening, sb.moneyOpening, sa.moneyDelta, sb.moneyDelta)
	}
	// The BUG-288 once-per-tick ledger-closing trackers (previousClosingPop,
	// previousClosingMoney, lastClosedTick) are DELIBERATELY NOT serialized:
	// they are clock-relative and not digest-hashed, and restoring them onto a
	// tick-0 loaded engine would freeze closeLedgerForTick (FEAT-1972079944 /
	// FEAT-1972079936 inc3 own the clock-continuation). This is a STATE
	// snapshot, so they are not asserted here.

	// Sanity: the run genuinely moved the compose ledgers off default, so the
	// matches above are not vacuous (a fresh-vs-fresh comparison).
	if sa.moneyFlows == 0 && sa.peopleDelta == 0 && sa.moneyOpening == 0 {
		t.Error("compose ledgers are all zero after a driven run — the round-trip match may be vacuous; strengthen driveMultiDomain")
	}

	// Therefore the FULL digest matches.
	if compA.StateDigest() != compB.StateDigest() {
		t.Error("expected full StateDigest to round-trip (every hashed input restored), but it diverged")
	}
}
