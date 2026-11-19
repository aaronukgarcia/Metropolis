package main

// FEAT-9995385625: every money figure the terminal game draws is whole pounds,
// a pound sign, a comma every three digits, no pence (Aaron, 2026-11-18: "I need
// a comma in the thousands and drop the .00 so its £123,485,323"). These tests
// read the REAL composed frame at the game's minimum size and scan the source
// for a second money formatter.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

var (
	// A decimal point after a pound figure ("£848484.00", "£1.25").
	moneyPenceRe = regexp.MustCompile(`£[0-9,]*[0-9]\.[0-9]`)
	// Four or more digits in a row straight after a pound sign or "money ":
	// a figure with no thousands comma.
	moneyBareRunRe = regexp.MustCompile(`(£|money )[0-9]{4,}`)
	// "money " followed by a figure that has no pound sign.
	moneyNoPoundRe = regexp.MustCompile(`money [0-9-]`)
)

// moneyFrameFaults lists every line of frame that breaks the money style.
func moneyFrameFaults(frame string) []string {
	var faults []string
	for _, ln := range strings.Split(frame, "\n") {
		if moneyPenceRe.MatchString(ln) || moneyBareRunRe.MatchString(ln) || moneyNoPoundRe.MatchString(ln) {
			faults = append(faults, ln)
		}
	}
	return faults
}

func TestMoneyFrameFaults_DetectsTheOldStyle(t *testing.T) {
	for _, bad := range []string{"money 26851123 | pop 3", "Net  £26851123.25", "x £848484.00", "money -5", "£1234"} {
		if len(moneyFrameFaults(bad)) != 1 {
			t.Errorf("the checker missed the old style in %q", bad)
		}
	}
	for _, good := range []string{"money £26,851,123 | pop 3", "Net  -£1,234", "£0", "£999"} {
		if f := moneyFrameFaults(good); len(f) != 0 {
			t.Errorf("the checker flagged the new style %q: %v", good, f)
		}
	}
}

// The top line and the F2 money screen at 120x30 after two month closes, so
// real figures (nine-digit-capable, negative net) are on screen.
func TestFEAT9995385625_TopLineAndF2ReadAsWholePounds(t *testing.T) {
	r := rcNew(t)
	plAdvance(t, r.w, 60)
	f2 := r.snap("F2 money screen", tcell.KeyF2)
	top := strings.Split(f2, "\n")[0]
	if !regexp.MustCompile(`money £[0-9]{1,3}(,[0-9]{3})*( |$)`).MatchString(top) {
		t.Fatalf("top line has no comma'd pound figure: %q", top)
	}
	if faults := moneyFrameFaults(top); len(faults) != 0 {
		t.Fatalf("top line breaks the money style: %v", faults)
	}
	if !strings.Contains(f2, "£") {
		t.Fatalf("F2 shows no money at all:\n%s", f2)
	}
	if faults := moneyFrameFaults(f2); len(faults) != 0 {
		t.Fatalf("F2 breaks the money style on %d line(s): %q\n%s", len(faults), faults, f2)
	}
	rcSave(t, "money_f2_120x30.txt", f2)
}

// Every registered screen, whole frame, same rule. F3 is drawn after a month
// has closed so the catalogue costs are on screen.
func TestFEAT9995385625_EveryScreenReadsAsWholePounds(t *testing.T) {
	r := rcNew(t)
	plAdvance(t, r.w, 60)
	screens := []struct {
		name string
		key  tcell.Key
	}{
		{"F1", tcell.KeyF1}, {"F2", tcell.KeyF2}, {"F3", tcell.KeyF3}, {"F4", tcell.KeyF4},
		{"F5", tcell.KeyF5}, {"F10", tcell.KeyF10}, {"F12", tcell.KeyF12},
	}
	moneyScreens := 0
	for _, s := range screens {
		f := r.snap("sweep "+s.name, s.key)
		if faults := moneyFrameFaults(f); len(faults) != 0 {
			t.Errorf("%s breaks the money style on %d line(s): %q", s.name, len(faults), faults)
		}
		if strings.Contains(f, "£") {
			moneyScreens++
		}
	}
	if moneyScreens < 2 {
		t.Errorf("only %d screens showed any money; the sweep is not reaching the figures", moneyScreens)
	}
}

// The UI keeps one copy of the money scale (widgets.UnitsPerPound); the engine
// owns the real one. The UI may not import the engine, so this test, which may,
// pins them equal: a future rescale cannot leave the screens silently wrong.
func TestFEAT9995385625_UIScaleMatchesEngineScale(t *testing.T) {
	if int64(finance.MicropoundsPerPound) != widgets.UnitsPerPound {
		t.Fatalf("widgets.UnitsPerPound %d != engine MicropoundsPerPound %d", widgets.UnitsPerPound, int64(finance.MicropoundsPerPound))
	}
}

var (
	// A function whose name says it formats money: any of money, pound, cost
	// or price together with any of format, fmt, text or pretty, in either
	// order, or a name with "pounds", "gbp" or "sterling" in it.
	moneyFuncNameRe = regexp.MustCompile(`(?i)(pounds|gbp|sterling)|((money|pound|cost|price).*(format|fmt|text|pretty))|((format|fmt|text|pretty).*(money|pound|cost|price))`)
)

// moneyFuncAllowed is the explicit, reasoned allow list: a function outside the
// shared file whose name looks like a money formatter but which only delegates
// to it. Keyed by "<file base name>:<function>". Nothing else is allowed.
var moneyFuncAllowed = map[string]string{
	// costText calls widgets.FormatMoneyText, the one strict parser; it formats nothing itself.
	"render.go:costText": "delegates to widgets.FormatMoneyText",
}

// poundLiteralBuildsString reports a string literal holding a pound sign that is
// an argument of a Sprintf/Fprintf/Errorf/Sprint call, or an operand of a +
// concatenation: a money string built from a number outside the shared file.
func poundLiteralBuildsString(body ast.Node) []string {
	var hits []string
	isPound := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "£") {
			return lit.Value, true
		}
		return "", false
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Sprintf", "Fprintf", "Errorf", "Sprint", "Sprintln", "Appendf":
				for _, a := range x.Args {
					if v, ok := isPound(a); ok {
						hits = append(hits, v)
					}
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				for _, e := range []ast.Expr{x.X, x.Y} {
					if v, ok := isPound(e); ok {
						hits = append(hits, v)
					}
				}
			}
		}
		return true
	})
	return hits
}

func TestFEAT9995385625_R2_MoneyScanCatchesWhatItShould(t *testing.T) {
	src := "package p\nimport (\"fmt\"; \"strconv\")\n" +
		"func a(n int) string { return fmt.Sprintf(\"£%d\", n) }\n" +
		"func b(n int) string { return \"£\" + strconv.Itoa(n) }\n" +
		"func c(n int) string { return strconv.Itoa(n) + \"£\" }\n" +
		"func d(n int) string { return \"plain\" }\n"
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && len(poundLiteralBuildsString(fd.Body)) > 0 {
			got = append(got, fd.Name.Name)
		}
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("pound-sign scan flagged %v, want a,b,c", got)
	}
	for _, name := range []string{"formatMoney", "fmtCost", "priceText", "prettyPounds", "moneyFmt", "costString2Text", "FormatPounds", "poundsText"} {
		if !moneyFuncNameRe.MatchString(name) {
			t.Errorf("the name check missed %q", name)
		}
	}
	for _, name := range []string{"drawText", "renderRow", "formatDate", "textWidth"} {
		if moneyFuncNameRe.MatchString(name) {
			t.Errorf("the name check flagged %q", name)
		}
	}
}

// No package under internal/ui or cmd/metropolis other than the shared one may
// declare a function that formats money, or build a money string from a number
// with a pound sign.
func TestFEAT9995385625_NoSecondMoneyFormatter(t *testing.T) {
	shared := filepath.Join("..", "..", "internal", "ui", "widgets", "money.go")
	roots := []string{".", filepath.Join("..", "..", "internal", "ui")}
	scanned := 0
	allowUsed := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Clean(path) == filepath.Clean(shared) {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			file, perr := parser.ParseFile(token.NewFileSet(), path, src, 0)
			if perr != nil {
				return perr
			}
			scanned++
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				key := filepath.Base(path) + ":" + fd.Name.Name
				if moneyFuncNameRe.MatchString(fd.Name.Name) {
					if _, allowed := moneyFuncAllowed[key]; allowed {
						allowUsed[key] = true
					} else {
						t.Errorf("%s declares %s: money is formatted only by internal/ui/widgets/money.go", path, fd.Name.Name)
					}
				}
				if fd.Body == nil {
					continue
				}
				if _, allowed := moneyFuncAllowed[key]; allowed {
					continue
				}
				for _, h := range poundLiteralBuildsString(fd.Body) {
					t.Errorf("%s: %s builds a money string with a pound sign: %s", path, fd.Name.Name, h)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
	for key := range moneyFuncAllowed {
		if !allowUsed[key] {
			t.Errorf("allow entry %s matched no function: remove it", key)
		}
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d production files; the scan is not reaching the code", scanned)
	}
}
