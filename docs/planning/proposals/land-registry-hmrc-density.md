# Proposal: Density Zoning, Land Economics, Land Registry & In-Game HMRC

**Author:** Bev, 2026-08-18 (Aaron-directed, interview rulings recorded §8) · **Status:** BOW-filed design; part of the revenue/capex/opex engine programme. All rates and magnitudes are balance-number-regime placeholders.

## 1. Zone types, density 1–5, our own palette
Every zone type carries a **density range 1–5** (Blue's light-green→very-dark-green ladder, generalised). Zone types split industry three ways:

| Zone type | Density 1 (example) | Density 5 (example) | Palette family (ours, tcell 256) |
|---|---|---|---|
| Residential | detached seaside houses | tower-block flats | greens (pale → deep) |
| Commercial | corner shops / parade | intense retail core | teals (pale → deep) |
| Office | business park | CBD towers (City of London / NYC downtown) | violets (pale → deep) |
| Industry | workshops | heavy plant | ambers (pale → deep) |
| Farming | smallholding | industrial agri | olive/khaki ladder |
| Mining | surface pit | deep/large-scale extraction | rust/brown ladder |

Palette is a proposal — final colours are a UI pass with Aaron's eyes on a rendered swatch; keys in data (`data/zoning.json`), never hardcoded (GR#15). Colour names/keys are semantic (`res1..res5`, `com1..com5`, `off…`, `ind…`, `farm…`, `mine…`).

## 2. Density control — three levels (ruled)
1. **Per-tile paint:** player picks density 1–5 when zoning; repaintable later.
2. **District policy:** allowed density band per district (e.g. "res 4–5 only" → flats are the only option; can't afford them → live outside and commute in, loading the transport network per FEAT-161/162 aggregate flows).
3. **Citywide default policy:** fallback band when a district has none.
**Auto-allocation** works within whatever band applies: the CBD gradient pattern (dense core → stepped falloff) is the default auto-allocator shape.

## 3. Land economics
- Every tile starts at a **nominal value** at game start.
- **Land value** rises with demand (pressure per square foot), investment, adjacency, and building maturity; **falls** when demand slackens — land cost to the player tracks the same curve, so low-pressure land is cheap to acquire.
- Density interacts with value both ways: value supports density; enforced density concentrates demand.

## 4. Land registry (module, consistency checker BY DESIGN)
A per-tile ledger and the SSOT for tenure:
- **Crown model (ruled):** the player is the ultimate owner of all land (the "king"), with **compulsory purchase** powers. Private parties hold **freehold or leasehold** interests in buildings.
- A building (any type) is owned by a **netizen** or a **netibusiness** — which may be a not-for-profit **housing association** where housing policies allow (policy-gated).
- Tracks per tile: player acquisition cost, current land value, tenure records (who owns what), building id, maturity level.
- **Consistency checker, designed-in from day one:** every property correctly listed ∧ every listing points to the correct property (bidirectional referential integrity). Runs **once per game year** as a scheduled audit event; any mismatch is a registry error (GR#7) and surfaces in-game as an HMRC audit finding. This is the same invariant discipline as the people/money conservation checks: the registry must never silently drift from the world.

## 5. Building maturity: level 1–99, milestone every 10 (all four effects ruled)
Well-maintained **and** well-utilised buildings rise in level over time (maintenance funded × occupancy/custom × age). Each milestone (10/20/…/90) confers **all** of:
1. **Land value multiplier** — raises the tile's value contribution (and therefore council tax / rateable base).
2. **Capacity/quality step** — more residents/jobs/custom or higher service tier.
3. **Visual + prestige** — appearance tier + feeds area attractiveness (parks-style demographic pull).
4. **Unlock gates** — tile-level upgrades become available (penthouse conversion, flagship store, HQ status…).
Neglect/vacancy decays the level. Deterministic: level change is a pure function of tracked inputs per month (GR#21).

## 6. HMRC (in-game revenue function; the land registry is part of it)
One coherent tax authority handling:
- **PAYE income tax** — every working netizen.
- **NI** — national insurance alongside PAYE.
- **Corporation tax** — every netibusiness, simple model: % of profits.
- **Stamp duty** — on **purchases only** (ruled): freehold/leasehold ownership transfers. Renters moving pay nothing.
- **Council tax** — every household, monthly.
- **Import/export duty** — international movements only (chunnel, RoRo ferry, air freight); **zero** tax on commuting and on domestic out-of-tile goods movement.
- **Annual registry audit** (§4) surfaces as an HMRC function.
Rates are data-file instruments extending `data/tax_instruments.json` (FEAT-056 loader precedent); all magnitudes balance-regime placeholders with directional tests.

## 7. Service throttles & sliders (ties FEAT-161/162)
Player can **stop** a service (trains, buses, taxis…) or slide provision up/down: more vehicles/frequency ⇒ higher opex, per line/route. Auto-allocation (FEAT-161) remains the default; sliders are the manual override layer on top, per service type and per line. Stopping a service load-sheds its riders onto other modes/roads (and hits attractiveness/worklife).

## 8. Interview rulings (Aaron, 2026-08-18)
1. **Ownership:** crown/player ultimate land owner + compulsory purchase; freehold/leasehold building ownership by netizens or netibusinesses incl. policy-gated NFP housing associations.
2. **Stamp duty:** purchases only.
3. **Density control:** all three levels (per-tile paint, district bands, citywide default).
4. **Milestones:** all four effects (value multiplier + capacity/quality + prestige + unlock gates).

## 9. Ties & guard-rails
engine.world (tiles), engine.build (structures), engine.finance/fiscal/tax (money + instruments), engine.households (moving-home FEAT-170: a purchase move triggers stamp duty), engine.attract (prestige/value feedback), FEAT-161/162 (sliders), engine.maintenance (level inputs), policies (housing-association gating). New cross-module edges MUST be registered in code.json before acceptance prose (GR#25) — BA flags, lead registers. Determinism: all valuations/level changes are pure monthly functions of tracked state (GR#21). Registry consistency check mirrors invariant-module patterns (GR#12/#17: the audit failing must be loud, and an audit that cannot evaluate must not report success).
