package households

import (
	"errors"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// errNoHousingTypologies is the descriptive error loadTypologies returns
// when data/buildings.json carries no catalogueSection "HS" entries at all.
// NewFromBuildings wraps it into the registry-sourced ErrTypologyDataInvalid.
var errNoHousingTypologies = errors.New("no catalogueSection \"HS\" housing typologies in data/buildings.json")

// The tag→weight translation (ASM-248). data/buildings.json's 17 HS entries
// carry their §21 "profile sketch" as free-text tags in appealProfile; the
// numeric stage × wealth × personality weights §21 implies do not exist in
// the data, so this package owns the translation layer that maps each tag
// onto the household stage × wealth × personality axes (AC-4).
//
// The translation is deliberately split into two documented sets:
//
//   - mappedTags: tags with a spec-grounded axis mapping, applied in
//     appealContribution. Stage tags contribute a fixed bonus when the
//     household's life stage matches; personality-axis tags contribute the
//     household's value on that axis (0-100); wealth tags contribute a
//     band-scaled bonus.
//   - neutralTags: tags present in the HS vocabulary but with no
//     stage/wealth/personality mapping in §21 (density/amenity descriptors
//     like "flats", "high-rise-tier", "rural", "coastal"). They contribute
//     zero — recognised, so they do NOT trip the fallback — but carry no
//     weight. ASM-248 flags this as the data-schema question if the
//     vocabulary proves too coarse to differentiate: extending the mapping
//     is a data/design decision for foundation.data/Aaron, not a silent
//     numeric-weight invention here.
var mappedTags = map[string]bool{
	"novelty":         true, // personality: novelty-seeking axis +
	"community":       true, // personality: community-mindedness axis +
	"families":        true, // stage: family +
	"retirees":        true, // stage: retired +
	"care-integrated": true, // stage: retired (care-oriented) +
	"students":        true, // stage: student +
	"young-singles":   true, // stage: young single +
	"wealth":          true, // wealth: positive
	"wealth-magnet":   true, // wealth: strongly positive
	"premium":         true, // wealth: strongly positive
	"cheap-entry":     true, // wealth: low-wealth (affordable) +
}

var neutralTags = map[string]bool{
	"rural":             true,
	"farm-plots":        true,
	"garden-water-plus": true,
	"coastal":           true,
	"shore-only":        true,
	"flats":             true,
	"high-rise-tier":    true,
	"mega-density":      true,
}

// anyRecognisedTag reports whether a typology's tag array holds at least one
// tag in the known vocabulary (mapped or neutral). A typology with an empty
// or entirely-unrecognised tag array degrades to the neutral-appeal fallback
// (AC-11); a typology with only neutral tags is recognised (its appeal is a
// genuine, computed zero, not a fallback).
func anyRecognisedTag(tags []string) bool {
	for _, t := range tags {
		if mappedTags[t] || neutralTags[t] {
			return true
		}
	}
	return false
}

// appealContribution returns one tag's contribution to a household's appeal
// for the typology carrying it. It is the ASM-248 translation applied to one
// (tag, profile) pair; the values are all small, bounded integers, and the
// caller sums them with satAdd.
func appealContribution(tag string, p HouseholdProfile) int64 {
	switch tag {
	case "novelty":
		return int64(p.Personality[citizens.AxisNovelty])
	case "community":
		return int64(p.Personality[citizens.AxisCommunity])
	case "families":
		if p.Stage == LifeStageFamily {
			return stageMatchBonus
		}
	case "retirees":
		if p.Stage == LifeStageRetired {
			return stageMatchBonus
		}
	case "students":
		if p.Stage == LifeStageStudent {
			return stageMatchBonus
		}
	case "young-singles":
		if p.Stage == LifeStageYoungSingle {
			return stageMatchBonus
		}
	case "care-integrated":
		if p.Stage == LifeStageRetired {
			return careMatchBonus
		}
	case "wealth":
		return satMulSmall((wealthBand(p.Wealth) + 1), wealthBonusUnit)
	case "wealth-magnet", "premium":
		return satMulSmall((wealthBand(p.Wealth) + 1), wealthMagnetUnit)
	case "cheap-entry":
		return satMulSmall((5 - wealthBand(p.Wealth)), cheapEntryUnit)
	}
	return 0 // neutral or unrecognised: no contribution
}

// The appeal-contribution bonus units (documented, integer-only). They are
// directional placeholders pending M2 Batch tuning (GR#15's balance-number
// regime) — what matters is that a matching stage/wealth/personality scores
// higher than a non-matching one, never the absolute magnitude.
const (
	stageMatchBonus  int64 = 100
	careMatchBonus   int64 = 50
	wealthBonusUnit  int64 = 20
	wealthMagnetUnit int64 = 50
	cheapEntryUnit   int64 = 20
)

// satMulSmall multiplies two small non-negative integers with satAdd-style
// saturation, so the appeal path never touches a raw * that could overflow
// if a future Batch value were enlarged. Both operands are bounded here
// (band ∈ [0,4], units ≤ 100), so this is defence-in-depth (FEAT-086).
func satMulSmall(a, b int64) int64 {
	v, _ := num.SafeMul(a, b)
	return v
}
