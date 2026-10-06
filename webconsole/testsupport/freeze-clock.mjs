// freeze-clock.mjs — a deterministic test clock for the date-sensitive
// save/lineage suites (anti-rot). This does NOT disable or skip any test; it
// only pins "now" so the suites stay deterministic. Every assertion still runs
// against the real production logic and can still fail if that logic breaks.
//
// WHY: those suites stamp savepoints with fixed 2026-09 dates and depend on the
// real wall-clock staying within AUTOSAVE_RETENTION_MS (30 days, replay.ts) of
// them. Once real time passed that window the fixtures were auto-purged as
// stale, so the suites went RED with NO code change — green at the 2026-09-11
// pause, red 25 days later (the 2026-10-06 inherited CI red). Pinning now to a
// fixed instant just after the fixtures restores the exact conditions they were
// authored and passed under. Intentionally-old fixtures (2026-01-01, 2020) stay
// correctly old relative to this frozen now, so staleness is still exercised.
//
// SCOPE: imported ONLY by the affected files (as the first import), never
// globally — every other suite and the hook tests keep the real clock. For the
// mutation-harness child processes, the same module is passed through the
// harness's own `extraArgs: ['--import', ...]` at the call site (explicit, not
// an ambient env injection).

const FIXED_MS = new Date(2026, 8, 7, 12, 0, 0).getTime(); // 2026-09-07 12:00 local
const RealDate = Date;

class FrozenDate extends RealDate {
  constructor(...args) {
    super(...(args.length ? args : [FIXED_MS]));
  }
  static now() {
    return FIXED_MS;
  }
}

globalThis.Date = FrozenDate;
