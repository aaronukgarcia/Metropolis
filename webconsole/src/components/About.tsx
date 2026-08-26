import { useEffect } from 'react';
import { versionInfo, CHANGELOG } from '../generated/version';
import { CHANGELOG_CAP_NOTE } from '../sim/version';

// FEAT-1972079872 — About panel. Reachable by clicking the version badge in the
// TopBar. Lists check-ins (commit hash + subject + author/commit date) and
// version/tag boundaries, newest first, entirely from the build-time
// git-generated changelog. No network calls; the data is baked at build.

function fmtDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function AboutModal({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div className="about-backdrop" onClick={onClose} role="presentation">
      <section
        className="panel about-panel"
        role="dialog"
        aria-modal="true"
        aria-label="About Metropolis Command Console"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="panel-h">
          <span className="panel-title">About — Metropolis Command Console</span>
          <button className="btn tiny" onClick={onClose} aria-label="Close About">
            Close
          </button>
        </header>
        <div className="panel-body about-body">
          <div className="about-version">
            <span className="about-version-num mono">{versionInfo.version}</span>
            {!versionInfo.gitAvailable && (
              <span className="muted"> (fallback — git was unavailable at build)</span>
            )}
          </div>
          <div className="about-meta muted mono">
            Built {fmtDate(versionInfo.generatedAt)} · {CHANGELOG.length} check-ins shown
          </div>

          <div className="about-log">
            {CHANGELOG.length === 0 && (
              <div className="muted">No changelog available (git history not read at build).</div>
            )}
            {CHANGELOG.map((c) => (
              <div key={c.hash} className="about-entry">
                <div className="about-entry-head">
                  <span className="about-hash mono">{c.hash}</span>
                  {c.tag && <span className="about-tag">{c.tag}</span>}
                  <span className="about-date muted mono">{fmtDate(c.date)}</span>
                </div>
                <div className="about-subject">{c.subject}</div>
              </div>
            ))}
          </div>

          <div className="about-foot muted">{CHANGELOG_CAP_NOTE}</div>
        </div>
      </section>
    </div>
  );
}
