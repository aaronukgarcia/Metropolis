package converge

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// stripCorrelationSuffix removes a trailing " (correlation: ...)" segment
// from an already fully-rendered errs.E display string (errs.go's
// E.Display(): "[code] msg (correlation: id)"). Used by RunFinanceActionsComposed's
// issue closure to avoid double-wrapping a rejected command's inner error
// inside this bridge's own outer codeActionOpFailed error — see the call
// site's doc comment (F7, independent round r1) for the full history.
func stripCorrelationSuffix(display string) string {
	const marker = " (correlation: "
	if idx := strings.LastIndex(display, marker); idx >= 0 && strings.HasSuffix(display, ")") {
		return display[:idx]
	}
	return display
}

// This file is FEAT-1972079936 Phase-3 inc2's P2 bridge
// (docs/planning/phase3-convergence-plan.md §3): it decodes the SAME
// canonical action list webconsole/test/converge-fixture-emit.mjs drives
// the TS engine.ts reducer from (webconsole/test/converge-fixtures/
// converge-finance-actions.json) and replays the Go-equivalent operations
// as real protocol.Command envelopes into a compose.Wire'd engine — the
// composition root, not FinanceDomain's direct *finance.FinanceAPI drive
// (finance_domain.go, inc1). This is deliberately a SECOND, independent
// path onto the same engine.finance package: inc1's FinanceDomain proves
// the harness machinery against finance's own public API in isolation;
// this file proves the SAME journal shape survives the full composed
// engine (world/citizens/market/consumption/finance/build/attract wiring,
// PhaseDailyTick/PhaseMonthly ordering, the financeHook stub) — which is
// what a real player's action stream actually goes through.

// actionListFile is the on-disk shape of converge-finance-actions.json.
// This struct is intentionally a SUBSET of the JSON's fields — "tsSpec"
// and "note" are TS-only/documentation fields this Go reader ignores
// (encoding/json silently drops unknown-to-it — actually unrecognised
// JSON fields with no matching Go field are simply skipped by
// json.Unmarshal, never an error), so a field this Go bridge has no use
// for does not need a matching Go struct field to round-trip correctly.
type actionListFile struct {
	Actions []actionEntry `json:"actions"`
}

type actionEntry struct {
	Op              string      `json:"op"`
	N               int64       `json:"n"`
	SampleAfterTick int64       `json:"sampleAfterTick"`
	Cell            *actionCell `json:"cell"`
	ZoneType        string      `json:"zoneType"`
}

type actionCell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// loadActionList reads and decodes path (converge-finance-actions.json).
// A missing/unreadable file or malformed JSON returns
// codeActionListDecodeFailed (MET-H504) — never a panic, mirroring
// LoadFixture's own load/decode discipline (fixture.go).
func loadActionList(path string) (actionListFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return actionListFile{}, errs.New(codeActionListDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": err.Error(),
		})
	}
	var f actionListFile
	if err := json.Unmarshal(b, &f); err != nil {
		return actionListFile{}, errs.New(codeActionListDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": err.Error(),
		})
	}
	if len(f.Actions) == 0 {
		return actionListFile{}, errs.New(codeActionListDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": "action list has no \"actions\" entries",
		})
	}
	return f, nil
}

// skippedGoOps records, in order, the ops this bridge could not translate
// to a Go protocol.Command and why — set by RunFinanceActionsComposed so a
// caller (and the AB report) can name the coverage gap explicitly rather
// than the skip being silent (mirrors GR#17's "no silent failure" spirit
// for this harness/tooling package, same discipline as
// applyFinanceJournalOp's codeJournalOpFailed in finance_domain.go).
type SkippedOp struct {
	Index  int
	Op     string
	Reason string
}

// RunFinanceActionsComposed replays actionsPath (converge-finance-actions.json)
// against a freshly composed *core.Engine (compose.Wire, the SAME
// composition root cmd/metropolis and every other real entry point uses —
// GR#20's "no other package calls RegisterPhaseHook for the real modules")
// and returns the resulting finance Trajectory (the single field this
// bridge can read off Composition's public API today: "treasury", from
// Composition.Treasury() — see finance_ab_test.go's field-mapping doc
// comment for why Reserves/Debt/NetWorth are NOT sampled here) plus the
// list of actions this bridge had no Go equivalent for.
//
// Every gameplay op is issued as a REAL protocol.Command through
// e.HandleCommand (the exact synchronous entry point engine.core's own
// command dispatch table uses — internal/engine/core/commands.go), not a
// direct call into engine.build/engine.finance — this is the P2 bridge's
// whole point: prove the canonical action list survives the composed
// engine's actual command-handling path, the same path a live player (or
// EnginePlayer replaying a recorded protocol.Command fixture,
// internal/harness/replay) goes through.
//
// Determinism (GR#21): RunFinanceActionsComposed constructs a fresh Engine
// every call (no shared state across calls) and never reads the wall clock
// — finance_ab_test.go's TestFinanceAB_ComposedRun_Deterministic proves two
// calls over the same actionsPath produce a reflect.DeepEqual Trajectory.
func RunFinanceActionsComposed(actionsPath string) (Trajectory, []SkippedOp, error) {
	list, err := loadActionList(actionsPath)
	if err != nil {
		return nil, nil, err
	}

	cid := errs.NewCorrelationID()
	e := core.NewEngine(core.WithWorldSeed(1972079936))
	comp, err := compose.Wire(e, &compose.Deps{CorrelationID: cid})
	if err != nil {
		return nil, nil, errs.New(codeActionOpFailed, cid, map[string]any{
			"index": -1, "op": "wire", "reason": err.Error(),
		})
	}

	var traj Trajectory
	var skipped []SkippedOp
	var logicalTick int64
	var tileBought bool

	issue := func(idx int, kind protocol.Kind, payload protocol.CommandPayload) error {
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(fmt.Sprintf("%s-%d", cid, idx)),
			IssuedAtTick:    protocol.Tick(e.TicksCompleted()),
			Kind:            kind,
			Payload:         payload,
		}
		res := e.HandleCommand(cmd)
		if !res.Accepted {
			reason := "rejected with no error detail"
			if res.Error != nil {
				// F7 fix (independent round r1): res.Error.Display is
				// ALREADY a fully-rendered errs.E string, "[code] msg
				// (correlation: id)" (core/commands.go's toErrorRef ->
				// errs.E.Display()). Embedding it verbatim (plus a
				// redundant "res.Error.Code + \": \"" prefix) as the
				// "reason" of a FRESH errs.New(codeActionOpFailed, cid,
				// ...) call double-wrapped both the inner code (once from
				// the manual prefix, once from Display's own brackets) and
				// the correlation ID (once from Display's own suffix, once
				// from this outer error's own — same cid, since one
				// RunFinanceActionsComposed run mints exactly one
				// correlation ID), e.g. "MET-H505: ... op Zone: MET-G501:
				// [MET-G501] ... (correlation: X) (correlation: X)".
				// stripCorrelationSuffix removes the inner Display's own
				// trailing "(correlation: ...)" segment so the ID appears
				// exactly once (from this outer wrap), and the inner
				// "[code] msg" prefix is kept as-is rather than duplicated.
				reason = stripCorrelationSuffix(res.Error.Display)
			}
			return errs.New(codeActionOpFailed, cid, map[string]any{
				"index": idx, "op": string(kind), "reason": reason,
			})
		}
		return nil
	}

	for idx, a := range list.Actions {
		switch a.Op {
		case "advance":
			if a.N <= 0 {
				return nil, nil, errs.New(codeActionOpFailed, cid, map[string]any{
					"index": idx, "op": a.Op, "reason": "\"n\" must be positive",
				})
			}
			if err := issue(idx, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: a.N}); err != nil {
				return nil, nil, err
			}
			logicalTick += a.N
			if a.SampleAfterTick != 0 && a.SampleAfterTick != logicalTick {
				return nil, nil, errs.New(codeActionOpFailed, cid, map[string]any{
					"index": idx, "op": a.Op,
					"reason": fmt.Sprintf("declared sampleAfterTick=%d does not match cumulative logical tick %d", a.SampleAfterTick, logicalTick),
				})
			}
			traj = append(traj, Sample{
				Tick: logicalTick,
				Values: map[string]int64{
					"treasury": comp.Treasury(),
				},
			})

		case "zone":
			if a.Cell == nil {
				return nil, nil, errs.New(codeActionOpFailed, cid, map[string]any{
					"index": idx, "op": a.Op, "reason": "\"zone\" op requires a \"cell\"",
				})
			}
			ref := protocol.CellRef{X: a.Cell.X, Y: a.Cell.Y}
			// Zone requires ownership (engine.build's requireOwned) — Buy the
			// cell first, exactly as a real player would (§7 "player buys
			// before building"; internal/protocol/commands.go's BuyPayload doc
			// comment). Baseline-one's cellFromRef maps EVERY {x,y} cell onto
			// the SAME single world.TileCoord (compose.go: "the whole playable
			// extent is the single start tile"), and world.PurchaseTile grants
			// ownership at TILE granularity, not per-cell — so only the FIRST
			// Buy in this run actually purchases anything; every subsequent
			// Buy targets an already-owned tile and world.PurchaseTile rejects
			// it outright (MET-E404 "already owned"), which is not this
			// bridge's failure to report — it is issued once, tracked via
			// tileBought, mirroring how a real player only buys a tile once.
			if !tileBought {
				if err := issue(idx, protocol.KindBuy, protocol.BuyPayload{Cell: ref}); err != nil {
					return nil, nil, err
				}
				tileBought = true
			}
			if err := issue(idx, protocol.KindZone, protocol.ZonePayload{Cell: ref, ZoneType: a.ZoneType}); err != nil {
				return nil, nil, err
			}

		case "place_utility_ts_only":
			// No Go protocol.Kind exists for a standalone utility-building
			// placement (see converge-finance-actions.json's own note on this
			// op, and this file's doc comment) — engine.build's baseline-one
			// catalogue only exposes the eight ZoneType land-use zones via
			// KindBuild/KindZone, not a distinct power/water generator Kind.
			// Recorded, never silently dropped.
			skipped = append(skipped, SkippedOp{
				Index:  idx,
				Op:     a.Op,
				Reason: "no protocol.Kind covers a standalone utility-building placement in the v1 command vocabulary (internal/protocol/commands.go); engine.build's baseline-one catalogue exposes only the eight ZoneType land-use zones",
			})

		default:
			return nil, nil, errs.New(codeActionOpFailed, cid, map[string]any{
				"index": idx, "op": a.Op, "reason": "unrecognised op name",
			})
		}
	}

	return traj, skipped, nil
}
