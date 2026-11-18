package rules

import (
	"strconv"
	"strings"
)

// Status says whether a registry entry's rule is in use by the engine.
type Status string

const (
	// StatusPendingWiring: the entry and its constant exist but no call
	// site uses the constant yet. Requires Rule.PendingItem. The registry
	// tests fail if a call site exists while the entry still says this.
	StatusPendingWiring Status = "pending-wiring"

	// StatusWired: a call site uses the constant and the old-journal
	// fixture exists. The registry tests fail if either is missing.
	StatusWired Status = "wired"
)

// Rule is one registry entry: one change to what a player's command does
// (accepted or refused, and what it costs). It is NOT for daily simulation
// formulas (migration weights, tax, charges); those are never versioned.
// See the package documentation for the boundary.
type Rule struct {
	// Number is the version this rule introduced. Contiguous from 1.
	Number Version
	// Const is the Go name of the constant for Number in rules.go.
	Const string
	// Date is the ISO date (YYYY-MM-DD) the rule landed; never goes
	// backwards down the registry.
	Date string
	// Item is the BOW code of the change (BUG-n, FEAT-n, MOD-n, INT-n).
	Item string
	// Old is one plain-English line: how the engine behaved before.
	Old string
	// New is one plain-English line: how it behaves now.
	New string
	// Fixture is the repo-root-relative directory of the old-journal
	// fixture recorded at Number-1. It must exist once the entry is wired.
	Fixture string
	// Status is StatusPendingWiring or StatusWired.
	Status Status
	// PendingItem is the BOW code of the work that will wire the rule.
	// Required while Status is StatusPendingWiring, empty once wired.
	PendingItem string
}

// registry is the one list. Add the next rule at the end. Only changes to
// what a player's command does belong here (accepted or refused, and what
// it costs); a daily simulation formula never gets an entry.
var registry = []Rule{
	{
		Number:      RefuseOccupiedCell,
		Const:       "RefuseOccupiedCell",
		Date:        "2026-11-18",
		Item:        "BUG-1224",
		Old:         "a second build order on a cell that already holds a queued or standing structure is accepted and charged",
		New:         "a second build order on an occupied cell is refused with the cell-in-use error and not charged",
		Fixture:     "internal/engine/compose/testdata/rules/v1",
		Status:      StatusPendingWiring,
		PendingItem: "FEAT-9995385612",
	},
}

// Registry returns a copy of every entry in ascending Number order.
func Registry() []Rule {
	out := make([]Rule, len(registry))
	copy(out, registry)
	return out
}

// Entry returns the entry for version v. ok is false for 0 and for any
// version with no entry (callers raise ErrUnknownRuleVersion).
func Entry(v Version) (Rule, bool) {
	for _, r := range registry {
		if r.Number == v {
			return r, true
		}
	}
	return Rule{}, false
}

// Between returns the entries a restore crossed: every Number n with
// from < n <= to, ascending. Used to build the older-rules warning.
func Between(from, to Version) []Rule {
	var out []Rule
	for _, r := range registry {
		if r.Number > from && r.Number <= to {
			out = append(out, r)
		}
	}
	return out
}

// Describe renders the entries crossed between from (exclusive) and to
// (inclusive), one clause per rule: number, date, item, old and new text,
// all sourced from the registry. Empty when nothing was crossed.
func Describe(from, to Version) string {
	var b strings.Builder
	for i, r := range Between(from, to) {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString("rule " + strconv.Itoa(int(r.Number)) + " (" + r.Date + ", " + r.Item + ") was: " + r.Old + "; now: " + r.New)
	}
	return b.String()
}
