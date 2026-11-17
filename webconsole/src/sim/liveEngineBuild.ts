// liveEngineBuild.ts — BUG-1097 / Q100145: the framework-free, module-level
// record of which engine build the live-engine handshake last connected to,
// versus this client's own build. Mirrors financeStatusTracker.ts's pattern
// (a side-channel singleton fed by LiveEngineBadge's ProtocolClient; never
// SimState, never journaled, no determinism surface).
//
// Aaron's ruling (Q100145): a differing build string is a WARNING, never a
// block. debugjson.ts reads this to write meta.engineBuild / meta.clientBuild
// / meta.engineBuildMismatch, and the GR#27 pre-wipe capture inherits them
// because it is built from the same debug JSON. Null means "no live-engine
// handshake has completed in this session" and the three keys are then
// ABSENT from the debug JSON (not null, not false).

export interface LiveEngineBuild {
  /** The engine's build string, exactly as the handshake response carried it. */
  engineBuild: string;
  /** This client's build string (versionRaw). */
  clientBuild: string;
  /** True when the two differ after stripping the volatile "-dirty" suffix. */
  mismatch: boolean;
}

let current: LiveEngineBuild | null = null;

/** Record the build pair from a completed live-engine handshake. */
export function setLiveEngineBuild(b: LiveEngineBuild): void {
  current = { ...b };
}

/** The last recorded pair, or null when no handshake has completed. */
export function getLiveEngineBuild(): LiveEngineBuild | null {
  return current ? { ...current } : null;
}

/** Forget the recorded pair (the connection is gone or the badge unmounted). */
export function resetLiveEngineBuild(): void {
  current = null;
}
