package converge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// BUG-1140 Run side: every Domain fails closed on the routes that used to
// produce a meaningless trajectory.

func je(tick int64, op, args string) JournalEntry {
	e := JournalEntry{Tick: tick, Op: op}
	if args != "" {
		e.Args = json.RawMessage(args)
	}
	return e
}

// financeFund is a balanced opening-balance post using the real account ids.
func financeFund() string {
	return fmt.Sprintf(`{"description":"open","entries":[{"account":%q,"side":"credit","amount":100,"category":"imports"},{"account":%q,"side":"debit","amount":100,"category":"imports"}]}`,
		string(finance.AcctTreasury), string(finance.AcctExternal))
}

type strictCase struct {
	name string
	run  func(Journal) (Trajectory, error)
	j    Journal
	code string
}

func wantCode(t *testing.T, c strictCase) {
	t.Helper()
	traj, err := c.run(c.j)
	if err == nil {
		t.Fatalf("%s: expected %s, got nil error (traj=%v)", c.name, c.code, traj)
	}
	if !strings.Contains(err.Error(), c.code) {
		t.Fatalf("%s: error %q does not carry %s", c.name, err.Error(), c.code)
	}
	if traj != nil {
		t.Fatalf("%s: failed Run returned a trajectory %v", c.name, traj)
	}
}

func strictCases() []strictCase {
	pop := PopulationDomain{}.Run
	fin := FinanceDomain{}.Run
	svc := ServicesDomain{}.Run
	fund := financeFund()
	place := `{"buildingID":"fire_station","goKind":"fire","goServiceID":"fire-1"}`
	return []strictCase{
		// (a) empty journal
		{"population empty", pop, Journal{}, codeEmptyJournal},
		{"finance empty", fin, Journal{}, codeEmptyJournal},
		{"services empty", svc, Journal{}, codeEmptyJournal},
		// no sample op at all
		{"population no sample", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`)}}, codeNoSample},
		{"finance no sample", fin, Journal{Entries: []JournalEntry{je(0, "post", fund)}}, codeNoSample},
		{"services no sample", svc, Journal{Entries: []JournalEntry{je(0, "place_service", place)}}, codeNoSample},
		// (b) seedless / setup-less advance and sample
		{"population seedless advance", pop, Journal{Entries: []JournalEntry{je(0, "advance", `{"days":5}`), je(1, "sample", "")}}, codeSetupMissing},
		{"population advance before the seed", pop, Journal{Entries: []JournalEntry{je(0, "advance", `{"days":5}`), je(0, "seed", `{"count":10,"ageYears":30}`), je(1, "sample", "")}}, codeSetupMissing},
		{"population seedless sample", pop, Journal{Entries: []JournalEntry{je(1, "sample", "")}}, codeSetupMissing},
		{"finance setup-less sample", fin, Journal{Entries: []JournalEntry{je(0, "begin_month", `{"month":1}`), je(1, "sample", "")}}, codeSetupMissing},
		{"services sample before place", svc, Journal{Entries: []JournalEntry{je(0, "set_population", `{"n":100}`), je(1, "sample", "")}}, codeSetupMissing},
		{"services sample before population", svc, Journal{Entries: []JournalEntry{je(0, "place_service", place), je(1, "sample", "")}}, codeSetupMissing},
		// (c) unknown argument keys
		{"population unknown seed key", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYear":30}`), je(1, "sample", "")}}, codeUnknownArg},
		{"population unknown advance key", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`), je(0, "advance", `{"days":1,"day":2}`), je(1, "sample", "")}}, codeUnknownArg},
		{"population sample with args", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`), je(1, "sample", `{"x":1}`)}}, codeUnknownArg},
		{"finance unknown wages key", fin, Journal{Entries: []JournalEntry{je(0, "post", fund), je(0, "post_wages", `{"total":5,"totl":9}`), je(1, "sample", "")}}, codeUnknownArg},
		{"finance unknown post key", fin, Journal{Entries: []JournalEntry{je(0, "post", `{"entries":[],"extra":1}`), je(1, "sample", "")}}, codeUnknownArg},
		{"finance unknown nested entry key", fin, Journal{Entries: []JournalEntry{je(0, "post", `{"entries":[{"account":"treasury","side":"debit","amount":1,"categry":"x"}]}`), je(1, "sample", "")}}, codeUnknownArg},
		{"services unknown place key", svc, Journal{Entries: []JournalEntry{je(0, "place_service", `{"buildingID":"fire_station","goKind":"fire","goServiceID":"f","goKinds":"x"}`), je(1, "sample", "")}}, codeUnknownArg},
		{"services unknown population key", svc, Journal{Entries: []JournalEntry{je(0, "place_service", place), je(0, "set_population", `{"n":5,"pop":6}`), je(1, "sample", "")}}, codeUnknownArg},
		// (d) missing required arguments
		{"population missing ageYears", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10}`), je(1, "sample", "")}}, codeMissingArg},
		{"population missing count", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"ageYears":30}`), je(1, "sample", "")}}, codeMissingArg},
		{"population missing days", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`), je(0, "advance", ""), je(1, "sample", "")}}, codeMissingArg},
		{"finance missing total", fin, Journal{Entries: []JournalEntry{je(0, "post", fund), je(0, "post_wages", `{}`), je(1, "sample", "")}}, codeMissingArg},
		{"finance missing month", fin, Journal{Entries: []JournalEntry{je(0, "begin_month", ""), je(1, "sample", "")}}, codeMissingArg},
		{"finance missing interest", fin, Journal{Entries: []JournalEntry{je(0, "post", fund), je(0, "service_debt", `{"principal":1}`), je(1, "sample", "")}}, codeMissingArg},
		{"finance missing entries", fin, Journal{Entries: []JournalEntry{je(0, "post", `{"description":"x"}`), je(1, "sample", "")}}, codeMissingArg},
		{"finance missing nested amount", fin, Journal{Entries: []JournalEntry{je(0, "post", `{"entries":[{"account":"treasury","side":"debit"}]}`), je(1, "sample", "")}}, codeMissingArg},
		{"services missing goKind", svc, Journal{Entries: []JournalEntry{je(0, "place_service", `{"buildingID":"fire_station","goServiceID":"f"}`), je(1, "sample", "")}}, codeMissingArg},
		{"services missing n", svc, Journal{Entries: []JournalEntry{je(0, "place_service", place), je(0, "set_population", ""), je(1, "sample", "")}}, codeMissingArg},
		// sample tick order
		{"population duplicate sample tick", pop, Journal{Entries: []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`), je(2, "sample", ""), je(2, "sample", "")}}, codeSampleTickOrder},
		{"finance decreasing sample tick", fin, Journal{Entries: []JournalEntry{je(0, "post", fund), je(3, "sample", ""), je(2, "sample", "")}}, codeSampleTickOrder},
		{"services negative sample tick", svc, Journal{Entries: []JournalEntry{je(0, "place_service", place), je(0, "set_population", `{"n":5}`), je(-1, "sample", "")}}, codeSampleTickOrder},
	}
}

func TestRun_FailsClosed_EveryDomainEveryRoute(t *testing.T) {
	for _, c := range strictCases() {
		t.Run(c.name, func(t *testing.T) { wantCode(t, c) })
	}
}

// The strict decoder must not reject the valid shapes: one good journal per
// domain (the full shared-journal tests also exercise every real op).
func TestRun_ValidMinimalJournals_StillRun(t *testing.T) {
	fund := financeFund()
	if _, err := (FinanceDomain{}).Run(Journal{Entries: []JournalEntry{je(0, "post", fund), je(1, "sample", "{}")}}); err != nil {
		t.Fatalf("finance: %v", err)
	}
	// ageYears 0 is an explicit newborn cohort, not a missing argument.
	if _, err := (PopulationDomain{}).Run(Journal{Entries: []JournalEntry{
		je(0, "seed", `{"count":4,"ageYears":0}`), je(0, "advance", `{"days":1}`), je(1, "sample", "null")}}); err != nil {
		t.Fatalf("population: %v", err)
	}
}

// BUG-1140 advisory: the zero-reference-mean branch of the distribution
// check had no test, so an always-OK mutant survived.
func TestCompare_Distribution_ZeroReferenceMean(t *testing.T) {
	contract := Contract{"population": {Tier: TierDistribution, Window: 1, BandPct: 0.05}}
	zero := Trajectory{{Tick: 1, Values: map[string]int64{"population": 0}}}
	nonzero := Trajectory{{Tick: 1, Values: map[string]int64{"population": 7}}}

	if rep := Compare("demographics", zero, zero, contract); !rep.Pass {
		t.Fatalf("both means zero must pass, got %v", rep.Diffs)
	}
	rep := Compare("demographics", zero, nonzero, contract)
	if rep.Pass {
		t.Fatal("reference mean zero with a non-zero candidate must fail")
	}
	if len(rep.Diffs) != 1 || rep.Diffs[0].Field != "population" {
		t.Fatalf("expected one population diff, got %v", rep.Diffs)
	}
}
