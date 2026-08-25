export function fmtMoney(n: number): string {
  const sign = n < 0 ? '-' : '';
  return `${sign}¤${Math.abs(Math.round(n)).toLocaleString()}`;
}

export function fmtSigned(n: number): string {
  return `${n >= 0 ? '+' : '-'}¤${Math.abs(Math.round(n)).toLocaleString()}`;
}

export function fmtPct(n: number, digits = 1): string {
  return `${(n * 100).toFixed(digits)}%`;
}
