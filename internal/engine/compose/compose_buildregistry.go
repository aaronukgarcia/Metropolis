package compose

import (
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// compose_buildregistry.go closes the BUG-734/BUG-743 gap this lane owns,
// keeping compose.go's own diff to the single documented one-line call
// buildHook.ApplyEffect makes into runDeathServiceBuildingRegistry below
// (BUG-720's lane owns compose.go's other wiring): engine.build exposes a
// completion feed — [build.BuildAPI.CompletedBuildings], returning each
// completed order's catalogue kind (BuildOrder.BuildingID) alongside its
// real submission identity (BuildOrder.ID — UNCHANGED, the exact same value
// [build.BuildAPI.Queue] would show for the same order) and a SEPARATE
// cursor axis, BuildOrder.CompletionSeq (per the lead's ruling on this
// round's own F1 follow-up: ID means the submission id in EVERY accessor,
// never overloaded — see CompletedBuildings' own doc comment) — and a
// demolition feed, [build.BuildAPI.DemolishedSince], the exact mirror for
// the opposite direction of the build/demolish lifecycle.
//
// registerCompletedServiceBuildings / unregisterDemolishedServiceBuildings
// are the two calls that turn those feeds into real RegisterCemetery/
// RegisterCrematorium/UnregisterCemetery/UnregisterCrematorium calls —
// BUG-720's own description of the exact gap this closes ("compose calls
// only deathservices intake, never RegisterCemetery/Crematorium").
// runDeathServiceBuildingRegistry (below) is the live wiring: called once
// per day-tick from buildHook.ApplyEffect (compose.go), it is WHERE
// BUG-734/BUG-743 actually reach the tick — see its own doc comment for the
// two-cursor recipe.
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
// # Two-cursor daily hook — WIRED (BUG-743)
//
// runDeathServiceBuildingRegistry (below) is called once per day-tick from
// compose.go's buildHook.ApplyEffect, piggybacked on the EXISTING daily
// build hook (the exact same "one more call after Tick" pattern
// registerLeisureVenues already uses for its own build->leisure bridge) —
// deliberately NOT a new core.PhaseHook (which would change PhaseHookCount
// and invalidate the CI perf baseline). AFTER build's own Tick has run for
// the day and BEFORE deathservices' own monthly Intake/daily run hooks —
// completions FIRST, then demolitions, threading TWO persisted cursors on
// simState ("buildRegistryCursor" and "buildDemolitionCursor", both
// build.BuildOrderID, compose.go's own field docs) the same way BUG-689
// threads its own DeathHandoffSince cursor. Registering a completion before
// processing that SAME tick's demolitions matters when a structure is both
// completed and demolished before either cursor is ever polled (the
// build-then-demolish-before-poll shape BUG-743's own test suite pins): a
// registration for an already-demolished order is a harmless idempotent
// no-op (RegisterCemetery/RegisterCrematorium's own contract), whereas
// processing demolitions BEFORE completions could unregister a building
// that has not yet been (re-)registered this tick, if ever.
//
// Both cursors are plain build.BuildOrderID-typed scalars holding the
// highest CompletionSeq/DemolitionSeq this method has ever consumed — NEVER
// a real submission order id (see CompletedBuildings'/Demolition's own docs
// for why that axis cannot serve as either cursor); on any error from
// EITHER dispatch call, that half's cursor (and roster mutation — see
// below) is deliberately NOT advanced, so the same completions/demolitions
// are retried on the very next day-tick rather than silently skipped.
// Persisted via composeLedgerParticipant (compose_ledger_participant.go).
//
// Beyond registering/unregistering with engine.deathservices, this also
// maintains simState's OWN deathServiceBridgeCemeteryIDs/
// deathServiceBridgeCrematoriumIDs rosters (compose.go's own field doc,
// deliberately SEPARATE from the Wire-time deathServiceCemeteryIDs/
// deathServiceCrematoriumIDs stopgap roster BUG-720 introduced — see that
// field's own doc for why a Load must be able to rewind THIS roster without
// ever touching the stopgap one) — the sorted id lists
// deathServicesRunHook's daily RunHearseTransport/Cremate sweep iterates
// alongside the stopgap roster. Without this, a crematorium built and
// registered via THIS bridge would never actually cremate anything:
// deathservices exposes no enumeration accessor of its own, so a roster
// entry is the ONLY way the daily run loop discovers a live crematorium/
// cemetery id to drive.
func (st *simState) runDeathServiceBuildingRegistry() {
	if st.buildAPI == nil {
		return
	}

	completions := st.buildAPI.CompletedBuildings(st.buildRegistryCursor)
	if err := registerCompletedServiceBuildings(completions, st.deathServices, st.cid); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "deathservices", "op": "registerCompletedServiceBuildings", "cause": err.Error()})
	} else {
		for _, c := range completions {
			switch c.BuildingID {
			case serviceBuildingCemetery:
				st.deathServiceBridgeCemeteryIDs = addSortedUniqueString(st.deathServiceBridgeCemeteryIDs, cemeteryInstanceID(c.ID))
			case serviceBuildingCrematorium:
				st.deathServiceBridgeCrematoriumIDs = addSortedUniqueString(st.deathServiceBridgeCrematoriumIDs, crematoriumInstanceID(c.ID))
			}
			// MUST track CompletionSeq here, NEVER c.ID: a re-round on this
			// exact recipe (TestRR2_DocumentedHookRecipeIsTheROUND1DEFECT)
			// proved that advancing the cursor by `c.ID` reintroduces the
			// original F1 defect verbatim by copy-paste — c.ID is the
			// submission id (unrelated to completion order, see
			// CompletedBuildings' own doc comment), so tracking it instead
			// silently loses any order whose completion is reordered
			// relative to its submission id, exactly the class BUG-734 F1
			// fixed.
			if build.BuildOrderID(c.CompletionSeq) > st.buildRegistryCursor {
				st.buildRegistryCursor = build.BuildOrderID(c.CompletionSeq)
			}
		}
	}

	demolitions := st.buildAPI.DemolishedSince(st.buildDemolitionCursor)
	if err := unregisterDemolishedServiceBuildings(demolitions, st.deathServices, st.cid); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "deathservices", "op": "unregisterDemolishedServiceBuildings", "cause": err.Error()})
	} else {
		for _, d := range demolitions {
			switch d.BuildingID {
			case serviceBuildingCemetery:
				st.deathServiceBridgeCemeteryIDs = removeSortedString(st.deathServiceBridgeCemeteryIDs, cemeteryInstanceID(d.OrderID))
			case serviceBuildingCrematorium:
				st.deathServiceBridgeCrematoriumIDs = removeSortedString(st.deathServiceBridgeCrematoriumIDs, crematoriumInstanceID(d.OrderID))
			}
			// Same discipline, mirrored EXACTLY: track d.DemolitionSeq,
			// NEVER d.OrderID — DemolitionSeq is BUG-743's own separate
			// monotonic axis (Demolition.DemolitionSeq's doc), unrelated to
			// submission order for the identical reason CompletionSeq is
			// unrelated to it.
			if build.BuildOrderID(d.DemolitionSeq) > st.buildDemolitionCursor {
				st.buildDemolitionCursor = build.BuildOrderID(d.DemolitionSeq)
			}
		}
	}
}

// addSortedUniqueString inserts id into the already-sorted ids slice at its
// correct position, or returns ids unchanged if id is already present
// (idempotent — a redelivered/replayed registration for an id already on
// the roster is a no-op here, mirroring RegisterCemetery/
// RegisterCrematorium's own idempotency one layer up). GR#21: a plain
// binary-search insert into an already-deterministic slice, never a map.
func addSortedUniqueString(ids []string, id string) []string {
	i := sort.SearchStrings(ids, id)
	if i < len(ids) && ids[i] == id {
		return ids
	}
	ids = append(ids, "")
	copy(ids[i+1:], ids[i:])
	ids[i] = id
	return ids
}

// removeSortedString removes id from the already-sorted ids slice, or
// returns ids unchanged if id is not present (idempotent — a redelivered
// demolition for an id already removed from the roster, or one that was
// never on it, is a no-op here).
func removeSortedString(ids []string, id string) []string {
	i := sort.SearchStrings(ids, id)
	if i < len(ids) && ids[i] == id {
		return append(ids[:i], ids[i+1:]...)
	}
	return ids
}

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

// unregisterDemolishedServiceBuildings deregisters every demolished
// cemetery/crematorium in demolitions from ds (BUG-743): the demolition-side
// mirror of registerCompletedServiceBuildings, closing the gap BUG-734 left
// open — engine.build/engine.deathservices got UnregisterCemetery/
// UnregisterCrematorium, but nothing yet calls them from a bulldoze on the
// live player path. demolitions is expected to be a
// [build.BuildAPI.DemolishedSince] result — a plain zone order (an EMPTY
// BuildingID) is already excluded by that method's own contract, mirrored
// here defensively exactly like registerCompletedServiceBuildings' own "" case.
//
// A nil ds is a no-op, not an error — the same "not wired yet" stance
// registerCompletedServiceBuildings takes for the completion side.
// correlationID is threaded through to every deathservices call (GR#1).
//
// # Idempotency across replay and across a never-registered id (BUG-743)
//
// Unlike RegisterCemetery/RegisterCrematorium, UnregisterCemetery/
// UnregisterCrematorium are documented as NOT idempotent-as-success: naming
// an id that is not currently registered (never registered at all, or
// already removed by an earlier call) returns ErrUnknownCemetery/
// ErrUnknownCrematorium rather than silently succeeding (cemetery.go's own
// doc — "a programming error worth surfacing", GR#1's stance for THAT
// package's own direct callers). But a demolition feed's own idempotency
// contract ([build.BuildAPI.DemolishedSince]'s doc: "calling it twice with
// the SAME cursor... returns the identical result") means THIS caller can
// legitimately see the SAME demolition delivered twice — a re-delivered
// window from a caller that has not yet advanced its persisted cursor, or a
// demolition for a building this helper's own caller never actually
// registered in the first place (e.g. a save written before BUG-734/BUG-743
// existed). Hard-failing the whole batch on that shape would make a replay
// unsafe, exactly the same class of problem
// registerCompletedServiceBuildings' RegisterCemetery/RegisterCrematorium
// idempotency already avoids on the completion side. So THIS helper — not
// engine.deathservices, whose own direct-caller contract is deliberately
// unchanged — catches ErrUnknownCemetery/ErrUnknownCrematorium and converts
// it into a logged, GR#17-observable no-op ([ErrDemolitionAlreadyDeregistered])
// rather than either silently swallowing it (losing the "this callback
// discovered a stale id" fact) or propagating it as a hard error.
//
// Any OTHER error (an unexpected engine.deathservices failure unrelated to
// the "already gone" case) still propagates verbatim — this helper narrows
// its tolerance to exactly the one documented idempotency shape, never a
// blanket swallow.
func unregisterDemolishedServiceBuildings(demolitions []build.Demolition, ds *deathservices.DeathServicesAPI, correlationID string) error {
	if ds == nil {
		return nil
	}
	for _, d := range demolitions {
		switch d.BuildingID {
		case serviceBuildingCemetery:
			err := ds.UnregisterCemetery(cemeteryInstanceID(d.OrderID), correlationID)
			if err == nil {
				continue
			}
			if !isUnknownDeathServiceRegistration(err) {
				return err
			}
			_ = errs.New(ErrDemolitionAlreadyDeregistered, correlationID, map[string]any{
				"buildingID": d.BuildingID,
				"orderID":    uint64(d.OrderID),
			})
		case serviceBuildingCrematorium:
			err := ds.UnregisterCrematorium(crematoriumInstanceID(d.OrderID), correlationID)
			if err == nil {
				continue
			}
			if !isUnknownDeathServiceRegistration(err) {
				return err
			}
			_ = errs.New(ErrDemolitionAlreadyDeregistered, correlationID, map[string]any{
				"buildingID": d.BuildingID,
				"orderID":    uint64(d.OrderID),
			})
		case "":
			// The plain zone-order case — DemolishedSince already excludes
			// this, but mirror registerCompletedServiceBuildings' own
			// defensive "" branch rather than assuming.
		default:
			_ = errs.New(ErrUnknownDeathServiceBuildingKind, correlationID, map[string]any{
				"buildingID": d.BuildingID,
				"orderID":    uint64(d.OrderID),
			})
		}
	}
	return nil
}

// isUnknownDeathServiceRegistration reports whether err is
// deathservices.ErrUnknownCemetery or deathservices.ErrUnknownCrematorium —
// the ONE class of UnregisterCemetery/UnregisterCrematorium failure this
// helper treats as an idempotent no-op rather than a hard failure (see
// unregisterDemolishedServiceBuildings' own doc for why).
func isUnknownDeathServiceRegistration(err error) bool {
	e, ok := err.(*errs.E)
	if !ok {
		return false
	}
	return e.Code == deathservices.ErrUnknownCemetery || e.Code == deathservices.ErrUnknownCrematorium
}
