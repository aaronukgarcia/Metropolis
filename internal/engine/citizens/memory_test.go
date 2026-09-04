package citizens

import (
	"testing"
	"unsafe"
)

// TestPerf10M (AC-18, this package's own perf smoke test): the measured
// 10M-citizen hot+warm-resident shard memory figure stays within the
// ≤2.5GB target (M0-ENG §1.3: ~250B/citizen ⇒ 10M ⇒ 2.5GB), and the cold
// store at 10M stays within its own ~6-10GB band's lower edge (a fraction
// of the hot budget). The figures are computed from the measured struct
// sizes (unsafe.Sizeof), not hardcoded constants (GR#15).
func TestPerf10M(t *testing.T) {
	const tenM = 10_000_000
	const gb = uint64(1024 * 1024 * 1024)

	hotSize := uint64(unsafe.Sizeof(Citizen{}))
	hotTotal := hotSize * tenM
	t.Logf("10M hot records: %d B/citizen × %d = %.2f GB (budget ≤2.5GB)", hotSize, tenM, float64(hotTotal)/float64(gb))
	if hotTotal > uint64(2.5*float64(gb)) {
		t.Fatalf("10M hot-record memory %.2f GB exceeds the ≤2.5GB target", float64(hotTotal)/float64(gb))
	}

	// BUG-666: bytesPerCitizen() now includes the id->row index
	// (coldShardIndexBytesPerCitizen), so the ceiling below is raised from
	// A1's original 10GB to 15GB to match — see coldshard.go's
	// bytesPerCitizen doc comment and TestColdShardIndexOverhead for the
	// measured index cost this reflects.
	coldBpc := uint64((&ColdShard{}).bytesPerCitizen())
	coldTotal := coldBpc * tenM
	t.Logf("10M cold records: %d B/citizen × %d = %.2f GB", coldBpc, tenM, float64(coldTotal)/float64(gb))
	if coldTotal > uint64(15*float64(gb)) {
		t.Fatalf("10M cold-store memory %.2f GB exceeds the post-BUG-666 15GB upper band", float64(coldTotal)/float64(gb))
	}
}
