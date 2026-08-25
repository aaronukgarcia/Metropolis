interface Props {
  series: number[];
  gentle: number;
  fast: number;
  label: string;
}

export function TrendArrows({ series, gentle, fast, label }: Props) {
  const s = series.slice(-12);
  if (s.length < 4) return null;
  const d = (s[s.length - 1] - s[0]) / (s.length - 1);
  const a = Math.abs(d);
  if (a < gentle) {
    return (
      <span className="trend flat" title={`${label}: steady`}>
        =
      </span>
    );
  }
  const up = d > 0;
  const dbl = a >= fast;
  return (
    <svg
      className={`trend ${up ? 'up' : 'down'}`}
      width={dbl ? 17 : 10}
      height={13}
      viewBox={dbl ? '0 0 17 13' : '0 0 10 13'}
      role="img"
      aria-label={`${label} ${dbl ? 'strongly ' : ''}${up ? 'rising' : 'falling'}`}
    >
      <g transform={up ? undefined : 'translate(0,13) scale(1,-1)'}>
        <path d="M1.5,8 L5,3.5 L8.5,8" />
        {dbl && <path d="M8.5,8 L12,3.5 L15.5,8" />}
      </g>
    </svg>
  );
}
