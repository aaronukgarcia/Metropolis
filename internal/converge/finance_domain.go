package converge

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FinanceDomain is FEAT-1972079936 Phase 3 inc1's [Domain] adapter for
// engine.finance — the first, and hardest, domain named in
// docs/planning/phase3-convergence-plan.md §5 (finance is a ledger vs
// the TS sim's scalar, flow-tax vs count-tax, micropound vs
// placeholder). This adapter does NOT change finance's own logic or its
// money base unit (Money stays int64 micropounds, AC-2) — it only
// drives an ordinary *finance.FinanceAPI through its already-PUBLIC
// surface (BeginMonth/Post/PostWages/PostHouseholdSpend/SettleOpex/
// SettleConstruction/ServiceDebt/AccountBalance/OutstandingDebt) and
// reports a [Trajectory] of the aggregate figures
// docs/planning/phase3-convergence-plan.md's investigation named as
// finance's read surface: Treasury, Reserves, Debt(outstanding),
// NetWorth (Treasury+Reserves-Debt).
//
// # Layering (GR#20)
//
// This file lives in harness.converge, NOT in engine.finance, and
// imports finance's already-public API — the correct direction for a
// high-level harness consuming a low-level engine module's contract.
// engine.finance itself carries NO import of internal/converge
// (grep -rn "internal/converge" internal/engine/finance/ matches
// nothing outside _test.go) — a converge<-finance edge would invert
// the layer graph (a foundational engine module depending on a
// harness/tooling package) and is exactly the shape GR#20's
// contract-first rule exists to prevent. This adapter therefore never
// touches finance's unexported copy-guard (checkNotCopied is
// package-private to engine.finance) — it relies entirely on the
// guard every one of the exported methods below already runs
// internally, which is sufficient: nothing here holds a *FinanceAPI
// across goroutines or struct-copies it.
//
// The zero value is ready to use — FinanceDomain holds no state of its
// own; Run constructs a fresh *finance.FinanceAPI for every call so
// successive Run calls never share ledger state (the determinism proof
// in finance_domain_test.go depends on this: two Run calls over the
// same Journal must be running against two INDEPENDENT FinanceAPI
// instances, not the same one twice).
type FinanceDomain struct{}

// Name implements Domain.
func (FinanceDomain) Name() string { return "finance" }

// Contract implements Domain: engine.finance's own money model is
// integer, deterministic and non-stochastic (its doc.go, AC-2), so
// every field here starts life at TierExact — the strongest bar
// (docs/planning/phase3-convergence-plan.md §2's Tier A). A units/
// rounding convention decision (BUG-355) may later relax "treasury" to
// TierBounded once a TS-pounds<->Go-micropounds mapping is agreed for
// the real cross-model flip (inc3.2); this harness ships Tier A as the
// honest default for a Go-vs-Go reference/candidate comparison, which
// is exactly what this increment's own tests exercise.
func (FinanceDomain) Contract() Contract {
	return Contract{
		"treasury": {Tier: TierExact},
		"reserves": {Tier: TierExact},
		"debt":     {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
}

// Run implements Domain: it constructs a fresh *finance.FinanceAPI,
// applies j's entries in order via the small op vocabulary below, and
// returns the resulting Trajectory. Every op either mutates the ledger
// or (for "sample") appends a Sample snapshotting the four Contract
// fields at the entry's Tick — snapshot cadence is therefore explicit
// in the journal, not implied by BeginMonth or any other bookkeeping
// call, so a candidate/fixture trajectory can be aligned to exactly
// the ticks the Go reference chose to sample.
//
// Op vocabulary (applyFinanceJournalOp):
//   - "begin_month" {month}: FinanceAPI.BeginMonth.
//   - "post" {description, entries:[{account, side:"debit"|"credit",
//     amount, category}]}: the general escape hatch — an arbitrary
//     balanced Transaction via FinanceAPI.Post, for opening balances
//     (a fresh FinanceAPI starts every account at zero, AC-2's "the
//     zero value is not usable" mirrored at the domain-fixture level:
//     a journal that wants a funded treasury must post it explicitly,
//     rather than this adapter inventing an implicit opening balance)
//     or any flow the named stage helpers below don't cover.
//   - "post_wages" {total}: FinanceAPI.PostWages.
//   - "post_household_spend" {quantity, price}: FinanceAPI.PostHouseholdSpend.
//   - "settle_opex" {opex}: FinanceAPI.SettleOpex.
//   - "settle_construction" {cost}: FinanceAPI.SettleConstruction.
//   - "service_debt" {interest, principal}: FinanceAPI.ServiceDebt.
//   - "sample": no args; snapshotFinance captures the current
//     treasury/reserves/debt/netWorth at entry.Tick.
//
// Run is deterministic (GR#21): it never reads the wall clock, and
// FinanceAPI's own operations are themselves deterministic (its doc.go)
// — finance_domain_test.go's TestFinanceDomain_DeterministicTrajectory
// proves the same Journal run twice produces a reflect.DeepEqual
// Trajectory.
func (FinanceDomain) Run(j Journal) (Trajectory, error) {
	f := finance.NewFinanceAPI("")
	var traj Trajectory

	for _, entry := range j.Entries {
		if err := applyFinanceJournalOp(f, entry); err != nil {
			return nil, err
		}
		if entry.Op == "sample" {
			traj = append(traj, snapshotFinance(f, entry.Tick))
		}
	}
	return traj, nil
}

// applyFinanceJournalOp dispatches one JournalEntry to the matching
// finance.FinanceAPI call. Unknown op names, malformed Args, or an
// underlying FinanceAPI error all surface as codeJournalOpFailed
// (MET-H503) — a journal entry this adapter cannot apply is a fixture
// defect, never silently skipped (mirroring GR#17's "no silent
// failure" spirit for what is, here, test/tooling infrastructure).
func applyFinanceJournalOp(f *finance.FinanceAPI, entry JournalEntry) error {
	fail := func(reason string) error {
		return errs.New(codeJournalOpFailed, errs.NewCorrelationID(), map[string]any{
			"tick": entry.Tick, "op": entry.Op, "domain": "finance", "reason": reason,
		})
	}

	switch entry.Op {
	case "sample":
		return nil // handled by the caller after this returns nil

	case "post":
		var args struct {
			Description string `json:"description"`
			Entries     []struct {
				Account  string `json:"account"`
				Side     string `json:"side"` // "debit" or "credit"
				Amount   int64  `json:"amount"`
				Category string `json:"category"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		tx := finance.Transaction{Description: args.Description}
		for _, e := range args.Entries {
			var side finance.Side
			switch e.Side {
			case "debit":
				side = finance.SideDebit
			case "credit":
				side = finance.SideCredit
			default:
				return fail(fmt.Sprintf("entry.side must be \"debit\" or \"credit\", got %q", e.Side))
			}
			tx.Entries = append(tx.Entries, finance.Entry{
				Account:  finance.AccountID(e.Account),
				Side:     side,
				Amount:   finance.Money(e.Amount),
				Category: finance.Category(e.Category),
			})
		}
		if _, err := f.Post(tx); err != nil {
			return fail(err.Error())
		}
		return nil

	case "begin_month":
		var args struct {
			Month int64 `json:"month"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if err := f.BeginMonth(args.Month); err != nil {
			return fail(err.Error())
		}
		return nil

	case "post_wages":
		var args struct {
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if _, err := f.PostWages(finance.Money(args.Total)); err != nil {
			return fail(err.Error())
		}
		return nil

	case "post_household_spend":
		var args struct {
			Quantity int64 `json:"quantity"`
			Price    int64 `json:"price"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if _, err := f.PostHouseholdSpend(args.Quantity, finance.Money(args.Price)); err != nil {
			return fail(err.Error())
		}
		return nil

	case "settle_opex":
		var args struct {
			Opex int64 `json:"opex"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if _, err := f.SettleOpex(finance.Money(args.Opex)); err != nil {
			return fail(err.Error())
		}
		return nil

	case "settle_construction":
		var args struct {
			Cost int64 `json:"cost"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if _, err := f.SettleConstruction(finance.Money(args.Cost)); err != nil {
			return fail(err.Error())
		}
		return nil

	case "service_debt":
		var args struct {
			Interest  int64 `json:"interest"`
			Principal int64 `json:"principal"`
		}
		if err := json.Unmarshal(entry.Args, &args); err != nil {
			return fail(fmt.Sprintf("malformed args: %v", err))
		}
		if err := f.ServiceDebt(finance.Money(args.Interest), finance.Money(args.Principal)); err != nil {
			return fail(err.Error())
		}
		return nil

	default:
		return fail("unrecognised op name")
	}
}

// snapshotFinance reads the four Contract fields off f at their
// current value (via its exported accessors — see the layering note on
// FinanceDomain above for why this file never touches finance's own
// unexported copy guard) and returns them as a Sample at tick.
func snapshotFinance(f *finance.FinanceAPI, tick int64) Sample {
	treasury, _ := f.AccountBalance(finance.AcctTreasury)
	reserves, _ := f.AccountBalance(finance.AcctReserves)
	debt := f.OutstandingDebt()
	netWorth := treasury + reserves - debt

	return Sample{
		Tick: tick,
		Values: map[string]int64{
			"treasury": int64(treasury),
			"reserves": int64(reserves),
			"debt":     int64(debt),
			"netWorth": int64(netWorth),
		},
	}
}
