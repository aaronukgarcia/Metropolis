package compose

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/gameinit"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// compose_gameinit_test.go — BUG-737 (FEAT-143 compose wiring): the
// composition-root end of the seam gameinit/doc.go's own "Composition
// seam" section documents. Every other package's own tests already prove
// gameinit.GameInit and finance.ModeGate/unlimitedLocked are individually
// correct (internal/engine/gameinit, internal/engine/finance's own
// attack_feat143_round_test.go); what was MISSING, and is BUG-737's whole
// point, is that Wire never actually called gameinit.New/SetModeGate at
// all. These tests are therefore integration-shaped: they drive the real
// composed Wire path with Deps.GameMode, never a hand-rolled ModeGate
// double.

const gameInitTestSeed = uint64(737_143)

// buildGameInitComposition wires a fresh composed engine at a fixed seed
// with the given GameMode ("" / "real" / "unlimited" / an invalid value).
func buildGameInitComposition(t *testing.T, gameMode string) (*core.Engine, *Composition, error) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(gameInitTestSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{GameMode: gameMode})
	return e, comp, err
}

// testInitialTreasury is BUG-737's round finding P3 (GR#3) fix: compose.go
// no longer carries its own initialTreasury Go literal at all (removed
// entirely, see that file's own doc comment) — Wire's opening-treasury
// seeding reads gi.StartingCapitalMicropounds() from data/gameinit.json
// directly. Every test that used to assert against the removed constant
// now derives the SAME expected figure from the SAME data file this
// helper loads, so there is exactly one source of truth for the number,
// never two that could silently drift apart.
func testInitialTreasury(t *testing.T) int64 {
	t.Helper()
	cfg, err := gameinit.LoadDefaultConfig(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("gameinit.LoadDefaultConfig: %v", err)
	}
	return cfg.StartingCapitalMicropounds()
}

// TestComposeGameInit_DefaultRealModeUnchanged proves Deps.GameMode's
// zero value ("", every pre-BUG-737 Wire caller/test) reproduces prior
// behaviour byte-for-byte: real mode, and the treasury seeded from
// data/gameinit.json equals the pre-wiring initialTreasury literal
// exactly (the data file was deliberately aligned to that value — see
// its own disclosure comment).
func TestComposeGameInit_DefaultRealModeUnchanged(t *testing.T) {
	wantTreasury := testInitialTreasury(t)

	_, comp, err := buildGameInitComposition(t, "")
	if err != nil {
		t.Fatalf("Wire(GameMode=\"\"): %v", err)
	}
	if got := comp.GameMode(); got != "real" {
		t.Fatalf("GameMode() = %q, want %q (empty Deps.GameMode must default to real)", got, "real")
	}
	if comp.Treasury() != wantTreasury {
		t.Fatalf("Treasury() = %d, want %d (data/gameinit.json's startingCapitalMicropounds) — BUG-737's data-sourcing switch must be behaviour-preserving", comp.Treasury(), wantTreasury)
	}

	// Also exercise the explicit "real" string, not just the empty default.
	_, comp2, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(GameMode=\"real\"): %v", err)
	}
	if comp2.Treasury() != wantTreasury {
		t.Fatalf("explicit real-mode Treasury() = %d, want %d", comp2.Treasury(), wantTreasury)
	}
}

// TestComposeGameInit_UnknownModeFailsWire proves AC-1's fail-closed
// contract survives compose's own plain-string seam: an unrecognised
// Deps.GameMode value is never silently coerced to real, it fails Wire
// loudly.
func TestComposeGameInit_UnknownModeFailsWire(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(gameInitTestSeed), core.WithPoolSize(1))
	if _, err := Wire(e, &Deps{GameMode: "bogus-mode"}); err == nil {
		t.Fatal("Wire(GameMode=\"bogus-mode\") succeeded, want a registry-sourced failure naming gameinit/ErrUnknownGameMode")
	}
}

// TestComposeGameInit_UnlimitedWiresFinanceBypass is BUG-737's headline
// proof: Deps.GameMode="unlimited" must ACTUALLY reach financeAPI via
// wireGameInit's SetModeGate call — not merely construct a *GameInit that
// nothing consults. Mirrors finance's own
// TestAttackFEAT143_UnlimitedFundsGoNegative exactly (24 unfunded months,
// treasury goes negative, insolvency never advances), but drives the REAL
// composed financeAPI (comp.state.finance) reached only through Wire's
// own wiring, never a hand-rolled ModeGate double.
func TestComposeGameInit_UnlimitedWiresFinanceBypass(t *testing.T) {
	_, comp, err := buildGameInitComposition(t, "unlimited")
	if err != nil {
		t.Fatalf("Wire(GameMode=\"unlimited\"): %v", err)
	}
	if got := comp.GameMode(); got != "unlimited" {
		t.Fatalf("GameMode() = %q, want %q", got, "unlimited")
	}
	f := comp.state.finance

	before := f.TotalMoneyInCirculation()
	for month := int64(1); month <= 24; month++ {
		if err := f.BeginMonth(month); err != nil {
			t.Fatalf("BeginMonth(%d): %v", month, err)
		}
		// Compose seeds a large opening treasury regardless of mode (see
		// gi.StartingCapitalMicropounds's own doc comment) — the debit must
		// exceed the seeded balance across the run to genuinely drive the
		// account negative, unlike finance's own bare-NewFinanceAPI unit
		// test which starts from zero.
		tx := finance.Transaction{
			Description: "monthly payroll draining well past the seeded treasury (BUG-737 wiring proof)",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: 200_000_000, Category: finance.CatOpex},
				{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: 200_000_000, Category: finance.CatOpex},
			},
		}
		if _, err := f.Post(tx); err != nil {
			t.Fatalf("month %d: Post failed in Unlimited mode — the composed finance bypass did not engage: %v", month, err)
		}
		res := f.RecordMonthResult(false, false)
		if res.ConsecutiveFailedMonths != 0 || res.GameOver {
			t.Fatalf("month %d: insolvency progressed despite Deps.GameMode=\"unlimited\": %+v", month, res)
		}
	}
	after := f.TotalMoneyInCirculation()
	if after != before {
		t.Fatalf("money stock moved %d -> %d across purely balanced internal transfers — the composed unlimited bypass MINTED money", before, after)
	}
	bal, ok := f.AccountBalance(finance.AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(treasury) not found")
	}
	if bal >= 0 {
		t.Fatalf("treasury balance after 24 unfunded months = %d, want negative (a real gate, not a big balance)", bal)
	}
	if f.IsInsolvent() {
		t.Fatalf("IsInsolvent() = true despite Deps.GameMode=\"unlimited\"")
	}
}

// TestComposeGameInit_UnlimitedMoneyPublishedOnlyWhenUnlimited proves
// AC-7: the f2.finance wirePatch's UnlimitedMoney field mirrors the
// composed session's actual locked mode, true only for an unlimited
// session and false for a real one — never omitted (both sessions here
// are fully Wired, so gi is never nil).
func TestComposeGameInit_UnlimitedMoneyPublishedOnlyWhenUnlimited(t *testing.T) {
	assertUnlimitedMoney := func(t *testing.T, mode string, want bool) {
		t.Helper()
		_, comp, err := buildGameInitComposition(t, mode)
		if err != nil {
			t.Fatalf("Wire(GameMode=%q): %v", mode, err)
		}
		raw, err := comp.state.buildFinanceBalanceSheetPatch()
		if err != nil {
			t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
		}
		var patch financeBalanceSheetWirePatch
		if err := json.Unmarshal(raw, &patch); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if patch.UnlimitedMoney == nil {
			t.Fatalf("mode=%q: UnlimitedMoney omitted from the patch, want a published bool", mode)
		}
		if *patch.UnlimitedMoney != want {
			t.Fatalf("mode=%q: UnlimitedMoney = %v, want %v", mode, *patch.UnlimitedMoney, want)
		}
	}
	assertUnlimitedMoney(t, "real", false)
	assertUnlimitedMoney(t, "unlimited", true)
}

// TestComposeGameInit_SaveLoadSameModeRoundTrips proves AC-4: Save
// declares the session's locked mode and a same-mode Load succeeds
// (never a false-positive refusal).
func TestComposeGameInit_SaveLoadSameModeRoundTrips(t *testing.T) {
	for _, mode := range []string{"real", "unlimited"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			_, comp, err := buildGameInitComposition(t, mode)
			if err != nil {
				t.Fatalf("Wire: %v", err)
			}
			dir := t.TempDir()
			if err := comp.Save(dir); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if err := comp.Load(dir); err != nil {
				t.Fatalf("Load (same mode %q) refused, want success: %v", mode, err)
			}
		})
	}
}

// TestComposeGameInit_SaveLoadCrossModeRefused proves AC-5: loading a
// save whose declared mode differs from the loading session's own locked
// mode fails closed with save.ErrGameModeMismatch (MET-E820), before any
// participant state is mutated. Two independently Wired compositions at
// the SAME seed (so the world-seed check does not also fire and mask
// this test's actual subject) — one real, one unlimited — cross-load
// each other's bundle.
func TestComposeGameInit_SaveLoadCrossModeRefused(t *testing.T) {
	_, realComp, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real): %v", err)
	}
	_, unlimitedComp, err := buildGameInitComposition(t, "unlimited")
	if err != nil {
		t.Fatalf("Wire(unlimited): %v", err)
	}

	dir := t.TempDir()
	if err := realComp.Save(dir); err != nil {
		t.Fatalf("Save (real): %v", err)
	}

	err = unlimitedComp.Load(dir)
	if err == nil {
		t.Fatal("unlimited session Load of a real-mode save succeeded, want ErrGameModeMismatch (MET-E820)")
	}
	if !strings.Contains(err.Error(), save.ErrGameModeMismatch) {
		t.Fatalf("Load error = %v, want it to carry %s (ErrGameModeMismatch)", err, save.ErrGameModeMismatch)
	}

	// The reverse direction too: an unlimited save into a real session.
	dir2 := t.TempDir()
	if err := unlimitedComp.Save(dir2); err != nil {
		t.Fatalf("Save (unlimited): %v", err)
	}
	err = realComp.Load(dir2)
	if err == nil {
		t.Fatal("real session Load of an unlimited-mode save succeeded, want ErrGameModeMismatch (MET-E820)")
	}
	if !strings.Contains(err.Error(), save.ErrGameModeMismatch) {
		t.Fatalf("Load error = %v, want it to carry %s (ErrGameModeMismatch)", err, save.ErrGameModeMismatch)
	}
}

// TestComposeGameInit_LoadCallerSuppliedExpectedModeOptionNeverWins is
// round finding P2: save/options.go's own doc comment says there is
// deliberately NO AllowGameModeMismatch escape hatch mirroring
// AllowSeedMismatch — a caller passing their own save.WithExpectedGameMode
// through Composition.Load's opts parameter must never be able to
// resurrect that nonexistent escape hatch via resolveLoadOptions'
// last-write-wins semantics. Proves BOTH directions: a caller-supplied
// option that would (if it won) WRONGLY ALLOW a real mismatch must not
// win, and a caller-supplied option that would (if it won) WRONGLY REFUSE
// a real match must not win either — the composition's own locked mode is
// the only value that is ever actually enforced, regardless of opts.
func TestComposeGameInit_LoadCallerSuppliedExpectedModeOptionNeverWins(t *testing.T) {
	_, realComp, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real): %v", err)
	}
	_, unlimitedComp, err := buildGameInitComposition(t, "unlimited")
	if err != nil {
		t.Fatalf("Wire(unlimited): %v", err)
	}

	// Case A: a real-mode save, loaded by a real-mode session that also
	// passes a caller-supplied WithExpectedGameMode("unlimited") — if
	// that opt won, Load would WRONGLY REFUSE a genuinely matching save.
	dirReal := t.TempDir()
	if err := realComp.Save(dirReal); err != nil {
		t.Fatalf("Save (real): %v", err)
	}
	if err := realComp.Load(dirReal, save.WithExpectedGameMode("unlimited")); err != nil {
		t.Fatalf("Load(real save, real session, caller opt says unlimited) = %v, want SUCCESS — the composition's own real mode must still be the one enforced, not the caller's opt", err)
	}

	// Case B: a real-mode save, loaded by an UNLIMITED-mode session that
	// passes a caller-supplied WithExpectedGameMode("real") crafted to
	// MATCH the bundle — if that opt won, Load would WRONGLY ALLOW a
	// genuine cross-mode load through (the bundle is real, this session
	// is unlimited; the opt's only purpose here is to try to trick the
	// check into agreeing with the bundle instead of with the session).
	err = unlimitedComp.Load(dirReal, save.WithExpectedGameMode("real"))
	if err == nil {
		t.Fatal("Load(real save, unlimited session, caller opt says real) succeeded — the caller-supplied option let a genuine cross-mode load through, resurrecting the escape hatch save/options.go says does not exist")
	}
	if !strings.Contains(err.Error(), save.ErrGameModeMismatch) {
		t.Fatalf("Load error = %v, want it to carry %s (ErrGameModeMismatch)", err, save.ErrGameModeMismatch)
	}
}

// legacyBundleDir saves comp, then hand-edits the resulting bundle's
// save-meta.json to blank out GameMode — simulating a genuine
// pre-FEAT-143 save (this fix's own AC-4 didn't exist yet, so no real
// bundle from that era ever had the field), through the REAL on-disk
// save.List/ReadMeta/WriteMeta surface rather than a synthetic
// hand-rolled save.Context (which the round's own ruling distinguishes
// from "a real save that predates the feature" only in provenance, not
// shape — this helper makes the provenance realistic too).
func legacyBundleDir(t *testing.T, comp *Composition) string {
	t.Helper()
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	summaries, _, err := save.List(dir)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	var bundleDir string
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			bundleDir = s.Path
			break
		}
	}
	if bundleDir == "" {
		t.Fatalf("save.List found no %q bundle under %s", compositionSaveName, dir)
	}
	meta, err := save.ReadMeta(bundleDir)
	if err != nil {
		t.Fatalf("save.ReadMeta: %v", err)
	}
	if meta.GameMode == "" {
		t.Fatal("precondition: comp.Save's own GameMode was already empty before the hand-edit")
	}
	meta.GameMode = ""
	if err := save.WriteMeta(bundleDir, meta); err != nil {
		t.Fatalf("save.WriteMeta: %v", err)
	}
	return dir
}

// TestComposeGameInit_LegacySaveBundleLoadsRealWarnsNeverUnlimited is
// BUG-737's round-2 lead ruling item (2), proven through the REAL
// Composition.Save/Load surface (compose/save_wire.go), not just
// save.Manager directly: a save bundle predating FEAT-143 (no GameMode
// field at all) loads successfully into a REAL session (the
// conservative default) and STILL refuses into an UNLIMITED one — the
// same three-way rule save's own TestGameMode_AbsentModeAssumedRealNeverUnlimited
// proves at the save-package level, exercised here one layer up through
// the actual composition Save/Load path every real caller uses.
func TestComposeGameInit_LegacySaveBundleLoadsRealWarnsNeverUnlimited(t *testing.T) {
	_, realComp, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real): %v", err)
	}
	dir := legacyBundleDir(t, realComp)

	// A fresh REAL session loading the legacy (mode-absent) bundle must
	// SUCCEED (the migration path) — never refuse.
	_, realComp2, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real, second): %v", err)
	}
	if err := realComp2.Load(dir); err != nil {
		t.Fatalf("Load(legacy bundle, real session) refused, want the legacy-migration ACCEPT: %v", err)
	}

	// The SAME legacy bundle loaded into an UNLIMITED session must still
	// refuse — an absent mode is never treated as "matches unlimited".
	_, unlimitedComp, err := buildGameInitComposition(t, "unlimited")
	if err != nil {
		t.Fatalf("Wire(unlimited): %v", err)
	}
	err = unlimitedComp.Load(dir)
	if err == nil {
		t.Fatal("Load(legacy bundle, unlimited session) succeeded, want ErrGameModeMismatch")
	}
	if !strings.Contains(err.Error(), save.ErrGameModeMismatch) {
		t.Fatalf("Load error = %v, want it to carry %s (ErrGameModeMismatch)", err, save.ErrGameModeMismatch)
	}
}

// TestComposeGameInit_LegacySaveBundleRestampedOnNextSave proves the
// "re-stamped with the session mode on its next save" half of the round-2
// ruling: after a real session loads a legacy (mode-absent) bundle, the
// VERY NEXT Save call writes a bundle with GameMode="real" — the gap is
// closed automatically because Composition.Save always writes the
// CURRENT session's own locked mode, never what was loaded.
func TestComposeGameInit_LegacySaveBundleRestampedOnNextSave(t *testing.T) {
	_, seedComp, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real): %v", err)
	}
	dir := legacyBundleDir(t, seedComp)

	_, comp, err := buildGameInitComposition(t, "real")
	if err != nil {
		t.Fatalf("Wire(real): %v", err)
	}
	if err := comp.Load(dir); err != nil {
		t.Fatalf("Load(legacy bundle): %v", err)
	}

	resaveDir := t.TempDir()
	if err := comp.Save(resaveDir); err != nil {
		t.Fatalf("Save (re-stamp): %v", err)
	}
	summaries, _, err := save.List(resaveDir)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	var bundleDir string
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			bundleDir = s.Path
		}
	}
	meta, err := save.ReadMeta(bundleDir)
	if err != nil {
		t.Fatalf("save.ReadMeta: %v", err)
	}
	if meta.GameMode != "real" {
		t.Fatalf("re-saved bundle GameMode = %q, want %q — the legacy gap must close on the very next save", meta.GameMode, "real")
	}
}
