package projections

import "sync"

// fakeProvider is a simple CurveProvider test fixture backed by a
// map[monthIndex]value — every AC's "fake curve provider" language
// resolves to one of these unless the AC specifically needs mutable
// state (fakeTrendProvider below).
type fakeProvider struct {
	values map[int64]float64
	def    float64
}

func (f fakeProvider) Value(monthIndex int64) (float64, error) {
	if v, ok := f.values[monthIndex]; ok {
		return v, nil
	}
	return f.def, nil
}

// fakeTrendProvider is a CurveProvider whose Value at ANY month
// returns the CURRENT state's slice, indexed relative to state's own
// first entry — used by AC-17/AC-18's "successively worsening trend
// states" tests, where the SAME registered provider must return
// different values across successive MarginTo* calls as the
// underlying system's own bookkeeping (simulated here by re-pointing
// state) evolves. Safe for concurrent use (AC-14's concurrency tests
// touch it from multiple goroutines).
type fakeTrendProvider struct {
	mu    sync.RWMutex
	state map[int64]float64
	peak  float64
}

func newFakeTrendProvider() *fakeTrendProvider {
	return &fakeTrendProvider{state: map[int64]float64{}}
}

func (f *fakeTrendProvider) setState(state map[int64]float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *fakeTrendProvider) setPeak(peak float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peak = peak
}

func (f *fakeTrendProvider) Value(monthIndex int64) (float64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state[monthIndex], nil
}

func (f *fakeTrendProvider) HistoricPeak() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.peak
}
