package gameinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestAttackFEAT143_NoJSONUnmarshalPathIntoMode: GameInit's fields are all
// unexported, so encoding/json cannot write the mode from outside the
// package. This pins that a Config round-trip through JSON can never
// carry a mode at all (the mode is not part of Config), closing the
// "unmarshal into the struct" mode-change vector of AC-3.
func TestAttackFEAT143_NoJSONUnmarshalPathIntoMode(t *testing.T) {
	g, err := New(ModeReal, testConfig(), "attack-json")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The JSON vector is closed STATICALLY, not just dynamically:
	// staticcheck's SA9005 fires on any json.Unmarshal into *GameInit
	// precisely because the struct has no exported fields and no custom
	// marshaling, so encoding/json can never reach the locked mode. This
	// asserts that same property directly (and would red the moment any
	// field became exported or an UnmarshalJSON method appeared).
	rt := reflect.TypeOf(GameInit{})
	if rt.NumField() == 0 {
		t.Fatalf("GameInit has no fields at all; this probe would be vacuous")
	}
	for i := 0; i < rt.NumField(); i++ {
		if f := rt.Field(i); f.IsExported() {
			t.Errorf("AC-3 RISK: GameInit.%s is EXPORTED — an exported field is a mutation surface reachable by encoding/json and by any caller outside this package", f.Name)
		}
	}
	if _, ok := any(g).(json.Unmarshaler); ok {
		t.Errorf("AC-3 RISK: *GameInit implements json.Unmarshaler — a custom decode path could write the locked mode")
	}

	if got, err := g.Mode("attack-json"); err != nil {
		t.Fatalf("Mode: %v", err)
	} else if got != ModeReal {
		t.Fatalf("AC-3 VIOLATION: json.Unmarshal changed the locked mode to %q", got)
	}
	if unlimited, err := g.Unlimited("attack-json"); err != nil {
		t.Fatalf("Unlimited: %v", err)
	} else if unlimited {
		t.Fatalf("AC-3 VIOLATION: json.Unmarshal flipped Unlimited() to true")
	}

	// The Config accessor returns a value copy; mutating it must not
	// reach the locked instance.
	cfg, err := g.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.Params.StartingCapitalMicropounds.Value = 42
	if got, err := g.StartingCapitalMicropounds("attack-json"); err != nil {
		t.Fatalf("StartingCapitalMicropounds: %v", err)
	} else if got == 42 {
		t.Fatalf("Config() leaked a mutable reference: mutating the returned copy changed the instance")
	}
}

// TestAttackFEAT143_ConfigReloadCannotReMode: Load/LoadDefault always
// construct a NEW *GameInit; there is no reload-into-existing path. This
// asserts that constructing a second GameInit from a mutated data file
// never touches the first one's locked mode.
func TestAttackFEAT143_ConfigReloadCannotReMode(t *testing.T) {
	dir := t.TempDir()
	writeGameInitJSON(t, dir, validGameInitJSON)
	first, err := Load(dir, ModeReal, "attack-reload")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Rewrite the data file, then load again in the other mode.
	writeGameInitJSON(t, dir, validGameInitJSON)
	second, err := Load(dir, ModeUnlimited, "attack-reload-2")
	if err != nil {
		t.Fatalf("Load(2): %v", err)
	}
	firstMode, err := first.Mode("attack-reload")
	if err != nil {
		t.Fatalf("first.Mode: %v", err)
	}
	if firstMode != ModeReal {
		t.Fatalf("AC-3 VIOLATION: a second Load re-moded the first instance to %q", firstMode)
	}
	secondMode, err := second.Mode("attack-reload-2")
	if err != nil {
		t.Fatalf("second.Mode: %v", err)
	}
	if secondMode != ModeUnlimited {
		t.Fatalf("second.Mode() = %q, want unlimited", secondMode)
	}
}

// TestAttackFEAT143_DeterministicWire (determinism): two constructions
// from the same config produce byte-identical GameModeWire()/Mode()/
// StartingCapitalMicropounds() outputs.
func TestAttackFEAT143_DeterministicWire(t *testing.T) {
	dir := t.TempDir()
	writeGameInitJSON(t, dir, validGameInitJSON)
	for _, m := range []Mode{ModeReal, ModeUnlimited} {
		a, err := Load(dir, m, "attack-det-a")
		if err != nil {
			t.Fatalf("Load(a,%q): %v", m, err)
		}
		b, err := Load(dir, m, "attack-det-b")
		if err != nil {
			t.Fatalf("Load(b,%q): %v", m, err)
		}
		aWire, err := a.GameModeWire("attack-det-a")
		if err != nil {
			t.Fatalf("a.GameModeWire: %v", err)
		}
		bWire, err := b.GameModeWire("attack-det-b")
		if err != nil {
			t.Fatalf("b.GameModeWire: %v", err)
		}
		if aWire != bWire {
			t.Fatalf("mode %q: GameModeWire differs %q vs %q", m, aWire, bWire)
		}
		aCapital, err := a.StartingCapitalMicropounds("attack-det-a")
		if err != nil {
			t.Fatalf("a.StartingCapitalMicropounds: %v", err)
		}
		bCapital, err := b.StartingCapitalMicropounds("attack-det-b")
		if err != nil {
			t.Fatalf("b.StartingCapitalMicropounds: %v", err)
		}
		if aCapital != bCapital {
			t.Fatalf("mode %q: starting capital differs %d vs %d", m, aCapital, bCapital)
		}
		aUnlimited, err := a.Unlimited("attack-det-a")
		if err != nil {
			t.Fatalf("a.Unlimited: %v", err)
		}
		bUnlimited, err := b.Unlimited("attack-det-b")
		if err != nil {
			t.Fatalf("b.Unlimited: %v", err)
		}
		if aUnlimited != bUnlimited {
			t.Fatalf("mode %q: Unlimited() differs", m)
		}
	}
}

// TestAttackFEAT143_CopiedGameInitSilentlyReportsRealMode is the round's
// central finding probe, now re-run against the P2-B fix: Mode(),
// Unlimited(), GameModeWire(), and StartingCapitalMicropounds() all return
// (value, error), and every one of them must return the SEC-020 copy-guard
// error on a struct-copied *GameInit rather than silently reporting a
// zero value with no error on any channel — the exact silent re-mode AC-3
// exists to prevent, and the exact failure mode that would previously have
// been threaded into save.Context.GameMode and finance.SetModeGate.
func TestAttackFEAT143_CopiedGameInitSilentlyReportsRealMode(t *testing.T) {
	g, err := New(ModeUnlimited, testConfig(), "attack-copy")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if unlimited, err := g.Unlimited("attack-copy"); err != nil {
		t.Fatalf("g.Unlimited: %v", err)
	} else if !unlimited {
		t.Fatalf("precondition: original must be unlimited")
	}
	cp := gameInitByteCopy(g)

	if _, err := cp.Unlimited("attack-copy"); err == nil {
		t.Errorf("FEAT143 REGRESSION (P2-B): copied *GameInit.Unlimited() returned no error — a copy must never silently report a mode at all")
	}
	if _, err := cp.Mode("attack-copy"); err == nil {
		t.Errorf("FEAT143 REGRESSION (P2-B): copied *GameInit.Mode() returned no error")
	}
	if _, err := cp.GameModeWire("attack-copy"); err == nil {
		t.Errorf("FEAT143 REGRESSION (P2-B): copied *GameInit.GameModeWire() returned no error — an empty wire string with no error is exactly the value that would reach save.Context.GameMode (writing a mode-less bundle) AND save.WithExpectedGameMode (whose fixed check now also refuses an empty expected mode outright — see the save package's paired attack test)")
	}
	if _, err := cp.StartingCapitalMicropounds("attack-copy"); err == nil {
		t.Errorf("FEAT143 REGRESSION (P2-B): copied *GameInit.StartingCapitalMicropounds() returned no error")
	}
	t.Logf("FEAT143 attack: copied *GameInit now returns the SEC-020 copy-guard error from every read accessor instead of a silent zero value")
}

// TestAttackFEAT143_GrepProofIsReal re-runs AC-3's mechanical grep check
// from the round's side, and PROVES the check can fail by planting a
// synthetic mutator source file in a temp dir and scanning it with the
// same regexp the author's test uses.
func TestAttackFEAT143_GrepProofIsReal(t *testing.T) {
	// Positive control: the real package scans clean.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || (len(name) >= 8 && name[len(name)-8:] == "_test.go") {
			continue
		}
		scanned++
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if loc := modeAssignmentOutsideConstructor.FindIndex(b); loc != nil {
			t.Errorf("%s: mode assignment at byte %d", name, loc[0])
		}
	}
	if scanned < 5 {
		t.Fatalf("grep proof scanned only %d production files; the check would be near-vacuous", scanned)
	}

	// Negative control: the regexp DOES catch a real mutator.
	mutator := []byte("package gameinit\n\nfunc (g *GameInit) evil(m Mode) {\n\tg.mode = m\n}\n")
	if loc := modeAssignmentOutsideConstructor.FindIndex(mutator); loc == nil {
		t.Fatalf("AC-3's grep check is VACUOUS: the regexp does not match `g.mode = m`")
	}
	// And it does NOT match the legitimate struct literal.
	literal := []byte("g := &GameInit{mode: mode, cfg: cfg}\n")
	if loc := modeAssignmentOutsideConstructor.FindIndex(literal); loc != nil {
		t.Fatalf("regexp false-positives on the constructor literal at byte %d", loc[0])
	}
	t.Logf("FEAT143 attack: AC-3 grep proof scanned %d production files, positive+negative controls both hold", scanned)
}

// TestAttackFEAT143_CorruptDataFileNeverSilentlyDefaults hammers the
// AC-6 data path with every corruption shape: truncated JSON, wrong
// types, unknown fields, an empty file, a directory in place of the file,
// NaN/Inf, and a huge value. Every one must return an error, never a
// silently-substituted default.
func TestAttackFEAT143_CorruptDataFileNeverSilentlyDefaults(t *testing.T) {
	cases := map[string]string{
		"truncated":       `{"version":1,"meta":{`,
		"empty":           ``,
		"null":            `null`,
		"array":           `[]`,
		"wrongTypeValue":  `{"version":1,"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":"lots","unit":"u","disclosure":"d"}}}`,
		"unknownField":    `{"version":1,"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":1,"unit":"u","disclosure":"d"}},"backdoor":true}`,
		"noVersion":       `{"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":1,"unit":"u","disclosure":"d"}}}`,
		"noMeta":          `{"version":1,"params":{"startingCapitalMicropounds":{"value":1,"unit":"u","disclosure":"d"}}}`,
		"noParams":        `{"version":1,"meta":{"module":"m","bowCode":"b"}}`,
		"noUnit":          `{"version":1,"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":1,"disclosure":"d"}}}`,
		"negativeVersion": `{"version":-1,"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":1,"unit":"u","disclosure":"d"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeGameInitJSON(t, dir, body)
			cfg, err := LoadConfig(dir, "attack-corrupt")
			if err == nil {
				t.Fatalf("corrupt config %q loaded successfully, StartingCapital=%d — must be a registry error, never a silent default", name, cfg.StartingCapitalMicropounds())
			}
		})
	}

	t.Run("directoryInPlaceOfFile", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, FileGameInit), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if _, err := LoadConfig(dir, "attack-dir"); err == nil {
			t.Fatalf("LoadConfig succeeded with a DIRECTORY at gameinit.json")
		}
	})

	// A huge value is accepted by validate (finite + positive) but is
	// truncated by the int64 cast in StartingCapitalMicropounds. Record
	// the actual behaviour rather than assuming.
	t.Run("hugeValueTruncation", func(t *testing.T) {
		dir := t.TempDir()
		writeGameInitJSON(t, dir, `{"version":1,"meta":{"module":"m","bowCode":"b"},"params":{"startingCapitalMicropounds":{"value":1e30,"unit":"u","disclosure":"d"}}}`)
		cfg, err := LoadConfig(dir, "attack-huge")
		if err != nil {
			t.Logf("1e30 rejected at load: %v", err)
			return
		}
		got := cfg.StartingCapitalMicropounds()
		t.Logf("FEAT143 attack: startingCapitalMicropounds=1e30 loads clean and casts to int64 %d", got)
		if got <= 0 {
			t.Errorf("FEAT143 finding (P3): a data file value that passes validate() as finite+positive (1e30) casts to a NON-POSITIVE int64 %d — validate checks the float64, StartingCapitalMicropounds returns the truncated int64, so AC-6's 'finite positive capital' guarantee does not survive the accessor. Validate should bound the value to the int64 range", got)
		}
	})
}

// TestAttackFEAT143_ParseModeSurfaceIsCaseAndSpaceStrict pins that no
// near-miss wire string parses, so a hand-edited save's mode can never
// coerce to a valid mode.
func TestAttackFEAT143_ParseModeSurfaceIsCaseAndSpaceStrict(t *testing.T) {
	for _, s := range []string{"", " ", "real ", " real", "Real", "REAL", "UNLIMITED", "unlimited\n", "real\x00", "true", "0", "1", "sandbox", "none"} {
		if m, err := ParseMode(s, "attack-parse"); err == nil {
			t.Fatalf("ParseMode(%q) succeeded as %q, want ErrUnknownGameMode", s, m)
		}
		if _, err := New(Mode(s), testConfig(), "attack-new"); err == nil {
			t.Fatalf("New(Mode(%q)) succeeded, want ErrUnknownGameMode", s)
		}
	}
}
