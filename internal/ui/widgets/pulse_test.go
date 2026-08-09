package widgets

import "testing"

func TestPulse_ActiveWithinWindowThenInactive(t *testing.T) {
	s := TriggerPulse()
	if !s.Active {
		t.Fatalf("TriggerPulse did not start active")
	}

	s = TickPulse(s, 100)
	if !s.Active {
		t.Fatalf("pulse inactive after 100ms, want active (< %dms)", PulseDurationMS)
	}
	s = TickPulse(s, 100)
	if !s.Active {
		t.Fatalf("pulse inactive after 200ms, want active (< %dms)", PulseDurationMS)
	}
	s = TickPulse(s, 100)
	if s.Active {
		t.Fatalf("pulse still active at %dms, want inactive (>= %dms)", s.ElapsedMS, PulseDurationMS)
	}
	if s.ElapsedMS != PulseDurationMS {
		t.Fatalf("ElapsedMS = %d, want clamped to %d", s.ElapsedMS, PulseDurationMS)
	}
}

func TestPulse_ExactBoundaryIsInactive(t *testing.T) {
	s := TriggerPulse()
	s = TickPulse(s, PulseDurationMS)
	if s.Active {
		t.Fatalf("pulse active exactly at boundary %dms, want inactive", PulseDurationMS)
	}
}

func TestPulse_TickingInactiveIsNoOp(t *testing.T) {
	s := TriggerPulse()
	s = TickPulse(s, PulseDurationMS+50)
	before := s
	s = TickPulse(s, 1000)
	if s != before {
		t.Fatalf("ticking an inactive pulse changed state: %+v -> %+v", before, s)
	}
}

func TestPulse_NegativeDeltaTreatedAsZero(t *testing.T) {
	s := TriggerPulse()
	s = TickPulse(s, -50)
	if !s.Active || s.ElapsedMS != 0 {
		t.Fatalf("negative delta changed state: %+v", s)
	}
}

func TestPulse_NoWallClockUsage(t *testing.T) {
	// This is a static/documentation check exercised for real by the
	// AC-12 grep gate (`grep -rn "time.Now" internal/ui/widgets/*.go`
	// excluding _test.go). This test just documents that TickPulse's
	// behaviour is fully determined by its explicit deltaMS argument,
	// not by anything sampled internally: calling it twice with the
	// same inputs from the same starting state always produces the
	// same result.
	a := TickPulse(TriggerPulse(), 150)
	b := TickPulse(TriggerPulse(), 150)
	if a != b {
		t.Fatalf("TickPulse not deterministic for identical inputs: %+v vs %+v", a, b)
	}
}
