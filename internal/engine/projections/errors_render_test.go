package projections

import (
	"math"
	"regexp"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-313: six of engine.projections' ten registry error codes had
// message templates whose placeholder names did not match the ctx keys
// their real call sites supplied, so the literal "{token}" text reached
// the user instead of a value. Every existing test in this package only
// ever asserted on e.Code (see assertCode) — never on the rendered
// message — so the drift was invisible to the whole suite.
//
// unsubstitutedPlaceholder matches any brace-wrapped token STILL present
// after errs.New/errs.Wrap has rendered the template. A correctly
// substituted message never contains a bare "{identifier}" run (the
// registry's own "code"/"correlationId" builtins always resolve, and
// every other key is either replaced by its ctx value or degrades to the
// literal "{key}" text — see errs.renderTemplate's doc comment). Finding
// one of these in a real call site's rendered Display() is exactly the
// class of defect BUG-313 fixed.
//
// Deliberately permissive: `[^}]+`, NOT an identifier-shaped class like
// `[A-Za-z][A-Za-z0-9_]*`. errs.renderTemplate accepts ANY bytes between
// "{" and the next "}" as a token name — it does not require the token to
// look like an identifier. A narrower, "tidier" regex here would miss a
// hyphenated or otherwise non-identifier token (e.g. "{provider-key}")
// that renders broken while this gate stays green — exactly how BUG-313's
// round found the gate blind to a live case and a repo-wide scan then
// found 7 more (BUG-317). Do not narrow this for tidiness.
var unsubstitutedPlaceholder = regexp.MustCompile(`\{[^}]+\}`)

// assertRenders fails t if err's Display() text contains any
// unsubstituted "{token}" — the rendering standard BUG-313 requires:
// prove the message is actually readable, not just that the code
// matches.
func assertRenders(t *testing.T, err error, wantCode string) {
	t.Helper()
	assertCode(t, err, wantCode)
	e := err.(*errs.E)
	display := e.Display()
	if loc := unsubstitutedPlaceholder.FindString(display); loc != "" {
		t.Errorf("%s renders with an unsubstituted placeholder %q: %s", wantCode, loc, display)
	}
	if display == "" {
		t.Errorf("%s rendered an empty Display()", wantCode)
	}
}

// projectionsRenderCopy is the byte-copy struct-copy helper this test
// needs to provoke ErrCopiedValue through the real guard without
// tripping go vet's copylocks check on a literal "cp := *p" (see
// engine.services/copyguard_test.go's servicesCopy, the house pattern
// this mirrors exactly).
func projectionsRenderCopy(p *ProjectionsAPI) *ProjectionsAPI {
	c := new(ProjectionsAPI)
	*(*[unsafe.Sizeof(ProjectionsAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(ProjectionsAPI{})]byte)(unsafe.Pointer(p))
	return c
}

// TestEveryErrorRendersWithRealCallSiteKeys is the permanent fix for
// BUG-313's class of defect: it drives the REAL production call sites
// (never a hand-built errs.New/ctx map that could itself drift from the
// call site it is meant to represent) for every one of engine.
// projections' ten registry codes and asserts the rendered Display()
// text substitutes every placeholder. Reading errors.go/errors.json is
// exactly what let six of ten drift silently — this test can only be
// satisfied by rendering.
func TestEveryErrorRendersWithRealCallSiteKeys(t *testing.T) {
	t.Run("MET-G100_ErrUnknownCurveKey_via_Curve", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.Curve("no-such-curve-key", 0, 1)
		assertRenders(t, err, ErrUnknownCurveKey)
	})
	t.Run("MET-G100_ErrUnknownCurveKey_via_Threshold", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.Threshold("no-such-threshold-key")
		assertRenders(t, err, ErrUnknownCurveKey)
	})
	t.Run("MET-G100_ErrUnknownCurveKey_via_MarginToInsolvency", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.MarginToInsolvency(0)
		assertRenders(t, err, ErrUnknownCurveKey)
	})
	t.Run("MET-G100_ErrUnknownCurveKey_via_MarginToGhostCity", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.MarginToGhostCity(0)
		assertRenders(t, err, ErrUnknownCurveKey)
	})

	t.Run("MET-G101_ErrNegativeMonthQuery_via_SetCurrentMonth", func(t *testing.T) {
		api := NewProjectionsAPI()
		err := api.SetCurrentMonth(-1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})
	t.Run("MET-G101_ErrNegativeMonthQuery_via_Curve_negative", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.Curve("k", -5, 1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})
	t.Run("MET-G101_ErrNegativeMonthQuery_via_Curve_inverted", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.Curve("k", 10, 1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})
	t.Run("MET-G101_ErrNegativeMonthQuery_via_Curve_tooWide", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.Curve("k", 0, maxCurveQueryMonths+1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})
	t.Run("MET-G101_ErrNegativeMonthQuery_via_MarginToInsolvency", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.MarginToInsolvency(-1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})
	t.Run("MET-G101_ErrNegativeMonthQuery_via_MarginToGhostCity", func(t *testing.T) {
		api := NewProjectionsAPI()
		_, err := api.MarginToGhostCity(-1)
		assertRenders(t, err, ErrNegativeMonthQuery)
	})

	t.Run("MET-G102_ErrInvalidFuseYears", func(t *testing.T) {
		api := NewProjectionsAPI()
		err := api.EnqueueDecision(Decision{ID: "render-nan", FuseYears: math.NaN()})
		assertRenders(t, err, ErrInvalidFuseYears)
	})

	t.Run("MET-G103_ErrSlowFuseMissingPayload", func(t *testing.T) {
		api := NewProjectionsAPI()
		err := api.EnqueueDecision(Decision{ID: "render-slowfuse", FuseYears: 6})
		assertRenders(t, err, ErrSlowFuseMissingPayload)
	})

	t.Run("MET-G104_ErrUnknownDecision", func(t *testing.T) {
		api := NewProjectionsAPI()
		err := api.CancelDecision("no-such-decision")
		assertRenders(t, err, ErrUnknownDecision)
	})

	t.Run("MET-G105_ErrNilCurveProvider", func(t *testing.T) {
		api := NewProjectionsAPI()
		err := api.RegisterCurveProvider("render-nil-provider", nil)
		assertRenders(t, err, ErrNilCurveProvider)
	})

	t.Run("MET-G106_ErrDuplicateCurveProvider", func(t *testing.T) {
		api := NewProjectionsAPI()
		provider := fakeProvider{def: 1}
		if err := api.RegisterCurveProvider("render-dup", provider); err != nil {
			t.Fatalf("first RegisterCurveProvider: %v", err)
		}
		err := api.RegisterCurveProvider("render-dup", provider)
		assertRenders(t, err, ErrDuplicateCurveProvider)
	})

	t.Run("MET-G107_ErrEmbeddedConfigInvalid_malformedJSON", func(t *testing.T) {
		origHorizon := embeddedHorizonJSON
		t.Cleanup(func() {
			embeddedHorizonJSON = origHorizon
			resetConfigCacheForTest()
		})
		embeddedHorizonJSON = []byte(`{ not valid json`)
		resetConfigCacheForTest()
		_, err := loadHorizonConfig(testCorrelationID())
		assertRenders(t, err, ErrEmbeddedConfigInvalid)
	})
	t.Run("MET-G107_ErrEmbeddedConfigInvalid_zeroHorizon", func(t *testing.T) {
		origHorizon := embeddedHorizonJSON
		t.Cleanup(func() {
			embeddedHorizonJSON = origHorizon
			resetConfigCacheForTest()
		})
		embeddedHorizonJSON = []byte(`{"version":1,"baseHorizonMonths":0,"rationale":"x"}`)
		resetConfigCacheForTest()
		_, err := loadHorizonConfig(testCorrelationID())
		assertRenders(t, err, ErrEmbeddedConfigInvalid)
	})
	t.Run("MET-G107_ErrEmbeddedConfigInvalid_missingDisclosure", func(t *testing.T) {
		origDeathWarnings := embeddedDeathWarningsJSON
		t.Cleanup(func() {
			embeddedDeathWarningsJSON = origDeathWarnings
			resetConfigCacheForTest()
		})
		embeddedDeathWarningsJSON = []byte(`{"version":1,"insolvency":{"warningThresholdMonths":1,"minWarningLeadMonths":1,"disclosure":""},"ghostCity":{"warningThresholdMonths":1,"minWarningLeadMonths":1,"disclosure":"x"}}`)
		resetConfigCacheForTest()
		_, err := loadDeathWarningConfig(testCorrelationID())
		assertRenders(t, err, ErrEmbeddedConfigInvalid)
	})

	t.Run("MET-G108_ErrCurveProviderMissingPeak", func(t *testing.T) {
		api := NewProjectionsAPI()
		// Registered under the reserved key but NOT implementing
		// GhostCityPeakProvider (a plain fakeProvider has no HistoricPeak).
		if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, fakeProvider{def: 1}); err != nil {
			t.Fatalf("RegisterCurveProvider: %v", err)
		}
		_, err := api.MarginToGhostCity(0)
		assertRenders(t, err, ErrCurveProviderMissingPeak)
	})

	t.Run("MET-G109_ErrCopiedValue", func(t *testing.T) {
		orig := NewProjectionsAPI()
		copied := projectionsRenderCopy(orig)
		_, err := copied.Curve("anything", 0, 1)
		assertRenders(t, err, ErrCopiedValue)
	})
}

// TestASM1233ProviderShapeCodeDistinctFromSpiralGhostCity pins the ASM-1233
// rename (Aaron ruling 2026-08-20): the non-spiral provider-shape error was
// renamed ErrGhostCityProviderShape -> ErrCurveProviderMissingPeak so it no
// longer pattern-collides with engine.spiral's genuine ghost-city death path
// (DeathGhostCity / ErrGhostCityNoWarning, MET-G1102). This regression guards
// against the collision silently reappearing: the provider-registration
// failure mode in engine.projections must raise its OWN distinct registered
// code (MET-G108), never the spiral death-path code (MET-G1102). engine.spiral
// keeps the canonical "GhostCity" death name; this module must not reuse it.
func TestASM1233ProviderShapeCodeDistinctFromSpiralGhostCity(t *testing.T) {
	// The spiral ghost-city no-warning death code (data/errors.json,
	// module engine.spiral). Compared as a literal so this package does
	// not import engine.spiral (module-boundary hygiene) — the registry
	// check (add-error.js check) is the global no-duplicate-code guarantee;
	// this test guards the specific pair the ASM-1233 collision was about.
	const spiralGhostCityNoWarning = "MET-G1102"

	if ErrCurveProviderMissingPeak != "MET-G108" {
		t.Fatalf("ErrCurveProviderMissingPeak = %q, want MET-G108 (the projections provider-shape code)", ErrCurveProviderMissingPeak)
	}
	if ErrCurveProviderMissingPeak == spiralGhostCityNoWarning {
		t.Fatalf("ASM-1233 collision reintroduced: projections provider-shape error shares the spiral ghost-city death code %q", spiralGhostCityNoWarning)
	}

	// The provider-shape site must actually raise its own code at runtime.
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	_, err := api.MarginToGhostCity(0)
	assertRenders(t, err, ErrCurveProviderMissingPeak)
	if e, ok := err.(*errs.E); ok && e.Code == spiralGhostCityNoWarning {
		t.Fatalf("ASM-1233: MarginToGhostCity raised the spiral ghost-city death code %q, not its own provider-shape code", spiralGhostCityNoWarning)
	}
}
