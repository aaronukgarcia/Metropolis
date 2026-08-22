package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// This file is engine.roads / §20's own: the deterministic auto-naming
// corpus (a Kentish road place-name list plus a per-road-class suffix
// table), loaded from data/naming_corpus.json (FEAT-047 /
// data.modes-naming). See docs/planning/acceptance/data.modes-naming.md
// for the acceptance criteria and the data file's own $comment for the
// field reference.
//
// The schema is deliberately structural rather than a flat map.
// RoadSuffixes is a struct with one field per non-numbered road class
// (engine.roads' 11-rung class ladder minus the two numbered classes —
// urban expressway and motorway — which §20 assigns the M-/A- numbering
// scheme instead of a place-name+suffix pair, per the file's
// numberedSchemeNote). A struct rather than a map[string][]string means
// the nine class keys are compile-time fixed, so Validate can reject a
// corpus missing any one of them (or an entirely unknown class key)
// instead of silently accepting an arbitrary key set that a consumer
// would later fail to look up.

// NamingCorpus is data/naming_corpus.json's top-level schema (§20):
// the Kentish road place-name corpus and the class-appropriate suffix
// table consumed by engine.roads' deterministic seed+id auto-naming
// (engine.roads AC-9/AC-10). The two numbered road classes (urban
// expressway, motorway) are deliberately out of scope for this file —
// §20 numbers them M-/A- rather than pairing them with a place name —
// and that omission is documented in NumberedSchemeNote rather than
// left implicit.
type NamingCorpus struct {
	// Comment carries the file's top-level "$comment" provenance note
	// (spec section, transcript source, and the coverage-floor judgment
	// call — see data.modes-naming.md AC-9/ASM-392).
	Comment string `json:"$comment,omitempty"`

	Version int `json:"version"`

	// NumberedSchemeNote restates §20's carve-out: urban expressway and
	// motorway use the M-/A- numbering scheme, not this corpus's
	// place-name+suffix pairing (data.modes-naming.md AC-12).
	NumberedSchemeNote string `json:"numberedSchemeNote,omitempty"`

	Categories NamingCategories `json:"categories"`
}

// NamingCategories is the "categories" wrapper object: the place-name
// list, the per-class suffix table, and the suffix-table's own note.
type NamingCategories struct {
	// RoadPlaceNames is the Kentish toponym list §20 names ("Kentish
	// corpus"), sized to avoid visible repetition at city scale
	// (data.modes-naming.md AC-9). Distinct, non-empty strings.
	RoadPlaceNames []string `json:"roadPlaceNames"`

	RoadSuffixes RoadSuffixes `json:"roadSuffixes"`

	// RoadSuffixesNote documents why the suffix lists are class-appropriate
	// rather than one universal list copy-pasted across all nine classes
	// (data.modes-naming.md AC-10's named lazy-implementation failure mode).
	RoadSuffixesNote string `json:"roadSuffixesNote,omitempty"`
}

// RoadSuffixes is the per-road-class suffix table (§20's "class-
// appropriate suffix" rule). One field per non-numbered road class in
// engine.roads' 11-rung ladder, keyed by the JSON class key. Each class
// must carry at least one non-empty suffix; the lists are class-specific
// (e.g. "Close"/"Mews"/"Lane" on the lower-tier classes, "Avenue"/"Way"/
// "Drive" on the higher-capacity classes — see the file's
// roadSuffixesNote).
type RoadSuffixes struct {
	Alley             []string `json:"alley"`
	Gravel            []string `json:"gravel"`
	ResidentialStreet []string `json:"residential_street"`
	TwoLane           []string `json:"two_lane"`
	OneWayPairs       []string `json:"one_way_pairs"`
	Avenue2Plus2      []string `json:"avenue_2_plus_2"`
	BusLaneVariant    []string `json:"bus_lane_variant"`
	TramTrackVariant  []string `json:"tram_track_variant"`
	DualCarriageway   []string `json:"dual_carriageway"`
}

// UnmarshalJSON gives RoadSuffixes strict decoding (SEC-058): the package
// doc comment above already claims a struct schema "lets Validate reject an
// entirely unknown class key", but plain encoding/json silently drops
// unknown struct keys, so an extra or misspelled class key (e.g.
// "eleventh_class" alongside the nine real ones) previously loaded
// successfully with the extra key silently discarded and no load-time
// signal. Decoding through a json.Decoder with DisallowUnknownFields turns
// that into an immediate error, the same effect DisallowUnknownFields has
// on any other struct-shaped decode target in the standard library. A
// local roadSuffixes alias avoids infinite recursion into this same method.
func (s *RoadSuffixes) UnmarshalJSON(b []byte) error {
	type roadSuffixesAlias RoadSuffixes
	var alias roadSuffixesAlias
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&alias); err != nil {
		return err
	}
	*s = RoadSuffixes(alias)
	return nil
}

// roadClassList is the fixed, ordered enumeration of the nine
// non-numbered road classes, paired with each struct field. Validate
// walks it in this fixed order rather than iterating a map, so the
// first violation returned is deterministic for a given malformed file
// (GR#21 / data.modes-naming.md AC-20) — no sort step is needed because
// there is no map iteration in the validation path.
type roadClass struct {
	name     string
	suffixes []string
}

func roadClasses(s RoadSuffixes) []roadClass {
	return []roadClass{
		{"alley", s.Alley},
		{"gravel", s.Gravel},
		{"residential_street", s.ResidentialStreet},
		{"two_lane", s.TwoLane},
		{"one_way_pairs", s.OneWayPairs},
		{"avenue_2_plus_2", s.Avenue2Plus2},
		{"bus_lane_variant", s.BusLaneVariant},
		{"tram_track_variant", s.TramTrackVariant},
		{"dual_carriageway", s.DualCarriageway},
	}
}

// Validate implements Validator. It enforces the structural shape only
// — non-empty place-name list, non-empty non-blank distinct place names,
// and a non-empty, non-blank, distinct suffix list for each of the nine
// road classes (data.modes-naming.md AC-7/AC-10/AC-11/AC-18). It does
// NOT enforce AC-9's >=40-entry coverage floor: that is a corpus-quality
// check asserted by the test (naming_corpus_test.go), not a schema
// malformation this loader must reject, and hard-coding a minimum count
// here would be a GR#15 violation of the "validators derive from data"
// rule this file exists to serve.
func (n *NamingCorpus) Validate() error {
	if err := requireVersion(n.Version); err != nil {
		return err
	}

	names := n.Categories.RoadPlaceNames
	if len(names) == 0 {
		return fieldErr("categories.roadPlaceNames", "required, must be non-empty")
	}
	seenNames := make(map[string]bool, len(names))
	for i, name := range names {
		// Non-blank, not just non-empty (SEC-057): a whitespace-only name
		// ("   ", a tab, a newline) is not a real place name either.
		if strings.TrimSpace(name) == "" {
			return fieldErr(fmt.Sprintf("categories.roadPlaceNames[%d]", i), "required, must be non-empty")
		}
		if seenNames[name] {
			return fieldErr(fmt.Sprintf("categories.roadPlaceNames[%d]", i), fmt.Sprintf("duplicate place name %q", name))
		}
		seenNames[name] = true
	}

	for _, c := range roadClasses(n.Categories.RoadSuffixes) {
		if len(c.suffixes) == 0 {
			return fieldErr("categories.roadSuffixes."+c.name, "required, must have at least one suffix")
		}
		seen := make(map[string]bool, len(c.suffixes))
		for i, suffix := range c.suffixes {
			// Same non-blank contract as the place-name list above
			// (SEC-057): a whitespace-only suffix must not pass.
			if strings.TrimSpace(suffix) == "" {
				return fieldErr(fmt.Sprintf("categories.roadSuffixes.%s[%d]", c.name, i), "required, must be non-empty")
			}
			if seen[suffix] {
				return fieldErr(fmt.Sprintf("categories.roadSuffixes.%s[%d]", c.name, i), fmt.Sprintf("duplicate suffix %q", suffix))
			}
			seen[suffix] = true
		}
	}

	return nil
}
