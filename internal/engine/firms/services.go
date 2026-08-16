package firms

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// ServicesDemand returns professional/financial-services sector demand as a
// SUPERLINEAR function of the count of non-services firms served (AC-11):
//
//	Demand(n) = multiplier × n^(exponent),  exponent > 1
//
// with the exponent carried as exponentPerMille (data/firms.json, e.g. 1300
// → 1.3). Doubling n more than doubles the figure — a linear Demand(n) =
// k·n is exactly the lazy implementation this form rejects. The exponent
// and multiplier are balance data pending M2 tuning (GR#15); the functional
// form is the deliverable. The result is a derived, non-money demand figure
// (never a micro-pound amount), so float math (math.Pow, deterministic
// across Go's pure-Go math package) is acceptable here; the float→int64
// conversion is saturated via num.ClampInt64FromFloat, never a bare int64()
// that would wrap an oversized demand into a negative figure (SEC-104).
func (f *FirmsAPI) ServicesDemand(servedFirmCount int64) int64 {
	if err := f.checkNotCopied("ServicesDemand"); err != nil {
		return 0
	}
	return servicesDemandAt(f.cfg.ServicesDemand.ExponentPerMille, f.cfg.ServicesDemand.Multiplier, servedFirmCount)
}

// servicesDemandAt evaluates Demand(n) = multiplier × n^(exponentPerMille/1000)
// at n, saturating the float→int64 conversion (SEC-104). Pure, so
// buildConfig reuses it to validate that a configured exponent actually
// produces superlinear demand (SEC-105).
func servicesDemandAt(exponentPerMille, multiplier, n int64) int64 {
	if n <= 0 {
		return 0
	}
	exponent := float64(exponentPerMille) / 1000.0
	demand := math.Pow(float64(n), exponent)
	return num.ClampInt64FromFloat(float64(multiplier) * demand)
}

// NonServicesFirmCount returns the number of non-services firms (the served
// firm count feeding ServicesDemand, AC-11): every firm whose sector is not
// tertiary/public (i.e. the blue-collar producing firms that consume
// accounting/legal/insurance/banking).
func (f *FirmsAPI) NonServicesFirmCount() int64 {
	if err := f.checkNotCopied("NonServicesFirmCount"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var n int64
	for _, fs := range f.firms {
		if !isServicesSector(fs.firm.Sector) {
			n++
		}
	}
	return n
}
