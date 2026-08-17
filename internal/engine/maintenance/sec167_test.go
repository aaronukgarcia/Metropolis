package maintenance

import "testing"

// TestSEC167CallerConfigMutationDoesNotCorruptAPI reproduces SEC-167 (P3,
// config-aliasing, the SEC-157 sibling class): New stores the caller's Config
// by value, which copies the struct but NOT the Classes map (a reference
// type), so a.cfg.Classes aliases the caller's map. A caller that constructs
// a Config programmatically, calls New, then mutates cfg.Classes after New
// returns would silently change the running API's validated class table —
// bypassing validate()'s positive-rate check, letting Register succeed with a
// negative BaseEngineerDaysPerYear, and letting AdvanceMonth accrue a
// negative-cost job that drives TotalBacklog negative with no crew work
// (AC-6/AC-7 conservation broken, no error returned).
//
// The fix (cloneConfig in New) deep-copies the Classes map, so a post-New
// mutation of the caller's config is inert: the API keeps the original
// validated rates, a class added after New stays unknown, and conservation
// holds. This test fails pre-fix (BaseEngineerDaysPerYear leaks the caller's
// -5) and passes post-fix.
func TestSEC167CallerConfigMutationDoesNotCorruptAPI(t *testing.T) {
	cfg := testConfig()
	origRate := cfg.Classes["dwelling"].EngineerDaysPerYear // the validated rate, e.g. 10

	a, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The attack, applied to the CALLER's map after New returned:
	//   (1) replace an existing class entry with a negative rate; and
	//   (2) add a brand-new class.
	// Both must be inert — a partial clone (e.g. entries copied but a shared
	// backing map, or a copied map header only) would leak one or the other.
	cfg.Classes["dwelling"] = ClassConfig{EngineerDaysPerYear: -5, LifetimeYears: 50}
	cfg.Classes["phantom"] = ClassConfig{EngineerDaysPerYear: 7, LifetimeYears: 10}

	// The existing class must still resolve — but with the ORIGINAL rate.
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register dwelling: %v", err)
	}
	v, err := a.View(1, "test")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if v.BaseEngineerDaysPerYear != origRate {
		t.Fatalf("BaseEngineerDaysPerYear = %d, want %d (the caller's -5 must not leak into the API)", v.BaseEngineerDaysPerYear, origRate)
	}
	if got := a.cfg.Classes["dwelling"].EngineerDaysPerYear; got != origRate {
		t.Fatalf("stored class rate = %d, want %d (the stored map must not alias the caller's)", got, origRate)
	}

	// A class added to the caller's map after New must stay unknown.
	wantCode(t, a.Register(2, "phantom", RegisterOptions{}, "test"), ErrUnknownClass)

	// Advance a year. With the original positive rate the dwelling accrues one
	// year of positive demand (base == origRate at the default 1.0x size, age
	// 12 << lifetime 600). Pre-fix the aliased -5 accrues a negative-cost job
	// and drives TotalBacklog negative with no crew work.
	if err := a.AdvanceMonth(12, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got := mustTotalBacklog(t, a); got != origRate {
		t.Fatalf("TotalBacklog = %d, want %d (no negative-cost job; conservation holds)", got, origRate)
	}
}
