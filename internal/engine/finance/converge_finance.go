package finance

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/converge"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Registry error codes MET-H500-H503 (harness.converge, data/errors.json)
// are the ones a converge.Domain.Run implementation is documented to
// surface via errs — this file wraps them with the finance-specific
// context (tick/op/reason) a caller of the harness needs, rather than
// inventing a parallel finance-owned error code range for what is
// purely a test/tooling adapter, not a gameplay-facing surface.
const codeFinanceConvergeJournalOpFailed = "MET-H503"

// FinanceDomain is FEAT-1972079936 Phase 3 inc1's [converge.Domain]
// adapter for engine.finance — the first, and hardest, domain named in
// docs/planning/phase3-convergence-plan.md §5 (finance is a ledger vs
// the TS sim's scalar, flow-tax vs count-tax, micropound vs
// placeholder). This adapter does NOT change finance's own logic or its
// money base unit (Money stays int64 micropounds, AC-2) — it only
// drives an ordinary *FinanceAPI through its already-public surface
// (BeginMonth/PostWages/PostHouseholdSpend/SettleOpex/
// SettleConstruction/ServiceDebt/AccountBalance/OutstandingDebt) and
// reports a [converge.Trajectory] of the aggregate figures
// docs/planning/phase3-convergence-plan.md's investigation named as
// finance's read surface: Treasury, Reserves, Debt(outstanding),
// NetWorth (Treasury+Reserves-Debt).
//
// The zero value is ready to use — FinanceDomain holds no state of its
// own; Run constructs a fresh *FinanceAPI for every call so successive
// Run calls never share ledger state (the determinism proof in
// converge_finance_test.go depends on this: two Run calls over the same
// Journal must be running against two INDEPENDENT FinanceAPI instances,
// not the same one twice).
type FinanceDomain struct{}

// Name implements converge.Domain.
func (FinanceDomain) Name() string { return "finance" }

// Contract implements converge.Domain: engine.finance's own money model
// is integer, deterministic and non-stochastic (doc.go, AC-2), so every
// field here starts life at converge.TierExact — the strongest bar
// (docs/planning/phase3-convergence-plan.md §2's Tier A). A units/
// rounding convention decision (BUG-355) may later relax "treasury" to
// TierBounded once a TS-pounds<->Go-micropounds mapping is agreed for
// the real cross-model flip (inc3.2); this harness ships Tier A as the
// honest default for a Go-vs-Go reference/candidate comparison, which
// is exactly what this increment's own tests exercise.
func (FinanceDomain) Contract() converge.Contract {
	return converge.Contract{
		"treasury": {Tier: converge.TierExact},
		"reserves": {Tier: converge.TierExact},
		"debt":     {Tier: converge.TierExact},
		"netWorth": {Tier: converge.TierExact},
	}
}

// Run implements converge.Domain: it constructs a fresh *FinanceAPI,
// applies j's entries in order via the small op vocabulary below, and
// returns the resulting converge.Trajectory. Every op either mutates
// the ledger or (for "sample") appends a converge.Sample snapshotting
// the four Contract fields at the entry's Tick — snapshot cadence is
// therefore explicit in the journal, not implied by BeginMonth or any
// other bookkeeping call, so a candidate/fixture trajectory can be
// aligned to exactly the ticks the Go reference chose to sample.
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
// FinanceAPI's own operations are themselves deterministic (doc.go) —
// converge_finance_test.go's TestFinanceDomain_DeterministicTrajectory
// proves the same Journal run twice produces a reflect.DeepEqual
// Trajectory.
func (FinanceDomain) Run(j converge.Journal) (converge.Trajectory, error) {
	f := NewFinanceAPI("")
	var traj converge.Trajectory

	for _, entry := range j.Entries {
		if err := applyFinanceJournalOp(f, entry); err != nil {
			return nil, err
		}
		if entry.Op == "sample" {
			s, err := snapshotFinance(f, entry.Tick)
			if err != nil {
				return nil, err
			}
			traj = append(traj, s)
		}
	}
	return traj, nil
}

// applyFinanceJournalOp dispatches one JournalEntry to the matching
// FinanceAPI call. Unknown op names, malformed Args, or an underlying
// FinanceAPI error all surface as codeFinanceConvergeJournalOpFailed
// (MET-H503) — a journal entry this adapter cannot apply is a fixture
// defect, never silently skipped (mirroring GR#17's "no silent
// failure" spirit for what is, here, test/tooling infrastructure).
func applyFinanceJournalOp(f *FinanceAPI, entry converge.JournalEntry) error {
	fail := func(reason string) error {
		return errs.New(codeFinanceConvergeJournalOpFailed, errs.NewCorrelationID(), map[string]any{
			"tick": entry.Tick, "op": entry.Op, "domain": "finance", "reason": reason,
		})
	}

	// SEC-020-style copy guard (astgate): this function accepts a
	// *FinanceAPI directly rather than only calling its already-guarded
	// public methods, so it carries its own checkNotCopied check up
	// front — a struct-copied FinanceAPI is rejected here before any of
	// the op-specific calls below ever run.
	if err := f.checkNotCopied("applyFinanceJournalOp"); err != nil {
		return err
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
		tx := Transaction{Description: args.Description}
		for _, e := range args.Entries {
			var side Side
			switch e.Side {
			case "debit":
				side = SideDebit
			case "credit":
				side = SideCredit
			default:
				return fail(fmt.Sprintf("entry.side must be \"debit\" or \"credit\", got %q", e.Side))
			}
			tx.Entries = append(tx.Entries, Entry{
				Account:  AccountID(e.Account),
				Side:     side,
				Amount:   Money(e.Amount),
				Category: Category(e.Category),
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
		if _, err := f.PostWages(Money(args.Total)); err != nil {
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
		if _, err := f.PostHouseholdSpend(args.Quantity, Money(args.Price)); err != nil {
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
		if _, err := f.SettleOpex(Money(args.Opex)); err != nil {
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
		if _, err := f.SettleConstruction(Money(args.Cost)); err != nil {
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
		if err := f.ServiceDebt(Money(args.Interest), Money(args.Principal)); err != nil {
			return fail(err.Error())
		}
		return nil

	default:
		return fail("unrecognised op name")
	}
}

// snapshotFinance reads the four Contract fields off f at their current
// value and returns them as a converge.Sample at tick.
func snapshotFinance(f *FinanceAPI, tick int64) (converge.Sample, error) {
	// SEC-020-style copy guard (astgate): like applyFinanceJournalOp,
	// this function accepts a *FinanceAPI directly, so it checks its
	// own identity up front rather than relying solely on the guards
	// inside AccountBalance/OutstandingDebt.
	if err := f.checkNotCopied("snapshotFinance"); err != nil {
		return converge.Sample{}, err
	}

	treasury, _ := f.AccountBalance(AcctTreasury)
	reserves, _ := f.AccountBalance(AcctReserves)
	debt := f.OutstandingDebt()
	netWorth := treasury + reserves - debt

	return converge.Sample{
		Tick: tick,
		Values: map[string]int64{
			"treasury": int64(treasury),
			"reserves": int64(reserves),
			"debt":     int64(debt),
			"netWorth": int64(netWorth),
		},
	}, nil
}
