// Shared display formatters (FEAT-1972079879).
// ONE source of truth for how numbers reach the screen:
//   fmtNum   — thousands-separated integer (e.g. 33000000 -> "33,000,000")
//   fmtMoney — funds, always carrying the GBP pound prefix (e.g. "£33,000,000")
//   fmtSigned— signed funds for deltas (e.g. "+£1,200" / "-£340")
// Every component renders figures through these, never ad-hoc toLocaleString /
// bespoke currency glyphs, so the whole console reads in one consistent style.

// en-GB is pinned so the separators are deterministic (comma thousands,
// no dependence on the host machine's locale) — matters for the format tests.
const LOCALE = 'en-GB';

/** Thousands-separated integer. NaN/Infinity degrade to "0" rather than leaking "NaN". */
export function fmtNum(n: number): string {
  if (!Number.isFinite(n)) return '0';
  return Math.round(n).toLocaleString(LOCALE);
}

/** Funds with a leading £; negative amounts render as "-£1,234" (sign before the symbol). */
export function fmtMoney(n: number): string {
  const v = Number.isFinite(n) ? n : 0;
  const sign = v < 0 ? '-' : '';
  return `${sign}£${fmtNum(Math.abs(v))}`;
}

/** Signed funds for per-tick / delta displays: always shows +£ or -£. */
export function fmtSigned(n: number): string {
  const v = Number.isFinite(n) ? n : 0;
  return `${v >= 0 ? '+' : '-'}£${fmtNum(Math.abs(v))}`;
}

/** Per-unit earnings: same £ prefix/sign as fmtMoney, two decimals when 0 < |n| < 1. */
export function fmtMoneyEach(n: number): string {
  const v = Number.isFinite(n) ? n : 0;
  const sign = v < 0 ? '-' : '';
  const abs = Math.abs(v);
  if (abs > 0 && abs < 1) {
    return `${sign}£${abs.toLocaleString(LOCALE, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  }
  return `${sign}£${fmtNum(abs)}`;
}

// COLD AUDIT (BUG-657 class): every other formatter in this file
// (fmtNum/fmtMoney/fmtSigned/fmtMoneyEach) guards Number.isFinite and degrades
// to a safe default — fmtPct was the one exception, so a NaN or Infinity input
// (e.g. a 0/0 ratio slipping past a call site's own guard) rendered literally
// as "NaN%"/"Infinity%" to the player. No live call site hits this today, but
// the inconsistency is exactly the class this audit hunts (an untested display
// helper that typechecks and lints while being wrong), so it is closed here
// rather than left as a landmine for the next caller that forgets to guard.
export function fmtPct(n: number, digits = 1): string {
  if (!Number.isFinite(n)) return '0%';
  return `${(n * 100).toFixed(digits)}%`;
}

/** Calendar label for a sim tick: 360-day year, 30-day months, months 1..12. */
export function gameDate(tick: number): string {
  const dayOfYear = tick % 360;
  const year = Math.floor(tick / 360) + 1;
  const month = Math.floor(dayOfYear / 30) + 1;
  const day = (dayOfYear % 30) + 1;
  return `Y${year} D${day}·M${month}`;
}

/**
 * Auto-scale power units from MW to GW/TW for large magnitudes.
 * Pure formatting function — does not mutate input.
 *
 * Scaling rules (SI base unit MW):
 *   mw < 1000        → "<n> MW"           (e.g. 950 MW)
 *   1000 ≤ mw < 1M   → "<n.n> GW"        (one decimal, e.g. 1.5 GW, 17.3 GW)
 *   mw >= 1M         → "<n.n> TW"        (one decimal, e.g. 1.2 TW)
 *
 * Trailing .0 is stripped (2000 MW → "2 GW", not "2.0 GW").
 * Negative values keep the sign (-500 MW, -1.5 GW, etc.).
 * Non-finite (NaN/Infinity) degrade to "0 MW" defensively.
 */
export function formatPower(mw: number): string {
  if (!Number.isFinite(mw)) return '0 MW';

  const abs = Math.abs(mw);
  const sign = mw < 0 ? '-' : '';

  if (abs < 1000) {
    return `${sign}${Math.round(abs)} MW`;
  }

  if (abs < 1_000_000) {
    const gw = abs / 1000;
    // Round to one decimal
    const rounded = Math.round(gw * 10) / 10;
    // Strip trailing .0
    const str = rounded === Math.floor(rounded) ? String(Math.floor(rounded)) : rounded.toFixed(1);
    return `${sign}${str} GW`;
  }

  const tw = abs / 1_000_000;
  // Round to one decimal
  const rounded = Math.round(tw * 10) / 10;
  // Strip trailing .0
  const str = rounded === Math.floor(rounded) ? String(Math.floor(rounded)) : rounded.toFixed(1);
  return `${sign}${str} TW`;
}
