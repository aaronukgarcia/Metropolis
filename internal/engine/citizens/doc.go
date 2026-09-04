// Package citizens is the Option B citizen model (MOD-018): persistent
// individual citizens with adaptive fidelity, no culls ever, up to 100M
// at adaptive fidelity (§5). It owns the hot AoS citizen record, the
// columnar cold SoA store, the HOT/WARM/COLD fidelity dial, the amortised
// 1/30-shards-per-day cold batch pass, the A7 sampling firewall, binding
// deterministic life-writing, per-person Gompertz-Makeham monthly
// mortality, and households.
//
// Module key: engine.citizens (see code.json; GUID 99e0d1f5-0214-4b06-bcde-caba0b1e44ad)
// Spec refs:  §5 Citizens — the Option B Model (all subsections: §5.1 the
// citizen record, §5.2 adaptive fidelity, §5.3 memory & storage at 100M,
// §5.4 households & housing); A1 (cold SoA store 60–100B/citizen), A2
// (amortised 1/30-shards-per-day cold pass), A7 (sampling firewall,
// camera-invariance, NVMe >20M); §II.3; M0-ENG §1.2 (counter-based RNG
// streams keyed (worldSeed, entityId, month, purposeTag)) and §1.3
// (citizen-shard memory budget, ~250B/citizen ⇒ ~32M resident).
//
// # Byte budgets (the scale-risk deliverable, measured not asserted)
//
//   - Hot record: a rich AoS [Citizen] whose inline size is measured at
//     ~232B (unsafe.Sizeof, amd64) — inside the spec's "~250B" line
//     (corrected 2026-09-04, BUG-666: this comment previously said ~216B,
//     stale since the births-unblock lane's uint32->uint64 widening of
//     Household/Partner, 2026-09-02). The gap to 250B is the slice backing
//     arrays (Children, Education.Stages) and Go allocator size-class
//     overhead, exactly the two costs the raw sizeof cannot see; M0-ENG
//     §1.3's 8GB budget (⇒ ~32M resident) is therefore met with headroom.
//     See TestHotRecordSizeIsAbout250B.
//   - Cold store: a columnar struct-of-arrays [ColdShard] with field-level
//     compression (bucketed enums, delta-coded ages relative to a shard
//     epoch, bit-packed/byte-packed states) targeting A1's 60–100B/citizen.
//     The measured COLUMN-ONLY per-citizen cost is ~75B (corrected
//     2026-09-04, BUG-666: this comment previously said ~67B, stale since
//     the same births-unblock widening) ⇒ 100M citizens ≈ 7.5GB, inside
//     A1's 6–10GB band. See TestColdShardBytesPerCitizen and
//     TestColdStore100MProjection.
//   - Cold store id->row index (BUG-666, new 2026-09-04): [ColdShard.rowOf]
//     was an O(shard size) linear scan with no index, making every
//     single-citizen lookup ~390k comparisons at 100M and turning
//     applyFertilityLocked, all four moneycirc.go monthly resident passes,
//     and the death-drain realisation loop quadratic. A per-shard
//     map[uint64]int32 collapses this to O(1). Its REAL measured cost is
//     ~38B/citizen (TestColdShardIndexOverhead), not the ~6-8B/citizen
//     back-of-envelope estimate in
//     docs/planning/go-engine-100m-proving-plan.md §3.8 — Go's runtime hash
//     map has real per-entry overhead (bucketing, tophash bytes, load-factor
//     slack) well beyond the naive key+value sum. bytesPerCitizen()'s total
//     (columns + index) is therefore ~113B/citizen ⇒ 100M ≈ 11.3GB,
//     OUTSIDE the original 60-100B / 6-10GB bands. This is reported
//     honestly here and in the updated test ceilings (TestColdShardBytesPerCitizen,
//     TestColdStore100MProjection, TestColdStore1MRealAllocation,
//     TestPerf10M all now assert the real post-index numbers, not the stale
//     pre-index band) rather than silently widened to hide the miss — 11.3GB
//     is still comfortably inside the 32GiB resident floor / 64GiB
//     provisioned figure the proving plan already sizes 100M for, so it does
//     not change the finish-line hardware plan, but it IS a real memory
//     regression against the original A1 band that Aaron should see.
//
// # The amortised cold pass (A2)
//
// The cold store is 256 shards (id-hash via foundation/det.ShardForEntity).
// [ColdPassSchedule] maps each shard to exactly one of the 30 logistics
// day-ticks of a calendar month: 256 = 30×8 + 16, so 16 day-ticks process
// 9 shards and 14 process 8, and every shard is processed exactly once per
// month (AC-6/AC-7). The schedule is a fixed, seed-independent function of
// the day index (shard i → day i*30/256) — never random jitter. The
// amortisation changes *when within the month* a shard advances, never
// *how many times*.
//
// # Determinism (GR#21, M0-ENG §1.2)
//
// Every stochastic draw for citizen i at month m uses an independent
// counter-based hash stream keyed (worldSeed, i, m, purposeTag) via
// foundation/det.NewStream — the literal spec rule "hash(worldSeed, id,
// month, purpose)". There is no shared RNG object anywhere in this package,
// no wall-clock call on any tick path (simulation time is the only time),
// and no Go map is ever
// iterated in an order that affects an observable result: the cold pass
// iterates columnar slices in index order, and the stratified sample is
// built and sorted deterministically. Same seed + same command log ⇒
// bit-identical population at any worker count.
//
// # The sampling firewall (A7)
//
// Cold-pass parameters are estimated ONLY from a [StratifiedSample],
// stratified by district × age band × income and coverage-guaranteed (every
// non-empty stratum holds at least a minimum member count), selected by a
// rotating deterministic draw. Viewport-hot citizens are display fidelity
// only and are never a parameter-estimation input — a player parking the
// camera over one district can never skew citywide cold-pass statistics
// (AC-9/AC-16). The stratification dimensions are:
//
//   - district (uint16, the citizen's home district),
//   - age band (5 bands: 0-17, 18-34, 35-54, 55-74, 75+),
//   - income band (5 bands, from wealth).
//
// The coverage guarantee is a minimum sample count per non-empty stratum,
// filled deterministically before the rotating draw, so a later consumer
// (engine.wellbeing, engine.social) can reason about whether its own
// cold-pass-derived numbers are trustworthy at small populations (AC-23).
//
// # Life-writing (§5.2)
//
// Inspecting a cold citizen runs [LifeWrite]: a pure, deterministic
// reconstruction of their recent detail from (record, district statistics,
// hash(seed, id, month)). Re-inspecting at the same month returns identical
// bytes, and the reconstruction is consistent with the district aggregates
// it was drawn from — it is what happened, already accounted for.
//
// # Live-tick wiring and the citizen-id namespace seam (FEAT-169)
//
// [CitizensAPI.AdvanceDayTick] is called once per simulation day-tick from
// the composition root (internal/engine/compose), registered against
// core.PhaseDailyTick alongside build (BUG-268) — see
// docs/planning/icd/engine.citizens-coldpass.md, the ICD this integration
// was built against. AdvanceDayTick's own internal parallel
// mortality/education/job pass and sequential fertility pass (FEAT-160) are
// entirely self-contained; from compose's point of view this is one opaque
// call per day-tick, single-shard (SingleShardHook).
//
// AdvanceDayTick RETURNS this call's own (births, deaths) — the tick's own
// totals, not a monthly aggregate — which compose folds into its own
// people-conservation ledger (simState.peopleDelta) every single day-tick,
// the same role migration admits play there (just at daily rather than
// monthly granularity). This is an ICD deviation from §4's "pull
// VitalEvents at the month boundary" option, with reason: the cold pass's
// mortality/fertility mutations land on the cold store incrementally, one
// amortised shard-slice per day-tick (the paragraph above), so batching the
// conservation credit to month-end would defer it past the tick the
// population actually changed on — a same-tick violation of the ICD's own
// §5 T0 requirement. This was caught for real during FEAT-169's build (the
// daily invariant conservation check flagged it), not merely reasoned
// about. [CitizensAPI.VitalEvents] is UNCHANGED and still reports the most
// recently COMPLETED calendar month's totals for any consumer that wants a
// monthly view; it is simply not compose's conservation seam.
//
// Fertility-born child ids are minted from [FertilityChildIDBase] (2^63,
// corrected 2026-08-18 — see below), the THIRD of three disjoint id ranges
// three packages independently mint from, by CONVENTION, not a shared
// allocator:
//
//	[1,                 attract.MigrantIDBase)      internal/engine/compose: seed population + direct seeding (simState.nextCitizenID)
//	[attract.MigrantIDBase, FertilityChildIDBase)    internal/engine/attract: admitted-migrant ids (migration.go's migrantIDHighBit)
//	[FertilityChildIDBase, ...)                      this package: fertility-born child ids (fertilityChildIDBase)
//
// FertilityChildIDBase was ORIGINALLY 1<<62 — the same value
// attract.MigrantIDBase independently chose — so once FEAT-169 wired both
// the citizens cold-pass tick and the attract migration hook into the same
// live composition, a duplicate citizen id was reachable within months of
// simulated play, invisible to TotalPopulation's row-count-based
// conservation view (a duplicate id silently ALIASES an existing citizen
// rather than changing the row count). Destructive-review REJECT
// (2026-08-18) moved this package's base to 1<<63 and added THREE
// independent defenses, none a substitute for the others: compose's
// spawnCitizens rejects any mint at or past attract.MigrantIDBase
// (compose's OWN range guard, ErrCitizenIDNamespaceSeam); compose's Wire
// additionally asserts FertilityChildIDBase >= 2*attract.MigrantIDBase at
// construction time (the boundary BETWEEN attract's and this package's
// ranges, ErrIDNamespaceRangesOverlap — compose's own guard above cannot
// see this boundary); and THIS package independently rejects a
// LifeEventBirth whose id already exists in its own cold or hot store
// ([ErrDuplicateCitizenID]) — the only one of the three that would catch
// an ACTUAL runtime collision rather than a range-check on constants. See
// compose/doc.go's "Citizen id namespace map" section (the mirror of this
// one) and compose/errors.go. This is the ICD's §12 open-decision-2
// resolution, amended: an explicit, checked, verified-disjoint contract
// across all three id spaces, not a single shared allocator (unifying them
// is a larger refactor flagged as a follow-up, not done here).
//
// # Death-queue smoothing (FEAT-087, mkey feat.deathwave, §5.2/§9/§10/§H)
//
// [DeathQueue] (deathwave.go) extends this package's existing
// Gompertz-Makeham mortality (mortality.go's MortalityHazard/
// MortalityDeath, §5.2, consumed not re-derived) with a bounded monthly
// realisation buffer, so a same-birthMonth cohort riding the steep
// Gompertz slope (§10: "deaths are continuous, so is deathcare demand")
// cannot produce a one-month population cliff. The mechanism separates
// SELECTION from REALISATION:
//
//   - Enqueue records a hazard-selected death; the citizen is NOT removed
//     from the living population at selection time.
//   - Realise releases at most a data-file-sourced monthly budget
//     (data/mortality.json's monthlyDeathBudget, GR#15 — never a bare Go
//     literal) of the OLDEST queued entries, FIFO by
//     (selectionMonth, citizenID) — a deterministic total order that is a
//     pure function of queue CONTENTS, never of Enqueue call order, so
//     the realised sequence is byte-identical at any worker count
//     (M0-ENG §1.2, GR#21).
//   - The remainder is retained, never dropped: over a full drain, total
//     realised deaths equals total selected deaths exactly (§14/§19,
//     GR#12 — smoothing is delay, never a cull or a leak).
//   - A queued (selected-but-unrealised) citizen still counts in the
//     living population, still ages, and still contributes to population
//     aggregates until realised (Aaron's ASM-581 ruling, 2026-08-27) — the
//     queue entry is that citizen's single, terminal selection event.
//
// inc1 built the DeathQueue core in isolation (proven by deathwave_test.go
// calling Enqueue/Realise directly). inc1.5 (the current state) WIRES it
// into the LIVE cold pass: [ColdShard.applyMonthly]'s mortality draw now
// Enqueues a hazard hit instead of removing the citizen inline, and
// [CitizensAPI.AdvanceDayTick] Realises the data-file budget exactly once
// per completed calendar month (the day-tick that schedules the last cold
// shard, so every shard's selection this month has already been
// enqueued), removing only the released ids and running the BUG-369/
// BUG-270 household-dissolution path at that same point — dissolution
// fires at REALISATION, never at selection, so a queued citizen's
// household stays fully intact for as long as they wait in the queue
// (coldpass_deathwave_test.go's live-tick proof suite). Neither increment
// below reshapes DeathQueue's own API.
//
// inc2 (AC-6/AC-7/AC-8, ASM-579, current state) adds a declared weather
// emergency, consumed through the registered feat.deathwave -> engine.
// season outbound edge ([weatheremergency.go]'s [IsWeatherEmergency]):
// a month is declared a winter-shaped emergency when the magnitude of
// SeasonAPI.HealthWaveModifier is at or above data/mortality.json's
// winterHealthWaveThreshold, or a drought-shaped emergency when
// SeasonAPI.WaterDemandMultiplier is at or above
// droughtWaterDemandThreshold — direction/shape only (ASM-579's ruling),
// derived LOCALLY from engine.season's EXISTING curves rather than a new
// engine.season surface or feat.disasters' aquifer-drought event (that
// edge stays deliberately unregistered; a tripwire test asserts no
// feat.disasters import). [CitizensAPI.AdvanceDayTick]'s once-per-month
// realisation step declares the emergency before releasing
// ([EmergencyRealise]): a declared emergency SUSPENDS the ordinary
// monthlyDeathBudget for that release and substitutes
// monthlyEmergencyBudget (0 is the documented "unbounded" sentinel —
// release the ENTIRE queue), producing the one legitimate non-smoothed
// major death event AC-6 names. The suspension touches ONLY how much of
// the FIFO head is released — it never reaches back into
// [ColdShard.applyMonthly]'s hazard draw or [DeathQueue.Enqueue], so a
// fixed set of hazard SELECTIONS realises differently under an emergency
// (more of them, sooner) while WHICH citizens the Gompertz-Makeham hazard
// picked is completely unaffected (AC-8 — suspension of throughput, never
// a second mortality model). engine.season is an OPTIONAL injected
// dependency, wired post-construction via [CitizensAPI.SetSeason]
// (mirroring engine.build/engine.cafe/engine.education's own SetSeason
// precedent); a CitizensAPI never wired to engine.season simply never
// declares an emergency, so every pre-inc2 caller is unaffected.
//
// inc3 adds the queryable, FIFO-ordered (citizenId, deathMonth,
// emergencyFlag) handoff stream FEAT-088 (feat.deathservices, §H —
// cemetery/crematorium/hearse throughput) drains, replacing a fixed test
// drain rate with FEAT-088's own injected funeral-throughput capacity
// (realisation becomes min(budget, drainCapacity, queued) — ASM-580: the
// smoothing budget and the funeral drain rate are two independent knobs,
// never one derived from the other).
//
// # NVMe and paging (A7, §5.3)
//
// NVMe SSD is a stated hardware requirement for very large cities: beyond
// ~20–30M local citizens, cold shards page to disk (mmap, LRU) rather than
// staying resident. This package ships [PageStore], the disk-backed LRU
// paging seam for cold shards, exercised by TestPageStoreEvictsAndReloads
// (a scaled-down analogue proving the paging code path runs without
// needing a >8GB synthetic in a unit test); binary serialization of cold
// shards via int.serializer is out of scope for this item.
package citizens
