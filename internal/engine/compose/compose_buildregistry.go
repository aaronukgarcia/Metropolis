package compose

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// compose_buildregistry.go closes the BUG-734 gap this lane owns WITHOUT
// touching compose.go (BUG-720's lane owns that file): engine.build now
// exposes a completion feed — [build.BuildAPI.CompletedBuildings], returning
// each completed order's catalogue kind (BuildOrder.BuildingID) alongside
// its real submission identity (BuildOrder.ID — UNCHANGED, the exact same
// value [build.BuildAPI.Queue] would show for the same order) and a
// SEPARATE cursor axis, BuildOrder.CompletionSeq (per the lead's ruling on
// this round's own F1 follow-up: ID means the submission id in EVERY
// accessor, never overloaded — see CompletedBuildings' own doc comment) —
// but nothing yet calls RegisterCemetery/RegisterCrematorium from that feed
// on the live player path — BUG-720's own description of the exact gap this
// closes ("compose calls only deathservices intake, never
// RegisterCemetery/Crematorium").
//
// registerCompletedServiceBuildings is that missing call, packaged as a
// small, independently-testable, exported helper this file owns rather than
// compose.go. It is deliberately NOT wired to compose's tick loop yet — see
// the "One-line hook" note below for exactly where BUG-720's lane (or the
// lead) plugs it in.
//
// # Catalogue ids, not a ServiceKind edge (a deliberate GR#25 boundary)
//
// FEAT-build-services-bridge-2026-09-02's BuildingEntry.ServiceKind field
// (data/buildings.json) is how engine.build discovers an engine.services
// registration at completion — but data/buildings.json's own "cemetery" and
// "crematorium" entries carry NO serviceKind (verified against the live
// data file): engine.deathservices is a SEPARATE registered edge
// (engine.build -> engine.deathservices is NOT itself a registered
// code.json edge — only feat.compositionroot -> engine.build and
// feat.compositionroot -> engine.deathservices are), so this helper lives in
// engine.compose, the one package both edges legitimately terminate at,
// exactly like registerServiceLocked's engine.services call lives inside
// engine.build itself for ITS registered edge. Dispatch here is therefore
// on BuildOrder.BuildingID's catalogue id directly ("cemetery" /
// "crematorium"), the same identifier data/buildings.json already uses as
// its stable primary key — not a re-derived or hand-invented classification.
//
// # Capacity: RegisterCemetery, not RegisterCemeteryWithCapacity (documented deviation)
//
// The BUG-734 brief asked for RegisterCemeteryWithCapacity fed from
// data/buildings.json's capacityRaw ("2k plots"). This helper deliberately
// calls [deathservices.DeathServicesAPI.RegisterCemetery] instead — the
// production, data-sourced path — because:
//
//  1. BuildingEntry.CapacityRaw is explicitly documented (buildings.go) as
//     preserved VERBATIM prose, "rather than force-parsed into a single
//     numeric field", precisely because Part IV's cost/capacity columns mix
//     flat numbers, per-km rates, ranges, and non-numeric text that do not
//     share one numeric shape — no parser for it exists anywhere in this
//     codebase (grep-verified), and hand-rolling one just for "2k plots" ->
//     2000 here would be exactly the fragile, unregistered re-derivation
//     GR#15 exists to prevent, not an application of it.
//  2. deathservices.json already owns the real GR#15 source of truth for
//     plot capacity (cfg.GraveyardPlotCapacity(), a single citywide seed
//     figure — RegisterCemetery reads it directly), so re-deriving the same
//     number from buildings.json's prose column would be a SECOND,
//     drift-prone source for a value that must stay single-sourced (GR#3).
//  3. RegisterCemeteryWithCapacity's own doc comment (cemetery.go) already
//     documents it as "[RegisterCemetery]'s test-facing sibling... production
//     callers always go through RegisterCemetery" — using the test-facing
//     override in the one real production call site would contradict that
//     package's own documented contract.
//
// RegisterCrematorium takes no capacity parameter at all (its daily
// throughput is likewise sourced from cfg, in crematory.go), so no analogous
// deviation exists there.
//
// # Idempotency across replay
//
// RegisterCemetery/RegisterCrematorium are both already idempotent
// no-ops on a re-registration of the SAME id (cemetery.go/crematory.go's own
// doc comments) — this helper relies on that rather than tracking its own
// dedupe set, so calling it twice with an overlapping (or even fully
// identical) completions slice — e.g. a replay re-delivering a
// build.CompletedBuildings window the caller's cursor had not yet advanced
// past — registers each real building exactly once, never erroring on the
// repeat.
//
// # One-line hook (documented for BUG-720's lane / the lead)
//
// Call this once per tick, in compose.go's deathServicesHook.Run (or
// wherever BUG-720's lane wires RegisterCemetery/Crematorium/Bury/Cremate/
// RunHearseTransport/Dispense generally), AFTER build's own Tick has run for
// the month and BEFORE deathservices.Intake, threading a persisted
// "buildRegistryCursor BuildOrderID" the same way BUG-689 threads its own
// DeathHandoffSince cursor:
//
//	completions := h.st.build.CompletedBuildings(h.st.buildRegistryCursor)
//	if err := registerCompletedServiceBuildings(completions, h.st.deathServices, h.st.cid); err != nil {
//	    return err // or the hook's existing ErrModuleFailed wrap, matching its sibling calls
//	}
//	for _, c := range completions {
//	    // MUST track CompletionSeq here, NEVER c.ID: a re-round on this
//	    // exact recipe (TestRR2_DocumentedHookRecipeIsTheROUND1DEFECT)
//	    // proved that advancing the cursor by `c.ID` reintroduces the
//	    // original F1 defect verbatim by copy-paste — c.ID is the
//	    // submission id (unrelated to completion order, see
//	    // CompletedBuildings' own doc comment), so a caller that copies
//	    // "if c.ID > cursor { cursor = c.ID }" from an ID-based habit
//	    // silently loses any order whose completion is reordered relative
//	    // to its submission id, exactly the class BUG-734 F1 fixed.
//	    if build.BuildOrderID(c.CompletionSeq) > h.st.buildRegistryCursor {
//	        h.st.buildRegistryCursor = build.BuildOrderID(c.CompletionSeq)
//	    }
//	}
//
// The cursor itself is a plain BuildOrderID-typed scalar holding the highest
// CompletionSeq this method has ever returned — NEVER a real submission
// order id (see CompletedBuildings' own doc for why that axis cannot serve
// as a cursor). Persisting it is a one-field addition to whichever save
// participant already owns simState's scalar cursors (mirroring
// deathservices' own handoffCursor field), left to that lane since it
// touches compose's save-wire surface, not engine.build's.

// serviceBuildingCemetery / serviceBuildingCrematorium are
// data/buildings.json's own stable ids for the two deathservices catalogue
// entries (verified against the live data file) — not a re-derived
// classification, just this file's local names for the literals so the
// switch below reads as intent rather than magic strings.
const (
	serviceBuildingCemetery    = "cemetery"
	serviceBuildingCrematorium = "crematorium"
)

// cemeteryInstanceID / crematoriumInstanceID derive a per-structure
// registration id from a completed order's real submission identity
// (BuildOrder.ID — the SAME value [build.BuildAPI.Queue] would show for the
// same order; CompletedBuildings never overloads ID, see its doc comment),
// mirroring engine.build's own registerServiceLocked precedent for
// engine.services ("build-order-%d") exactly, so a cemetery and a
// crematorium built as different orders never collide even though both
// derive from the same BuildOrderID space.
func cemeteryInstanceID(orderID build.BuildOrderID) string {
	return fmt.Sprintf("build-order-%d-cemetery", orderID)
}

func crematoriumInstanceID(orderID build.BuildOrderID) string {
	return fmt.Sprintf("build-order-%d-crematorium", orderID)
}

// registerCompletedServiceBuildings registers every completed cemetery/
// crematorium in completions with ds (BUG-734). completions is expected to
// be a [build.BuildAPI.CompletedBuildings] result — a plain zone order (an
// EMPTY BuildingID) is silently skipped, exactly like engine.build's own
// AC-7 leniency for an unrecognised/absent BuildingID.
//
// A nil ds is treated as "deathservices not wired yet" and is a no-op,
// not an error — mirroring engine.build's own SetServices/registerService
// optionality (registerServiceLocked's doc) rather than hard-failing a tick
// whose composition simply has not wired this dependency yet. correlationID
// is threaded through to every deathservices call (GR#1).
//
// F4 (round finding, P3): only a COMPLETE order is ever registered.
// completions is documented as a [build.BuildAPI.CompletedBuildings] result
// (which already guarantees this), but the independent round showed that
// nothing in THIS function enforced it — a caller mistakenly passing a
// Queue() slice (containing in-progress orders) would open a cemetery for a
// building site that has not finished. The guard makes the function safe by
// construction rather than by caller discipline, at zero cost to every
// correct caller.
//
// F3 (round finding, P3, GR#17): a NON-EMPTY BuildingID that names neither
// serviceBuildingCemetery nor serviceBuildingCrematorium — a renamed/typo'd
// data/buildings.json id, or a future catalogue entry this helper does not
// yet know — is recorded via a discarded [ErrUnknownDeathServiceBuildingKind]
// (GR#17: every monitoring failure also writes a registry error) rather than
// vanishing with zero observable trace, which is what the independent
// round's own reproduction found (four near-miss ids dropped silently). This
// is deliberately NOT returned as an error from the function itself — a
// single unrecognised id in a batch must not abort every OTHER recognised
// completion in the same call (the same "one bad id doesn't sink the whole
// batch" principle RegisterCemetery/RegisterCrematorium's own idempotent
// contract already relies on). Confirmed observable via [errs.Recent] —
// see TestRegisterCompletedServiceBuildings_UnknownKindIsObservableViaErrsRecent
// — construct/logEntry (internal/foundation/errs/log.go) unconditionally
// persists every constructed *E to the configured sink or the in-memory
// ring buffer, regardless of whether the caller keeps the returned value.
//
// P3 doc note (round-2 follow-up): internal/foundation/errs' ring buffer
// COALESCES consecutive occurrences of the SAME Code into one Entry with
// Repeat incremented, reflecting the MOST RECENT occurrence's Ctx, not the
// first (ringBuffer.push's own doc comment) — this is a Logger-agnostic,
// in-memory-ring-only behaviour, not something this helper controls. A tick
// that skips MANY unknown BuildingIDs in one call (e.g. 50 differently-typo'd
// ids in a single batch) therefore surfaces as ONE MET-G815 slot in
// [errs.Recent] with Repeat=49 and Ctx["buildingID"]/Ctx["orderID"] naming
// only the LAST one seen — every earlier occurrence's specific
// buildingID/orderID is not individually retained in the ring (a
// FILE-backed Logger, if one is configured, has no such coalescing and logs
// every occurrence in full). This is an accepted, documented tradeoff, not
// a fix target: the per-occurrence detail would need either a distinct Code
// per unknown id (unregistrable — GR#7 requires a fixed, finite registry)
// or restructuring this loop to aggregate every skipped id into one batched
// Ctx field, which the round judged not cheap enough to be worth doing here
// versus simply knowing the shape (a human reading the log sees "this
// system is producing unknown-kind skips", which is the operationally
// actionable fact; recovering every distinct offending id is a job for the
// FILE-backed Logger path, which does not coalesce).
func registerCompletedServiceBuildings(completions []build.BuildOrder, ds *deathservices.DeathServicesAPI, correlationID string) error {
	if ds == nil {
		return nil
	}
	for _, c := range completions {
		if c.Status != build.OrderComplete {
			continue
		}
		switch c.BuildingID {
		case serviceBuildingCemetery:
			if err := ds.RegisterCemetery(cemeteryInstanceID(c.ID), correlationID); err != nil {
				return err
			}
		case serviceBuildingCrematorium:
			if err := ds.RegisterCrematorium(crematoriumInstanceID(c.ID), correlationID); err != nil {
				return err
			}
		case "":
			// The plain zone-order case — not a service building at all,
			// never a skip worth recording (AC-7's leniency, mirrored).
		default:
			_ = errs.New(ErrUnknownDeathServiceBuildingKind, correlationID, map[string]any{
				"buildingID": c.BuildingID,
				"orderID":    uint64(c.ID),
			})
		}
	}
	return nil
}
