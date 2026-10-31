package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-2326609833: the "f4.coastal" read-only protocol view -- the coastal
// arrivals pipeline census (arrivals, people, in-processing, in-hotels,
// granted, not-granted), the rescue shortfall figures, the cumulative costs
// posted to the ledger, and any rescue service id the last month found
// unregistered. PROPOSED name (the pack leaves it to the lead: "f4.services
// extension or f4.coastal"); a separate view keeps ui.screen.services'
// wirePatch untouched. There is no UI twin, so this schema is compose-owned
// and versioned independently (coastalWireSchemaVersion).
const coastalViewSubscriptionName = "f4.coastal"

const coastalWireSchemaVersion = 1

// coastalWirePatch is the f4.coastal wire shape.
type coastalWirePatch struct {
	SchemaVersion int   `json:"schemaVersion"`
	Month         int64 `json:"month"`
	// Arrivals is the number of boat arrival events so far; People the number
	// of people aboard them (= cases created).
	Arrivals int   `json:"arrivals"`
	People   int64 `json:"people"`
	// InProcessing counts cases not yet resolved; InHotels those of them
	// awaiting a caseworker (the hotel-requisition overflow).
	InProcessing int64 `json:"inProcessing"`
	InHotels     int64 `json:"inHotels"`
	Granted      int64 `json:"granted"`
	NotGranted   int64 `json:"notGranted"`
	// RescueShortfalls counts arrival events whose rescue was under-resourced.
	RescueShortfalls int   `json:"rescueShortfalls"`
	ShortfallPeople  int64 `json:"shortfallPeople"`
	// RescueUnregistered lists rescue service ids absent from engine.services
	// in the last month (distinct from zero capacity).
	RescueUnregistered []string `json:"rescueUnregistered"`
	// Posted* are the cumulative micropounds posted to the ledger.
	PostedHotels      int64 `json:"postedHotels"`
	PostedDeparture   int64 `json:"postedDeparture"`
	PostedProcessing  int64 `json:"postedProcessing"`
	PostedIntegration int64 `json:"postedIntegration"`
}

// buildCoastalPatch serves the f4.coastal view. A nil coastal wiring (a bare
// simState) yields the honest all-zero patch, never an error.
func (st *simState) buildCoastalPatch() (json.RawMessage, error) {
	p := coastalWirePatch{SchemaVersion: coastalWireSchemaVersion, RescueUnregistered: []string{}}
	if w := st.coastalW; w != nil {
		c := w.api.Counts()
		w.mu.RLock()
		p.Month = w.lastMonth
		p.PostedHotels, p.PostedDeparture, p.PostedProcessing, p.PostedIntegration = w.posted[0], w.posted[1], w.posted[2], w.posted[3]
		if len(w.unregistered) > 0 {
			p.RescueUnregistered = append([]string{}, w.unregistered...)
		}
		w.mu.RUnlock()
		p.Arrivals, p.People = c.Arrivals, c.People
		p.InProcessing, p.InHotels = c.Processing, c.InHotels
		p.Granted, p.NotGranted = c.Granted, c.NotGranted
		p.RescueShortfalls, p.ShortfallPeople = c.Shortfalls, c.ShortfallPP
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "coastal", "accessor": "json.Marshal"})
	}
	return raw, nil
}
