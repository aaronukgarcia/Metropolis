package converge

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// BUG-1140 r4: null-for-required, duplicate keys, the advance bound, empty
// service ids, the exact per-op allow-lists, and the strict journal entry.

type opSpec struct {
	domain   string
	run      func(Journal) (Trajectory, error)
	prefix   func() []JournalEntry
	op       string
	required [][2]string // key, valid JSON value
	optional [][2]string // key, valid JSON value
}

func noPrefix() []JournalEntry { return nil }

func popPrefix() []JournalEntry {
	return []JournalEntry{je(0, "seed", `{"count":10,"ageYears":30}`)}
}

func svcPrefix() []JournalEntry {
	return []JournalEntry{
		je(0, "place_service", `{"buildingID":"fire_station","goKind":"fire","goServiceID":"fire-1"}`),
		je(0, "set_population", `{"n":5}`),
	}
}

func finPrefix() []JournalEntry { return []JournalEntry{je(0, "post", financeFund())} }

func opSpecs() []opSpec {
	pop, fin, svc := PopulationDomain{}.Run, FinanceDomain{}.Run, ServicesDomain{}.Run
	entry := `[{"account":"a","side":"debit","amount":1}]`
	return []opSpec{
		{"population", pop, noPrefix, "seed", [][2]string{{"count", "10"}, {"ageYears", "30"}}, nil},
		{"population", pop, popPrefix, "advance", [][2]string{{"days", "1"}}, nil},
		{"population", pop, popPrefix, "sample", nil, nil},
		{"services", svc, noPrefix, "place_service", [][2]string{{"buildingID", `"fire_station"`}, {"goKind", `"fire"`}, {"goServiceID", `"fire-1"`}}, [][2]string{{"tsSpec", `"x"`}, {"x", "1"}, {"y", "2"}}},
		{"services", svc, svcPrefix, "set_population", [][2]string{{"n", "5"}}, nil},
		{"services", svc, svcPrefix, "sample", nil, nil},
		{"finance", fin, finPrefix, "post", [][2]string{{"entries", entry}}, [][2]string{{"description", `"d"`}}},
		{"finance", fin, finPrefix, "begin_month", [][2]string{{"month", "1"}}, nil},
		{"finance", fin, finPrefix, "post_wages", [][2]string{{"total", "1"}}, nil},
		{"finance", fin, finPrefix, "post_household_spend", [][2]string{{"quantity", "1"}, {"price", "1"}}, nil},
		{"finance", fin, finPrefix, "settle_opex", [][2]string{{"opex", "1"}}, nil},
		{"finance", fin, finPrefix, "settle_construction", [][2]string{{"cost", "1"}}, nil},
		{"finance", fin, finPrefix, "service_debt", [][2]string{{"interest", "1"}, {"principal", "1"}}, nil},
		{"finance", fin, finPrefix, "sample", nil, nil},
	}
}

func objectOf(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%q:%s", p[0], p[1])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (s opSpec) runWith(args string) error {
	es := append(s.prefix(), je(1, s.op, args), je(2, "sample", ""))
	_, err := s.run(Journal{Entries: es})
	return err
}

func mustHave(t *testing.T, name string, err error, code, extra string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %s, got nil", name, code)
	}
	if !strings.Contains(err.Error(), code) {
		t.Fatalf("%s: error %q lacks %s", name, err.Error(), code)
	}
	if extra != "" && !strings.Contains(err.Error(), extra) {
		t.Fatalf("%s: error %q lacks %q", name, err.Error(), extra)
	}
}

// Hole 1: a JSON null for a REQUIRED key is the missing-required error.
func TestDecode_NullRequiredKey_EveryDomainEveryOp(t *testing.T) {
	for _, s := range opSpecs() {
		for i, kv := range s.required {
			pairs := append([][2]string{}, s.required...)
			pairs[i] = [2]string{kv[0], "null"}
			name := s.domain + "/" + s.op + "/" + kv[0]
			mustHave(t, name, s.runWith(objectOf(pairs...)), codeMissingArg, kv[0])
		}
	}
}

// A null for an optional key is not a missing key.
func TestDecode_NullOptionalKey_Accepted(t *testing.T) {
	for _, s := range opSpecs() {
		for _, kv := range s.optional {
			pairs := append(append([][2]string{}, s.required...), [2]string{kv[0], "null"})
			if err := s.runWith(objectOf(pairs...)); err != nil && strings.Contains(err.Error(), codeMissingArg) {
				t.Fatalf("%s/%s optional %s null refused as missing: %v", s.domain, s.op, kv[0], err)
			}
		}
	}
}

// Hole 2: a duplicate key is refused, naming the key.
func TestDecode_DuplicateKey_EveryDomainEveryOp(t *testing.T) {
	for _, s := range opSpecs() {
		all := append(append([][2]string{}, s.required...), s.optional...)
		for _, kv := range all {
			pairs := append(append([][2]string{}, all...), kv)
			name := s.domain + "/" + s.op + "/" + kv[0]
			mustHave(t, name, s.runWith(objectOf(pairs...)), codeJournalOpFailed, fmt.Sprintf("duplicate key %q", kv[0]))
		}
	}
}

// Duplicate keys and nulls inside a nested finance post entry.
func TestDecode_FinanceNestedEntry_Strict(t *testing.T) {
	s := opSpecs()[6]
	cases := []struct{ name, entries, code, extra string }{
		{"dup amount", `[{"account":"a","side":"debit","amount":1,"amount":2}]`, codeJournalOpFailed, `duplicate key "amount"`},
		{"null side", `[{"account":"a","side":null,"amount":1}]`, codeMissingArg, "side"},
		{"null amount", `[{"account":"a","side":"debit","amount":null}]`, codeMissingArg, "amount"},
		{"null account", `[{"account":null,"side":"debit","amount":1}]`, codeMissingArg, "account"},
		{"unknown key", `[{"account":"a","side":"debit","amount":1,"amnt":2}]`, codeUnknownArg, "amnt"},
	}
	for _, c := range cases {
		mustHave(t, c.name, s.runWith(`{"entries":`+c.entries+`}`), c.code, c.extra)
	}
	mustHave(t, "entries null", s.runWith(`{"entries":null}`), codeMissingArg, "entries")
}

// Hole 5: pin each op's allowed key set (required + optional) exactly. Every
// key any op accepts, plus a bogus one, is tried on every op: it must be
// refused (MET-H516) unless it is in that op's own set, and accepted
// (never H516) when it is. Widening any allow-list turns this red.
func TestDecode_AllowListsPinnedExactly(t *testing.T) {
	probe := map[string]bool{"zzz": true, "note": true, "Count": true}
	for _, s := range opSpecs() {
		for _, kv := range append(append([][2]string{}, s.required...), s.optional...) {
			probe[kv[0]] = true
		}
	}
	for _, k := range []string{"description", "category", "tsSpec", "x", "y", "n"} {
		probe[k] = true
	}
	for _, s := range opSpecs() {
		own := map[string]bool{}
		for _, kv := range s.required {
			own[kv[0]] = true
		}
		for _, kv := range s.optional {
			own[kv[0]] = true
		}
		base := append([][2]string{}, s.required...)
		for k := range probe {
			if own[k] {
				continue
			}
			name := s.domain + "/" + s.op + "/+" + k
			mustHave(t, name, s.runWith(objectOf(append(append([][2]string{}, base...), [2]string{k, "1"})...)), codeUnknownArg, k)
		}
		for _, kv := range s.optional {
			err := s.runWith(objectOf(append(append([][2]string{}, base...), kv)...))
			if err != nil && strings.Contains(err.Error(), codeUnknownArg) {
				t.Fatalf("%s/%s: own optional key %s refused: %v", s.domain, s.op, kv[0], err)
			}
		}
	}
	// the nested finance entry's own set
	fin := opSpecs()[6]
	base := [][2]string{{"account", `"a"`}, {"side", `"debit"`}, {"amount", "1"}}
	for _, k := range []string{"zzz", "description", "entries", "tsSpec", "x"} {
		args := `{"entries":[` + objectOf(append(append([][2]string{}, base...), [2]string{k, "1"})...) + `]}`
		mustHave(t, "entry+"+k, fin.runWith(args), codeUnknownArg, k)
	}
	args := `{"entries":[` + objectOf(append(append([][2]string{}, base...), [2]string{"category", `"c"`})...) + `]}`
	if err := fin.runWith(args); err != nil && strings.Contains(err.Error(), codeUnknownArg) {
		t.Fatalf("entry category refused: %v", err)
	}
}

// Hole 3: advance days has an upper bound.
func TestPopulationAdvance_DaysBounded(t *testing.T) {
	s := opSpecs()[1]
	mustHave(t, "1e11", s.runWith(`{"days":100000000000}`), codeAdvanceTooLarge, "100000000000")
	mustHave(t, "bound+1", s.runWith(fmt.Sprintf(`{"days":%d}`, maxAdvanceDays+1)), codeAdvanceTooLarge, fmt.Sprint(maxAdvanceDays))
	if maxAdvanceDays < 30*10 {
		t.Fatalf("bound %d too tight for the real 30-day advance", maxAdvanceDays)
	}
	if err := s.runWith(fmt.Sprintf(`{"days":%d}`, maxAdvanceDays)); err != nil {
		t.Fatalf("days at the bound must run: %v", err)
	}
}

// Hole 4: empty buildingID, goKind and goServiceID are all refused at decode
// with the same code.
func TestServices_EmptyIdentifiers_Refused(t *testing.T) {
	s := opSpecs()[3]
	for i, kv := range s.required {
		pairs := append([][2]string{}, s.required...)
		pairs[i] = [2]string{kv[0], `""`}
		mustHave(t, "empty "+kv[0], s.runWith(objectOf(pairs...)), codeJournalOpFailed, kv[0]+" must not be empty")
	}
}

// Hole 6: the journal entry is decoded strictly.
func TestJournalEntry_UnknownTopLevelField_Refused(t *testing.T) {
	var j Journal
	err := json.Unmarshal([]byte(`{"entries":[{"tik":5,"op":"sample"}]}`), &j)
	mustHave(t, "misspelled tick", err, codeUnknownArg, "tik")
	err = json.Unmarshal([]byte(`{"entries":[{"tick":5,"op":"sample","bogus":1}]}`), &j)
	mustHave(t, "extra field", err, codeUnknownArg, "bogus")
	// "note" is documentation the services fixture carries on purpose.
	if err := json.Unmarshal([]byte(`{"entries":[{"tick":5,"op":"sample","args":{},"note":"hi"}]}`), &j); err != nil {
		t.Fatalf("note must be allowed: %v", err)
	}
	if j.Entries[0].Tick != 5 || j.Entries[0].Op != "sample" {
		t.Fatalf("decoded entry wrong: %+v", j.Entries[0])
	}
	want := []string{"tick", "op", "args", "note"}
	if fmt.Sprint(journalEntryFields) != fmt.Sprint(want) {
		t.Fatalf("allowed top-level fields changed: %v", journalEntryFields)
	}
}

// Every real journal file still decodes strictly.
func TestJournalEntry_RealActionFilesDecode(t *testing.T) {
	for _, p := range []string{populationActionsPath, servicesActionsPath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var j Journal
		if err := json.Unmarshal(b, &j); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(j.Entries) == 0 {
			t.Fatalf("%s: no entries", p)
		}
	}
}
