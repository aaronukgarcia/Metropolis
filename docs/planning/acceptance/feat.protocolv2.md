BOW code: FEAT-042

# FEAT-042 has moved

Acceptance criteria for FEAT-042 (extend `int.protocol`: entity-level drill
addressing and an explicit crisis signal) now live in
`docs/planning/acceptance/int.protocol.md`, under the `# FEAT-042` heading
(AC-19 through AC-36), appended after `int.protocol`'s own frozen v1
criteria (AC-1–AC-18).

This file previously held those criteria as a standalone draft. It was
folded into the parent module's doc 2026-08-13 per the convention FEAT-057
established (`engine.finance.md`, `# FEAT-057` section appended after
`MOD-022`'s own ACs): an engine/protocol-side extension feature with a
clean `code.json` mkey (`int.protocol`) is folded into that module's
acceptance doc as a new headed section, not filed separately. `code.json`
has no `protocolv2`/`feat.protocolv2` module key — the affected key is
`int.protocol` — so this file's original name was never a real mkey match,
just an available filename.

Kept in place (rather than deleted) only as a redirect, so a stale link or
memory pointing at `feat.protocolv2.md` still lands somewhere useful.
