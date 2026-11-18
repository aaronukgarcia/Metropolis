package why

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// ViewSubscriptionName is the protocol view name this screen subscribes to
// ("f5.why"). The composition root decides whether the view is served; until
// it is, the screen shows its honest no-data state.
const ViewSubscriptionName = "f5.why"

// wireSchemaVersion is the version stamp every f5.why patch must carry.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single patch payload (256 KiB; a real patch is
// well under 4 KiB). A larger one is refused before decoding.
const maxPatchWireBytes = 256 << 10

// maxParts bounds the parts list per lever, and maxText the bytes kept of
// any label, so a hostile patch cannot make the screen allocate or draw
// without limit.
const (
	maxParts = 16
	maxText  = 64
)

// decodePatch validates the size and schema envelope, decodes raw into a
// Patch and checks the contract. Every rejection is an ordinary error for
// logMalformed to file under ErrMalformedPatch.
func decodePatch(raw json.RawMessage) (Patch, error) {
	if len(raw) > maxPatchWireBytes {
		return Patch{}, fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", len(raw), maxPatchWireBytes)
	}
	var p Patch
	if err := json.Unmarshal(raw, &p); err != nil {
		return Patch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return Patch{}, fmt.Errorf("unsupported schemaVersion %d (want %d)", p.SchemaVersion, wireSchemaVersion)
	}
	if !p.Known {
		// Nothing but the envelope matters before the first month closes.
		return Patch{SchemaVersion: wireSchemaVersion}, nil
	}
	if err := validate(&p); err != nil {
		return Patch{}, err
	}
	return p, nil
}

// validate checks a known patch against the contract and trims labels.
func validate(p *Patch) error {
	if p.Month < 0 {
		return fmt.Errorf("negative month %d", p.Month)
	}
	if len(p.Levers) != LeverCount {
		return fmt.Errorf("want %d levers, got %d", LeverCount, len(p.Levers))
	}
	if n := len(p.Modifiers); n != 0 && n != ModifierCount {
		return fmt.Errorf("want 0 or %d modifiers, got %d", ModifierCount, n)
	}
	if p.AWorld == nil && p.Neutral == nil {
		return fmt.Errorf("neither aWorld nor neutral is present")
	}
	scalars := []struct {
		name string
		v    float64
	}{
		{"score", p.Score}, {"net", p.Net}, {"reputation", p.Reputation},
		{"reputationPoints", p.ReputationPoints}, {"wellbeingFactor", p.WellbeingFactor},
		{"wellbeingPoints", p.WellbeingPoints},
	}
	if p.AWorld != nil {
		scalars = append(scalars, struct {
			name string
			v    float64
		}{"aWorld", *p.AWorld})
	}
	if p.Neutral != nil {
		scalars = append(scalars, struct {
			name string
			v    float64
		}{"neutral", *p.Neutral})
	}
	for _, s := range scalars {
		if !finite(s.v) {
			return fmt.Errorf("%s is not a finite number", s.name)
		}
	}
	if math.Abs(p.Reputation) > 100 {
		return fmt.Errorf("reputation %v outside -100..100", p.Reputation)
	}
	if p.Room < 0 {
		return fmt.Errorf("negative room %d", p.Room)
	}
	if err := validateBounds(p); err != nil {
		return err
	}
	switch p.LimitedBy {
	case "", LimitHomes, LimitTransport:
	default:
		return fmt.Errorf("limitedBy %q is neither %q nor %q", p.LimitedBy, LimitHomes, LimitTransport)
	}
	switch p.ReputationTrend {
	case "", TrendRising, TrendSteady, TrendFalling:
	default:
		return fmt.Errorf("reputationTrend %q is not rising, steady or falling", p.ReputationTrend)
	}
	for i := range p.Levers {
		if err := validateLever(&p.Levers[i], LeverIDs[i], LeverNames[i]); err != nil {
			return fmt.Errorf("lever %d (%s): %w", i, LeverIDs[i], err)
		}
	}
	for i := range p.Modifiers {
		if err := validateLever(&p.Modifiers[i], ModifierIDs[i], ModifierNames[i]); err != nil {
			return fmt.Errorf("modifier %d (%s): %w", i, ModifierIDs[i], err)
		}
	}
	return nil
}

// validateLever checks one lever or modifier line against its position.
func validateLever(l *Lever, wantID, wantName string) error {
	for _, got := range []string{l.ID, l.Name} {
		if got == "" {
			continue
		}
		n := normalise(got)
		if n != wantID && n != normalise(wantName) {
			return fmt.Errorf("id/name %q does not belong at this position", clip(got, maxText))
		}
	}
	if !finite(l.Value) || !finite(l.Points) {
		return fmt.Errorf("value or points is not a finite number")
	}
	if l.Built {
		if l.Value < 0 || l.Value > 100 {
			return fmt.Errorf("value %v outside 0..100", l.Value)
		}
		if math.Abs(l.Points) > 100 {
			return fmt.Errorf("points %v outside -100..100", l.Points)
		}
	}
	if len(l.Parts) > maxParts {
		return fmt.Errorf("%d parts exceeds the limit %d", len(l.Parts), maxParts)
	}
	for i := range l.Parts {
		if !finite(l.Parts[i].V) {
			return fmt.Errorf("part %d value is not a finite number", i)
		}
		if math.Abs(l.Parts[i].V) > maxCount {
			return fmt.Errorf("part %d value outside +-%d", i, int64(maxCount))
		}
		l.Parts[i].K = clip(l.Parts[i].K, maxText)
	}
	return nil
}

// normalise lower-cases s and keeps letters and digits only.
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Bounds for the numbers a patch carries (FEAT-9995385616 round r1 B2). A
// patch with any number outside them is refused whole, like every other
// refusal here. maxCount bounds people (counts, Wanted, Room and Net, which is
// Wanted before the cap): a billion is ten times the 100M citizen target.
// maxScalar bounds the unit-less scores and point shares: a lever line is held
// to -100..100 above, ten levers and four extra lines sum to under 1,500, so
// 10,000 leaves the engine seven times the head-room and still prints in a
// few characters (the points column is 5 wide for a normal line). maxFactor
// bounds the wellbeing multiplier (1.0 = no effect).
const (
	maxCount  = 1e9
	maxScalar = 1e4
	maxFactor = 100
)

// validateBounds refuses a known patch whose numbers cannot be printed
// honestly. Wanted is signed (negative = people want to leave), so its
// magnitude is bounded; Room and the counts are non-negative.
func validateBounds(p *Patch) error {
	if math.Abs(p.Net) > maxCount {
		return fmt.Errorf("net %v outside +-%v", p.Net, float64(maxCount))
	}
	for _, s := range []struct {
		name string
		v    float64
	}{{"score", p.Score}, {"reputationPoints", p.ReputationPoints}, {"wellbeingPoints", p.WellbeingPoints}} {
		if math.Abs(s.v) > maxScalar {
			return fmt.Errorf("%s %v outside +-%v", s.name, s.v, float64(maxScalar))
		}
	}
	if p.AWorld != nil && math.Abs(*p.AWorld) > maxScalar {
		return fmt.Errorf("aWorld %v outside +-%v", *p.AWorld, float64(maxScalar))
	}
	if p.Neutral != nil && math.Abs(*p.Neutral) > maxScalar {
		return fmt.Errorf("neutral %v outside +-%v", *p.Neutral, float64(maxScalar))
	}
	if p.WellbeingFactor < 0 || p.WellbeingFactor > maxFactor {
		return fmt.Errorf("wellbeingFactor %v outside 0..%d", p.WellbeingFactor, maxFactor)
	}
	if p.Wanted > maxCount || p.Wanted < -maxCount {
		return fmt.Errorf("wanted %d outside +-%d", p.Wanted, int64(maxCount))
	}
	if p.Room > maxCount {
		return fmt.Errorf("room %d above %d", p.Room, int64(maxCount))
	}
	for _, c := range []struct {
		name string
		v    *int64
	}{{"movedIn", p.MovedIn}, {"left", p.Left}, {"inflow", p.Inflow}, {"outflow", p.Outflow}} {
		if c.v != nil && (*c.v < 0 || *c.v > maxCount) {
			return fmt.Errorf("%s %d outside 0..%d", c.name, *c.v, int64(maxCount))
		}
	}
	if p.Homeless != nil && (p.Homeless.Count < 0 || p.Homeless.Count > maxCount) {
		return fmt.Errorf("homeless count %d outside 0..%d", p.Homeless.Count, int64(maxCount))
	}
	return nil
}
