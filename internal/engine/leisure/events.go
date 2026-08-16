package leisure

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ScheduleEvent schedules one event on the §42 calendar and pushes its
// crowd-transport demand through the engine.leisure → engine.traffic edge
// (AC-6) — a real trip-generation load, not a satisfaction-only flag. A
// malformed or missing date/venue reference is rejected at schedule time
// (ErrMalformedEvent/ErrUnknownVenue), never a silently-dropped event (AC-12).
func (a *LeisureAPI) ScheduleEvent(e Event, correlationID string) error {
	if err := a.checkNotCopied("ScheduleEvent"); err != nil {
		return err
	}
	if e.Day < 0 || e.Day >= citizens.DaysPerMonth {
		return errs.New(ErrMalformedEvent, correlationID, map[string]any{"field": "day"})
	}
	if e.VenueID == 0 {
		return errs.New(ErrMalformedEvent, correlationID, map[string]any{"field": "venueId"})
	}
	if int(e.Kind) >= int(numEventKinds) {
		return errs.New(ErrMalformedEvent, correlationID, map[string]any{"field": "kind"})
	}

	a.mu.Lock()
	_, ok := a.venues[e.VenueID]
	traffic := a.traffic
	crowd := a.cfg.EventCrowd[e.Kind]
	a.mu.Unlock()

	if !ok {
		return errs.New(ErrUnknownVenue, correlationID, map[string]any{"venueId": e.VenueID})
	}

	// Push the crowd-transport demand BEFORE recording the event so a traffic
	// failure cannot leave a silently-dropped event (AC-12's failure ordering).
	if traffic != nil {
		if err := traffic.AddTripDemand(TripDemand{
			District: e.District,
			Day:      e.Day,
			Count:    crowd,
			Purpose:  "event:" + e.Kind.String(),
		}); err != nil {
			return err
		}
	}

	a.mu.Lock()
	a.events = append(a.events, e)
	a.mu.Unlock()
	return nil
}

// Events returns the scheduled events calendar in schedule order.
func (a *LeisureAPI) Events(correlationID string) []Event {
	_ = a.checkNotCopied("Events")
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Event, len(a.events))
	copy(out, a.events)
	return out
}
