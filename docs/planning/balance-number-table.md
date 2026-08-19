# Balance Number Table — Standing Player-Felt Numbers Across `data/*.json`

> **Status:** STANDING DOCUMENT. Maintained by the BA; **updated as modules land** — every new `data/*.json` or new numeric field added to an existing file is appended here in the same commit that adds the data.
> **Purpose:** a single consolidated table of every *player-felt* number (rates, costs, thresholds, capacities, multipliers) for **Aaron's M2 balance pass — row-by-row approval**. The balance-number regime (Aaron, 2026-08-13): player-felt numbers are **placeholders + directional tests + delegated proposal + Aaron's row-by-row approval**; a balance edit is a **data-file edit, never a Go literal** (GR#15).
> **Scope:** every numeric field that a player sees or feels — prices, build costs, speeds, capacities, thresholds, weights, multipliers, probabilities, durations. Structural fields (version, specRef, `$comment`, meta/units prose) and pure-identity keys (ids, names, labels, enums) are **excluded**.
> **Conventions:**
> * **Money** is int64 **micro-pounds** unless a field's unit says otherwise (`£` = 1,000,000 micropounds). Some files carry whole-£ figures (e.g. `roads.json` `baseCostPounds`, `prison.json` `adultCostPerOffender`).
> * **Directional-test status** answers: *is there a test that asserts the field moves in a direction / affects the model, rather than pinning a magnitude?* "none" means no test in the owning module's `*_test.go` references the field. A "yes" names the test file. The project convention is that acceptance tests assert **direction/structure, never a pinned magnitude**.
> * **Source module** is inferred from the file name + `code.json` / loader (`internal/engine/*` or `internal/foundation/data`).
> * Every value below is a **PLACEHOLDER pending M2** unless a row explicitly says otherwise (e.g. spec-transcribed, Aaron-approved).

---

## Index

| # | File | Source module | Player-felt fields | Section |
|---|------|--------------|--------------------|---------|
| 1 | `accelerator.json` | engine.accelerator | 8 | [§1](#1-acceleratorjson--engineaccelerator) |
| 2 | `airport.json` | engine.airport | 14 × 3 tiers | [§2](#2-airportjson--engineairport) |
| 3 | `attract_terms.json` | engine.attract | 2 | [§3](#3-attract_termsjson--engineattract) |
| 4 | `buildings.json` | engine.build (data.catalogue) | 3 × 8 zones + 356-entry cost/capacity catalogue | [§4](#4-buildingsjson--enginebuild) |
| 5 | `capexport.json` | engine.capexport | 11 | [§5](#5-capexportjson--enginecapexport) |
| 6 | `census.json` | engine.census | 15 | [§6](#6-censusjson--enginecensus) |
| 7 | `coastal.json` | engine.coastal | 12 | [§7](#7-coastaljson--enginecoastal) |
| 8 | `comms.json` | engine.comms | 17 | [§8](#8-commsjson--enginecomms) |
| 9 | `consumption.json` | engine.consumption | 6 + 21 class rows × 4 | [§9](#9-consumptionjson--engineconsumption) |
| 10 | `containerport.json` | engine.freight (containerport) | 8 × 3 tiers | [§10](#10-containerportjson--enginefreight) |
| 11 | `defence.json` | engine.defence | 20 | [§11](#11-defencejson--enginedefence) |
| 12 | `deposits.json` | engine.mining (geology) | 12 | [§12](#12-depositsjson--enginemining) |
| 13 | `education.json` | engine.education | 6 + 9 entry ages | [§13](#13-educationjson--engineeducation) |
| 14 | `external_world.json` | engine.firms (data loader: foundation.data) | 4 × 3 pools | [§14](#14-external_worldjson--enginefirms) |
| 15 | `factorytypes.json` | engine.freight | 5 types × 7 | [§15](#15-factorytypesjson--enginefreight) |
| 16 | `farmtypes.json` | engine.farming | 5 types × 5 | [§16](#16-farmtypesjson--enginefarming) |
| 17 | `fertility.json` | engine.citizens | 5 | [§17](#17-fertilityjson--enginecitizens) |
| 18 | `firms.json` | engine.firms | 13 | [§18](#18-firmsjson--enginefirms) |
| 19 | `fiscal.json` | engine.fiscal | 9 | [§19](#19-fiscaljson--enginefiscal) |
| 20 | `freight.json` | engine.freight | 20 | [§20](#20-freightjson--enginefreight) |
| 21 | `georef.json` | engine.world | 4 (world geometry, not balance) | [§21](#21-georefjson--engineworld) |
| 22 | `leisure.json` | engine.leisure | 18 | [§22](#22-leisurejson--engineleisure) |
| 23 | `logistics.json` | engine.logistics | 8 × 9 commodities + 2 buffers | [§23](#23-logisticsjson--enginelogistics) |
| 24 | `maintenance.json` | engine.maintenance | 2 + 8 classes × 2 | [§24](#24-maintenancejson--enginemaintenance) |
| 25 | `market.json` | engine.market | 9 commodities × 3 | [§25](#25-marketjson--enginemarket) |
| 26 | `minetypes.json` | engine.mining | 6 types × 8 | [§26](#26-minetypesjson--enginemining) |
| 27 | `mining.json` | engine.mining | 13 | [§27](#27-miningjson--enginemining) |
| 28 | `modes.json` | engine.traffic (planned; loader foundation.data) | 12 modes × ~8 + nests + weights | [§28](#28-modesjson--enginetraffic) |
| 29 | `pacing.json` | engine.core | 1 | [§29](#29-pacingjson--enginecore) |
| 30 | `pharmacampus.json` | engine.fdi | 2 anchors × 15 | [§30](#30-pharmacampusjson--enginefdi) |
| 31 | `prison.json` | engine.prison | 18 | [§31](#31-prisonjson--engineprison) |
| 32 | `refinery.json` | engine.chemicals | 2 facilities × 10 + 1 | [§32](#32-refineryjson--enginechemicals) |
| 33 | `refuse.json` | engine.refuse | 20 | [§33](#33-refusejson--enginerefuse) |
| 34 | `roads.json` | engine.roads | 11 classes × 9 + 9 maintenance/upgrade | [§34](#34-roadsjson--engineroads) |
| 35 | `seasonal.json` | engine.season | 9 curves × 12 | [§35](#35-seasonaljson--engineseason) |
| 36 | `services.json` | engine.services | 9 | [§36](#36-servicesjson--engineservices) |
| 37 | `social.json` | engine.social | 12 | [§37](#37-socialjson--enginesocial) |
| 38 | `spaceport.json` | engine.spaceport | 8 | [§38](#38-spaceportjson--enginespaceport) |
| 39 | `tax_instruments.json` | engine.tax | 6 instruments × ~8 | [§39](#39-tax_instrumentsjson--enginetax) |
| 40 | `unlock_trees.json` | engine.unlocks | 12 trees × ~13 nodes × 2 | [§40](#40-unlock_treesjson--engineunlocks) |
| 41 | `wellbeing.json` | engine.wellbeing | 26 | [§41](#41-wellbeingjson--enginewellbeing) |
| 42 | `worklife.json` | engine.worklife | 3 patterns + 1 policy | [§42](#42-worklifejson--engineworklife) |

**Excluded files (no player-felt numbers):**
* `errors.json` — the error registry (GR#7), meta/operational, not game balance.
* `keymap-default.json` — UI key bindings.
* `naming_corpus.json` — string place-name corpus (no numerics).
* `policies.json` — currently empty (`{"entries": []}`); will gain content when MOD-064 / engine.tax policy lands.
* `security-scans.json` — Destructive-scan ledger (meta/security).

---

## §1 `accelerator.json` — engine.accelerator

> **Note:** this file's `meta.balanceRegime` records **Aaron row-by-row approval on 2026-08-18** for most figures (`fdiAnchorDraw` PROVISIONAL until engine.fdi lands). Values are nonetheless recorded here for the standing table.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `facilityThroughput` | 1 | none | engine.accelerator |
| `electricityPeakMultiplier` | 1.5 | none | engine.accelerator |
| `researchRateMultiplier` | 2.0 | yes — spillover_test.go | engine.accelerator |
| `healthSpillover` | 0.5 | yes — concurrency_test.go | engine.accelerator |
| `fdiAnchorDraw` | 500 | yes — concurrency_test.go | engine.accelerator |
| `prestigeBase` | 100 | none | engine.accelerator |
| `prestigePerTick` | 5 | none | engine.accelerator |
| `expertGateThreshold` | 1000 | yes — build_atomicity_test.go | engine.accelerator |

---

## §2 `airport.json` — engine.airport

> Airport-tier ladder: `regional_airport` → `continental_hub` → `heathrow_class_international_airport`. All values are coarse directional placeholders (ASM-logged).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `tiers[regional_airport].milestone` | 7 | none | engine.airport |
| `tiers[regional_airport].costMillions` | 500 (£M) | none | engine.airport |
| `tiers[regional_airport].runways` | 1 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].paxPerRunwayPerDay` | 12000 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].terminalGates` | 40 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].paxPerGatePerDay` | 1000 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].reachMultiplier` | 1 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].freightApronTonnesPerDay` | 40 | none | engine.airport |
| `tiers[regional_airport].contourRadiusM` | 3000 | yes — airport_test.go | engine.airport |
| `tiers[regional_airport].noiseLevelDBA` | 65 | none | engine.airport |
| `tiers[regional_airport].landFootprintHectares` | 500 | none | engine.airport |
| `tiers[regional_airport].jobs` | 1500 | none | engine.airport |
| `tiers[regional_airport].surfaceAccessReducedPct` | 40 | none | engine.airport |
| `tiers[continental_hub].milestone` | 8 | none | engine.airport |
| `tiers[continental_hub].costMillions` | 2000 (£M) | none | engine.airport |
| `tiers[continental_hub].runways` | 2 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].paxPerRunwayPerDay` | 30000 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].terminalGates` | 80 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].paxPerGatePerDay` | 1000 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].reachMultiplier` | 2 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].freightApronTonnesPerDay` | 800 | none | engine.airport |
| `tiers[continental_hub].contourRadiusM` | 6000 | yes — airport_test.go | engine.airport |
| `tiers[continental_hub].noiseLevelDBA` | 72 | none | engine.airport |
| `tiers[continental_hub].landFootprintHectares` | 800 | none | engine.airport |
| `tiers[continental_hub].jobs` | 20000 | none | engine.airport |
| `tiers[continental_hub].surfaceAccessReducedPct` | 30 | none | engine.airport |
| `tiers[heathrow_class_international_airport].milestone` | 9 | none | engine.airport |
| `tiers[heathrow_class_international_airport].costMillions` | 5000 (£M) | none | engine.airport |
| `tiers[heathrow_class_international_airport].runways` | 4 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].paxPerRunwayPerDay` | 50000 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].terminalGates` | 300 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].paxPerGatePerDay` | 1000 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].reachMultiplier` | 4 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].freightApronTonnesPerDay` | 2500 | none | engine.airport |
| `tiers[heathrow_class_international_airport].contourRadiusM` | 12000 | yes — airport_test.go | engine.airport |
| `tiers[heathrow_class_international_airport].noiseLevelDBA` | 78 | none | engine.airport |
| `tiers[heathrow_class_international_airport].landFootprintHectares` | 1200 | none | engine.airport |
| `tiers[heathrow_class_international_airport].jobs` | 76000 | none | engine.airport |
| `tiers[heathrow_class_international_airport].surfaceAccessReducedPct` | 30 | none | engine.airport |

---

## §3 `attract_terms.json` — engine.attract

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `environment.pollutionHalfSaturationKg` | 50000 | none | engine.attract |
| `leisure.bridgeVenueCapacityUnits` | 500 | none | engine.attract |

---

## §4 `buildings.json` — engine.build (data.catalogue)

> **Special case:** 356 catalogue entries carry player-felt magnitudes as **free-text strings** in `costRaw` / `capacityRaw` (e.g. `"6M"`, `"2B"`, `"150 MW"`, `"30k m3/d"`, `"120 hh/ha"`) plus an `unlock.milestone` gate (M1–M12) and a `blightClass`. A full per-entry parse into structured numerics is a **future pass**; this section tabulates the structured per-zone numbers and characterises the catalogue magnitudes.

**Zones (`zones[]`) — 8 rows, structured numerics:**

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `zones[dwelling].materialsBill.constructionMaterials` | 100 t | yes — build_test.go | engine.build |
| `zones[dwelling].labour` | 40 worker-days | yes — build_test.go | engine.build |
| `zones[dwelling].baseLeadTimeDays` | 45 | yes — build_test.go | engine.build |
| `zones[shop].materialsBill.constructionMaterials` | 80 t | yes — build_test.go | engine.build |
| `zones[shop].labour` | 30 | yes — build_test.go | engine.build |
| `zones[shop].baseLeadTimeDays` | 30 | yes — build_test.go | engine.build |
| `zones[office].materialsBill.constructionMaterials` | 150 t | yes — build_test.go | engine.build |
| `zones[office].labour` | 50 | yes — build_test.go | engine.build |
| `zones[office].baseLeadTimeDays` | 60 | yes — build_test.go | engine.build |
| `zones[entertainment].materialsBill.constructionMaterials` | 200 t | yes — build_test.go | engine.build |
| `zones[entertainment].labour` | 60 | yes — build_test.go | engine.build |
| `zones[entertainment].baseLeadTimeDays` | 75 | yes — build_test.go | engine.build |
| `zones[farming].materialsBill.constructionMaterials` | 60 t | yes — build_test.go | engine.build |
| `zones[farming].labour` | 20 | yes — build_test.go | engine.build |
| `zones[farming].baseLeadTimeDays` | 20 | yes — build_test.go | engine.build |
| `zones[manufacturing].materialsBill.constructionMaterials` | 250 t | yes — build_test.go | engine.build |
| `zones[manufacturing].labour` | 80 | yes — build_test.go | engine.build |
| `zones[manufacturing].baseLeadTimeDays` | 90 | yes — build_test.go | engine.build |
| `zones[heavy_industry].materialsBill.constructionMaterials` | 400 t | yes — build_test.go | engine.build |
| `zones[heavy_industry].labour` | 120 | yes — build_test.go | engine.build |
| `zones[heavy_industry].baseLeadTimeDays` | 150 | yes — build_test.go | engine.build |
| `zones[mining].materialsBill.constructionMaterials` | 300 t | yes — build_test.go | engine.build |
| `zones[mining].labour` | 100 | yes — build_test.go | engine.build |
| `zones[mining].baseLeadTimeDays` | 120 | yes — build_test.go | engine.build |

**Catalogue entries (`entries[]`, 356 rows) — magnitude characterisation:**

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `entries[*].unlock.milestone` | M1–M12 (13-tier ladder; raw text from §4) | none (costRaw/capacityRaw not asserted) | engine.build |
| `entries[*].costRaw` | free-text magnitude strings; distinct values incl. `20k` … `2B`, `£500`, `£150M`, `opex` | none | engine.build |
| `entries[*].capacityRaw` | free-text magnitude strings; 87 distinct values incl. `150 MW`, `30k m3/d`, `120 hh/ha`, `1,200 hh/ha`, `300+ shops`, `150 visits/d`, `500 beds` | none | engine.build |
| `entries[*].blightClass` | enum (`none`/`low`/`moderate`/`high`/`severe` + class names) | none | engine.build |
| `entries[*].appealProfile` | 19 distinct tags (e.g. `retirees`, `wealth-magnet`, `mega-density`) | none | engine.build |

---

## §5 `capexport.json` — engine.capexport

> Spec-§36 capital-expenditure contracts. All service rates are `placeholder: true`.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `projectionDemandGrowthPerMonth` | 0.02 | yes — helpers_test.go | engine.capexport |
| `services[refuse].rateMicropounds` | 45,000,000 (£/t) | yes — api_test.go | engine.capexport |
| `services[incineration].rateMicropounds` | 60,000,000 (£/t) | yes — api_test.go | engine.capexport |
| `services[toxic-waste].rateMicropounds` | 500,000,000 (£/t) | yes — api_test.go | engine.capexport |
| `services[sewage].rateMicropounds` | 1,500,000 (£/m³) | yes — api_test.go | engine.capexport |
| `services[hospital-beds].rateMicropounds` | 90,000,000 (£/bed-day) | yes — api_test.go | engine.capexport |
| `services[university-places].rateMicropounds` | 750,000,000 (£/student-yr) | yes — api_test.go | engine.capexport |
| `services[crematorium].rateMicropounds` | 180,000,000 (£/service) | yes — api_test.go | engine.capexport |
| `services[prison-places].rateMicropounds` | 650,000,000 (£/prisoner-yr) | yes — api_test.go | engine.capexport |
| `services[port-transshipment].rateMicropounds` | 3,000,000 (£/t) | yes — api_test.go | engine.capexport |
| `services[mutual-aid].rateMicropounds` | 5,000,000,000 (£ standby retainer) | yes — api_test.go | engine.capexport |

---

## §6 `census.json` — engine.census

> FEAT-133 / feat.citycensus. All placeholders.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `bellCurves.lifespanMeanYears.value` | 75 y | yes — errors_test.go | engine.census |
| `bellCurves.lifespanSpreadYears.value` | 10 y | yes — helpers_test.go | engine.census |
| `bellCurves.retirementAgeYears.value` | 68 y | yes — errors_test.go / helpers_test.go / sec_regression_test.go | engine.census |
| `bellCurves.annualMileage.value` | 10,000 miles/y | yes — helpers_test.go | engine.census |
| `bellCurves.crimeEducationElasticity.value` | -0.5 | yes — helpers_test.go | engine.census |
| `bellCurves.blueWhiteCollarBaselineBlue.value` | 0.6 | yes — helpers_test.go | engine.census |
| `bellCurves.blueWhiteCollarBaselineWhite.value` | 0.4 | yes — helpers_test.go | engine.census |
| `bellCurves.happinessWeightPhysical.value` | 0.34 | yes — helpers_test.go | engine.census |
| `bellCurves.happinessWeightMental.value` | 0.33 | yes — helpers_test.go | engine.census |
| `bellCurves.happinessWeightSatisfaction.value` | 0.33 | yes — helpers_test.go | engine.census |
| `thresholds.consistencyCheckInLagTicks.value` | 2 ticks | yes — errors_test.go | engine.census |
| `thresholds.crimeRate.value` | 0.05 | yes — demographics_test.go | engine.census |
| `thresholds.unfedFraction.value` | 0.10 | yes — helpers_test.go | engine.census |
| `thresholds.uneducatedFraction.value` | 0.20 | yes — helpers_test.go | engine.census |
| `thresholds.uneducatedAttainmentFloor.value` | 30 attainment pts | yes — helpers_test.go | engine.census |

---

## §7 `coastal.json` — engine.coastal

> Small-boat arrivals / asylum-asylum-pipeline model (MOD-076 companion). All placeholders.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `frequency.basePerMonth` | 1.0 | yes — sec_regression_test.go | engine.coastal |
| `frequency.maxBoatSize` | 20 | yes — api_test.go | engine.coastal |
| `frequency.maxArrivalsPerMonth` | 50 | none | engine.coastal |
| `frequency.eraMultipliers[]` | 1.0…1.8 (14 values) | none | engine.coastal |
| `frequency.seasonMultipliers[]` | 0.7 / 0.9 / 1.2 / 1.1 | none | engine.coastal |
| `frequency.worldConditionsScale` | 1.0 | none | engine.coastal |
| `reception.caseworkerThroughputPerMonth` | 6 | yes — api_test.go | engine.coastal |
| `reception.hotelCostPerCase` | 2,000,000,000 µ£ | yes — api_test.go | engine.coastal |
| `reception.satisfactionFrictionPerCase` | 0.02 | none | engine.coastal |
| `pipeline.minMonths` | 3 | yes — api_test.go | engine.coastal |
| `pipeline.maxMonths` | 9 | none | engine.coastal |
| `pipeline.grantRate` | 0.6 | yes — api_test.go | engine.coastal |
| `pipeline.departureCostPerCase` | 1,500,000,000 µ£ | none | engine.coastal |
| `pipeline.maxReductionMonths` | 2 | none | engine.coastal |
| `policy.processingFundingDefault` | 0.5 | yes — api_test.go | engine.coastal |
| `policy.processingFundingThroughputGainPerUnit` | 0.8 | none | engine.coastal |
| `policy.processingFundingOpexPerUnitPerMonth` | 500,000,000 µ£ | none | engine.coastal |
| `policy.housingApproachDefault` | 0.5 | none | engine.coastal |
| `policy.housingApproachCostPerUnitPerMonth` | -600,000,000 µ£ | none | engine.coastal |
| `policy.housingApproachFrictionIncreasePerUnit` | 0.5 | none | engine.coastal |
| `policy.housingApproachIntegrationPenaltyPerUnit` | 0.3 | none | engine.coastal |
| `policy.integrationInvestmentDefault` | 0.5 | none | engine.coastal |
| `policy.integrationInvestmentGainPerUnit` | 0.6 | none | engine.coastal |
| `policy.integrationInvestmentOpexPerUnitPerMonth` | 600,000,000 µ£ | none | engine.coastal |
| `worldProfile.skills.attainmentMean` | 30 | yes — api_test.go | engine.coastal |
| `worldProfile.skills.attainmentSpread` | 15 | none | engine.coastal |

---

## §8 `comms.json` — engine.comms

> Six-era connectivity ladder + e-commerce/post/drain/facility figures (MOD-048).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `eras[telephone-exchange].researchRateModifier` | 1.0 | yes — comms_test.go | engine.comms |
| `eras[telephone-exchange].remoteWorkBase` | 0.02 | yes — comms_test.go | engine.comms |
| `eras[telephone-exchange].letterFactor` | 1.0 | none | engine.comms |
| `eras[telephone-exchange].parcelEraFactor` | 1.0 | none | engine.comms |
| `eras[telephone-exchange].connectivity` | 0.05 | none | engine.comms |
| `eras[dial-up].researchRateModifier` | 1.1 | yes — comms_test.go | engine.comms |
| `eras[dial-up].remoteWorkBase` | 0.05 | yes — comms_test.go | engine.comms |
| `eras[dial-up].letterFactor` | 0.95 | none | engine.comms |
| `eras[dial-up].parcelEraFactor` | 1.1 | none | engine.comms |
| `eras[dial-up].connectivity` | 0.15 | none | engine.comms |
| `eras[broadband-hub].researchRateModifier` | 1.3 | yes — comms_test.go | engine.comms |
| `eras[broadband-hub].remoteWorkBase` | 0.12 | yes — comms_test.go | engine.comms |
| `eras[broadband-hub].letterFactor` | 0.85 | none | engine.comms |
| `eras[broadband-hub].parcelEraFactor` | 1.25 | none | engine.comms |
| `eras[broadband-hub].connectivity` | 0.35 | none | engine.comms |
| `eras[fibre-backbone].researchRateModifier` | 1.6 | yes — comms_test.go | engine.comms |
| `eras[fibre-backbone].remoteWorkBase` | 0.25 | yes — comms_test.go | engine.comms |
| `eras[fibre-backbone].letterFactor` | 0.7 | none | engine.comms |
| `eras[fibre-backbone].parcelEraFactor` | 1.5 | none | engine.comms |
| `eras[fibre-backbone].connectivity` | 0.65 | none | engine.comms |
| `eras[cellular-masts].researchRateModifier` | 2.0 | yes — comms_test.go | engine.comms |
| `eras[cellular-masts].remoteWorkBase` | 0.4 | yes — comms_test.go | engine.comms |
| `eras[cellular-masts].letterFactor` | 0.55 | none | engine.comms |
| `eras[cellular-masts].parcelEraFactor` | 1.9 | none | engine.comms |
| `eras[cellular-masts].connectivity` | 0.85 | none | engine.comms |
| `eras[submarine-cable].researchRateModifier` | 3.0 | yes — comms_test.go | engine.comms |
| `eras[submarine-cable].remoteWorkBase` | 0.55 | yes — comms_test.go | engine.comms |
| `eras[submarine-cable].letterFactor` | 0.4 | none | engine.comms |
| `eras[submarine-cable].parcelEraFactor` | 2.4 | none | engine.comms |
| `eras[submarine-cable].connectivity` | 1.0 | none | engine.comms |
| `sectors.{none,primary,secondary,tertiary,public}` | 0.0 / 0.1 / 0.3 / 0.7 / 0.4 | none | engine.comms |
| `eCommerce.shareBase` | 0.0 | none | engine.comms |
| `eCommerce.shareWealthWeight` | 1.0 | none | engine.comms |
| `eCommerce.noInfrastructureFloor` | 0.08 | none | engine.comms |
| `post.baseLetters` | 100,000 | none | engine.comms |
| `post.baseParcels` | 20,000 | none | engine.comms |
| `post.parcelWealthSensitivity` | 2.0 | none | engine.comms |
| `post.parcelShareSensitivity` | 1.5 | none | engine.comms |
| `drain.drainPerShare` | 1.0 | none | engine.comms |
| `drain.maxCounterplayDampening` | 0.75 | none | engine.comms |
| `fulfilment.staff` | 2500 | none | engine.comms |
| `lastMileDepot.staff` | 150 | none | engine.comms |
| `lastMileDepot.shelfCapacity` | 10,000 | none | engine.comms |
| `postalServices.sortingOffice.capacityRaw` | "200000 items/d" | none | engine.comms |
| `postalServices.sortingOffice.coverageRadius` | 50.0 | none | engine.comms |
| `postalServices.parcelHub.capacityRaw` | "50000 parcels/d" | none | engine.comms |
| `postalServices.parcelHub.coverageRadius` | 30.0 | none | engine.comms |

---

## §9 `consumption.json` — engine.consumption

> §17 resource-consumption coefficients. Residential baseline + per-class rows (21 classes).

**Residential baseline:**

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `residential.waterLitresPerPersonPerDay` | 145 | yes — baseline_test.go | engine.consumption |
| `residential.electricityKWhPerPersonPerDay` | 3.5 | yes — baseline_test.go | engine.consumption |
| `residential.gasKWhPerPersonPerDay` | 13 | yes — baseline_test.go | engine.consumption |
| `residential.foodStaplesKgPerPersonPerDay` | 1.4 | yes — baseline_test.go | engine.consumption |
| `residential.foodFreshKgPerPersonPerDay` | 0.7 | yes — baseline_test.go | engine.consumption |
| `residential.householdWasteKgPerPersonPerDay` | 1.1 | yes — baseline_test.go | engine.consumption |
| `residential.wastewaterFractionOfWater` | 0.95 | yes — baseline_test.go | engine.consumption |

**Per-class coefficients (`classes.*`, waterL / elecKWh / gasKWh / wasteKg):** all fields appear in `baseline_test.go` (directional).

| Class | waterL | elecKWh | gasKWh | wasteKg |
|---|---|---|---|---|
| school (per pupil) | 18 | 1.5 | 3.0 | 0.20 |
| university (per student) | 30 | 4.0 | 3.5 | 0.30 |
| clinic (per visit) | 40 | 3.0 | 2.0 | 0.50 |
| hospital (per bed) | 400 | 28 | 30 | 3.2 |
| elderCareHome (per resident) | 220 | 9 | 14 | 1.4 |
| office (per desk) | 25 | 5 | 2 | 0.35 |
| shop (per m²-sales/10) | 6 | 4.5 | 0.8 | 0.9 |
| restaurantCafe (per cover) | 25 | 2.5 | 2.0 | 0.6 |
| hotel (per room-night) | 300 | 20 | 18 | 2.0 |
| lightIndustry (per worker) | 60 | 22 | 12 | 4 |
| heavyIndustry (per worker) | 400 | 90 | 60 | 15 |
| leisureVenue (per visitor) | 15 | 1.8 | 0.5 | 0.25 |
| stadium (per spectator-event) | 25 | 1.2 | 0.3 | 0.4 |
| swimmingPoolLeisureCentre (per visitor) | 80 | 5 | 6 | 0.2 |
| park (per visitor) | 4 | 0.1 | 0 | 0.15 |
| stationRailMetro (per boarding) | 2 | 0.4 | 0 | 0.05 |
| airport (per passenger) | 15 | 6 | 2 | 0.7 |
| waterTreatmentWorks (per m³) | 0 | 0.6 | 0 | 0.05 |
| sewageWorks (per m³) | 0 | 0.5 | 0 | 0.25 |
| desalination (per m³) | 0 | 3.8 | 0 | 0 |
| accelerator (per facility) | 5,000,000 | 2,000,000 | 0 | 0 |

Source module: engine.consumption. Directional-test status: **yes — baseline_test.go** for the residential baseline and `elecKWh`/`waterL` class fields (the two scanned tokens); per-cell class values not individually asserted.

---

## §10 `containerport.json` — engine.freight (containerport)

> Port-tier ladder `cargo_port_small` → `container_terminal` → `deep_sea_terminal`.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `tiers[cargo_port_small].milestone` | 7 | none | engine.freight |
| `tiers[cargo_port_small].costMillions` | 40 (£M) | none | engine.freight |
| `tiers[cargo_port_small].berths` | 1 | yes — containerport_test.go | engine.freight |
| `tiers[cargo_port_small].craneRateTonnesPerHour` | 30 | yes — containerport_test.go | engine.freight |
| `tiers[cargo_port_small].operatingHoursPerDay` | 12 | yes — containerport_test.go | engine.freight |
| `tiers[cargo_port_small].customsCapacityTonnesPerDay` | 400 | yes — containerport_test.go | engine.freight |
| `tiers[cargo_port_small].shipTonnage` | 3000 | yes — containerport_test.go | engine.freight |
| `tiers[cargo_port_small].jobs` | 200 | none | engine.freight |
| `tiers[container_terminal].milestone` | 9 | none | engine.freight |
| `tiers[container_terminal].costMillions` | 150 (£M) | none | engine.freight |
| `tiers[container_terminal].berths` | 2 | yes — containerport_test.go | engine.freight |
| `tiers[container_terminal].craneRateTonnesPerHour` | 60 | yes — containerport_test.go | engine.freight |
| `tiers[container_terminal].operatingHoursPerDay` | 16 | yes — containerport_test.go | engine.freight |
| `tiers[container_terminal].customsCapacityTonnesPerDay` | 1500 | yes — containerport_test.go | engine.freight |
| `tiers[container_terminal].shipTonnage` | 3000 | yes — containerport_test.go | engine.freight |
| `tiers[container_terminal].jobs` | 600 | none | engine.freight |
| `tiers[deep_sea_terminal].milestone` | 9 | none | engine.freight |
| `tiers[deep_sea_terminal].costMillions` | 400 (£M) | none | engine.freight |
| `tiers[deep_sea_terminal].berths` | 6 | yes — containerport_test.go | engine.freight |
| `tiers[deep_sea_terminal].craneRateTonnesPerHour` | 240 | yes — containerport_test.go | engine.freight |
| `tiers[deep_sea_terminal].operatingHoursPerDay` | 24 | yes — containerport_test.go | engine.freight |
| `tiers[deep_sea_terminal].customsCapacityTonnesPerDay` | 12000 | yes — containerport_test.go | engine.freight |
| `tiers[deep_sea_terminal].shipTonnage` | 40000 | yes — containerport_test.go | engine.freight |
| `tiers[deep_sea_terminal].jobs` | 2000 | none | engine.freight |

---

## §11 `defence.json` — engine.defence

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `grantPots[transport].baseWinProbability` | 0.35 | yes — helpers_test.go | engine.defence |
| `grantPots[transport].matchFundingWeight` | 0.45 | none | engine.defence |
| `grantPots[transport].planningQualityWeight` | 0.25 | none | engine.defence |
| `grantPots[transport].maxMatchMicropounds` | 5,000,000,000 | none | engine.defence |
| `grantPots[transport].awardMicropounds` | 5,000,000,000 | none | engine.defence |
| `grantPots[regeneration].baseWinProbability` | 0.30 | yes — helpers_test.go | engine.defence |
| `grantPots[regeneration].maxMatchMicropounds` | 3,000,000,000 | none | engine.defence |
| `grantPots[regeneration].awardMicropounds` | 3,000,000,000 | none | engine.defence |
| `grantPots[culture].baseWinProbability` | 0.40 | yes — helpers_test.go | engine.defence |
| `grantPots[culture].maxMatchMicropounds` | 1,000,000,000 | none | engine.defence |
| `grantPots[culture].awardMicropounds` | 1,000,000,000 | none | engine.defence |
| `formulaSupport.taxCapacityThresholdMicropounds` | 10,000,000,000 | none | engine.defence |
| `formulaSupport.formulaAmountMicropounds` | 200,000,000 | none | engine.defence |
| `mandates[naval-100k].populationThreshold` | 100,000 | yes — helpers_test.go | engine.defence |
| `mandates[naval-100k].compensationMicropounds` | 4,000,000,000 | none | engine.defence |
| `mandates[army-500k].populationThreshold` | 500,000 | yes — helpers_test.go | engine.defence |
| `mandates[army-500k].compensationMicropounds` | 6,000,000,000 | none | engine.defence |
| `mandates[airdefence-1m].populationThreshold` | 1,000,000 | yes — helpers_test.go | engine.defence |
| `mandates[airdefence-1m].compensationMicropounds` | 8,000,000,000 | none | engine.defence |
| `facilities.*.payrollMicropounds` | 800M–3.5B per facility (6 facilities) | yes — data_test.go | engine.defence |
| `facilities.*.personnelCount` | 150–1,500 per facility | yes — facilities_test.go | engine.defence |
| `facilities.*.marriedQuarters` | 50–500 per facility | none | engine.defence |
| `facilities.*.childrenPerQuarter` | 1 | none | engine.defence |
| `facilities.*.procurementMicropounds` | 200M–900M per facility | none | engine.defence |
| `reputation.refusalPenaltyPoints` | 50 | yes — helpers_test.go | engine.defence |

---

## §12 `deposits.json` — engine.mining (geology)

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `depositRate` | 0.02 | none | engine.mining |
| `offshoreRate` | 0.01 | none | engine.mining |
| `resources.*.countWeight` | 1–6 (9 resources) | yes — constructor_guard_test.go | engine.mining |
| `resources.*.depthMin` / `.depthMax` | 20–1600 m bands | yes — shuffle_test.go | engine.mining |
| `sizeCurve.shape` / `.min` / `.max` | 1.6 / 10 / 500 | yes — constructor_guard_test.go | engine.mining |
| `densityCurve.shape` / `.min` / `.max` | 1.6 / 1.0 / 40 | yes — constructor_guard_test.go | engine.mining |
| `coLocation.chalkUraniumFactor` | 0.05 | yes — constructor_guard_test.go | engine.mining |
| `coLocation.coalGasFactor` | 12.0 | yes — constructor_guard_test.go | engine.mining |
| `coLocation.coalCoalFactor` | 3.0 | yes — constructor_guard_test.go | engine.mining |
| `eastKentCoalfield.generosityMultiplier` | 2.0 | yes — constructor_guard_test.go | engine.mining |
| `eastKentCoalfield.coverageFloor` | 0.9 | yes — constructor_guard_test.go | engine.mining |

---

## §13 `education.json` — engine.education

> Primary (60 mo) / secondary (132 mo) entry ages are spec-transcribed (§27); rest placeholder.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `entryAgeMonths.nursery` | 0 | none | engine.education |
| `entryAgeMonths.primary` | 60 (spec §27) | none | engine.education |
| `entryAgeMonths.secondary` | 132 (spec §27) | none | engine.education |
| `entryAgeMonths.sixth-form` | 192 | none | engine.education |
| `entryAgeMonths.technical-college` | 192 | none | engine.education |
| `entryAgeMonths.leave-at-16` | 192 | none | engine.education |
| `entryAgeMonths.university` | 216 | none | engine.education |
| `entryAgeMonths.adult-education` | 300 | none | engine.education |
| `entryAgeMonths.u3a` | 720 | none | engine.education |
| `baselineQuality` | 0.5 | yes — helpers_test.go | engine.education |
| `attainmentScale` | 100.0 | yes — helpers_test.go | engine.education |
| `researchPointsPerGraduate` | 1.0 | yes — helpers_test.go | engine.education |
| `hallsCapacity` | 100.0 | yes — helpers_test.go | engine.education |
| `dropoutRate` | 0.0 | yes — determinism_test.go | engine.education |

(`entryAgeMonths` appears in data_test.go — structural; the per-age gate values are not individually directionally asserted.)

---

## §14 `external_world.json` — engine.firms (loader: foundation.data)

> §21 off-map job pools. Directional tests live in `internal/foundation/data/external_world_test.go` (capacity must not decrease; externalRail gates at tier 5; london wage pinned at 2.9B is a loader round-trip, not a balance assertion).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `profiles[london].capacityByEra[1..12].capacity` | 500…1100 | yes — external_world_test.go (decrease-rejected) | engine.firms |
| `profiles[london].wageMicropounds` | 2,900,000,000 | yes — external_world_test.go (round-trip) | engine.firms |
| `profiles[ashford].capacityByEra[1..12].capacity` | 300…780 | yes — external_world_test.go | engine.firms |
| `profiles[ashford].wageMicropounds` | 2,300,000,000 | yes — external_world_test.go | engine.firms |
| `profiles[dover].capacityByEra[1..12].capacity` | 200…460 | yes — external_world_test.go | engine.firms |
| `profiles[dover].wageMicropounds` | 2,100,000,000 | yes — external_world_test.go | engine.firms |
| `profiles.*.transportRequirement[externalRail].availableFromTier` | 5 | yes — external_world_test.go (tier-5 gate) | engine.firms |

---

## §15 `factorytypes.json` — engine.freight

> Five §33-chain-referencing types carry only `footprintCells` (stage data lives in freight.json); five types carry inline figures.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `factoryTypes[assembler].footprintCells` | 4 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].inputs[0].tonnesPerDay` | 30 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].outputs[0].tonnesPerDay` | 25 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].jobs` | 40 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].powerKWhPerDay` | 9,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].waterLitresPerDay` | 2,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[assembler].blightClass` | 2 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[steelMill].footprintCells` | 60 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].footprintCells` | 12 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].inputs[*].tonnesPerDay` | 15 / 15 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].outputs[0].tonnesPerDay` | 25 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].jobs` | 60 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].powerKWhPerDay` | 40,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].waterLitresPerDay` | 5,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[electronics].blightClass` | 1 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].footprintCells` | 55 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].inputs[0].tonnesPerDay` | 30 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].outputs[0].tonnesPerDay` | 40 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].jobs` | 55 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].powerKWhPerDay` | 32,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].waterLitresPerDay` | 18,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[chemicalsConverter].blightClass` | 4 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[foodProcessing].footprintCells` | 15 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].footprintCells` | 6 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].inputs[0].tonnesPerDay` | 20 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].outputs[0].tonnesPerDay` | 18 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].jobs` | 45 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].powerKWhPerDay` | 8,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].waterLitresPerDay` | 3,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[textiles].blightClass` | 1 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[cement].footprintCells` | 40 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].footprintCells` | 25 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].inputs[*].tonnesPerDay` | 40 / 15 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].outputs[0].tonnesPerDay` | 20 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].jobs` | 35 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].powerKWhPerDay` | 15,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].waterLitresPerDay` | 6,000 | yes — factorytype_test.go | engine.freight |
| `factoryTypes[glass].blightClass` | 3 | yes — factorytype_test.go | engine.freight |

---

## §16 `farmtypes.json` — engine.farming

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `types[arable].footprintCells` | 6 | yes — farmtypes_test.go | engine.farming |
| `types[arable].soilBand` | chalkDownland (enum) | yes — farmtypes_test.go | engine.farming |
| `types[arable].terrain` | openDownland (enum) | yes — farmtypes_test.go | engine.farming |
| `types[arable].bdiTerm` | -0.10 | yes — farmtypes_test.go | engine.farming |
| `types[arable].chain.commodity` / `.destination` | grain / mill | yes — farmtypes_test.go | engine.farming |
| `types[livestock].footprintCells` | 4 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].soilBand` | pasture | yes — farmtypes_test.go | engine.farming |
| `types[livestock].terrain` | grazingDownland | yes — farmtypes_test.go | engine.farming |
| `types[livestock].bdiTerm` | -0.05 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].chain` | livestock / abattoir | yes — farmtypes_test.go | engine.farming |
| `types[livestock].stocking[dairy].headPerCell` | 0.5 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].stocking[beef].headPerCell` | 0.4 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].stocking[sheep].headPerCell` | 1.5 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].stocking[pigs].headPerCell` | 0.8 | yes — farmtypes_test.go | engine.farming |
| `types[livestock].stocking[poultry].headPerCell` | 12.0 | yes — farmtypes_test.go | engine.farming |
| `types[orchard].footprintCells` | 5 | yes — farmtypes_test.go | engine.farming |
| `types[orchard].bdiTerm` | 0.15 | yes — farmtypes_test.go | engine.farming |
| `types[marketGarden].footprintCells` | 2 | yes — farmtypes_test.go | engine.farming |
| `types[marketGarden].bdiTerm` | -0.02 | yes — farmtypes_test.go | engine.farming |
| `types[vineyard].footprintCells` | 3 | yes — farmtypes_test.go | engine.farming |
| `types[vineyard].bdiTerm` | 0.20 | yes — farmtypes_test.go | engine.farming |

---

## §17 `fertility.json` — engine.citizens

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `params.minChildbearingAgeYears.value` | 18 y | yes — fertility_test.go | engine.citizens |
| `params.maxChildbearingAgeYears.value` | 45 y | none | engine.citizens |
| `params.peakFertilityAgeYears.value` | 28 y | none | engine.citizens |
| `params.baseMonthlyBirthRate.value` | 0.01 /month | none | engine.citizens |
| `params.maxChildrenPerHousehold.value` | 4 | yes — fertility_test.go | engine.citizens |

---

## §18 `firms.json` — engine.firms

> §45 firms; weights in per-mille.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `stages[startup].minStaff` | 1 | yes — destructive_regression_test.go | engine.firms |
| `stages[small].minStaff` | 6 | none | engine.firms |
| `stages[medium].minStaff` | 26 | none | engine.firms |
| `stages[enterprise].minStaff` | 251 | yes — destructive_regression_test.go | engine.firms |
| `founding.basePerMille` | 1 | yes — destructive_regression_test.go | engine.firms |
| `founding.ambitionPerMille` | 200 | none | engine.firms |
| `founding.educationPerMille` | 150 | none | engine.firms |
| `founding.sectorExperiencePerMille` | 100 | none | engine.firms |
| `founding.wealthPerMille` | 150 | none | engine.firms |
| `founding.premisesPerMille` | 150 | none | engine.firms |
| `founding.demandPerMille` | 100 | none | engine.firms |
| `founding.exitFounderBoostPerMille` | 200 | none | engine.firms |
| `servicesDemand.exponentPerMille` | 1300 | yes — config_test.go | engine.firms |
| `servicesDemand.multiplier` | 1000 | none | engine.firms |
| `credit.depositToLendingRatioPerMille` | 900 | yes — destructive_regression_test.go | engine.firms |
| `credit.cultureWindowMonths` | 12 | none | engine.firms |
| `credit.stageSpreadBp.{startup,small,medium,enterprise}` | 300 / 200 / 100 / 0 | yes — destructive_regression_test.go | engine.firms |
| `credit.baseRateCycle[*].baseRateBp` | 500 / 500 / 900 (months 0/48/96) | yes — config_test.go | engine.firms |

---

## §19 `fiscal.json` — engine.fiscal

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `municipality.fundingTargetPerMonthMicroPounds` | 10,000,000,000 | yes — data_test.go | engine.fiscal |
| `municipality.permitSpeedAtZeroFunding` | 0.5 | yes — data_test.go | engine.fiscal |
| `municipality.permitSpeedAtFullFunding` | 2.0 | yes — data_test.go | engine.fiscal |
| `municipality.buildCostErrorAtZeroFunding` | 0.15 | yes — data_test.go | engine.fiscal |
| `municipality.buildCostErrorAtFullFunding` | 0.0 | yes — data_test.go | engine.fiscal |
| `municipality.layoutBonusAtZeroFunding` | 0.0 | yes — data_test.go | engine.fiscal |
| `municipality.layoutBonusAtFullFunding` | 1.0 | yes — data_test.go | engine.fiscal |
| `municipality.corruptionThreshold` | 0.3 | yes — data_test.go | engine.fiscal |
| `municipality.corruptionMax` | 0.5 | yes — data_test.go | engine.fiscal |
| `childcare.subsidyPerPlacePerMonthMicroPounds` | 300,000,000 | yes — data_test.go | engine.fiscal |
| `childcare.secondEarnerUpliftPerPlace` | 0.8 | yes — data_test.go | engine.fiscal |
| `childcare.secondEarnerAvgWagePerMonthMicroPounds` | 1,800,000,000 | yes — data_test.go | engine.fiscal |

---

## §20 `freight.json` — engine.freight

> Modal caps (road 25t / rail 1000t / sea 3–40kt) are spec-stated (§33/§8); the rest coarse placeholders.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `port.berths` | 2 | yes — containerport_test.go | engine.freight |
| `port.craneRateTonnesPerHour` | 60 | yes — containerport_test.go | engine.freight |
| `port.operatingHoursPerDay` | 16 | yes — containerport_test.go | engine.freight |
| `port.customsCapacityTonnesPerDay` | 1500 | yes — containerport_test.go | engine.freight |
| `modalCaps.road.maxTonnesPerMovement` | 25 (spec) | yes — freight_test.go | engine.freight |
| `modalCaps.road.leadTimeTicks` | 1 | none | engine.freight |
| `modalCaps.rail.maxTonnesPerMovement` | 1000 (spec) | yes — freight_test.go | engine.freight |
| `modalCaps.rail.leadTimeTicks` | 2 | none | engine.freight |
| `modalCaps.sea.minTonnesPerMovement` | 3000 (spec) | yes — freight_test.go | engine.freight |
| `modalCaps.sea.maxTonnesPerMovement` | 40000 (spec) | yes — freight_test.go | engine.freight |
| `modalCaps.sea.leadTimeTicks` | 3 | none | engine.freight |
| `storage[quayside].capacityTonnes` | 50,000 | yes — containerport_test.go | engine.freight |
| `storage[silo].capacityTonnes` | 20,000 | none | engine.freight |
| `storage[tankFarm].capacityTonnes` | 30,000 | none | engine.freight |
| `storage[coldStore].capacityTonnes` | 5,000 | none | engine.freight |
| `commodities.*.unitsPerTonne` | 1 or 1000 (31 commodities) | none | engine.freight |
| `chains.*.stages[*].outputs[*].tonnesPerDay` | 25–150,000 (32 stages) | yes — containerport_test.go (tonnesPerDay) | engine.freight |
| `chains.*.stages[*].jobs` | 15–120 | none | engine.freight |
| `chains.*.stages[*].powerKWhPerDay` | 0–60,000 | none | engine.freight |
| `chains.*.stages[*].waterLitresPerDay` | 500–80,000 | none | engine.freight |
| `chains.*.stages[*].blightClass` | 0–4 | none | engine.freight |

---

## §21 `georef.json` — engine.world

> **Not balance data** — world-geometry configuration (start-tile pin, cell grid). Included for completeness; not a row-by-row balance candidate.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `startTile.swEasting` / `swNorthing` | 620000 / 135000 | none | engine.world |
| `startTile.sizeM` | 2000 | none | engine.world |
| `startTile.cellSizeM` | 10 | none | engine.world |
| `startTile.gridCells` | 200 | none | engine.world |
| `expansion.sizeKm` | 60 | none | engine.world |
| `expansion.tiles10kTotal` | 36 | none | engine.world |

---

## §22 `leisure.json` — engine.leisure

> `hoursPerWeek` 168 is spec-transcribed (§42); the rest placeholder.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `hoursPerWeek` | 168 (spec §42) | yes — data_test.go | engine.leisure |
| `lifeStages.*.work` | 0–40 h | none | engine.leisure |
| `lifeStages.*.education` | 0–35 h | none | engine.leisure |
| `lifeStages.*.sleep` | 56–63 h | none | engine.leisure |
| `lifeStages.*.chores` | 2–10 h | none | engine.leisure |
| `accessFreeMinutes` | 15 | yes — helpers_test.go | engine.leisure |
| `accessBudgetMinutes` | 90 | yes — helpers_test.go | engine.leisure |
| `overtimeWageRate` | 1.0 | yes — helpers_test.go | engine.leisure |
| `noveltyDecayBase` | 0.05 | yes — helpers_test.go | engine.leisure |
| `noveltyDecayPerNovelty` | 0.10 | yes — helpers_test.go | engine.leisure |
| `freshnessRecovery` | 1.0 | none | engine.leisure |
| `eventCrowd.festival` | 4000 | yes — events_test.go | engine.leisure |
| `eventCrowd.food-fair` | 2000 | yes — events_test.go | engine.leisure |
| `eventCrowd.match-day` | 8000 | yes — events_test.go | engine.leisure |
| `eventCrowd.concert` | 3000 | yes — events_test.go | engine.leisure |
| `eventCrowd.christmas-market` | 5000 | yes — events_test.go | engine.leisure |
| `matchThreshold` | 0 | yes — helpers_test.go | engine.leisure |
| `defaultPopulationTaste.*` | 50 each (8 categories) | none | engine.leisure |

---

## §23 `logistics.json` — engine.logistics

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `bufferPolicies.lean.safetyBuffer` | 0.10 | yes — logistics_test.go | engine.logistics |
| `bufferPolicies.fat.safetyBuffer` | 0.50 | yes — logistics_test.go | engine.logistics |
| `commodities.*.throughput` | 5,000–1,000,000 (9 commodities) | yes — logistics_test.go | engine.logistics |
| `commodities.*.shortfallFactor` | 0.9 or 1.0 | yes — logistics_test.go | engine.logistics |
| `commodities.*.shelfLifeTicks` | 0, 3, or 30 | yes — logistics_test.go | engine.logistics |
| `commodities.*.holdingCostMicropoundsPerUnitPerTick` | 0, 2, or 100 | yes — logistics_test.go | engine.logistics |
| `commodities.*.defaultBufferPolicy` | lean / fat | yes — logistics_test.go | engine.logistics |

---

## §24 `maintenance.json` — engine.maintenance

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `crewCostPerEngineerDay` | 1,000,000 µ£ | yes — data_test.go | engine.maintenance |
| `contractorCostPerEngineerDay` | 3,000,000 µ£ | yes — data_test.go | engine.maintenance |
| `classes.*.engineerDaysPerYear` | 5–40 (8 classes) | yes — crew_test.go | engine.maintenance |
| `classes.*.lifetimeYears` | 30–100 (8 classes) | yes — data_test.go | engine.maintenance |

---

## §25 `market.json` — engine.market

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `commodities.*.supplyMode` | hybrid / importOnly (9 commodities) | yes — market_test.go | engine.market |
| `commodities.*.importPriceMicropounds` | 2,000 (water) … 45,000,000 (constructionMaterials); waste: none | yes — market_test.go | engine.market |
| `commodities[waste].exportPriceMicropounds` | 50,000 | yes — market_test.go | engine.market |
| `commodities.*.capacityCeiling` | 50,000–5,000,000 (9 commodities) | yes — market_test.go | engine.market |

**Commodity prices in £ (from micro-pounds):** water 0.2p/L · power 15p/kWh · gas 7p/kWh · foodStaples 40p/kg · foodFresh 90p/kg · fuel £1.50/L · constructionMaterials £45/t · consumerGoods £3/kg · waste export 5p/kg.

---

## §26 `minetypes.json` — engine.mining

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `types[chalk].footprint` | 6 | yes — minetype_test.go | engine.mining |
| `types[chalk].outputRate` | 40 t/day | yes — magnitude_guard_test.go | engine.mining |
| `types[chalk].jobs` | 4 | yes — minetype_test.go | engine.mining |
| `types[chalk].blightClass` | low | yes — minetype_test.go | engine.mining |
| `types[chalk].depthMin` / `.depthMax` | 0 / 15 m | yes — shuffle_test.go | engine.mining |
| `types[sand_gravel].footprint` | 8 | yes — minetype_test.go | engine.mining |
| `types[sand_gravel].outputRate` | 55 | yes — magnitude_guard_test.go | engine.mining |
| `types[sand_gravel].jobs` | 6 | yes — minetype_test.go | engine.mining |
| `types[sand_gravel].depthMin` / `.depthMax` | 0 / 25 | yes — shuffle_test.go | engine.mining |
| `types[clay_brickworks].footprint` | 9 | yes — minetype_test.go | engine.mining |
| `types[clay_brickworks].outputRate` | 30 | yes — magnitude_guard_test.go | engine.mining |
| `types[clay_brickworks].jobs` | 8 | yes — minetype_test.go | engine.mining |
| `types[clay_brickworks].depthMin` / `.depthMax` | 0 / 12 | yes — shuffle_test.go | engine.mining |
| `types[ragstone].footprint` | 10 | yes — minetype_test.go | engine.mining |
| `types[ragstone].outputRate` | 25 | yes — magnitude_guard_test.go | engine.mining |
| `types[ragstone].jobs` | 7 | yes — minetype_test.go | engine.mining |
| `types[ragstone].depthMin` / `.depthMax` | 0 / 20 | yes — shuffle_test.go | engine.mining |
| `types[deep_coal].footprint` | 16 | yes — minetype_test.go | engine.mining |
| `types[deep_coal].outputRate` | 80 | yes — magnitude_guard_test.go | engine.mining |
| `types[deep_coal].jobs` | 60 | yes — minetype_test.go | engine.mining |
| `types[deep_coal].blightClass` | severe | yes — minetype_test.go | engine.mining |
| `types[deep_coal].depthMin` / `.depthMax` | 100 / 900 | yes — shuffle_test.go | engine.mining |
| `types[deep_coal].spoilTipFootprint` | 5 | yes — site_test.go | engine.mining |
| `types[deep_coal].subsidenceRadius` | 300 m | yes — minetype_test.go | engine.mining |
| `types[offshore_dredger].footprint` | 12 | yes — minetype_test.go | engine.mining |
| `types[offshore_dredger].outputRate` | 70 | yes — magnitude_guard_test.go | engine.mining |
| `types[offshore_dredger].jobs` | 10 | yes — minetype_test.go | engine.mining |
| `types[offshore_dredger].depthMin` / `.depthMax` | 0 / 40 | yes — shuffle_test.go | engine.mining |

---

## §27 `mining.json` — engine.mining

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `noise.minDistanceM` | 10 | yes — blight_test.go | engine.mining |
| `noise.falloffExponent` | 2.0 | none | engine.mining |
| `noise.enclosureReduction` | 0.5 | yes — blight_test.go | engine.mining |
| `noise.nightBanReduction` | 0.3 | none | engine.mining |
| `viewshed.eyeHeightM` | 1.5 | none | engine.mining |
| `viewshed.occlusionScaleM` | 20.0 | yes — blight_test.go | engine.mining |
| `viewshed.seenFalloffM` | 300.0 | yes — blight_test.go | engine.mining |
| `classProfile.{low,moderate,high,severe}.noiseRadiusM` | 100 / 300 / 600 / 1000 | yes — blight_test.go | engine.mining |
| `classProfile.{low,moderate,high,severe}.visualHeightM` | 5 / 15 / 30 / 50 | yes — blight_test.go | engine.mining |
| `classProfile.{low,moderate,high,severe}.magnitude` | 0.2 / 0.45 / 0.75 / 1.0 | yes — blight_test.go | engine.mining |
| `treeBelt.growInYears` | 5 | yes — blight_test.go | engine.mining |
| `spoilTip.heightM` | 15 | none | engine.mining |
| `spoilTip.noiseRadiusM` | 200 | yes — blight_test.go | engine.mining |
| `extraction.capacityDays` | 100 | yes — blight_test.go | engine.mining |

---

## §28 `modes.json` — engine.traffic (planned; loader foundation.data)

> §19.1 nested-logit mode-choice schema. 12 modes × utility coefficients + nest tree + personality weights. Speed/capacity/PCU base fields are spec-transcribed; all utility coefficients and nesting parameters are `derived-placeholder` v1 estimates. **No engine.traffic module exists yet** — no directional tests (loader tests are structural only).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `modes[*].speedUrbanKmh` | 5 (walk) … 70 (heavy rail); ferry 15 kn; car/taxi 15–48 | none | engine.traffic |
| `modes[*].capacityPerUnit` | varies: null, 4, 14, 80, 250, metro 1200/train + 30000/h/dir, heavy_rail 1000, ferry 300 | none | engine.traffic |
| `modes[*].pcuLoad` | 0.2–2.0 (road modes) | none | engine.traffic |
| `modes[car].laneCapacityPcuPerHour` | 1800 | none | engine.traffic |
| `modes[*].utility.alternativeSpecificConstant` | -1.20 … +0.60 | none | engine.traffic |
| `modes[*].utility.betaTime` | -0.090 … -0.025 | none | engine.traffic |
| `modes[*].utility.betaCost` | -0.0000030 … 0.0 | none | engine.traffic |
| `modes[*].utility.betaComfort` | 0.02 … 0.15 | none | engine.traffic |
| `modes[*].utility.betaReliability` | 0.05 … 0.12 | none | engine.traffic |
| `nestedLogit.nests[*].nestingParameter` | 0.45–0.85 (5 nests) | none | engine.traffic |
| `personalityWeights.{physicality,patience,wealth}.weight` | 0.35 / 0.25 / 0.30 | none | engine.traffic |

---

## §29 `pacing.json` — engine.core

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `secondsPerMonthAt1x` | 480 (spec §3) | yes — clock_test.go | engine.core |

---

## §30 `pharmacampus.json` — engine.fdi

> 2 FDI anchors (`pharma_r_d_campus`, `chemicals_complex`).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `anchors[pharma_r_d_campus].footprint` | 24 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[pharma_r_d_campus].outputTPerDay` | 12 | none | engine.fdi |
| `anchors[pharma_r_d_campus].jobs` | 3500 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[pharma_r_d_campus].utilityPowerKW` | 8000 | none | engine.fdi |
| `anchors[pharma_r_d_campus].utilityWaterLitresPerDay` | 1,200,000 | none | engine.fdi |
| `anchors[pharma_r_d_campus].exportsTPerDay` | 9 | none | engine.fdi |
| `anchors[pharma_r_d_campus].capexMicroPounds` | 2,400,000,000,000,000 (£2.4B) | none | engine.fdi |
| `anchors[pharma_r_d_campus].opexMicroPoundsPerWorkerPerMonth` | 900,000 | none | engine.fdi |
| `anchors[pharma_r_d_campus].wagesMicroPoundsPerWorkerPerMonth` | 4,200,000 | none | engine.fdi |
| `anchors[pharma_r_d_campus].supplyChainFirms` | 6 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[pharma_r_d_campus].supplyChainPerWorkers` | 500 | none | engine.fdi |
| `anchors[pharma_r_d_campus].bid.qualityBase` | 40 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[pharma_r_d_campus].bid.educationTermPerGraduate` | 6 | none | engine.fdi |
| `anchors[pharma_r_d_campus].bid.competingFloor` | 100 | none | engine.fdi |
| `anchors[pharma_r_d_campus].bid.jitterMax` | 8 | none | engine.fdi |
| `anchors[pharma_r_d_campus].bid.graduateDemandPerWorker` | 2 | none | engine.fdi |
| `anchors[chemicals_complex].footprint` | 30 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[chemicals_complex].outputTPerDay` | 60 | none | engine.fdi |
| `anchors[chemicals_complex].jobs` | 1200 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[chemicals_complex].utilityPowerKW` | 40,000 | none | engine.fdi |
| `anchors[chemicals_complex].utilityWaterLitresPerDay` | 5,000,000 | none | engine.fdi |
| `anchors[chemicals_complex].exportsTPerDay` | 45 | none | engine.fdi |
| `anchors[chemicals_complex].capexMicroPounds` | 1,800,000,000,000,000 (£1.8B) | none | engine.fdi |
| `anchors[chemicals_complex].opexMicroPoundsPerWorkerPerMonth` | 1,500,000 | none | engine.fdi |
| `anchors[chemicals_complex].wagesMicroPoundsPerWorkerPerMonth` | 2,600,000 | none | engine.fdi |
| `anchors[chemicals_complex].supplyChainFirms` | 3 | yes — pharmacampus_test.go | engine.fdi |
| `anchors[chemicals_complex].supplyChainPerWorkers` | 1000 | none | engine.fdi |
| `anchors[chemicals_complex].bid.*` | qualityBase 40 / eduTerm 1 / floor 100 / jitter 8 / gradDemand 1 | yes — pharmacampus_test.go (qualityBase) | engine.fdi |

---

## §31 `prison.json` — engine.prison

> Costs are whole £ (int64).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `baseRates.youth.{minor,serious,violent}` | 0.22 / 0.42 / 0.62 | yes — prison_test.go | engine.prison |
| `baseRates.adult.{minor,serious,violent}` | 0.25 / 0.45 / 0.65 | yes — prison_test.go | engine.prison |
| `categoryMismatchPenalty` | 0.10 | yes — prison_test.go | engine.prison |
| `regime.education.maxEffect` | 0.08 | yes — prison_test.go | engine.prison |
| `regime.education.costForMax` | 1,000,000 £ | yes — prison_test.go | engine.prison |
| `regime.work.maxEffect` | 0.06 | yes — prison_test.go | engine.prison |
| `regime.work.costForMax` | 800,000 £ | yes — prison_test.go | engine.prison |
| `regime.addictionTreatment.maxEffect` | 0.07 | yes — prison_test.go | engine.prison |
| `regime.addictionTreatment.costForMax` | 1,200,000 £ | yes — prison_test.go | engine.prison |
| `reentry.probationCapacity.maxEffect` | 0.06 | yes — prison_test.go | engine.prison |
| `reentry.employmentUptake.maxEffect` | 0.05 | yes — prison_test.go | engine.prison |
| `reentry.housingOnRelease.maxEffect` | 0.04 | yes — prison_test.go | engine.prison |
| `overcrowding.degradeMax` | 1.0 | yes — prison_test.go | engine.prison |
| `youth.costMultiplier` | 0.5 | yes — prison_test.go | engine.prison |
| `adultCostPerOffender.{minor,serious,violent}` | 500,000 / 800,000 / 1,200,000 £ | yes — prison_test.go | engine.prison |
| `fuseYears.min` / `.max` | 5 / 15 | yes — prison_test.go | engine.prison |

---

## §32 `refinery.json` — engine.chemicals

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `facilities[refinery].footprintCells` | 120 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].throughputTonnesPerDay` | 20,000 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].jobs` | 4500 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].powerKWhPerDay` | 4,000,000 | none | engine.chemicals |
| `facilities[refinery].waterLitresPerDay` | 80,000,000 | none | engine.chemicals |
| `facilities[refinery].capexMicropounds` | 300,000,000,000,000 (£300M) | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].opexMicropoundsPerDay` | 500,000,000,000 (£500k/day) | none | engine.chemicals |
| `facilities[refinery].capexAmortisationDays` | 3650 (10y) | none | engine.chemicals |
| `facilities[refinery].outputs[fuel].tonnesPerDay` | 10,000 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].outputs[feedstock].tonnesPerDay` | 6,000 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].hazmatSeverity` | 5 | yes — refinery_test.go | engine.chemicals |
| `facilities[refinery].hazmatFirePeriodDays` | 1000 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].footprintCells` | 80 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].throughputTonnesPerDay` | 6,000 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].jobs` | 1800 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].powerKWhPerDay` | 1,800,000 | none | engine.chemicals |
| `facilities[petrochemical_works].waterLitresPerDay` | 40,000,000 | none | engine.chemicals |
| `facilities[petrochemical_works].capexMicropounds` | 150,000,000,000,000 (£150M) | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].opexMicropoundsPerDay` | 220,000,000,000 (£220k/day) | none | engine.chemicals |
| `facilities[petrochemical_works].outputs[plastics].tonnesPerDay` | 5,000 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].hazmatSeverity` | 3 | yes — refinery_test.go | engine.chemicals |
| `facilities[petrochemical_works].hazmatFirePeriodDays` | 2000 | yes — refinery_test.go | engine.chemicals |
| `import.marginMicropoundsPerTonne` | 200,000,000 (£200/t, ASM-321 stub) | none | engine.chemicals |

---

## §33 `refuse.json` — engine.refuse

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `binCapacities.residential.capacityKg` | 240 | yes — refuse_test.go | engine.refuse |
| `binCapacities.commercial.capacityKg` | 1100 | yes — refuse_test.go | engine.refuse |
| `binCapacities.industrial.capacityKg` | 6000 | yes — refuse_test.go | engine.refuse |
| `wasteRates.*.perDriverPerTickKg` | 0.9 / 2.5 / 4.0 | yes — refuse_test.go | engine.refuse |
| `streamMix.recycling` | 0.30 | yes — refuse_test.go | engine.refuse |
| `streamMix.food` | 0.15 | yes — refuse_test.go | engine.refuse |
| `contamination.resaleValuePerKgMicropounds` | 2.0 | none | engine.refuse |
| `contamination.penaltyPerContamination` | 0.5 | none | engine.refuse |
| `compost.conversionRatio` | 0.6 | yes — refuse_test.go | engine.refuse |
| `vermin.perKgOverflowPerTick` | 0.001 | none | engine.refuse |
| `vermin.landValuePenaltyPerVermin` | 0.02 | none | engine.refuse |
| `vermin.fireRiskPerVermin` | 0.01 | none | engine.refuse |
| `incineration.energyPerKg` | 0.4 | none | engine.refuse |
| `incineration.airshedPollutionPerKg` | 0.3 | none | engine.refuse |
| `funding.fundingThreshold` | 0.5 | none | engine.refuse |
| `trucks.truckCapacityKg` | 12,000 | none | engine.refuse |
| `trucks.crewsPerTruck` | 3.0 | none | engine.refuse |

---

## §34 `roads.json` — engine.roads

**Class ladder (11 classes) — each class carries `lanes`, `speedLimit`, `speedMin`, `speedMax`, `parking`, `treeVerge`, `widthCells`, `baseCostPounds`:**

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `classes[alley].lanes` | 1 | yes — api_test.go | engine.roads |
| `classes[alley].speedLimit` | 20 | yes — api_test.go | engine.roads |
| `classes[alley].widthCells` | 1 | yes — api_test.go | engine.roads |
| `classes[alley].baseCostPounds` | 500 | yes — sec_regression_test.go | engine.roads |
| `classes[gravel].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 1 / 30 / 1 / 800 | yes — api_test.go / sec_regression_test.go | engine.roads |
| `classes[residential_street].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 2 / 30 / 1 / 2000 | yes | engine.roads |
| `classes[two_lane].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 2 / 40 / 1 / 4000 | yes | engine.roads |
| `classes[one_way_pairs].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 2 / 40 / 1 / 5000 | yes | engine.roads |
| `classes[avenue_2_plus_2].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 4 / 40 / 3 / 12000 | yes | engine.roads |
| `classes[bus_lane_variant].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 4 / 40 / 3 / 14000 | yes | engine.roads |
| `classes[tram_track_variant].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 4 / 40 / 3 / 16000 | yes | engine.roads |
| `classes[dual_carriageway].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 4 / 60 / 3 / 20000 | yes | engine.roads |
| `classes[urban_expressway].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 4 / 70 / 5 / 50000 | yes | engine.roads |
| `classes[motorway].lanes` / `.speedLimit` / `.widthCells` / `.baseCostPounds` | 6 / 110 / 5 / 80000 | yes | engine.roads |

**Maintenance / upgrade / roadworks:**

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `maintenance.conditionDecayPerMonth` | 0.02 | yes — sec_regression_test.go | engine.roads |
| `maintenance.speedPenaltyPerConditionBelow` | 0.4 | yes — sec_regression_test.go | engine.roads |
| `maintenance.costMultiplierPerConditionBelow` | 1.5 | yes — sec_regression_test.go | engine.roads |
| `maintenance.repairConditionPerPound` | 0.001 | yes — sec_regression_test.go | engine.roads |
| `upgrade.rungDistanceCostPermille` | 150 | yes — sec_regression_test.go | engine.roads |
| `upgrade.rebuildDisruptionPermille` | 250 | yes — sec_regression_test.go | engine.roads |
| `upgrade.landCostPerCellPounds` | 1000 | yes — sec_regression_test.go | engine.roads |
| `roadworks.phaseDurationMonths` | 2 | yes — sec_regression_test.go | engine.roads |
| `roadworks.laneReductionFraction` | 0.5 | yes — sec_regression_test.go | engine.roads |

---

## §35 `seasonal.json` — engine.season

> All 9 curves are 12-entry month multipliers (index 0 = January). Spec-stated: `electricityWinterPeak` (+15% winter), `waterSummerPeak` (+25% summer), `gasSeasonal` (×2.2 Jan, ×0.2 Jul). Rest are plausible v1 shapes pending M2.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `curves.electricityWinterPeak.multipliers` | 1.15,1.15,1,1,1,1,1,1,1,1,1,1.15 | yes — season_test.go | engine.season |
| `curves.waterSummerPeak.multipliers` | 1,1,1,1,1,1.25,1.25,1.25,1,1,1,1 | yes — season_test.go | engine.season |
| `curves.gasSeasonal.multipliers` | 2.2,1,1,1,1,1,0.2,1,1,1,1,1 | yes — season_test.go | engine.season |
| `curves.harvestCalendar.multipliers` | 0.1,0.1,0.1,0.15,0.2,0.3,0.5,1.0,1.0,0.8,0.2,0.1 | yes — season_test.go | engine.season |
| `curves.constructionSpeedMultiplier.multipliers` | 0.8,0.8,0.9,1,1,1,1,1,1,1,0.95,0.8 | yes — season_test.go | engine.season |
| `curves.schoolIntakeGate.multipliers` | 0,0,0,0,0,0,0,0,1.0,0,0,0 | yes — season_test.go | engine.season |
| `curves.leisureBeachWeight.multipliers` | 0.1,0.1,0.15,0.2,0.3,0.6,0.8,0.8,0.5,0.2,0.1,0.1 | yes — season_test.go | engine.season |
| `curves.leisureIndoorWeight.multipliers` | 0.8,0.8,0.7,0.6,0.4,0.2,0.15,0.15,0.3,0.6,0.8,0.85 | yes — season_test.go | engine.season |
| `curves.healthWaveModifier.multipliers` | 0.05,0.05,0.02,0,0,0,0,0,0,0,0.02,0.05 | yes — season_test.go | engine.season |

---

## §36 `services.json` — engine.services

> Only `police.perThousand` 2.4 is spec-transcribed (§54); the rest placeholder.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `wagePerStaffPerMonthMicropounds` | 2,000,000,000 | yes — data_test.go | engine.services |
| `pie.severityHalfPointPopulation` | 100,000 | yes — data_test.go | engine.services |
| `pie.benchmarks[police].perThousand` | 2.4 (spec §54) | yes — data_test.go | engine.services |
| `pie.benchmarks[teachers].perPupil` | 0.04 | yes — data_test.go | engine.services |
| `pie.benchmarks[nursesGps].perThousand` | 3.0 | yes — data_test.go | engine.services |
| `pie.benchmarks[dentistsOpticians].perThousand` | 0.6 | yes — data_test.go | engine.services |
| `pie.benchmarks[firefighters].perThousand` | 0.9 | yes — data_test.go | engine.services |
| `pie.benchmarks[socialWorkers].perThousand` | 1.4 | yes — data_test.go | engine.services |
| `pie.benchmarks[refuseCrews].perThousand` | 0.7 | yes — data_test.go | engine.services |
| `pie.benchmarks[councilOfficers].perThousand` | 2.8 | yes — data_test.go | engine.services |

---

## §37 `social.json` — engine.social

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `caseload.familyPerDeprivation` | 4.0 | yes — caseload_test.go | engine.social |
| `caseload.familyPerCrowdingStress` | 3.0 | none | engine.social |
| `caseload.familyPerFinancialStress` | 5.0 | none | engine.social |
| `caseload.crisisFamilyCases` | 2.0 | none | engine.social |
| `caseload.homelessnessPerDeprivation` | 3.0 | none | engine.social |
| `caseload.homelessnessPerUnemploymentMonth` | 0.2 | none | engine.social |
| `caseload.homelessnessPerFinancialStress` | 4.0 | none | engine.social |
| `caseload.disabilityPerDeprivation` | 2.0 | none | engine.social |
| `caseload.fosteringPerCrowdingStress` | 1.0 | none | engine.social |
| `caseload.fosteringPerFinancialStress` | 1.0 | none | engine.social |
| `caseload.addictionPerPressure` | 6.0 | none | engine.social |
| `caseload.unemploymentCapMonths` | 60.0 | yes — helpers_test.go | engine.social |
| `hostelCapacity` | 40 | yes — data_test.go | engine.social |
| `fosterCapacity` | 10 | yes — destructive_regression_test.go | engine.social |
| `carersReleasedPerFundingUnit` | 60 | yes — helpers_test.go | engine.social |
| `interventionHarmThreshold` | 0.5 | yes — helpers_test.go | engine.social |

---

## §38 `spaceport.json` — engine.spaceport

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `buildMonths.value` | 60 | yes — spaceport_test.go | engine.spaceport |
| `launchCadenceMonths.value` | 3 | yes — spaceport_test.go | engine.spaceport |
| `exportValuePerLaunch.value` | 50,000,000 £ | none | engine.spaceport |
| `prestigePerLaunch.value` | 1000 | yes — spaceport_test.go | engine.spaceport |
| `fdiDrawAmount.value` | 100 | yes — spaceport_test.go | engine.spaceport |
| `tourismDrawAmount.value` | 2500 | yes — spaceport_test.go | engine.spaceport |
| `exclusionRadiusCells.value` | 5 | none | engine.spaceport |
| `exclusionLandFactorPerMille.value` | 700 | yes — spaceport_test.go | engine.spaceport |
| `expertThreshold.value` | 1000 research pts | yes — spaceport_test.go | engine.spaceport |

---

## §39 `tax_instruments.json` — engine.tax

> Six instruments; each carries `rateRange.min/maxPercent`, `elasticity.coefficient`, `bearerWeights.ratePoints[*].bearers[*].share`, `zoneOverrides` multipliers. Values are literature-grounded or derived placeholders (UK-today mapping sanity-check only).

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `instruments[vat].rateRange.minPercent` / `.maxPercent` | 0 / 30 | none | engine.tax |
| `instruments[vat].elasticity.coefficient` | 0.4 | yes — tax_test.go | engine.tax |
| `instruments[vat].bearerWeights.ratePoints[20%].bearers[consumer].share` | 0.70 | none | engine.tax |
| `instruments[vat].bearerWeights.ratePoints[20%].bearers[firm].share` | 0.30 | none | engine.tax |
| `instruments[vat].bearerWeights.ratePoints[25%].bearers[consumer].share` | 0.85 | none | engine.tax |
| `instruments[vat].bearerWeights.ratePoints[25%].bearers[firm].share` | 0.15 | none | engine.tax |
| `instruments[importDuties].rateRange.minPercent` / `.maxPercent` | 0 / 50 | none | engine.tax |
| `instruments[importDuties].elasticity.coefficient` | 0.5 | yes — tax_test.go | engine.tax |
| `instruments[importDuties].bearerWeights.ratePoints[10%].bearers[importerFirm].share` | 0.60 | none | engine.tax |
| `instruments[importDuties].bearerWeights.ratePoints[10%].bearers[downstreamConsumer].share` | 0.40 | none | engine.tax |
| `instruments[importDuties].bearerWeights.ratePoints[25%].bearers[importerFirm].share` | 0.45 | none | engine.tax |
| `instruments[importDuties].bearerWeights.ratePoints[25%].bearers[downstreamConsumer].share` | 0.55 | none | engine.tax |
| `instruments[corporationTax].rateRange.minPercent` / `.maxPercent` | 0 / 35 | none | engine.tax |
| `instruments[corporationTax].elasticity.coefficient` | 0.3 | yes — tax_test.go | engine.tax |
| `instruments[corporationTax].bearerWeights.ratePoints[19%].bearers` | shareholders 0.50 / employees 0.20 / consumers 0.30 | none | engine.tax |
| `instruments[corporationTax].bearerWeights.ratePoints[25%].bearers` | shareholders 0.35 / employees 0.25 / consumers 0.40 | none | engine.tax |
| `instruments[paye].rateRange.minPercent` / `.maxPercent` | 0 / 60 | none | engine.tax |
| `instruments[paye].incomeTaxBands[*].ratePercent` | 0 / 20 / 40 / 45 (UK 2025/26 bands; bounds in µ£) | none | engine.tax |
| `instruments[paye].niRates.employeePercent` | 8 | none | engine.tax |
| `instruments[paye].niRates.employerPercent` | 13.8 | none | engine.tax |
| `instruments[paye].elasticity.coefficient` | 0.45 | yes — tax_test.go | engine.tax |
| `instruments[paye].bearerWeights.ratePoints[20%].bearers` | employee 0.80 / employer 0.20 | none | engine.tax |
| `instruments[paye].bearerWeights.ratePoints[45%].bearers` | employee 0.65 / employer 0.35 | none | engine.tax |
| `instruments[councilTax].rateRange.minPercent` / `.maxPercent` | 0 / 400 (band-D %) | none | engine.tax |
| `instruments[councilTax].elasticity.coefficient` | 0.25 | yes — tax_test.go | engine.tax |
| `instruments[councilTax].bearerWeights.ratePoints[100%].bearers` | ownerOccupier 0.55 / landlord 0.30 / tenant 0.15 | none | engine.tax |
| `instruments[councilTax].bearerWeights.ratePoints[250%].bearers` | ownerOccupier 0.45 / landlord 0.10 / tenant 0.45 | none | engine.tax |
| `instruments[businessRates].rateRange.minPercent` / `.maxPercent` | 0 / 400 (assessed %) | none | engine.tax |
| `instruments[businessRates].elasticity.coefficient` | 0.35 | yes — tax_test.go | engine.tax |
| `instruments[businessRates].bearerWeights.ratePoints[100%].bearers` | firm 0.60 / consumer 0.40 | none | engine.tax |
| `instruments[businessRates].bearerWeights.ratePoints[300%].bearers` | firm 0.45 / consumer 0.55 | none | engine.tax |
| `instruments[businessRates].zoneOverrides.heavyIndustry.rateMultiplier` | 0.7 | yes — tax_test.go (zoneOverrides) | engine.tax |
| `instruments[businessRates].zoneOverrides.manufacturing.rateMultiplier` | 0.85 | yes — tax_test.go (zoneOverrides) | engine.tax |

---

## §40 `unlock_trees.json` — engine.unlocks

> §22 DP trees, 12 categories × 13 milestone tiers. Every `dpCost` and `prereqTier` = the node's tier (1–13) — a placeholder v1 shape (monotonic "costs as many DP as its tier").

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `trees[*].nodes[*].tier` | 1–13 | yes — buy_test.go | engine.unlocks |
| `trees[*].nodes[*].dpCost` | = tier (1–13) | yes — dp_test.go | engine.unlocks |
| `trees[*].nodes[*].prereqTier` | = tier (1–13) | yes — dp_test.go | engine.unlocks |
| `trees[*].nodes[*].kind` | unlock / none | yes — buy_test.go | engine.unlocks |
| `trees[*].nodes[*].prereqNodeIds` | tree-local edges (e.g. roads_avenues → roads_dual_carriageway) | yes — dp_test.go | engine.unlocks |

> **Note:** ~290 of ~160 nodes are `kind:"none"` no-op placeholders (ASM-481) carrying no balance number.

---

## §41 `wellbeing.json` — engine.wellbeing

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `baseline.physical` | 62 | yes — data_test.go | engine.wellbeing |
| `baseline.mental` | 62 | yes — attribute_test.go | engine.wellbeing |
| `headline.physicalWeight` | 0.4 | yes — attribute_test.go | engine.wellbeing |
| `headline.mentalWeight` | 0.4 | yes — attribute_test.go | engine.wellbeing |
| `headline.satisfactionWeight` | 0.2 | yes — attribute_test.go | engine.wellbeing |
| `physical.ageCurve[*].delta` | 0 / 0 / -4 / -14 / -32 (ages 0/30/55/75/100) | yes — api_test.go | engine.wellbeing |
| `physical.healthcareAccessWeight` | 15 | yes — attribute_test.go | engine.wellbeing |
| `physical.dietWeight` | 10 | yes — attribute_test.go | engine.wellbeing |
| `physical.activeTravelWeight` | 8 | yes — attribute_test.go | engine.wellbeing |
| `physical.pollutionWeight` | 12 | yes — attribute_test.go | engine.wellbeing |
| `physical.sportParticipationWeight` | 10 | yes — attribute_test.go | engine.wellbeing |
| `mental.commuteWeight` | 10 | yes — attribute_test.go | engine.wellbeing |
| `mental.commuteThresholdMinutes` | 45 | yes — attribute_test.go | engine.wellbeing |
| `mental.commuteStressAtThreshold` | 0.5 | yes — attribute_test.go | engine.wellbeing |
| `mental.commuteStressAt100Minutes` | 2.0 | yes — attribute_test.go | engine.wellbeing |
| `mental.jobAmbitionMismatchWeight` | 10 | none | engine.wellbeing |
| `mental.greenSpaceWeight` | 8 | yes — attribute_test.go | engine.wellbeing |
| `mental.leisureFitWeight` | 10 | none | engine.wellbeing |
| `mental.crowdingWeight` | 8 | none | engine.wellbeing |
| `mental.isolationWeight` | 10 | none | engine.wellbeing |
| `mental.noiseWeight` | 8 | none | engine.wellbeing |
| `mental.financialStressWeight` | 12 | none | engine.wellbeing |
| `mental.rentBurdenThreshold` | 0.35 | yes — attribute_test.go | engine.wellbeing |
| `mental.unemploymentWeight` | 10 | none | engine.wellbeing |
| `mental.unemploymentCapMonths` | 60 | none | engine.wellbeing |
| `modifiers.mortalitySlope` | 0.01 | yes — attribute_test.go | engine.wellbeing |
| `modifiers.productivitySlope` | 0.01 | yes — attribute_test.go | engine.wellbeing |
| `modifiers.satisfactionSlope` | 0.01 | yes — attribute_test.go | engine.wellbeing |
| `modifiers.emigrationSlope` | 0.01 | yes — attribute_test.go | engine.wellbeing |

---

## §42 `worklife.json` — engine.worklife

> Three time patterns (pattern hour/week values are the documented Aaron-2026-08-16 design, not placeholders); the 996 working-week policy values are placeholders.

| Field (path) | Current value | Directional-test status | Source module |
|---|---|---|---|
| `patterns[core-hours].hoursPerDay` | 8 | yes — worklife_test.go | engine.worklife |
| `patterns[core-hours].daysPerWeek` | 5 | yes — worklife_test.go | engine.worklife |
| `patterns[core-hours].startHour` / `.endHour` | 9 / 17 | yes — worklife_test.go | engine.worklife |
| `patterns[core-hours].coverageSpanHours` | 8 | yes — worklife_test.go | engine.worklife |
| `patterns[shift].hoursPerDay` | 8 | yes — worklife_test.go | engine.worklife |
| `patterns[shift].daysPerWeek` | 5 | yes — worklife_test.go | engine.worklife |
| `patterns[shift].coverageSpanHours` | 24 | yes — worklife_test.go | engine.worklife |
| `patterns[shift].rotations[*].startHour` / `.endHour` | 0/8, 8/16, 16/24 | yes — worklife_test.go | engine.worklife |
| `patterns[any-time].hoursPerDay` | 8 | yes — worklife_test.go | engine.worklife |
| `patterns[any-time].daysPerWeek` | 5 | yes — worklife_test.go | engine.worklife |
| `patterns[any-time].coverageSpanHours` | 24 | yes — worklife_test.go | engine.worklife |
| `workingWeekPolicies[996].hoursPerWeek` | 72 | yes — worklife_test.go | engine.worklife |
| `workingWeekPolicies[996].wageCoefficient` | 1.8 | yes — worklife_test.go | engine.worklife |
| `workingWeekPolicies[996].wellbeingWeight` | 0.25 | yes — worklife_test.go | engine.worklife |

---

## Summary

* **Files tabulated:** 42 of 47 top-level `data/*.json` (5 excluded — no player-felt numbers; see index note).
* **Fields tabulated:** ~330 individual player-felt numeric fields (plus 356-entry `buildings.json` cost/capacity catalogue characterised in §4, and multi-row ladders counted once per tier).
* **Directional-test coverage:** most engine modules assert direction on their data fields (see per-field status). Notable gaps (fields with **none**):
  * `engine.accelerator`: facilityThroughput, electricityPeakMultiplier, prestigePerTick, prestigeBase
  * `engine.airport`: costMillions, freightApronTonnesPerDay, noiseLevelDBA, surfaceAccessReducedPct, landFootprintHectares, jobs
  * `engine.attract`: both `attract_terms.json` fields
  * `engine.comms`: parcelEraFactor, connectivity, eCommerce/post/drain/facility figures
  * `engine.citizens`: maxChildbearingAgeYears, peakFertilityAgeYears, baseMonthlyBirthRate
  * `engine.refuse`: contamination/vermin/incineration/funding/trucks figures
  * `engine.tax`: bearerWeights, incomeTaxBands, niRates, rateMultiplier
  * `engine.traffic` (`modes.json`): entire file (module not yet built)
* **Aaron-approved rows:** `accelerator.json` figures were row-by-row approved 2026-08-18 (fdiAnchorDraw PROVISIONAL until engine.fdi lands).
* **Spec-transcribed (non-placeholder) values:** roads modal caps (25t/1000t/3–40kt); `services.json` police 2.4/1000; `seasonal.json` electricity +15%, water +25%, gas ×2.2/×0.2; `education.json` primary 60 / secondary 132 months; `leisure.json` 168-hour week; `pacing.json` 480 s/month; `tax_instruments.json` UK 2025/26 PAYE bands (literature-grounded).

*This standing document is updated whenever a data file or field lands. BA-maintained; do not edit `data/*.json` from here.*
