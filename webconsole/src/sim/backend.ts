const QUEUE_KEY = 'metropolis.debugQueue';

const errorLog: { at: number; msg: string }[] = [];

export function recordError(msg: string): void {
  errorLog.unshift({ at: Date.now(), msg });
  if (errorLog.length > 25) errorLog.pop();
}

export function recentErrors(): { at: number; msg: string }[] {
  return [...errorLog];
}

export interface CommitResult {
  ok: boolean;
  queued: boolean;
  id: string;
  message: string;
}

interface QueuedCommit {
  id: string;
  at: string;
  payload: unknown;
}

export function pendingCommits(): number {
  try {
    const q = JSON.parse(localStorage.getItem(QUEUE_KEY) ?? '[]');
    return Array.isArray(q) ? q.length : 0;
  } catch {
    return 0;
  }
}

function enqueue(id: string, payload: unknown): number {
  let q: QueuedCommit[] = [];
  try {
    const raw = JSON.parse(localStorage.getItem(QUEUE_KEY) ?? '[]');
    if (Array.isArray(raw)) q = raw;
  } catch {
    q = [];
  }
  q.push({ id, at: new Date().toISOString(), payload });
  const trimmed = q.slice(-50);
  localStorage.setItem(QUEUE_KEY, JSON.stringify(trimmed));
  return trimmed.length;
}

export async function commitDebug(payload: unknown): Promise<CommitResult> {
  const id = `DBG-${Date.now().toString(36).toUpperCase()}`;
  try {
    const res = await fetch('/api/debug/commit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, at: new Date().toISOString(), payload }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return { ok: true, queued: false, id, message: `Committed to backend as ${id}` };
  } catch {
    const n = enqueue(id, payload);
    return {
      ok: true,
      queued: true,
      id,
      message: `Backend unreachable — queued locally as ${id} (${n} awaiting processing)`,
    };
  }
}
