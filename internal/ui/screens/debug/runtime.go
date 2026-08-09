package debug

import (
	"fmt"
	"runtime"
	"sort"
	"time"
)

// DefaultRuntimeMetricsProvider returns a RuntimeMetricsFunc that fills
// what Sprint 1 can honestly measure directly from the Go runtime (heap
// in-use, sys, GC pause p99, goroutine count, uptime/real-elapsed) and
// leaves every field with no live source yet (sim date, speed, tick
// number, the three queue depths, input-echo latency, arena occupancy —
// arena occupancy is a foundation.det concept this package has no handle
// on) at its zero value, per RuntimeMetrics' doc comment.
//
// startedAt is the process/session start time; every call computes
// uptime as time.Now().Sub(startedAt). This is one of the two places in
// this package's non-test code that calls the wall clock — confined to
// producing a wall-clock-derived elapsed value, never anything else
// (AC-13's carve-out; grep this package's non-test .go files for
// "time.Now" to confirm the only other match is this same purpose).
func DefaultRuntimeMetricsProvider(startedAt time.Time) RuntimeMetricsFunc {
	return func() RuntimeMetrics {
		now := time.Now()
		uptime := now.Sub(startedAt).Seconds()
		if uptime < 0 {
			uptime = 0
		}

		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)

		return RuntimeMetrics{
			UptimeSeconds:      uptime,
			RealElapsedSeconds: uptime,

			HeapInUseBytes:   ms.HeapInuse,
			SysBytes:         ms.Sys,
			GCPauseP99Micros: gcPauseP99Micros(&ms),

			GoroutineCount: runtime.NumGoroutine(),
		}
	}
}

// gcPauseP99Micros computes the p99 GC pause, in microseconds, from
// runtime.MemStats' PauseNs circular buffer (the last min(NumGC, 256)
// pauses). Returns 0 if no GC has run yet (nothing to compute a
// percentile over).
func gcPauseP99Micros(ms *runtime.MemStats) uint64 {
	n := ms.NumGC
	if n == 0 {
		return 0
	}
	count := int(n)
	if count > len(ms.PauseNs) {
		count = len(ms.PauseNs)
	}

	samples := make([]uint64, count)
	for i := 0; i < count; i++ {
		// PauseNs is a circular buffer; the most recent NumGC%256 pause is
		// at that index, older ones wrap backward from there.
		idx := (int(ms.NumGC) + 255 - i) % 256
		samples[i] = ms.PauseNs[idx]
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	p99Index := int(float64(len(samples)-1) * 0.99)
	return samples[p99Index] / 1000
}

// formatBytes renders n bytes as a short human-readable string (e.g.
// "12.3 MB"), decimal units to match MemoryBudgetTable's own decimal-GB
// figures.
func formatBytes(n uint64) string {
	const unit = 1000.0
	f := float64(n)
	switch {
	case f >= unit*unit*unit:
		return fmt.Sprintf("%.2f GB", f/(unit*unit*unit))
	case f >= unit*unit:
		return fmt.Sprintf("%.2f MB", f/(unit*unit))
	case f >= unit:
		return fmt.Sprintf("%.2f KB", f/unit)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
