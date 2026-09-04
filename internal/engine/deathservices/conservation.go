package deathservices

// conservation.go implements AC-14's load-bearing identity and AC-15's
// terminal-exclusivity guarantee.

// Conservation is AC-14's six-term snapshot: every right-hand term is
// independently sourced from its own accessor (never one computed as
// BodiesReleased minus the others -- that would make the identity
// tautologically true and hide a dropped or double-counted body, per
// AC-14's own false-pass-risk note).
type Conservation struct {
	BodiesReleased              int64
	BodiesAwaitingHandling      int64
	BodiesEnRoute               int64
	BodiesBuried                int64
	BodiesCremated              int64
	BodiesHandledByDispensation int64
}

// Sum returns the sum of the five right-hand terms -- callers/tests
// compare this against BodiesReleased to check the identity (AC-14).
func (c Conservation) Sum() int64 {
	return c.BodiesAwaitingHandling + c.BodiesEnRoute + c.BodiesBuried + c.BodiesCremated + c.BodiesHandledByDispensation
}

// Snapshot computes AC-14's Conservation snapshot by independently walking
// every body record and classifying it into exactly one of the five
// buckets by its CURRENT state (AC-15: a body is in exactly one state at
// any time, so this classification can never double-count). BodiesReleased
// is the module's own independent releasedTotal counter, sourced from
// Intake -- not len(d.bodies) (which happens to be numerically identical
// today, since Intake is the only body-creating call, but keeping the two
// as separate fields/sources is what AC-14's "independently sourced"
// requirement is actually asking for: a future bug that let one Intake
// call create two body records for one death, or a body get deleted from
// the map, would show up as a released/current-total mismatch instead of
// being invisible).
func (d *DeathServicesAPI) Snapshot(correlationID string) (Conservation, error) {
	if err := d.checkNotCopied(correlationID, "Snapshot"); err != nil {
		return Conservation{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	c := Conservation{BodiesReleased: d.releasedTotal}
	for _, b := range d.bodies {
		switch b.state {
		case BodyAwaiting:
			c.BodiesAwaitingHandling++
		case BodyEnRoute:
			c.BodiesEnRoute++
		case BodyBuried:
			c.BodiesBuried++
		case BodyCremated:
			c.BodiesCremated++
		case BodyDispensed:
			c.BodiesHandledByDispensation++
		}
	}
	return c, nil
}
