package freight

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// StorageSite is the queryable view of one storage site (AC-6): its type,
// the commodity class it accepts, its documented capacity, and its current
// per-commodity stock (tonnes). It is the holding point for a chain stage's
// output pending onward movement or export.
type StorageSite struct {
	Type           SiteType
	CommodityClass StorageClass
	CapacityTonnes int64
	Stock          map[Commodity]int64
}

// StorageSite returns the named site's view, or ErrUnknownStorageSite for
// an unregistered site type (AC-12) — never a silently-created zero-value
// site.
func (f *FreightAPI) StorageSite(st SiteType) (StorageSite, error) {
	if err := f.checkNotCopied("StorageSite"); err != nil {
		return StorageSite{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	site, ok := f.sites[st]
	if !ok {
		return StorageSite{}, errs.New(ErrUnknownStorageSite, f.correlationID, map[string]any{
			"site": string(st),
		})
	}
	return snapshotSite(site), nil
}

// StorageSites returns all four documented site types in fixed order
// (deterministic, GR#21).
func (f *FreightAPI) StorageSites() []StorageSite {
	if err := f.checkNotCopied("StorageSites"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]StorageSite, 0, len(allSiteTypes))
	for _, st := range allSiteTypes {
		out = append(out, snapshotSite(f.sites[st]))
	}
	return out
}

func snapshotSite(s *siteState) StorageSite {
	stock := make(map[Commodity]int64, len(s.stock))
	for c, t := range s.stock {
		stock[c] = t
	}
	return StorageSite{
		Type:           s.cfg.Type,
		CommodityClass: s.cfg.CommodityClass,
		CapacityTonnes: s.cfg.CapacityTonnes,
		Stock:          stock,
	}
}

// Store explicitly re-homes tonnes of a commodity from its canonical
// storage site into a named site (AC-6's command path, distinct from the
// tick's automatic leftover routing). It enforces:
//
//   - a known commodity (ErrUnknownCommodity) and a known site type
//     (ErrUnknownStorageSite), never a silently-created entry (AC-12);
//   - a non-negative tonnage (ErrNegativeTonnage, AC-13);
//   - type matching: the commodity's storage class must equal the site's
//     commodity class (ErrStorageTypeMismatch) — grain cannot silently
//     occupy tank-farm capacity (AC-6);
//   - the site's capacity and the source's available stock (a partial
//     store is a real, bounded result, never a silent overflow).
//
// It is conservation-neutral: it moves tonnes OUT of the canonical site and
// INTO the named site, so total stock is unchanged. It returns the tonnes
// actually moved.
func (f *FreightAPI) Store(commodity Commodity, tonnes int64, site SiteType) (int64, error) {
	if err := f.checkNotCopied("Store"); err != nil {
		return 0, err
	}
	if tonnes < 0 {
		return 0, errs.New(ErrNegativeTonnage, f.correlationID, map[string]any{
			"commodity": string(commodity),
			"tonnes":    tonnes,
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return 0, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	s, ok := f.sites[site]
	if !ok {
		return 0, errs.New(ErrUnknownStorageSite, f.correlationID, map[string]any{
			"site": string(site),
		})
	}
	if cc.StorageClass != s.cfg.CommodityClass {
		return 0, errs.New(ErrStorageTypeMismatch, f.correlationID, map[string]any{
			"commodity":      string(commodity),
			"commodityClass": string(cc.StorageClass),
			"site":           string(site),
			"siteClass":      string(s.cfg.CommodityClass),
		})
	}

	source := f.sites[f.cfg.canonicalSite[cc.StorageClass]]
	move := tonnes
	if available := source.stock[commodity]; move > available {
		move = available
	}
	if headroom := num.SatSub(s.cfg.CapacityTonnes, s.stock[commodity]); move > headroom {
		move = headroom
	}
	if move < 0 {
		move = 0
	}
	source.stock[commodity] = num.SatSub(source.stock[commodity], move)
	s.stock[commodity] = num.SatAdd(s.stock[commodity], move)
	return move, nil
}

// Stock returns the current tonnage of a commodity held at a named site
// (AC-6's per-site stock accessor, the source of AC-10's StorageDelta
// term). Errors with ErrUnknownCommodity or ErrUnknownStorageSite as
// applicable.
func (f *FreightAPI) Stock(commodity Commodity, site SiteType) (int64, error) {
	if err := f.checkNotCopied("Stock"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.cfg.commodities[commodity]; !ok {
		return 0, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	s, ok := f.sites[site]
	if !ok {
		return 0, errs.New(ErrUnknownStorageSite, f.correlationID, map[string]any{
			"site": string(site),
		})
	}
	return s.stock[commodity], nil
}
