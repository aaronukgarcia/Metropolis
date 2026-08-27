package refuse

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is AC-11's mass-conservation identity. For every waste stream,
// at every point in the accounting period (the tick, documented in
// doc.go), the identity
//
//	TonnesGenerated == TonnesCollected + TonnesUncollected + TonnesInTransit + TonnesDisposalBacklog
//
// holds exactly (whole kilograms). Each of the four right-hand terms is
// computed INDEPENDENTLY from its own source â€” none is ever inferred as the
// remainder of the others, so a bug in any one term's accounting makes the
// identity fail rather than balancing tautologically by construction
// (exactly analogous to engine.wellbeing.md AC-2/AC-3's additive-identity-
// plus-independence pairing).

// TonnesGenerated returns the cumulative waste generated into bins for a
// stream (kg) â€” the independently-tracked generation counter (AC-11). An
// unknown stream is rejected with ErrUnknownStream.
func (r *RefuseAPI) TonnesGenerated(s Stream) (int64, error) {
	if err := r.checkNotCopied("TonnesGenerated"); err != nil {
		return 0, err
	}
	idx := streamIndex(s)
	if idx < 0 {
		return 0, unknownStreamError(r.correlationID, s)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generated[idx], nil
}

// TonnesCollected returns the cumulative waste that completed a round
// delivery and was processed into its terminal form (landfill fill,
// incineration, compost, or recycling resale) for a stream (kg) â€” the
// independently-tracked completed-delivery counter (AC-11).
func (r *RefuseAPI) TonnesCollected(s Stream) (int64, error) {
	if err := r.checkNotCopied("TonnesCollected"); err != nil {
		return 0, err
	}
	idx := streamIndex(s)
	if idx < 0 {
		return 0, unknownStreamError(r.correlationID, s)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.collected[idx], nil
}

// TonnesUncollected returns the current waste still at cells (in-bin plus
// overflow) for a stream (kg), summed from the bin-stock state itself â€”
// never inferred as a remainder (AC-11). It is computed on demand from the
// cell ledger.
func (r *RefuseAPI) TonnesUncollected(s Stream) (int64, error) {
	if err := r.checkNotCopied("TonnesUncollected"); err != nil {
		return 0, err
	}
	idx := streamIndex(s)
	if idx < 0 {
		return 0, unknownStreamError(r.correlationID, s)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total int64
	for _, c := range r.cells {
		total = num.SatAdd(total, num.SatAdd(c.levels[idx], c.overflow[idx]))
	}
	return total, nil
}

// TonnesInTransit returns the current waste collected by a round but not
// yet delivered to a disposal site (kg), summed from the round ledger â€”
// the refuse-side view of engine.logistics' movement (AC-11; at full
// logistics depth this is the movement ledger, see doc.go).
func (r *RefuseAPI) TonnesInTransit(s Stream) (int64, error) {
	if err := r.checkNotCopied("TonnesInTransit"); err != nil {
		return 0, err
	}
	idx := streamIndex(s)
	if idx < 0 {
		return 0, unknownStreamError(r.correlationID, s)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total int64
	for _, rd := range r.rounds {
		total = num.SatAdd(total, rd.inTransit[idx])
	}
	return total, nil
}

// TonnesDisposalBacklog returns the current waste queued at disposal sites
// awaiting processing (kg), summed from the disposal sites' own queues
// (AC-11) â€” never inferred as a remainder.
func (r *RefuseAPI) TonnesDisposalBacklog(s Stream) (int64, error) {
	if err := r.checkNotCopied("TonnesDisposalBacklog"); err != nil {
		return 0, err
	}
	idx := streamIndex(s)
	if idx < 0 {
		return 0, unknownStreamError(r.correlationID, s)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var total int64
	for _, site := range r.sites {
		total = num.SatAdd(total, site.backlog[idx])
	}
	return total, nil
}
