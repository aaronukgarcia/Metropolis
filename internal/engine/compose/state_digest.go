package compose

import (
	"crypto/sha256"
	"encoding/binary"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// StateDigest returns a deterministic sha256 fingerprint of EVERY composed
// module observable a phase hook can mutate over a run — not just the
// citizen population. It is the determinism gate's broad-coverage probe
// (BUG-375 r3).
//
// # Why a broad digest and not PopulationHash alone (BUG-375 r2)
//
// The gate previously hashed only header + Snapshot buf + PopulationHash.
// That catches population-class nondeterminism (births/deaths/migration)
// but NOTHING else: an independent destructive round injected conserving
// map-order nondeterminism into financeHook — a treasury<->households
// transfer of a map-iteration-order-dependent amount, total money
// conserved — and two same-seed 120-month runs diverged ~54,000
// micropounds in treasury while PopulationHash stayed byte-identical, and
// the gate PASSED. Finance/crime/refuse/ledger nondeterminism shipped
// green. StateDigest closes that hole: it observes the finance ledger's
// per-account balances, the crime module's threat/safety/per-type figures,
// the refuse module's per-stream tonnage, and compose's own conservation
// ledgers, so a nondeterministic ordering bug in ANY hook — not only a
// population hook — changes this digest and reddens the gate.
//
// # Coverage (state exactly what is hashed)
//
//  1. Citizens: PopulationHash (the citizen-store fingerprint — the r2
//     probe, retained).
//  2. Finance: the balance of every RoleMoney/liability/external account in
//     digestFinanceAccounts (a FIXED slice, ascending, never a map range),
//     PLUS the money-stock triple, tax/wages/spend/opex/debt aggregates and
//     both money-in-circulation totals. Per-account balances are the load-
//     bearing part: a conserving transfer between two accounts leaves their
//     SUM (RecomputeMoneyStock) invariant, so only the individual balances
//     expose it. In baseline one the RoleMoney account set is exactly the
//     well-known accounts (no module opens another — verified: the only
//     RoleMoney creator in the tree is finance's wellKnownAccounts), so
//     naming them enumerates the whole ledger.
//  3. Crime: ThreatLevel, ThreatRisingStreak, LastThreatEventMonth,
//     TriggerProbability, the citywide SafetyTerm, and per crime type (a
//     FIXED slice, enum order) ActiveCrime + Generation. Crime state is
//     mutated by crime.AdvanceMonth, driven each month from the attract
//     hook (compose.go safetyTerm).
//  4. Refuse: Contamination, RecyclingResaleValue, and per waste stream
//     (refuseStreams, a FIXED slice) the five §25 tonnage figures. Refuse
//     state is mutated by refuse.Generate, driven each month from the
//     attract hook (compose.go environmentTerm).
//  5. Compose's own conservation/liveness ledgers (plain simState fields a
//     hook mutates directly): treasury, citizenWealth, moneyFlows,
//     netMigration, consumptionDelivered, vitalBirths, vitalDeaths and the
//     people/money opening+delta pairs.
//
// # Known limits (honest)
//
//   - Only observables reachable through an exported module accessor are
//     hashed; a hook that mutated purely-internal module state with no
//     observable projection would not be seen. Every baseline-one hook's
//     effect DOES project (that is what the UI and invariants read), so this
//     is a theoretical gap, not a live one.
//   - Finance coverage is the named account set; if a future module opens a
//     new RoleMoney account, add it to digestFinanceAccounts (a compile-time
//     slice) in the same commit — the same GR#18 dead-code-audit discipline.
//
// # Determinism of the digest itself (AC-9 / GR#21)
//
// StateDigest ranges over three FIXED slices only (digestFinanceAccounts,
// digestCrimeTypes, refuseStreams) and never a Go map; every module
// accessor it calls is itself documented map-free on its read path. Floats
// are hashed by their IEEE-754 bit pattern (math.Float64bits) so equal
// values hash identically regardless of formatting. Called after the run's
// phase pipeline has joined (detgate cancels the command loop before
// snapshot), so the reads are single-goroutine; each accessor also takes
// its own module lock. Running it twice against the same state yields
// byte-identical output — the detgate regression test asserts this.
func (c *Composition) StateDigest() [32]byte {
	h := sha256.New()
	// Domain-separation tag: a version bump here is a deliberate,
	// gate-invalidating change (every stored gate hash shifts), never an
	// accident.
	h.Write([]byte("metropolis.compose.observable-digest.v1\x00"))

	st := c.state

	// 1. citizen-store fingerprint (the r2 probe, retained).
	pop := st.citizens.PopulationHash(st.cid)
	h.Write(pop[:])

	// 2. finance ledger — per-account balances first (the conserving-
	// transfer-visible part), then the aggregates.
	for _, acct := range digestFinanceAccounts {
		digestInt64(h, ledgerBalance(st.finance, acct))
	}
	if st.finance != nil {
		ms := st.finance.MoneyStock()
		digestInt64(h, int64(ms.Opening))
		digestInt64(h, int64(ms.Closing))
		digestInt64(h, int64(ms.TrackedDelta))
		digestInt64(h, int64(st.finance.TaxRevenue()))
		digestInt64(h, int64(st.finance.WagesPosted()))
		digestInt64(h, int64(st.finance.SpendPosted()))
		digestInt64(h, int64(st.finance.OpexTotal()))
		digestInt64(h, int64(st.finance.OutstandingDebt()))
		digestInt64(h, int64(st.finance.TotalMoneyInCirculation()))
		digestInt64(h, int64(st.finance.RecomputeMoneyStock()))
	}

	// 3. crime observables (mutated by crime.AdvanceMonth via the attract
	// hook).
	if st.crime != nil {
		digestFloat(h, st.crime.ThreatLevel())
		digestInt64(h, int64(st.crime.ThreatRisingStreak()))
		digestInt64(h, st.crime.LastThreatEventMonth())
		digestFloat(h, st.crime.TriggerProbability())
		safety, safetyErr := st.crime.SafetyTerm(citywideCrimeDistrict)
		digestFloatErr(h, safety, safetyErr)
		for _, t := range digestCrimeTypes {
			active, activeErr := st.crime.ActiveCrime(citywideCrimeDistrict, t)
			digestFloatErr(h, active, activeErr)
			gen, genErr := st.crime.Generation(citywideCrimeDistrict, t)
			digestFloatErr(h, gen, genErr)
		}
	}

	// 4. refuse observables (mutated by refuse.Generate via the attract
	// hook).
	if st.refuse != nil {
		digestFloat(h, st.refuse.Contamination())
		digestInt64(h, st.refuse.RecyclingResaleValue())
		for _, s := range refuseStreams {
			g, gErr := st.refuse.TonnesGenerated(s)
			digestInt64Err(h, g, gErr)
			col, colErr := st.refuse.TonnesCollected(s)
			digestInt64Err(h, col, colErr)
			unc, uncErr := st.refuse.TonnesUncollected(s)
			digestInt64Err(h, unc, uncErr)
			tr, trErr := st.refuse.TonnesInTransit(s)
			digestInt64Err(h, tr, trErr)
			bl, blErr := st.refuse.TonnesDisposalBacklog(s)
			digestInt64Err(h, bl, blErr)
		}
	}

	// 5. compose-owned conservation / liveness ledgers (plain simState
	// fields a hook mutates directly).
	digestInt64(h, st.treasury)
	digestInt64(h, st.citizenWealth)
	digestInt64(h, st.moneyFlows)
	digestInt64(h, st.netMigration)
	digestFloat(h, st.consumptionDelivered)
	digestInt64(h, st.vitalBirths)
	digestInt64(h, st.vitalDeaths)
	digestInt64(h, st.peopleOpening)
	digestInt64(h, st.peopleDelta)
	digestInt64(h, st.moneyOpening)
	digestInt64(h, st.moneyDelta)

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// digestFinanceAccounts is the fixed, ascending set of finance accounts
// StateDigest hashes the balance of — every account finance opens at
// construction (finance.wellKnownAccounts). A slice, NEVER a map range
// (GR#21). Extending finance's account set is a GR#18 obligation to extend
// this slice in the same commit.
var digestFinanceAccounts = []finance.AccountID{
	finance.AcctTreasury,
	finance.AcctHouseholds,
	finance.AcctFirms,
	finance.AcctReserves,
	finance.AcctDebt,
	finance.AcctExternal,
}

// digestCrimeTypes is the fixed, enum-ordered set of the nine crime types
// StateDigest hashes per-type figures for. A slice, NEVER a map range
// (GR#21) — mirrors crime's own internal crimeTypeKeys ordering.
var digestCrimeTypes = []crime.CrimeType{
	crime.CrimePettyTheft,
	crime.CrimeBurglary,
	crime.CrimeVehicleCrime,
	crime.CrimeCriminalDamage,
	crime.CrimeViolent,
	crime.CrimeDrugsSupply,
	crime.CrimeOrganised,
	crime.CrimeFraudCyber,
	crime.CrimeSmuggling,
}

// digestInt64 writes v as 8 big-endian bytes into the running hash.
func digestInt64(h interface{ Write([]byte) (int, error) }, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	_, _ = h.Write(b[:])
}

// digestFloat writes v's IEEE-754 bit pattern as 8 big-endian bytes, so
// equal float values hash identically regardless of formatting (GR#21
// determinism). NaN's exact bit pattern is preserved — the point is
// byte-stability across two runs of the same code, not float equality
// semantics.
func digestFloat(h interface{ Write([]byte) (int, error) }, v float64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	_, _ = h.Write(b[:])
}

// digestFloatErr writes a 1-byte ok/err tag then the value (zero on error),
// so an accessor that reports an error (e.g. an unregistered district)
// contributes a stable, distinct sequence rather than being silently
// skipped. The error path is deterministic per run, so this stays
// byte-stable across runs.
func digestFloatErr(h interface{ Write([]byte) (int, error) }, v float64, err error) {
	if err != nil {
		_, _ = h.Write([]byte{0})
		digestFloat(h, 0)
		return
	}
	_, _ = h.Write([]byte{1})
	digestFloat(h, v)
}

// digestInt64Err is digestFloatErr's integer sibling.
func digestInt64Err(h interface{ Write([]byte) (int, error) }, v int64, err error) {
	if err != nil {
		_, _ = h.Write([]byte{0})
		digestInt64(h, 0)
		return
	}
	_, _ = h.Write([]byte{1})
	digestInt64(h, v)
}
