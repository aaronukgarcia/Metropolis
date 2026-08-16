package leisure

import "testing"

// TestEventCrowdTransport proves AC-6: a scheduled event pushes a real
// crowd-transport trip-demand through the engine.traffic edge — a genuine
// trip-generation load, not a satisfaction-only flag.
func TestEventCrowdTransport(t *testing.T) {
	a, _, tr, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 100, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}

	baseline := tr.totalDemand()
	if baseline != 0 {
		t.Fatalf("expected zero baseline demand, got %d", baseline)
	}

	e := Event{ID: 1, Kind: EventFestival, District: 1, Day: 5, VenueID: 100}
	if err := a.ScheduleEvent(e, "test"); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	if got := tr.totalDemand(); got <= baseline {
		t.Fatalf("event must generate crowd-transport demand > baseline: %d", got)
	}
	if len(tr.demands) == 0 || tr.demands[0].District != 1 || tr.demands[0].Count <= 0 {
		t.Fatalf("trip demand record malformed: %+v", tr.demands)
	}
	if got := len(a.Events("test")); got != 1 {
		t.Fatalf("event not recorded on the calendar: %d", got)
	}
}

// TestMalformedEvent proves AC-12: a malformed or missing date/venue
// reference is rejected at schedule time with a registry-sourced error, and
// no event is silently dropped or created — the calendar stays empty and no
// trip demand is pushed.
func TestMalformedEvent(t *testing.T) {
	a, _, tr, _ := newWiredAPI(t, 1)

	if err := a.OpenVenue(Venue{ID: 100, Category: CategoryDining, District: 1, Capacity: 1000}, "test"); err != nil {
		t.Fatalf("open venue: %v", err)
	}

	// Malformed date.
	err := a.ScheduleEvent(Event{ID: 1, Kind: EventFestival, District: 1, Day: -1, VenueID: 100}, "test")
	assertErrCode(t, err, ErrMalformedEvent)

	// Missing venue reference.
	err = a.ScheduleEvent(Event{ID: 2, Kind: EventFestival, District: 1, Day: 5, VenueID: 0}, "test")
	assertErrCode(t, err, ErrMalformedEvent)

	// Unknown venue reference.
	err = a.ScheduleEvent(Event{ID: 3, Kind: EventFestival, District: 1, Day: 5, VenueID: 999}, "test")
	assertErrCode(t, err, ErrUnknownVenue)

	if got := len(a.Events("test")); got != 0 {
		t.Fatalf("malformed events must not be recorded: %d", got)
	}
	if got := tr.totalDemand(); got != 0 {
		t.Fatalf("malformed events must not push trip demand: %d", got)
	}
}
