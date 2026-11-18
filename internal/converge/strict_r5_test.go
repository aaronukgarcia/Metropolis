package converge

import (
	"sort"
	"strings"
	"testing"
)

// BUG-1140 r5: the exact key sets of every op, written out here and compared
// against the production declarations the decoder uses. Any key added to,
// removed from or renamed in any declaration turns this red, whichever key.

func keySet(ks []string) string {
	c := append([]string{}, ks...)
	sort.Strings(c)
	return "[" + strings.Join(c, ",") + "]"
}

func TestArgKeys_DeclarationsMatchExpectedTableExactly(t *testing.T) {
	// optional = allowed minus required, so the table lists both separately.
	type row struct {
		got      argKeys
		required []string
		optional []string
	}
	table := map[string]row{
		"finance/sample":               {argKeysFinanceSample, nil, nil},
		"finance/post":                 {argKeysFinancePost, []string{"entries"}, []string{"description"}},
		"finance/post_entry":           {argKeysFinancePostEntry, []string{"account", "side", "amount"}, []string{"category"}},
		"finance/begin_month":          {argKeysFinanceBeginMonth, []string{"month"}, nil},
		"finance/post_wages":           {argKeysFinancePostWages, []string{"total"}, nil},
		"finance/post_household_spend": {argKeysFinancePostHouseholdSpend, []string{"quantity", "price"}, nil},
		"finance/settle_opex":          {argKeysFinanceSettleOpex, []string{"opex"}, nil},
		"finance/settle_construction":  {argKeysFinanceSettleConstruction, []string{"cost"}, nil},
		"finance/service_debt":         {argKeysFinanceServiceDebt, []string{"interest", "principal"}, nil},
		"population/seed":              {argKeysPopulationSeed, []string{"count", "ageYears"}, nil},
		"population/advance":           {argKeysPopulationAdvance, []string{"days"}, nil},
		"population/sample":            {argKeysPopulationSample, nil, nil},
		"services/place_service":       {argKeysServicesPlaceService, []string{"buildingID", "goKind", "goServiceID"}, []string{"tsSpec", "x", "y"}},
		"services/set_population":      {argKeysServicesSetPopulation, []string{"n"}, nil},
		"services/sample":              {argKeysServicesSample, nil, nil},
	}
	if len(table) != 15 {
		t.Fatalf("table must list all 15 declarations, has %d", len(table))
	}
	for name, r := range table {
		wantAllowed := keySet(append(append([]string{}, r.required...), r.optional...))
		if got := keySet(r.got.allowed); got != wantAllowed {
			t.Errorf("%s: allowed keys are %s, want %s", name, got, wantAllowed)
		}
		if got, want := keySet(r.got.required), keySet(r.required); got != want {
			t.Errorf("%s: required keys are %s, want %s", name, got, want)
		}
	}
	if got, want := keySet(journalEntryFields), keySet([]string{"tick", "op", "args", "note"}); got != want {
		t.Errorf("journal entry fields are %s, want %s", got, want)
	}
}
