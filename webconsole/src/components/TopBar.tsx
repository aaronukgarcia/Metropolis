import { useState } from 'react';
import { useSim, levelOf, xpForLevel, wellbeingOf } from '../sim/store';
import { fmtMoney } from '../sim/utils';
import { versionBadgeLabel, versionRaw } from '../sim/version';
import { TrendArrows } from './Trend';
import { useBusy } from './Busy';
import { AboutModal } from './About';

const SPEEDS: { v: 0 | 1 | 2 | 3; label: string }[] = [
  { v: 0, label: 'Pause' },
  { v: 1, label: 'Play' },
  { v: 2, label: 'Fast' },
  { v: 3, label: 'Turbo' },
];

function gameDate(tick: number): string {
  const year = Math.floor(tick / 360) + 1;
  const day = (tick % 360) + 1;
  const month = Math.floor(day / 30) + 1;
  return `Y${year} D${day % 30 || 30}·M${month}`;
}

export function TopBar() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [aboutOpen, setAboutOpen] = useState(false);
  const level = levelOf(state.xp);
  const cur = xpForLevel(level);
  const next = xpForLevel(level + 1);
  const frac = Math.min(100, ((state.xp - cur) / Math.max(1, next - cur)) * 100);
  const wb = wellbeingOf(state);
  const wbColor = wb.overall >= 70 ? 'var(--done)' : wb.overall >= 45 ? 'var(--warn)' : 'var(--danger)';
  return (
    <header className="topbar">
      <div className="brand">
        <span className="brand-mark" />
        Metropolis
        <span className="muted">Command Console</span>
        <button
          className="version-badge mono"
          title={`Version ${versionRaw} — click for About & changelog`}
          onClick={() => setAboutOpen(true)}
        >
          {versionBadgeLabel()}
        </button>
      </div>
      <div className="top-center">
        <button
          className="btn danger"
          title="God mode: wipe everything and restart from the M20 junction seed"
          onClick={() => run(() => dispatch({ type: 'reset' }))}
        >
          Start Over
        </button>
      </div>
      <div className="top-stats">
        <span className="stat acc">
          {fmtMoney(state.funds)}
          <TrendArrows series={state.history.map((h) => h.funds)} gentle={15} fast={150} label="Treasury" />
        </span>
        <span className="stat">
          {state.population.toLocaleString()} citizens
          <TrendArrows series={state.history.map((h) => h.population)} gentle={0.05} fast={1} label="Population" />
        </span>
        <span className="stat" title={`Wellbeing ${wb.overall}/100`}>
          <span className="wb-dot" style={{ background: wbColor }} />
          {wb.overall}
          <span className="muted"> wellbeing</span>
          <span className="xp-mini">
            <span style={{ width: `${wb.overall}%`, background: wbColor }} />
          </span>
        </span>
        <span className="stat mono">{gameDate(state.tick)}</span>
        <span className="stat" title={`Level ${level}: ${state.xp} XP`}>
          Lv {level}
          <span className="xp-mini">
            <span style={{ width: `${frac}%` }} />
          </span>
        </span>
      </div>
      <div className="speed-ctl">
        {SPEEDS.map((s) => (
          <button
            key={s.v}
            className={`btn tiny${state.speed === s.v ? ' active' : ''}`}
            onClick={() => dispatch({ type: 'speed', speed: s.v })}
          >
            {s.label}
          </button>
        ))}
      </div>
      {aboutOpen && <AboutModal onClose={() => setAboutOpen(false)} />}
    </header>
  );
}
