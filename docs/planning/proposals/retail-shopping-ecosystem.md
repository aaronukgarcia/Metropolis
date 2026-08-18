# Proposal: Retail & Shopping Ecosystem (food, shops, online, delivery)

**Author:** Bev, 2026-08-18 (Aaron-directed) · **Status:** BOW-filed for future build; enriches engine.shopping (MOD-050), engine.market, engine.freight/logistics, and the transport-loading model (MOD-035/FEAT-161). All magnitudes are balance-number-regime placeholders.

## 1. The core loop
**People need food.** Households run **weekly** grocery shops (the staple cycle) and **monthly** big shops (bulk/top-up), plus ad-hoc trips to other shops. Every shopping act is one of: a **physical trip** (loads the transport network — walk/cycle/bus/car per the access model), or an **online order** (loads the road network with a **delivery van** instead). Supermarkets must themselves be **stocked** — freight deliveries from distribution via engine.freight/logistics, so a store that outsells its restocking runs empty shelves (fresh-food access and satisfaction fall).

## 2. Random dispersal
Shopping demand is **dispersed, not lockstep**: each household's shop day/time draws from a seeded deterministic stream (per-household, GR#21) so trips spread across the week and day rather than spiking identically — producing realistic staggered load on roads, buses, and store queues. Dispersal parameters (spread shape, peak weighting e.g. Saturday bias) are data placeholders.

## 3. Six supermarket archetypes (modelled on real UK chains, named generically in-game)
| Archetype | Real-world model | Character |
|---|---|---|
| Discounter | Aldi/Lidl | small footprint, low price, limited range, high turnover |
| Value big-box | Asda | large format, low price, big weekly/monthly shops, big car parks |
| Mainstream | Tesco | full-range, every format (express → superstore → hypermarket), delivery fleet |
| Quality mid | Sainsbury's | mainstream+, stronger fresh offer |
| Premium | Waitrose/M&S | high price, high quality, strong appeal to affluent demographics |
| Convenience | Spar/Co-op/corner | tiny footprint, walkable, high price-per-item, top-up shops |

Each archetype: price level, range breadth, fresh-food quality, footprint/formats, catchment draw, demographic affinity (ties the parks-style demographic-fit idea into retail), delivery capability, and restock intensity. Households choose stores by price-sensitivity × distance/access × quality preference (deterministic choice model).

## 4. Society shopping profile by settlement tier
The retail mix evolves with the settlement (ties the unlock ladder M1–M13):
- **Village:** one convenience shop (Spar-type). Everything else = travel out or online.
- **Small town:** convenience + 1 discounter/mainstream small format + high-street shops.
- **Large town:** multiple supermarkets (2–3 archetypes), retail park, weekly market.
- **City:** all six archetypes, hypermarket(s), mall, strong online penetration.
- **Mega city:** multiple hypermarkets per district, dark stores/fulfilment centres, near-total archetype coverage; the Planning-Dept auto-provision (FEAT-171) can auto-fill retail coverage gaps at scale.
The profile is a data-driven template per tier — what mix "should" exist per capita — driving both player guidance (demand signals) and auto-provision.

## 5. Online shopping & delivery fleets
Two streams, both **loading the road network with vans** (the transport digital-world models van fleets like buses: depot → route → road occupancy):
- **Supermarket delivery:** grocery orders fulfilled by the store's own van fleet (archetype-dependent: mainstream/value have big fleets; discounters little/none). Slots/capacity bound by vans + road time; failure → missed deliveries → satisfaction hit.
- **General online retail (Amazon-analogue "fulfilment giant"):** non-food goods ordered online, fulfilled from **fulfilment centres** (catalogue: fulfilment_centre, last_mile_depot, parcel_hub — already exist) via last-mile van routes. Online share grows with era/tier and undercuts high-street footfall (a real tension: convenience vs town-centre vitality, feeding engine.cafe's street-life and shopping's fresh-food access).
Online share by tier/era, van capacity, delivery-failure rates: data placeholders.

## 6. Supply chain (stores get stocked)
Every store consumes stock per sale; restocking arrives as freight (engine.freight commodity flows: foodStaples/consumerGoods) from distribution (warehouse/fulfilment tiers) on lorries — loading roads too. Shelf-stock is a conserved quantity (invariant-friendly): delivered − sold − waste = Δstock; empty shelves propagate to fresh-food access (MOD-050's AC-7 surface) and store appeal.

## 7. Ties & guard-rails
engine.shopping (MOD-050 — currently in rework; this design is its target end-state), engine.market (prices), engine.freight/logistics (restock + vans as road load), engine.traffic/MOD-035 (trip + van loading), FEAT-161/162 (fleet auto-allocation/saturation → user-agreed expansion), FEAT-171 (auto-provision), demographic-fit (parks precedent), unlock tiers. Determinism: all dispersal/choice draws via det.NewStream (GR#21). No real brand names in code/data — archetype keys only (this doc records the models). GR#25: new cross-module edges (shopping→freight, shopping→traffic van-loading) must be registered before build prose.
