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

export function fmtPct(n: number, digits = 1): string {
  return `${(n * 100).toFixed(digits)}%`;
}
