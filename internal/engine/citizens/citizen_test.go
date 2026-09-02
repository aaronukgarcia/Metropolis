package citizens

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestHotRecordSizeIsAbout250B (AC-1): the hot AoS record's inline size,
// measured via unsafe.Sizeof against the real struct, sits in the spec's
// "~250B" range. The exact value is logged; the band is 250 ± 25%. The
// slice backing arrays (Children, Education.Stages) and allocator overhead
// sit on top of this inline figure, which is what takes the real per-
// citizen cost to the spec's ~250B line (documented in doc.go).
func TestHotRecordSizeIsAbout250B(t *testing.T) {
	size := unsafe.Sizeof(Citizen{})
	t.Logf("unsafe.Sizeof(Citizen{}) = %d bytes", size)
	if size < 192 || size > 312 {
		t.Fatalf("hot record inline size %d bytes is outside the ~250B range [192, 312]", size)
	}
}

// TestCitizenAgeDerivedNotStored (AC-2): age is computed from BirthMonth +
// the current sim month, and the struct has no stored Age field to desync.
func TestCitizenAgeDerivedNotStored(t *testing.T) {
	c := Citizen{ID: 1, BirthMonth: 120, Month: 180}
	if got := c.Age(); got != 60 {
		t.Fatalf("Age() = %d, want 60", got)
	}
	if _, hasAge := reflect.TypeOf(Citizen{}).FieldByName("Age"); hasAge {
		t.Fatal("Citizen must have no stored Age field (AC-2): age is derived from BirthMonth")
	}
}

// TestInitPersonalityDeterministic (AC-3/AC-15): the same (seed, id, month,
// parents) always yields the same personality, and axes stay in range.
func TestInitPersonalityDeterministic(t *testing.T) {
	var a, b Personality
	for i := range a {
		a[i] = int32(i * 10)
		b[i] = int32(100 - i*8)
	}
	p1 := InitPersonality(42, 7, 3, a, b)
	p2 := InitPersonality(42, 7, 3, a, b)
	if p1 != p2 {
		t.Fatalf("InitPersonality is not deterministic: %v vs %v", p1, p2)
	}
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		if p1[axis] < 0 || p1[axis] > MaxPersonalityAxis {
			t.Fatalf("axis %d = %d out of [0,100]", axis, p1[axis])
		}
	}
	// A different id must (with overwhelming probability) differ on at
	// least one axis — proves the draw is actually id-keyed, not constant.
	p3 := InitPersonality(42, 99, 3, a, b)
	if p1 == p3 {
		t.Fatal("different ids produced identical personalities — draw is not id-keyed")
	}
}

// TestEducationDriftsPersonality (AC-3): education modifies P over time
// rather than P being fixed at birth. Good schooling widens ambition and
// novelty-seeking; poor schooling narrows them.
func TestEducationDriftsPersonality(t *testing.T) {
	var base Personality
	for i := range base {
		base[i] = 50
	}
	good := ApplyEducationEffect(base, 40)  // positive quality
	poor := ApplyEducationEffect(base, -40) // negative quality
	if good[AxisAmbition] <= base[AxisAmbition] {
		t.Fatalf("good schooling must raise ambition, got %d -> %d", base[AxisAmbition], good[AxisAmbition])
	}
	if poor[AxisAmbition] >= base[AxisAmbition] {
		t.Fatalf("poor schooling must lower ambition, got %d -> %d", base[AxisAmbition], poor[AxisAmbition])
	}
	// Axes stay in range.
	for axis := 0; axis < NumPersonalityAxes; axis++ {
		if good[axis] < 0 || good[axis] > MaxPersonalityAxis {
			t.Fatalf("good[%d] = %d out of range", axis, good[axis])
		}
		if poor[axis] < 0 || poor[axis] > MaxPersonalityAxis {
			t.Fatalf("poor[%d] = %d out of range", axis, poor[axis])
		}
	}
}

func testCitizen() Citizen {
	var p Personality
	for i := range p {
		p[i] = 50
	}
	return Citizen{
		ID:          1,
		BirthMonth:  0,
		Sex:         SexMale,
		Personality: p,
		HealthBand:  HealthGood,
		Fidelity:    FidelityHot,
	}
}

// assertRegistryCode asserts err is a *errs.E with the given code (GR#7
// assertion, BUG-100 — the code must match, not merely "an error returned").
func assertRegistryCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s error, got nil", code)
	}
	if !errors.Is(err, &errs.E{Code: code}) {
		t.Fatalf("expected registry code %s, got %v", code, err)
	}
}

// TestValidateCitizenAcceptsPreGenesisBirthMonth (BUG-517): a negative
// birthMonth is now legitimate (a citizen born before the world's month-0
// genesis — the seed population and migrants are exactly this case) and
// must NOT be rejected, as long as it stays inside the widened
// [MinInt16, MaxInt16] domain.
func TestValidateCitizenAcceptsPreGenesisBirthMonth(t *testing.T) {
	c := testCitizen()
	c.BirthMonth = -1
	if err := ValidateCitizen(c, func(uint64) bool { return true }, "corr"); err != nil {
		t.Fatalf("ValidateCitizen rejected a legitimate pre-genesis birthMonth: %v", err)
	}
}

// TestValidateCitizenRejectsInvalidBirthMonth (AC-13, widened by BUG-517): a
// birthMonth outside the [MinInt16, MaxInt16] domain returns the
// registry-sourced ErrInvalidBirthMonth, not a clamped record.
func TestValidateCitizenRejectsInvalidBirthMonth(t *testing.T) {
	c := testCitizen()
	c.BirthMonth = math.MinInt16 - 1
	err := ValidateCitizen(c, func(uint64) bool { return true }, "corr")
	assertRegistryCode(t, err, ErrInvalidBirthMonth)
}

// TestValidateCitizenRejectsPersonalityOutOfRange (AC-13): an axis outside
// 0-100 returns ErrPersonalityAxisOutOfRange, never a clamp.
func TestValidateCitizenRejectsPersonalityOutOfRange(t *testing.T) {
	c := testCitizen()
	c.Personality[AxisAmbition] = 101
	err := ValidateCitizen(c, func(uint64) bool { return true }, "corr")
	assertRegistryCode(t, err, ErrPersonalityAxisOutOfRange)

	c2 := testCitizen()
	c2.Personality[AxisPatience] = -1
	err2 := ValidateCitizen(c2, func(uint64) bool { return true }, "corr")
	assertRegistryCode(t, err2, ErrPersonalityAxisOutOfRange)
}

// TestValidateCitizenRejectsUnknownHousehold (AC-13): a householdId
// referencing a nonexistent household returns ErrUnknownHousehold.
func TestValidateCitizenRejectsUnknownHousehold(t *testing.T) {
	c := testCitizen()
	c.Household = 999
	err := ValidateCitizen(c, func(id uint64) bool { return id == 1 }, "corr")
	assertRegistryCode(t, err, ErrUnknownHousehold)
}

// TestBirthCommandRejectsInvalidAndDoesNotPersist (AC-13, GR#7/BUG-100): an
// invalid birth command returns the matching registry code AND persists
// nothing — the population count is unchanged and no clamped record exists.
func TestBirthCommandRejectsInvalidAndDoesNotPersist(t *testing.T) {
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	before := api.TotalPopulation("corr")

	bad := testCitizen()
	bad.ID = 500
	// BUG-517 widened the domain to admit pre-genesis (negative) birth
	// months, so this must use a value outside the WIDENED domain to still
	// exercise the rejection path.
	bad.BirthMonth = math.MinInt16 - 1
	cmd := LifeEventCommand{CorrelationID: "corr", Kind: LifeEventBirth, Citizen: bad}
	if err := api.ApplyLifeEventCommand(cmd); err == nil {
		t.Fatal("expected an error for an invalid birth, got nil")
	} else {
		assertRegistryCode(t, err, ErrInvalidBirthMonth)
	}
	if after := api.TotalPopulation("corr"); after != before {
		t.Fatalf("invalid birth persisted: population %d -> %d", before, after)
	}
	if _, ok := api.CitizenAt(500, "corr"); ok {
		t.Fatal("invalid birth created a citizen record that should not exist")
	}
}
