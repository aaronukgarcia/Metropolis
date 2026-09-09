# Traffic assumptions — FEAT-2326609792 inc0

**Scope:** data-only. This document records every modelling assumption behind `data/traffic/*.json`
(FEAT-2326609792 inc0). No engine code, no TypeScript, no `code.json` edit. GR#25: no cross-module
edge is proposed by this document or by the data files it describes.

---

## A-1 — Demand is generated from PEOPLE, not buildings

**Assumption:** trip generation scales with residents + in-city workers (`scale_ladder.json`
`workersInCity` field), never with building counts or floor area directly.

**Rationale:** Aaron's brief, verbatim: "place transport demand based on people within the city."
A building only matters as a land-use ATTRACTOR (see A-3), never as the demand source.

**What breaks if wrong:** a buildings-driven model double-counts demand when multiple buildings
share the same population (e.g. redeveloping a block into denser housing would spuriously multiply
trips without any population change), and cannot express "trips fall as households shrink" —
exactly the kind of building-vs-people confusion GR#3/GR#15 exist to prevent.

**Governs:** `scale_ladder.json` `dailyPersonTrips`, `workersInCity`.

---

## A-2 — No duplication of modes.json / roads.json / traffic.json / fuel.json values

**Assumption:** every number that already lives in those four files is referenced by id, never
restated. New tables add only NEW figures (average realised occupancy vs. seat capacity, ESAL
factors, parking footprint, per-class BPR overrides, etc).

**Rationale:** GR#3 single source of truth; a restated number silently drifts from its source on
the next balance pass.

**What breaks if wrong:** two copies of "car pcuLoad" or "avenue_2_plus_2 baseCostPounds" that
disagree after the next edit to the source file — an entire defect CLASS this project has already
paid for once (BUG-355 units mismatch class, closed by the units registry).

**Governs:** every table's `refinesModeId` / `roadClassId` / cross-reference fields; see AC-6 in
the acceptance doc for the mechanical check (`validate-traffic-tables.mjs`).

---

## A-3 — Trip ATTRACTION is a separate, deferred concern from trip GENERATION

**Assumption:** `trip_generation.json`'s `landUseTripRates` block documents attraction rates
(trips arriving AT a land use) as a downstream concern for inc2 (per-tile demand forecast); inc0
does not distribute trips spatially.

**Rationale:** inc0's own scope statement (BOW item comment) is "no engine change" — spatial
distribution requires reading the tile grid, which is inc2's job.

**What breaks if wrong:** conflating generation and attraction in one table makes inc2's job
harder (it would need to un-bundle them anyway) and risks inc0 quietly assuming a tile-grid
dependency it isn't supposed to have yet (GR#25 — no unregistered cross-module edge).

**Governs:** `trip_generation.json` `landUseTripRates`.

---

## A-4 — Density bands, not continuous density, anchor mode share

**Assumption:** `mode_share_by_density.json` defines 8 discrete density bands
(rural → megacity) with a log-linear-in-density interpolation rule between adjacent bands (A-6),
rather than a continuous regression function.

**Rationale:** discrete bands are directly citable against real surveys (UK NTS settlement types,
Tokyo Person Trip Survey's ward/prefecture breakdown, Singapore HITS's planning-area bands) —
each band anchors to one real dataset's characteristic density regime. A continuous regression
would need to invent a functional form with no source to check it against.

**What breaks if wrong:** an unanchored continuous curve is unauditable — nobody could tell you
which real city a given density's mode split is "supposed to" resemble.

**Governs:** `mode_share_by_density.json` `bands[]`.

---

## A-5 — Car mode share falls monotonically with density; rail/metro share rises monotonically

**Assumption:** across all 8 bands, `car` share strictly decreases and the sum of
`metro + heavy_rail + hs_rail + tram` strictly increases as density rises.

**Rationale:** this is the load-bearing empirical claim from Aaron's brief (Tokyo/Singapore shape:
"sub-linear per-capita car use as density rises") — it is the ORDERING, not the exact percentages,
that must be defensible.

**What breaks if wrong:** the whole point of the Tokyo-factoring exercise (A-11) collapses if a
megacity rung shows the same car dependency as a small town — every downstream consumer (parking
demand, fuel demand, congestion) would then over-provision for cars at the top of the scale
ladder, which is the opposite of the real-world pattern being modelled.

**Governs:** `mode_share_by_density.json` `bands[]`; verified empirically for the generated
`scale_ladder.json` rows (car: 0.604 at pop=100 falling to 0.090 at pop=98,000,000; rail-family:
0.034 rising to 0.575 over the same range).

---

## A-6 — Interpolation rule: log-linear in density between band anchors, exact renormalisation

**Assumption:** for a density strictly between two band thresholds, each mode's share is
LINEARLY interpolated by density fraction `t = (density - loThreshold) / (hiThreshold -
loThreshold)`, then the resulting 11-vector is renormalised to sum to exactly 1.0 (floating-point
correction only, never a substantive re-weight). The SAME rule governs `scale_ladder.json`'s
own rung-to-rung interpolation for a future runtime consumer (inc1's loader) that needs a
population between two listed rungs: **log-linear in POPULATION** between adjacent rungs, **never
extrapolated beyond the lowest (100) or highest (98,000,000) rung** — matching the BOW item's own
"interpolate, never extrapolate" ruling.

**Non-numeric leaves (BUG-823):** the log-linear rule above applies ONLY to numeric leaves. Every
non-numeric leaf on a `scale_ladder.json` rung — `densityBand` and the entire `provenance`
subtree — is NOT interpolated: it CARRIES THROUGH VERBATIM from the lower bracketing rung. This
is why `provenance` (including its `year` field) is defined as entirely non-numeric by rule —
every field under a rung's `provenance` object is a STRING (e.g. `"year": "2026"`, not `2026`) so
it can never be mistaken for an interpolation target. A numeric `provenance.year` would otherwise
silently log-linear-interpolate to a fractional year (e.g. 2026.4) the moment two bracketing
rungs carried different source years — harmless while every rung reads the same year, but a live
trap the day one rung is re-sourced. The inc1 loader MUST route any leaf under a rung's
`provenance` subtree through the carry-from-lower-rung path, never the log-linear path, and must
reject (not silently coerce) any numeric leaf found there.

**Rationale:** linear-in-density (not linear-in-population) for the mode-share table because
density is the causal driver per A-5; log-linear-in-population for the scale ladder because most
of the modelled quantities (trips, vehicle-km, freight tonnes) scale closer to log-linearly with
population than linearly, avoiding a stair-step artefact between adjacent order-of-magnitude
rungs (100 → 1,000 is a 10x jump; a linear interpolator would badly misrepresent the curve shape
between them).

**What breaks if wrong:** linear-in-population interpolation between rung 100 and rung 1,000
would produce nonsensical mid-values (e.g. a population of 500 interpolating almost entirely
toward the rung-1,000 shape, since linear interpolation over a 900-wide gap starting at 100 is
dominated by the far endpoint) — log-linear keeps the curve's shape sane across decades of scale.

**Governs:** `mode_share_by_density.json` `interpolationRule`; the not-yet-built inc1 loader
(documented here so inc1's BA doc can cite this rule rather than re-deriving it).

---

## A-7 — Purpose dimension (commute/shopping/leisure/business) is deferred, all-purpose v1 only

**Assumption:** `trip_generation.json` and the mode-share table are ALL-PURPOSE aggregates; no
purpose split exists yet.

**Rationale:** UK NTS0409 carries a purpose breakdown, but adding an 11-mode × 8-band × 5-purpose
cube (440 cells) for inc0 is disproportionate (GR#23 proportionality tier spirit) when no engine
consumer needs it yet — inc2's per-tile forecast is the first plausible consumer, and it can
demand the purpose split when it actually needs it.

**What breaks if wrong:** none yet — this is an explicit scope cut, not a risk. If inc2 needs
purpose-specific attraction (e.g. school trips concentrate at 08:30/15:30, not spread through the
day), the peak-hour factor (A-9) is too coarse and a purpose split becomes necessary then.

**Governs:** documents a KNOWN GAP; not a table.

---

## A-8 — Trip rate curve is a gentle bell, not flat, across the population scale

**Assumption:** `TRIP_RATE` per rung rises from 2.35 (pop 100) to a peak of ~2.88 (pop 600,000)
then eases back to 2.48 (pop 98,000,000).

**Rationale:** UK DfT NTS0409's all-purpose trip rate (~2.8/person/day) sits roughly at the
mid-city peak; small settlements show slightly fewer discretionary trips (fewer destinations to
chain trips between); the largest cities in this curve ease toward the Tokyo Person Trip Survey's
documented ~2.4-2.6/person/day figure, reflecting real time-budget and congestion
self-limiting effects at extreme scale (HCM 2010 Ch.16 discusses trip suppression under sustained
high v/c).

**What breaks if wrong:** a flat trip rate across 100 to 98,000,000 population would silently
assume settlement size has zero effect on trip-making behaviour, contradicted by every cited
survey; the specific numbers are still directional placeholders (confidence: derived), but the
SHAPE (non-monotone, bounded 2.3-2.9) is the defensible claim.

**Governs:** `scale_ladder.json` `tripRatePersonPerDay` per rung; `trip_generation.json`
`residentTripRate`.

---

## A-9 — Peak-hour factor falls (spreads) as the city grows

**Assumption:** the fraction of daily trips occurring in the single busiest hour falls from ~11%
at small population to ~9.5% at the largest rungs.

**Rationale:** larger, denser cities offer more transit options and see more staggered working
hours/24-hour economic activity, spreading peak demand — a well-documented pattern in large
transit-rich metros (informal cross-check against Tokyo's notably spread AM peak versus a small UK
town's sharper single-hour peak).

**What breaks if wrong:** an unrealistically sharp or unrealistically flat peak-hour factor would
mis-size any future peak-capacity gate (e.g. how many trains/hour a metro line actually needs);
this is a coarse placeholder pending real Tokyo/Singapore peak-spreading data.

**Governs:** `scale_ladder.json` `peakHourFactor`.

---

## A-10 — Vehicle occupancy is density-adjusted, distinct from modes.json's seat capacity

**Assumption:** `vehicle_classes.json`'s `avgOccupancyPersons` (actual average riders) is a
SEPARATE figure from `data/modes.json`'s `capacityPerUnit` (seat capacity range) — the two answer
different questions (how many people are actually IN the car on average, vs. how many COULD be).

**Rationale:** UK DfT NTS0905 measures actual car occupancy (~1.5-1.6 national average); using
seat capacity (`modes.json`'s 1-5 range) as a stand-in for realised demand would badly overstate
person-throughput per vehicle-trip and understate vehicle-km/parking/fuel demand.

**What breaks if wrong:** every vehicle-km, parking, fuel, and ESAL figure derived from car trips
would be wrong by roughly the ratio of seat capacity to realised occupancy (a ~2-3x error).

**Governs:** `vehicle_classes.json` `roadVehicles[].avgOccupancyPersons`; consumed by
`scale_ladder.json`'s `networkVehicleKmPerDay`/`parkingSpacesDemanded`/`fuelLitresDemandedPerDay`
derivations.

---

## A-11 — Bus subtype capacities are OPERATIONAL refinements of the generic 'bus' mode, not a
contradiction of modes.json's separate 'minibus' MODE

**Assumption:** the brief's bus-subtype list (minibus 16 / single-deck 70 / double-deck 90 /
articulated 120) refines `data/modes.json`'s generic `bus` mode id (seat capacity 80, a coarse
average across subtypes), NOT its separate `minibus` MODE id (14-seat, a distinct nested-logit
demand-choice alternative from the mode-choice model).

**Rationale:** `modes.json`'s `minibus` and `bus` are two of the 12 DEMAND-CHOICE alternatives a
citizen picks between (their capacities are choice-model inputs, tuned for game balance); this
file's 4 bus subtypes are FLEET-OPERATIONS vehicle specs (real UK PSV category capacities, used
for vehicle-km/ESAL/parking-footprint sizing, not for the choice model). The two "minibus" numbers
(14 in modes.json, 16 here) legitimately differ because they measure different things.

**What breaks if wrong:** silently overwriting `modes.json`'s `minibus.capacityPerUnit` with 16
would violate GR#3 (contradicting the SSOT) and risk destabilising the already-shipped nested-logit
demand model's tuning; keeping them explicitly separate (and documenting WHY, per-row, in
`vehicle_classes.json`) avoids a silent GR#3 violation while still satisfying the brief's explicit
ask for 4 named bus subtypes.

**Governs:** `vehicle_classes.json` `busSubtypes`.

---

## A-12 — ESAL factors are the sole home for road-wear ratios; ratios matter more than the
absolute normalisation constant

**Assumption:** `road_wear.json` owns every vehicle-class ESAL (Equivalent Single-Axle Load)
factor; the absolute per-100-vehicle-km unit chosen (car = 0.0003) is an internal normalisation
constant, not independently meaningful — only the RATIOS between classes (articulated truck ≈
10,000x a car, matching Aaron's brief anchor) carry real-world grounding from AASHTO's published
4th-power law.

**Rationale:** the AASHTO 1993 Guide's 4th-power law (`ESAL_ratio = (loadA/loadB)^4`) is the
literature-cited mechanism; the SPECIFIC axle loads Metropolis's placeholder vehicle fleet carries
are not independently measured, so only the resulting ratio is defensible, not the absolute scale.

**What breaks if wrong:** if a future consumer treats `esalFactorPer100VehicleKm` as an absolute,
externally-comparable AASHTO ESAL count (rather than an internally-consistent ratio), it will
mis-report against any real-world benchmark that expects true AASHTO units.

**Governs:** `road_wear.json` `esalFactors`; consumed by `scale_ladder.json`'s
`roadWearESALPerDay`.

---

## A-13 — Emergency incident rates and response-time targets are separately sourced; targets are
measured, incident rates are derived

**Assumption:** `emergency_response.json` distinguishes the RESPONSE-TIME TARGETS (NHS ARP,
UK Fire & Rescue IRMP first-attendance targets, UK police Grade 1 targets — measured/published
standards) from the INCIDENT RATES per 1,000 population (derived approximations cross-checked
against national incident-volume statistics, not a single per-city measured figure).

**Rationale:** the TARGET a service aims for is a publicly documented policy figure; how OFTEN a
given population generates a dispatched incident varies enormously by local context (deprivation,
demographics, land use) and no single "per 1,000 population" figure is authoritative — this table
is honest about that confidence gap (per-row `confidence: measured` vs `confidence: derived`).

**What breaks if wrong:** conflating the two would let a future consumer treat the incident RATE
as being as reliable as the TARGET, when in fact the rate is the far shakier number.

**Governs:** `emergency_response.json` `services[]` (targets) vs `incidentRates` (rates).

---

## A-14 — The Tokyo-factoring method for rungs ≥20,000,000

**Method, spelled out in five lines (per the task brief's explicit ask):**
1. Anchor Greater Tokyo (~37M population, ~2,700 persons/km² metro-wide, Tokyo Person Trip Survey
   rail-heavy central mode split) as the real-world reference point nearest the 20M-40M rung band.
2. Extend the density curve (A-4/A-5's 8 bands) SUB-LINEARLY beyond Tokyo's own density toward the
   98M rung, using Singapore's higher islandwide density (~8,300/km², LTA HITS) as the second
   real-world anchor for how mode share continues shifting at extreme density.
3. Hold the ORDERING claim (car share monotonically falls, rail/metro share monotonically rises)
   fixed all the way to 98M — this is the "sub-linear per-capita car use as density rises" the
   brief specifies, not a literal claim that a 98M city IS Tokyo scaled up 2.6x.
4. **Known gap, flagged not hidden:** the brief's own headline figure ("~40M+ trips/day" for
   Tokyo's 37M population, ≈1.08 trips/person) is LOWER than the well-documented Tokyo Person Trip
   Survey's own published ~2.4-2.6 trips/person/day figure — the brief's "40M trips/day" more
   plausibly describes a SUBSET (e.g. rail/transit boardings only, or a household-count rather than
   person-count denominator) than the full person-trip total. This document uses the Person Trip
   Survey's ~2.4-2.6 figure as the working anchor (A-8) and states this discrepancy explicitly
   rather than silently reconciling it — see "Where this brief may be wrong" below.
5. No rung is EXTRAPOLATED beyond 98,000,000 — the ladder's own top rung — per the BOW item's
   explicit "interpolate, never extrapolate" instruction (A-6).

**What breaks if wrong:** if the Tokyo anchor is misapplied (e.g. treating 37M-Tokyo's ABSOLUTE
trip count as directly transferable to a 40M Metropolis city with a different economic structure),
the megacity rungs would inherit Tokyo's specific numbers rather than a defensible ORDERING and
SHAPE — this document treats the anchor as a shape/ordering constraint, never a literal transplant.

**Governs:** `scale_ladder.json` rungs for population ≥ 20,000,000; `mode_share_by_density.json`
`megacity`/`metro_core`/`high_density` bands.

---

## Units needed (for the lead to register in `code.json`'s `units` section — not registered here)

| Proposed unit key | Meaning | Used in |
|---|---|---|
| `transport.personTripsPerDay` | person-trips/day, one-way journeys by one person | scale_ladder.json, trip_generation.json |
| `transport.densityPersonsPerKm2` | persons per square kilometre | scale_ladder.json, mode_share_by_density.json |
| `transport.vehicleKmPerDay` | vehicle-kilometres travelled per day | scale_ladder.json |
| `transport.tonnesFreightPerDay` | metric tonnes of freight moved per day (matches existing `freight.json`/`logistics.json` tonne convention — reuse, do not re-register) | scale_ladder.json, trip_generation.json |
| `transport.pcuPerLanePerHour` | passenger-car-unit throughput per lane per hour (HCM convention) | link_capacity.json |
| `transport.trainsPerHour` | scheduled trains per hour on a rail/tube class | link_capacity.json |
| `transport.esalPer100VehicleKm` | AASHTO-derived equivalent-single-axle-load units per 100 vehicle-km (internal ratio unit, see A-12) | road_wear.json, scale_ladder.json |
| `transport.parkingSpaces` | count of parking spaces (dimensionless, but distinct enough from a bare integer to warrant a unit key given how many tables use it) | parking.json, scale_ladder.json |
| `transport.evChargePoints` | count of EV charge points | scale_ladder.json |
| `transport.incidentsPer1000PopulationPerDay` | dispatched emergency incidents per 1,000 population per day | emergency_response.json |

All monetary figures (`taxation.json`) reuse the existing `money.micropound` unit already
registered in `code.json` — no new money unit needed.

---

## Schema section — every file's shape, precisely enough to write a loader from this document alone

### `scale_ladder.json`
Top-level: `version` (int), `meta` (object), `provenance` (object: `source`/`year`/`confidence`/`note`),
`rungs` (array, ascending by `population`). Each rung object:
```
population: int
densityPersonsPerKm2: number
densityBand: string (matches a mode_share_by_density.json band id, or "band1~band2" during
             interpolation — the generated file's rows always resolve to a single named band)
tripRatePersonPerDay: number
workersInCityShare: number [0,1]
workersInCity: int
dailyPersonTrips: int
modeShare: { <modeId>: number, ... }  // 11 keys, sums to 1.0 +/-1e-6, keys match
           mode_share_by_density.json's modeIds array
tripsByMode: { <modeId>: int, ... }  // dailyPersonTrips * modeShare[id], rounded
busSubtypeTrips: { minibus, single_deck, double_deck, articulated: int }  // split of tripsByMode.bus
avgTripLengthKm: number
peakHourFactor: number [0,1]
networkVehicleKmPerDay: int
freightTonnesPerDay: int
freightTonnesBySector: { construction, manufacturing, food, logistics_retail, services_office: int }
freightTonnesByVehicleClass: { cargo_van, rigid_truck, articulated_truck, freight_train: int }
parkingSpacesDemanded: int
fuelLitresDemandedPerDay: int
evKWhDemandedPerDay: int
evChargePointsNeeded: int
emergencyIncidentsPerDay: int
roadWearESALPerDay: int
provenance: { source: string, year: string, confidence: string }  // rung-level provenance;
           EVERY field here is a STRING by rule (BUG-823) -- year is "2026" not 2026 -- so the
           whole subtree is non-numeric and is carried through verbatim from the lower bracketing
           rung per A-6, never log-linear-interpolated
```

### `mode_share_by_density.json`
`modeIds` (array of 11 mode-id strings); `bands` (array of 8, ascending by
`densityPersonsPerKm2Min`), each `{ id, densityPersonsPerKm2Min, shares: {<modeId>: number},
source, year, confidence }` (per-row provenance strings, BUG-824 r2) with `shares` summing to
1.0; `interpolationRule` (string, see A-6).

### `trip_generation.json`
`residentTripRate` (documents the shape, points at scale_ladder.json for actual values — see A-8);
`workerTripRate.commuteLegsPerWorkerPerDay`; `landUseTripRates` (map of land-use id →
`{tripsAttractedPerUnitPerDay, unit, source, year, confidence}`); `freightTonnesPerJobPerDay`
(map of 5 sector ids); `sectorMixByDensityBand` (map of 8 band ids → sector-share map summing to
1.0).

### `vehicle_classes.json`
`roadVehicles` (array: car/motorbike/taxi/cargo_van/rigid_truck/articulated_truck, each with
`id`, `refinesModeId` (nullable), `avgOccupancyPersons`, `fuelLitresPerKm`, `kWhPerKm` (nullable),
`parkingFootprintM2`, optionally `capacityTonnes`); `busSubtypes` (map of 4 ids → subtype spec,
each `refinesModeId: "bus"`); `fixedTrack` (array: tram/metro/heavy_rail/hs_rail/ferry);
`freightTrain` (single object).

### `link_capacity.json`
`roadClasses` (array of 11, one per `data/roads.json` class id, `{roadClassId,
capacityPcuPerLanePerHour, note, source, year, confidence}` — per-row provenance strings,
BUG-824 r2); `roadClassSource` / `railClassSource` (strings); `bprCurve` (`defaultAlpha`/`defaultBeta`/
`defaultCapacityPerLanePerHour` as reference pointers to `data/traffic.json`, plus
`perClassOverrides` map); `railClasses` (array of 4: rail/hs1/tram/metro, each with
`trainsPerHourMax`).

### `emergency_response.json`
`services` (array of 3: ambulance/fire/police, each `{service, targetMinutesUrban,
targetMinutesRural, ...}`); `incidentRates` (map of 3 services + `combinedTotalPer1000PerDay`);
`speedDegradation` (`curve` array of `{vOverC, speedFactor}` anchors, plus `narrowClassPenalty`
and `interpolationRule`).

### `parking.json`
`demandByLandUse` (map of 7 land-use ids); `kerbVsOffStreet.byDensityBand` (map of 8 band ids →
`{kerbShare, offStreetShare}` summing to 1.0); `turnover` (map of 4 parking-type ids →
`{turnoverPerDaySpacesUsed}`).

### `road_wear.json`
`esalFactorBasis` (documents the AASHTO law + normalisation choice, A-12); `esalFactors` (map of
7 vehicle-class ids → `{esalFactorPer100VehicleKm, ratioToCarPerPass}`); `railWear` (placeholder
single object); `wearToRepairCost` (`conditionDecayPerESAL`, `repairTriggerConditionIndex`,
`repairCostCurve` array of `{conditionIndex, repairCostMultiplier}` anchors,
`interpolationRule`).

### `taxation.json`
`fuelDuty` (pointer to `data/fuel.json`); `vehicleExciseDuty` (`bands` array of 13 UK CO2 bands —
first-year rates for cars registered on/after 1 April 2017, 2024-25 DfT published table, gap-free
0 to 255+ g/km, `firstYearRateGBP` per band; `standardRateFlatGBPPerYear` a separate flat non-banded
field for the second-year-onwards rate under the same schedule, BUG-822 fix — the two rate types
were previously mixed under one field name; `fleetAverageByVehicleClass` map of 6 vehicle-class
ids); `roadPricing` (`congestionChargeDefault`, `electronicRoadPricingSingaporeStyle`);
`certificateOfEntitlement` (`quotaMechanism`, `illustrativePriceGBP`, `quotaGrowthRatePerYear`).

### `policy_levers.json`
`levers` (array of 6: coe_ownership_quota, erp_road_pricing, gibraltar_land_constraint_analogue,
integrated_transit_singapore_style, bus_priority_lanes, park_and_ride), each
`{id, name, modelledOn, mechanism, expectedEffect, sideEffects}`.

### `rewards.json`
`safeRoadScore` (`components` array of 4 weighted terms + `formula` string);
`integratedTransportScore` (`components` array of 2 weighted terms + `formula` string).

---

## Where this brief may be wrong, incomplete, or badly scoped

Stated plainly, per the task's own instruction that a disagreement found now is worth more than a
clean build of the wrong thing:

1. **The "~40M trips/day" Tokyo figure in the BOW brief likely undercounts real Tokyo person-trips
   by roughly 2x** (see A-14 point 4). If Aaron has a specific source in mind for that figure
   (possibly rail/transit boardings, or a household-trip count), it should be named explicitly —
   otherwise this document's use of the Person Trip Survey's ~2.4-2.6/person/day figure as the
   working anchor should be treated as the reference number, not the brief's own headline.

2. **"98M" as the top rung is not explained anywhere** — it doesn't match a round number, a known
   real-world city, or an obvious game-design milestone (100M is the design doc's stated citizen
   cap per CLAUDE.md's "Option B — no culls ever, up to 100M"). If 100M is the actual ceiling, a
   98M top rung leaves the last 2% of the population range in extrapolation territory forever
   (A-6 explicitly forbids extrapolation past the top rung) — worth confirming whether 98M is
   deliberate headroom or should be 100,000,000 to match the citizen cap exactly.

3. **inc0's freight-by-sector model is the weakest-sourced table in this set.**
   `trip_generation.json`'s `freightTonnesPerJobPerDay` and `sectorMixByDensityBand` are entirely
   derived approximations with no single cited survey (unlike mode share, which has three real
   surveys to anchor against) — this is flagged honestly (`confidence: derived` throughout) but a
   transport planner would likely push back hardest here. A dedicated ONS/DfT freight-intensity
   dataset search (freight tonnes moved per FTE by SIC sector) would meaningfully improve this
   table if time allows in a future increment.

4. **The brief conflates "provincial airport" with "regional airport" and implies inc0 should
   cover airport passenger demand tables, but `data/airport.json` already fully owns the airport
   tier ladder (pax/day, gates, jobs) — this document deliberately does NOT duplicate it** (GR#3).
   Airport STAFF commute trips are covered (`trip_generation.json`
   `landUseTripRates.airport_job`), but airport PASSENGER surface-access mode split (how do 50,000
   daily pax actually get to/from a Heathrow-class hub — car/taxi/rail/coach) is a genuine gap this
   inc0 does not fill, because `airport.json`'s existing `surfaceAccessReducedPct` field implies
   surface-access modelling is expected to exist somewhere and it currently doesn't, in either
   file. Worth a follow-up increment or an explicit BOW item.

5. **The 45-minute time cap is tight for the quality bar requested** ("defensible to a transport
   planner" across 11+ tables spanning demand, capacity, tax, safety, freight, emergency response,
   and policy). This document is honest about which tables are well-anchored (mode share: 3 real
   surveys; VED: measured UK bands; ESAL basis: literature AASHTO law) versus which are thin
   (freight sector mix, peak-hour spreading, safe-road score weights) — a genuine planner review
   pass on just the thin tables would likely be worth more than broadening scope further in one
   session.
