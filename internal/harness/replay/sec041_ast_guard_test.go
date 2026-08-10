package replay

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// SEC-041 (Tester-1, 2026-08-10): TestSEC034_RealReplayLoopMatchesTheFixedCopy
// (sec034_differential_test.go) used to be a substring-ORDER grep —
// strings.Index for "len(p.results)" versus "codeReplayTargetClosedEarly"
// — which only proved those two strings appeared in that order anywhere
// in the ctx.Done() branch's text. Tester-1 built a probe showing a
// "fix" that keeps `len(p.results)` textually present but DELETES the
// gating `if` that makes it effective still passed. This file replaces
// that check with a semantic, go/ast-based one: it requires an actual
// `if` statement, linked by variable identity to a `len(...results...)`
// assignment, whose body unconditionally exits (break/return), sitting
// BEFORE the statement that raises codeReplayTargetClosedEarly — not
// merely two substrings appearing in the right order.

// findMethodDecl locates the *ast.FuncDecl for a method with the given
// pointer-receiver type name and method name.
func findMethodDecl(file *ast.File, recvType, methodName string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != methodName || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != recvType {
			continue
		}
		return fn
	}
	return nil
}

// findCtxDoneClause finds the `case <-ctx.Done():` *ast.CommClause
// anywhere inside fn's body (any select statement, at any nesting).
func findCtxDoneClause(fn *ast.FuncDecl) *ast.CommClause {
	var found *ast.CommClause
	ast.Inspect(fn, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		cc, ok := n.(*ast.CommClause)
		if !ok || cc.Comm == nil {
			return true
		}
		exprStmt, ok := cc.Comm.(*ast.ExprStmt)
		if !ok {
			return true
		}
		unary, ok := exprStmt.X.(*ast.UnaryExpr)
		if !ok || unary.Op != token.ARROW {
			return true
		}
		call, ok := unary.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Done" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "ctx" {
			return true
		}
		found = cc
		return false
	})
	return found
}

// identNames returns the set of *ast.Ident names appearing anywhere
// under n.
func identNames(n ast.Node) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(n, func(x ast.Node) bool {
		if id, ok := x.(*ast.Ident); ok {
			names[id.Name] = true
		}
		return true
	})
	return names
}

// mentionsResults reports whether any identifier under n contains
// "results" — the len(p.results)/len(results) shape both production
// code and SEC-041's mutant use, without hardcoding which exact
// variable/selector name carries it.
func mentionsResults(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if id, ok := x.(*ast.Ident); ok && strings.Contains(id.Name, "results") {
			found = true
		}
		return true
	})
	return found
}

// isExitStmt reports whether stmt unconditionally leaves the enclosing
// case without falling through to whatever follows it — a `return` or a
// `break` (labeled or not). This is what makes an `if` a genuine GATE
// rather than decoration: only a body that exits actually stops
// execution from reaching a raise statement placed after it.
func isExitStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.BREAK
	}
	return false
}

func blockHasExit(b *ast.BlockStmt) bool {
	if b == nil {
		return false
	}
	for _, s := range b.List {
		if isExitStmt(s) {
			return true
		}
	}
	return false
}

// checkReplayCtxDoneGuard is SEC-041's semantic replacement for the old
// substring-order check. It inspects fn's `case <-ctx.Done():` clause
// and requires, in this order, among that clause's statements:
//
//  1. an assignment whose right-hand side calls len(...) on something
//     naming "results" (production code's `got := len(p.results)`),
//     recording the assigned variable name(s);
//  2. an `if` statement whose condition references one of those
//     variables AND whose own body unconditionally exits (break/return)
//     — the actual GATE, not just the recheck's bytes being present
//     somewhere in the branch;
//  3. only THEN a statement that raises codeReplayTargetClosedEarly.
//
// Deleting the `if` while leaving `len(p.results)` in place (SEC-041's
// exact mutation) is caught: there is no IfStmt left to find, so step 2
// fails regardless of where the leftover assignment sits relative to
// the raise.
func checkReplayCtxDoneGuard(fn *ast.FuncDecl) error {
	cc := findCtxDoneClause(fn)
	if cc == nil {
		return fmt.Errorf("could not find a `case <-ctx.Done():` branch — the loop this file mirrors has been restructured, so the differential no longer describes production code")
	}

	lenVars := map[string]bool{}
	raiseIdx := -1
	gateIdx := -1

	for i, stmt := range cc.Body {
		if assign, ok := stmt.(*ast.AssignStmt); ok {
			for _, rhs := range assign.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				fnIdent, ok := call.Fun.(*ast.Ident)
				if !ok || fnIdent.Name != "len" || len(call.Args) != 1 {
					continue
				}
				if !mentionsResults(call.Args[0]) {
					continue
				}
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						lenVars[id.Name] = true
					}
				}
			}
		}

		if gateIdx < 0 {
			if ifStmt, ok := stmt.(*ast.IfStmt); ok {
				names := identNames(ifStmt.Cond)
				linked := false
				for v := range lenVars {
					if names[v] {
						linked = true
						break
					}
				}
				if linked && blockHasExit(ifStmt.Body) {
					gateIdx = i
				}
			}
		}

		if raiseIdx < 0 && identNames(stmt)["codeReplayTargetClosedEarly"] {
			raiseIdx = i
		}
	}

	if raiseIdx < 0 {
		return fmt.Errorf("ctx.Done() branch no longer raises codeReplayTargetClosedEarly")
	}
	if gateIdx < 0 {
		return fmt.Errorf("SEC-032 fix missing (SEC-041): no `if <len-of-results recheck>{ break/return }` gate found in the ctx.Done() branch before it raises codeReplayTargetClosedEarly — a disconnected len(p.results) elsewhere in the branch does not satisfy this check")
	}
	if gateIdx > raiseIdx {
		return fmt.Errorf("SEC-032 fix missing: ctx.Done() branch raises codeReplayTargetClosedEarly before its recheck gate runs")
	}
	return nil
}

// mutantSourceGateRemoved reproduces Tester-1's EXACT mutation against
// Replay's ctx.Done() branch: the gating `if got >= want { break
// waitLoop }` is deleted, but the `got := len(p.results)` line is left
// in place — the shape that defeated the old substring-order check
// (strings.Index("len(p.results)") still found before
// strings.Index("codeReplayTargetClosedEarly")).
const mutantSourceGateRemoved = `package replay

import "context"

func (p *EnginePlayer) Replay(ctx context.Context) (*CompareResult, error) {
	want := len(p.commands)
waitLoop:
	for {
		p.mu.Lock()
		got := len(p.results)
		p.mu.Unlock()
		if got >= want {
			break waitLoop
		}
		select {
		case <-p.notify:
		case <-ctx.Done():
			p.mu.Lock()
			got := len(p.results)
			p.mu.Unlock()
			return nil, errs.New(codeReplayTargetClosedEarly, errs.NewCorrelationID(), map[string]any{
				"sent": want, "answered": got,
			})
		}
	}
	return nil, nil
}
`

// TestSEC041_ASTGateAcceptsRealFixedCode proves the AST gate does not
// merely reject everything: parsed against the ACTUAL, shipping
// player_engine.go, checkReplayCtxDoneGuard must find the real SEC-032
// gate and pass.
func TestSEC041_ASTGateAcceptsRealFixedCode(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "player_engine.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse player_engine.go: %v", err)
	}
	fn := findMethodDecl(file, "EnginePlayer", "Replay")
	if fn == nil {
		t.Fatalf("could not find func (*EnginePlayer) Replay in player_engine.go")
	}
	if err := checkReplayCtxDoneGuard(fn); err != nil {
		t.Fatalf("AST gate rejected the real, fixed production code: %v", err)
	}
}

// TestSEC041_ASTGateCatchesGateRemovedMutant is the mandatory proof:
// the AST gate must FAIL against Tester-1's exact mutant (gate removed,
// len(p.results) left present) — reproduced above, not hand-waved —
// and, as a sanity check on the mutant itself, the OLD substring check
// this replaces is shown PASSING that same mutant, so the contrast that
// justifies SEC-041 is part of the test record, not just prose.
func TestSEC041_ASTGateCatchesGateRemovedMutant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "mutant.go", mutantSourceGateRemoved, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse mutant source: %v", err)
	}
	fn := findMethodDecl(file, "EnginePlayer", "Replay")
	if fn == nil {
		t.Fatalf("could not find func (*EnginePlayer) Replay in the mutant source (test setup bug)")
	}

	if err := checkReplayCtxDoneGuard(fn); err == nil {
		t.Fatal("SEC-041: AST gate accepted the exact mutation Tester-1 used (gating `if` removed, `len(p.results)` left textually present) — no better than the substring check it replaces")
	} else {
		t.Logf("AST gate correctly rejected the gate-removed mutant: %v", err)
	}

	// Sanity: the OLD substring check would have PASSED this exact
	// mutant — that contrast is SEC-041's entire finding.
	idx := strings.Index(mutantSourceGateRemoved, "case <-ctx.Done():")
	if idx < 0 {
		t.Fatalf("test setup: mutant source has no ctx.Done() case")
	}
	rest := mutantSourceGateRemoved[idx:]
	oldAlarm := strings.Index(rest, "codeReplayTargetClosedEarly")
	oldRecheck := strings.Index(rest, "len(p.results)")
	if oldAlarm < 0 || oldRecheck < 0 || oldRecheck > oldAlarm {
		t.Fatalf("test setup: mutant no longer reproduces the old check's blind spot (recheck=%d, alarm=%d) — SEC-041's premise no longer holds for this source, update the mutant", oldRecheck, oldAlarm)
	}
	t.Logf("confirmed: the OLD substring-order check (len(p.results) at %d, codeReplayTargetClosedEarly at %d, within the ctx.Done() branch) would have PASSED this mutant — this is exactly what SEC-041 reported", oldRecheck, oldAlarm)
}
