package roads

import (
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// This file is the deterministic auto-naming service (§20). Every auto-name
// is a pure function of (seed, id) via the counter-RNG (foundation.det
// Stream, keyed (worldSeed, id, purpose)) — never Go's undefined map/slice
// iteration order and never an unseeded math/rand (AC-14/AC-16). The Kentish
// toponyms and the class-appropriate road suffixes come from
// data/naming_corpus.json via foundation.data (GR#15/AC-9); the civic/
// infrastructure/transit vocabularies come from data/roads.json's "naming"
// block (GR#15/AC-10). Selection indices are COMPUTED by the stream, so
// there is no "tie" to break — the documented tie-break rule is "the
// deterministic index the counter-RNG yields" (see doc.go).

// nameKey identifies one auto-named object for the player-rename registry
// (AC-11): the object kind plus the (seed, id) the auto-name was derived
// from. A road's key does NOT include its class, so an in-place upgrade
// (which changes the class) never invalidates the name.
type nameKey struct {
	kind ObjectKind
	seed uint64
	id   uint64
}

// suffixesForClass maps a non-numbered RoadClass to its suffix list in
// data/naming_corpus.json's RoadSuffixes table. The two numbered classes
// (urban expressway, motorway) return nil — they use the M-/A- numbering
// scheme, not a place-name+suffix pair (AC-9's documented carve-out).
func suffixesForClass(c RoadClass, s data.RoadSuffixes) []string {
	switch c {
	case ClassAlley:
		return s.Alley
	case ClassGravel:
		return s.Gravel
	case ClassResidentialStreet:
		return s.ResidentialStreet
	case ClassTwoLane:
		return s.TwoLane
	case ClassOneWayPairs:
		return s.OneWayPairs
	case ClassAvenue2Plus2:
		return s.Avenue2Plus2
	case ClassBusLaneVariant:
		return s.BusLaneVariant
	case ClassTramTrackVariant:
		return s.TramTrackVariant
	case ClassDualCarriageway:
		return s.DualCarriageway
	default:
		return nil
	}
}

// autoNameRoad is the pure road auto-name (AC-9): the numbered classes use
// the M-/A- scheme, everything else pairs a Kentish toponym with a
// class-appropriate suffix.
func autoNameRoad(seed, id uint64, class RoadClass, corpus data.NamingCorpus) string {
	if class.numbered() {
		s := det.NewStream(seed, id, 0, "roads.road.number")
		n := s.IntN(9999) + 1
		if class == ClassMotorway {
			return "M" + strconv.FormatInt(n, 10)
		}
		return "A" + strconv.FormatInt(n, 10)
	}
	s := det.NewStream(seed, id, 0, "roads.road.name")
	names := corpus.Categories.RoadPlaceNames
	place := names[s.IntN(int64(len(names)))]
	suffixes := suffixesForClass(class, corpus.Categories.RoadSuffixes)
	suffix := suffixes[s.IntN(int64(len(suffixes)))]
	return place + " " + suffix
}

// autoNameCivic is the pure civic-building auto-name (AC-10): a Kentish
// toponym + a civic-type word (§20's toponym+type fallback — the
// "notable deceased citizen" ranking is escalated to Aaron, see doc.go).
func autoNameCivic(seed, id uint64, corpus data.NamingCorpus, nm namingConfig) string {
	s := det.NewStream(seed, id, 0, "roads.civic.name")
	names := corpus.Categories.RoadPlaceNames
	place := names[s.IntN(int64(len(names)))]
	typ := nm.CivicTypes[s.IntN(int64(len(nm.CivicTypes)))]
	return place + " " + typ
}

// autoNameInfrastructure is the pure infrastructure auto-name (AC-10):
// §20's functional numbering ("Pumping Station No. 3").
func autoNameInfrastructure(seed, id uint64, nm namingConfig) string {
	s := det.NewStream(seed, id, 0, "roads.infra.name")
	typ := nm.InfrastructureTypes[s.IntN(int64(len(nm.InfrastructureTypes)))]
	n := s.IntN(999) + 1
	return typ + " No. " + strconv.FormatInt(n, 10)
}

// autoNameDistrict is the pure district auto-name (AC-10): §20's "real
// local toponyms first" — drawn from the same Kentish corpus. The
// "then generated compounds" refinement is deferred (see doc.go scope).
func autoNameDistrict(seed, id uint64, corpus data.NamingCorpus) string {
	s := det.NewStream(seed, id, 0, "roads.district.name")
	names := corpus.Categories.RoadPlaceNames
	return names[s.IntN(int64(len(names)))]
}

// autoNameTransit is the pure transit-line auto-name (AC-10): §20's
// auto-lettered, auto-coloured lines. Station names are derived by a
// consuming module as "<district/street name> Station" (out of this
// package's scope).
func autoNameTransit(seed, id uint64, nm namingConfig) string {
	s := det.NewStream(seed, id, 0, "roads.transit.name")
	letter := byte('A' + s.IntN(26))
	colour := nm.TransitColours[s.IntN(int64(len(nm.TransitColours)))]
	return "Line " + string(letter) + " (" + colour + ")"
}

// NameRoad returns the deterministic auto-name for a road of the given
// class from (seed, id) — or the player's rename if one was recorded via
// [RoadsAPI.Rename]. The same (seed, id, class) always yields the same
// name; a renamed road's name is never overwritten by a later naming pass
// (AC-9/AC-11). The class is required because it selects the suffix (or the
// M-/A- numbering scheme) for a road that does not yet exist in the graph.
//
// Once a road exists, its name is its CREATION name (or the player's rename),
// stable across an in-place class change: a road already in the graph returns
// its stored record name rather than a fresh derivation from the caller's
// class. Without that, an upgrade that flips the class would make
// NameRoad(seed, id, currentClass) diverge from [RoadsAPI.RoadInfo].Name for
// the same (seed, id) object (SEC-237, the same GR#3 family as SEC-232).
func (a *RoadsAPI) NameRoad(seed, id uint64, class RoadClass) (string, error) {
	if err := a.checkNotCopied("NameRoad"); err != nil {
		return "", err
	}
	if !class.valid() {
		return "", roadsErr(a.correlationID, ErrInvalidClass, map[string]any{"class": uint8(class)})
	}
	// SEC-237: a road in the graph is keyed by a.seed (every in-graph road is
	// auto-named under a.seed), so only consult the road record when the
	// caller's seed matches — a mismatched seed's NameRoad must keep returning
	// that seed's own registry/auto-name (SEC-232).
	if seed == a.seed {
		a.mu.RLock()
		if rs, ok := a.roads[RoadID(id)]; ok {
			name := rs.name
			a.mu.RUnlock()
			return name, nil
		}
		a.mu.RUnlock()
	}
	return a.resolveName(nameKey{kind: KindRoad, seed: seed, id: id}, func() string {
		return autoNameRoad(seed, id, class, a.corpus)
	}), nil
}

// NameFor returns the deterministic auto-name for any object kind from
// (seed, id) — or the player's rename if one was recorded. Roads are the
// one kind whose name depends on the class, so KindRoad is rejected here in
// favour of [RoadsAPI.NameRoad]; the other four kinds (civic building,
// infrastructure, district, transit) are named here (AC-10). An unknown
// kind is rejected with ErrUnknownObjectKind (AC-13), never a silent empty
// name.
func (a *RoadsAPI) NameFor(kind ObjectKind, seed, id uint64) (string, error) {
	if err := a.checkNotCopied("NameFor"); err != nil {
		return "", err
	}
	if !kind.valid() {
		return "", roadsErr(a.correlationID, ErrUnknownObjectKind, map[string]any{"kind": uint8(kind)})
	}
	if kind == KindRoad {
		return "", invalidInputError(a.correlationID, "kind",
			"KindRoad requires a RoadClass; use NameRoad instead")
	}
	return a.resolveName(nameKey{kind: kind, seed: seed, id: id}, func() string {
		switch kind {
		case KindCivicBuilding:
			return autoNameCivic(seed, id, a.corpus, a.cfg.naming)
		case KindInfrastructure:
			return autoNameInfrastructure(seed, id, a.cfg.naming)
		case KindDistrict:
			return autoNameDistrict(seed, id, a.corpus)
		default: // KindTransit
			return autoNameTransit(seed, id, a.cfg.naming)
		}
	}), nil
}
