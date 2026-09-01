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
//  2. The StateDigest non-round-trip is HONEST: every digest input owned by
//     one of the seven participants (citizens/finance/refuse observables)
//     matches after load; only crime (not a participant) and compose's own
//     conservation ledgers (saved by no participant) diverge.

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
	// so a fresh composition's stream must differ from the driven one.
	teethModules := []string{"world", "citizens", "finance", "build", "refuse", "unlocks"}
	for _, kind := range teethModules {
		if bytes.Equal(driven[kind], fresh[kind]) {
			t.Errorf("module %q: driven stream is byte-identical to a FRESH composition — the round-trip has no teeth (state never moved off default)", kind)
		}
		if !bytes.Equal(driven[kind], loaded[kind]) {
			t.Errorf("module %q: loaded stream did NOT reproduce the driven stream (reload not faithful)", kind)
		}
	}

	// Traffic: documented empty at-rest state. Assert it round-trips (loaded
	// == driven) but record that it carries no persistent state to test
	// teeth against — a change here (traffic gaining at-rest state) should
	// prompt promoting it into teethModules above.
	if !bytes.Equal(driven["traffic"], loaded["traffic"]) {
		t.Errorf("module traffic: loaded stream did NOT reproduce the driven stream")
	}
	if len(driven["traffic"]) != 0 {
		t.Logf("NOTE: traffic now has non-empty at-rest state (len=%d) — promote it into teethModules for a real teeth assertion", len(driven["traffic"]))
	}
}

// TestSaveRoundTrip_StateDigestDivergenceIsOnlyUnsavedState proves the
// StateDigest non-round-trip is genuinely confined to state NO participant
// saves. Every digest input owned by a participant (citizens/finance/refuse)
// MUST match after load; the compose-own ledgers (unsaved) MUST diverge
// (they moved during the run and no participant restores them), confirming
// the divergence is a documented compose-own gap, not a hidden module
// round-trip bug.
func TestSaveRoundTrip_StateDigestDivergenceIsOnlyUnsavedState(t *testing.T) {
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

	// Participant-owned digest inputs — MUST round-trip.
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

	// Compose-own ledgers — saved by NO participant, so MUST still diverge
	// after a real run (this is the documented gap the StateDigest finding
	// records). If these ever START matching, either a participant began
	// covering them (good — assert it) or the run stopped moving them (the
	// test lost its power) — either way this assertion must be revisited.
	if sa.treasury == sb.treasury && sa.moneyFlows == sb.moneyFlows && sa.netMigration == sb.netMigration {
		t.Error("compose-own ledgers (treasury/moneyFlows/netMigration) unexpectedly matched after load — the documented StateDigest gap may have changed; revisit this test and the finding")
	}

	// Therefore the FULL digest diverges, but only from unsaved state.
	if compA.StateDigest() == compB.StateDigest() {
		t.Error("expected full StateDigest to diverge (compose-own + crime state is unsaved), but it matched")
	}
}
