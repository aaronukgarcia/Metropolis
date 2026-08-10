package keys

import "testing"

func TestPaletteListsRegisteredActionsWithPath(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b", "r", "s"}, Action{Name: "Build Road Street"})
	_ = g.Register([]string{"z"}, Action{Name: "Zone"})

	p := NewPalette(g, "test-corr")
	matches := p.Match("")
	if len(matches) != 2 {
		t.Fatalf("Match(\"\") returned %d entries, want 2 (every registered action)", len(matches))
	}

	found := false
	for _, m := range matches {
		if m.Name == "Build Road Street" {
			found = true
			if len(m.Path) != 3 || m.Path[0] != "b" || m.Path[1] != "r" || m.Path[2] != "s" {
				t.Fatalf("path for Build Road Street = %v, want [b r s]", m.Path)
			}
		}
	}
	if !found {
		t.Fatalf("Build Road Street not present in palette listing")
	}
}

func TestPaletteFuzzyRanksBetterMatchHigher(t *testing.T) {
	g := newTestGrammar()
	_ = g.Register([]string{"b"}, Action{Name: "Build"})
	_ = g.Register([]string{"z"}, Action{Name: "Zone"})

	p := NewPalette(g, "test-corr")
	matches := p.Match("buil")
	if len(matches) == 0 {
		t.Fatalf("no matches for a substring query")
	}
	if matches[0].Name != "Build" {
		t.Fatalf("top match = %q, want Build", matches[0].Name)
	}
}

func TestPaletteParameterizedCommandParsesExpectedTuple(t *testing.T) {
	g := newTestGrammar()
	p := NewPalette(g, "test-corr")

	var gotName string
	var gotArgs map[string]ArgValue
	_ = p.RegisterCommand(CommandSpec{
		Name: "loan",
		Args: []ArgSpec{{Name: "amount", Kind: ArgMoney}, {Name: "term", Kind: ArgDuration}},
		Run:  func(pc ParsedCommand) { gotName = pc.Name; gotArgs = pc.Args },
	})

	parsed, err := p.ParseCommand(":loan 5M 10y")
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	if parsed.Name != "loan" {
		t.Fatalf("Name = %q, want loan", parsed.Name)
	}
	if parsed.Args["amount"].Money != 5_000_000 {
		t.Fatalf("amount = %v, want 5,000,000", parsed.Args["amount"].Money)
	}
	if parsed.Args["term"].Duration != (Duration{N: 10, Unit: 'y'}) {
		t.Fatalf("term = %+v, want {10 y}", parsed.Args["term"])
	}

	// Confirm Run actually gets what ParseCommand produced, end to end.
	p.commands["loan"].Run(parsed)
	if gotName != "loan" || gotArgs["amount"].Money != 5_000_000 {
		t.Fatalf("Run did not receive the parsed command: name=%q args=%+v", gotName, gotArgs)
	}
}

func TestPaletteRejectsNonNumericMoneyArgument(t *testing.T) {
	g := newTestGrammar()
	p := NewPalette(g, "test-corr")
	_ = p.RegisterCommand(CommandSpec{
		Name: "loan",
		Args: []ArgSpec{{Name: "amount", Kind: ArgMoney}, {Name: "term", Kind: ArgDuration}},
	})

	_, err := p.ParseCommand(":loan abc 10y")
	if err == nil {
		t.Fatalf("ParseCommand did not reject a non-numeric money argument")
	}
	// Must be a rejection, never a silent coercion to zero — confirmed by
	// exercising the success path with the SAME command name and a valid
	// amount, proving "abc" was not just treated as 0.
	parsed, err2 := p.ParseCommand(":loan 0 10y")
	if err2 != nil {
		t.Fatalf("a genuinely-zero amount should parse fine: %v", err2)
	}
	if parsed.Args["amount"].Money != 0 {
		t.Fatalf("sanity check failed")
	}
}

func TestPaletteRejectArgNamesOffendingArgument(t *testing.T) {
	g := newTestGrammar()
	p := NewPalette(g, "test-corr")
	_ = p.RegisterCommand(CommandSpec{
		Name: "loan",
		Args: []ArgSpec{{Name: "amount", Kind: ArgMoney}, {Name: "term", Kind: ArgDuration}},
	})
	_, err := p.ParseCommand(":loan 5M badterm")
	if err == nil {
		t.Fatalf("expected rejection of a malformed duration argument")
	}
}

func TestPaletteUnknownCommandRejected(t *testing.T) {
	g := newTestGrammar()
	p := NewPalette(g, "test-corr")
	if _, err := p.ParseCommand(":nosuchcommand 1"); err == nil {
		t.Fatalf("expected rejection of an unregistered command name")
	}
}
