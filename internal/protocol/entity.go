package protocol

import (
	"regexp"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// EntityID names one element INSIDE an already-open view's patch — a
// ledger line, a diagram arrow — as distinct from ViewName, which names
// the view/subscription itself (FEAT-042 AC-20). This is a genuinely new
// wire capability: the whole-entity drill cases UI-SPEC §4 describes
// ("a congestion percentage opens that junction") are already reachable
// today via ViewName's existing entity-scoped grammar
// (subscription.go's "junction.14.approaches"/"citizen.482913.detail"
// examples) with zero code change — see AC-19. What the existing grammar
// cannot reach is a single row or arrow buried inside one already-open
// view's Delta.Patch, because those elements are not separate
// subscriptions of their own. EntityID plugs that gap.
//
// EntityID reuses the "typed:id" opaque-reference convention
// InspectEntityPayload.EntityRef and Delta/Event's EntityRefs already
// document informally (commands.go, events.go) — this package validates
// only that an EntityID is a non-empty, well-formed opaque token; it
// never parses or interprets its internal structure. It is a distinct,
// exported, string-representable type (not a plain string) so a
// TargetRef's two fields stay unambiguous at the type level, and so a
// caller cannot silently pass a ViewName where an EntityID belongs (or
// vice versa) without an explicit conversion.
type EntityID string

// entityIDPattern: non-empty, ASCII letters/digits plus the separator
// characters an opaque engine-minted ID convention already uses
// elsewhere in this package (colon for "typed:id" as in EntityRef,
// hyphen/underscore/dot for the id-like values ViewName and
// SubscriptionID already tolerate). Deliberately permissive rather than
// grammar-strict like ViewName — EntityID names an opaque leaf value,
// not a structured path — but it still rejects the empty string and
// whitespace/control characters, which is the failure mode AC-20 exists
// to catch (a silently-empty or unusable target).
var entityIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// ValidateEntityID reports whether id is a non-empty, well-formed
// EntityID (FEAT-042 AC-20). Mirrors ValidateViewName's shape
// (subscription.go): a small, dependency-free, deterministic check any
// caller — this package's own tests, the engine's Subscribe/Delta
// construction path, or ui.dash's DrillTarget constructors — can call
// before trusting an EntityID. On failure it returns a registry-sourced
// error (GR#7, MET-P003 — see codes.go's ErrInvalidEntityID) rather than
// a bare sentinel, since unlike ValidateViewName (a pre-amendment,
// already-shipped check this amendment does not touch), this is new
// validation logic introduced by this amendment and so is held to this
// amendment's own AC-32 registry-error requirement.
func ValidateEntityID(id EntityID) error {
	if !entityIDPattern.MatchString(string(id)) {
		return errs.New(ErrInvalidEntityID, errs.NewCorrelationID(), map[string]any{
			"entityId": string(id),
		})
	}
	return nil
}

// TargetRef is the addressable pair a drill-through target binds to
// (FEAT-042 AC-21): which view (ViewName), and optionally which element
// inside that view's patch (EntityID). ui.dash's DrillTarget and
// ui.alerts' alert-jump-target are expected to embed or losslessly
// convert to/from TargetRef (see AC-22: a consumer that instead defines
// its own independent, parallel struct must carry a reflection-based
// field-parity test proving the two stay in lockstep, exactly as
// int.serializer's Header/headerWire pairing does today).
//
// Both fields are exported and JSON-tagged directly — there is
// deliberately no hand-maintained wire-mirror struct for TargetRef
// itself (FEAT-042 AC-27); if one is ever introduced, the same commit
// introducing it must add the same kind of parity test.
type TargetRef struct {
	// ViewName is the int.protocol view name (ValidateViewName) the
	// target's element lives inside. Never empty for a valid TargetRef.
	ViewName string `json:"viewName"`

	// EntityID optionally names the specific element inside ViewName's
	// patch (a ledger line, a diagram arrow). The zero value ("") means
	// "the whole view" — the pre-amendment, whole-entity drill case
	// AC-19 documents as already-working via ViewName's own grammar.
	EntityID EntityID `json:"entityId,omitempty"`
}

// Valid reports whether t is a resolvable drill target: it has a
// non-empty ViewName. This is the lenient "is there something to resolve
// at all" check callers (ui.dash's AuditDrillCoverage, engine.social's
// category/destination checks) use — grammar strictness is
// ui.dash.NewDrillTarget's/New<Kind>Tile's job at construction time,
// while a zero-value target (e.g. one that slipped in via a corrupt
// profile decode) is exactly the dead end an audit exists to surface.
//
// Lives here (not in ui.dash) because ui.dash.DrillTarget is a type
// ALIAS for TargetRef (FEAT-231 one-DrillTarget-type doctrine,
// architect ruling 2026-09-05) — a method on an alias's target type must
// be declared where the underlying type is defined, and the alias
// automatically carries every method its target type has.
func (t TargetRef) Valid() bool { return t.ViewName != "" }

// Worked example (FEAT-042 AC-23): a view's patch schema wanting its
// ledger lines individually drillable — UI-SPEC §4's "a cash figure
// opens its ledger lines" — would embed a TargetRef{ViewName: "f2.ledger",
// EntityID: "line-42"} alongside each rendered row, and a diagram wanting
// its arrows individually drillable — UI-SPEC §4's own drill-through
// examples generalise the same way for "that junction" — would embed
// TargetRef{ViewName: "f1.viewport", EntityID: "junction-14-approach-2"}
// per arrow. This is illustrative schema guidance for future view
// authors: no currently-shipped view's patch schema is required to adopt
// it by this amendment (see int.protocol.md's Out of scope).
