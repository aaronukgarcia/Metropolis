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
//     ~216B (unsafe.Sizeof, amd64) — inside the spec's "~250B" line. The
//     gap to 250B is the slice backing arrays (Children, Education.Stages)
//     and Go allocator size-class overhead, exactly the two costs the raw
//     sizeof cannot see; M0-ENG §1.3's 8GB budget (⇒ ~32M resident) is
//     therefore met with headroom. See TestHotRecordSizeIsAbout250B.
//   - Cold store: a columnar struct-of-arrays [ColdShard] with field-level
//     compression (bucketed enums, delta-coded ages relative to a shard
//     epoch, bit-packed/byte-packed states) targeting A1's 60–100B/citizen.
//     The measured per-citizen cost is ~67B ⇒ 100M citizens ≈ 6.7GB, inside
//     A1's 6–10GB band. See TestColdShardBytesPerCitizen and
//     TestColdStore100MProjection.
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
// # NVMe and paging (A7, §5.3)
//
// NVMe SSD is a stated hardware requirement for very large cities: beyond
// ~20–30M local citizens, cold shards page to disk (mmap, LRU) rather than
// staying resident. This package ships [PageStore], the disk-backed LRU
// paging seam for cold shards, exercised by TestPageStoreEvictsAndReloads
// (a scaled-down analogue proving the paging code path runs without
// needing a >8GB synthetic in a unit test); binary serialization of cold
// shards via int.serializer is out of scope for this item.
//
// # Mortality smoothing / death-wave (feat.deathwave, FEAT-087)
//
// The cold pass's Gompertz-Makeham mortality (§5.1/§5.2, AC-11) selects
// deaths; a [DeathQueue] then defers them and releases at most the
// data-sourced monthly death budget (data/mortality.json) per non-emergency
// month, so a same-birthMonth cohort aging onto the steep Gompertz slope
// becomes a smooth ~N-deaths/month tail, never a single-month population
// cliff (§10 "deaths are continuous", AC-1). Smoothing is pure delay —
// every selected death is eventually realised, none dropped or duplicated
// (AC-2, §14/§19). A deterministic, seeded citywide weather draw
// [WeatherSeverity] keyed hash(worldSeed, month, "weather") declares a
// weather emergency (§9) that suspends the budget (multiplying it, AC-6);
// the emergency source is engine.season per the acceptance criteria, but the
// engine.citizens → engine.season code.json edge is not yet registered, so
// the signal is derived locally and flagged for the SSOT pass. Realised
// deaths are exposed as an ordered (selection month, citizen id) handoff
// ledger ([RealisedDeath]: CitizenID, DeathMonth, Emergency) for FEAT-088's
// death services (graveyards, cremation, hearses — §H).
package citizens
