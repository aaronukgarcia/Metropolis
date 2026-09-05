// Shared "does this test file write into webconsole/src?" static tracer.
//
// BUG-744 (P2): extracted out of tools/scoped-runner-attack.mjs's F1 self-test
// (and its R3 synthetic-fixture companion) so the fix to the tracer's blind
// spots only has to be made in ONE place — the two copies had already started
// to drift apart, which is exactly how the BUG-739 re-round #2 finding
// (comma-truncated argument capture) went unnoticed by the R3 fixture even
// though R3 was written specifically to prove the tracer's own claims.
//
// Deliberately a regex/AST-lite PATTERN MATCH, not a real interpreter, a
// bundler, or `eval`/`require` of the target file — see the long history in
// tools/scoped-runner-attack.mjs's F1 comment block for the shapes this can
// and cannot see (spawned child processes, raw fd writes, arbitrary string
// concatenation). BUG-744 closes three specific gaps found in the R2 round:
//
//   1. Multi-argument `path.join(ROOT, 'src', 'sim', 'data.ts')` (or
//      `path.resolve(...)`) INLINE as a write call's own target argument —
//      the old capture regex `([^,)]+)` stopped at the first comma, so only
//      `path.join(ROOT` was ever tested, and the literal `'src'`/`'sim'`
//      segments after the first comma were invisible. (The SAME expression
//      assigned to a `const` first was already caught, because the
//      declaration-RHS capture took the whole line up to `;`/newline, not
//      just up to a comma — the bug was specific to the call-argument path.)
//   2. Object-property holders: `const paths = { engine: 'src/sim/x.ts' };`
//      then `writeFileSync(paths.engine, ...)` — the old tracer only ever
//      populated a set of BARE identifiers, never `obj.prop` dotted names.
//   3. Template literals whose literal quasis contain a src segment, e.g.
//      `` `${ROOT}/src/sim/data.ts` `` inline in the write call — also a
//      casualty of the same first-comma truncation when the template literal
//      itself contained a comma (e.g. inside a `${...}` expression).
//
// Fix shape: the write-call target argument is now extracted with a
// balanced-parenthesis scan (so nested calls like `path.join(...)` used AS
// the argument are captured whole, not truncated at their own internal
// commas), and object literals assigned to a traced-worthy `const` are
// parsed into dotted `name.prop` entries in the same traced-variable set
// used for bare-identifier aliases.
//
// BUG-744 round REJECT (opus-round-bug744, same session): the balanced-paren
// fix above introduced its own regression and missed a direction bug:
//
//   P1 (regression): a join whose BASE is a temp/shadow root —
//      `writeFileSync(join(shadowRoot, 'src', 'sim', 'engine.ts'), ...)`
//      where `shadowRoot = mkdtempSync(join(tmpdir(), 'shadow-'))` — is the
//      repo's own sanctioned idiom (webconsole/testsupport/mutant.mjs's
//      shadow-copy safety net) and must NOT be flagged just because the
//      literal segments after the base happen to spell a src path; the old
//      (pre-BUG-744) tracer never saw this at all because it truncated at
//      the first comma, so it accidentally got this case "right" for the
//      wrong reason. Fixed by recognising a join/resolve call's first
//      argument as a temp/shadow root — bound to mkdtempSync(...)/tmpdir()/
//      os.tmpdir(), or simply named `tmp*`/`shadow*` — and treating that
//      whole join expression as NOT a src path regardless of its later
//      literal segments.
//   P2 (direction bug): `copyFileSync(src, dest)`'s DESTINATION is argument
//      1, not 0 — the same shape as `renameSync`. The tracer treated
//      copyFileSync's SOURCE (arg 0) as the target, so
//      `copyFileSync(sabotaged, '../src/sim/engine.ts')` (destination IS
//      src — genuinely harmful) was missed, and
//      `copyFileSync('../src/sim/engine.ts', backupInTmp)` (source is src,
//      destination is a harmless backup — a legitimate read) was falsely
//      flagged. `cpSync` shares the same (src, dest) signature and gets the
//      same fix.
//   P3 (cheap, same round): `appendFileSync`/`cpSync`/`createWriteStream`
//      added to the write-call list; the mutant.mjs exemption now requires
//      an `import` KEYWORD to actually start the line (ignoring leading
//      whitespace) rather than matching the `from '...mutant.mjs'` /
//      `import('...mutant.mjs')` substring anywhere in the file — a
//      commented-out import (`// import ... from '../testsupport/mutant.mjs'`)
//      or a string merely mentioning the path no longer exempts a file that
//      has no real import of the helper.

// BUG-744 re-round REJECT (opus-reround-bug744, same session): the P1 fix's
// name-hint shortcut was ITSELF fail-open — it exempted anything merely
// NAMED tmp*/shadow* regardless of what the variable actually held, so
// `const tmpEnginePath = join(__dirname,'..','src','sim','engine.ts');
// writeFileSync(tmpEnginePath, mutated)` and
// `const shadowEngine = '../src/sim/engine.ts'; writeFileSync(shadowEngine, ...)`
// both went undetected — a real src write hidden behind a temp-sounding
// name. Narrow fix, three parts:
//   (i)   a declared identifier is now ALWAYS eligible for src-alias tracing
//         (the alias loop no longer skips names merely because they are ALSO
//         in tempRootVars) — a name can be both "looks temp" and "actually
//         holds a real src path"; the latter must win.
//   (ii)  tempRootVars only ever gains a name when the RHS is a genuine temp
//         binding (mkdtempSync(...)/tmpdir()/os.tmpdir()), OR the name hints
//         temp AND the RHS does not itself look like a src path AND the RHS
//         is not a repo-root binding (ROOT/REPO_ROOT/__dirname-based) — so
//         `tmpRoot = REPO_ROOT` and `shadowRoot = join(__dirname,'..')`
//         (both real repo roots wearing a temp-sounding name) are excluded
//         from the exemption, same as a name directly holding a src literal.
//   (iii) an UNDECLARED bare identifier merely named tmp*/shadow* (e.g. a
//         join's first argument that is a function parameter or otherwise
//         never bound in-file) is NOT exempt — only names that resolved into
//         tempRootVars via (ii) get the pass, closing the "just name your
//         parameter shadowDir" bypass.
// BUG-744 re-round 3 REJECT (opus-reround3-bug744, same session): the
// re-round-2 fix's "NAME hints temp AND rhs doesn't look repo-rooted"
// fallback was a TOKEN BLOCKLIST (checking the RHS text for ROOT/REPO_ROOT/
// __dirname) — trivially defeated by any repo-root expression that doesn't
// spell one of those tokens: `path.resolve('..')`, `process.cwd()`,
// `fileURLToPath(new URL('..', import.meta.url))`, or even a bare `'../'`
// literal. Lead ruling: do NOT add more tokens to the blocklist — DELETE the
// name-hint branch entirely. A name is a temp root IFF (and ONLY IFF) its
// RHS is a genuine temp binding — `mkdtempSync(...)`, `tmpdir()`,
// `os.tmpdir()` — or it DERIVES TRANSITIVELY from one via a
// `join(tmpRoot, ...)`/`resolve(tmpRoot, ...)` call whose base is already a
// verified temp root (see tempRootVars construction below, resolved the same
// multi-round way as the srcVars alias chain). The variable's NAME is never
// consulted again. TEMP_NAME_HINT is kept only as documentation of the shape
// this closes off, not read anywhere.
//
// Known P3 (accepted, not fixed): a temp root arriving as a bare FUNCTION
// PARAMETER (never bound via a traceable `const`/`let`/`var` in this file)
// can no longer be recognised as temp at all now that the name-hint route is
// gone, so a join under it now FALSE-POSITIVES (flags a write that is
// actually safe) — fail-safe (over-flagging, not under-flagging), and no
// such fixture is pinned as a negative.
const USES_MUTANT_HELPER =
  /^[ \t]*import\s[^\n]*\bfrom\s+['"]\.\.\/testsupport\/mutant\.mjs['"]|^[ \t]*(?:await\s+)?import\(\s*['"]\.\.\/testsupport\/mutant\.mjs['"]/m;

// Documentation only (BUG-744 re-round 3) — the shape a temp/shadow root's
// name USED to hint at; no longer read by any detection logic.
const TEMP_NAME_HINT = /^(?:tmp|shadow)/i;

// A src-path "shape": tolerates path.join/resolve-style comma/quote/space
// separated segments between the two path components, e.g. both
// `src/sim/data.ts` and `'src', 'sim', 'data.ts'` (and template-literal
// interpolation boundaries, which look like stray `{`/`}`/`$` characters —
// none of which this needs to explicitly skip since it only anchors on the
// literal text either side).
const SRC_MARKER = /src[\\/'",\s]{0,5}(?:sim|components)[\\/'",\s]{0,5}[\w.-]+\.tsx?|_TS_PATH|engineTsPath|dataTsPath/i;

/** Find the index of the character that closes the bracket opened at `openIdx`
 * (text[openIdx] must be `open`). Returns -1 if unbalanced. */
function matchBalanced(text, openIdx, open, close) {
  let depth = 0;
  for (let i = openIdx; i < text.length; i++) {
    if (text[i] === open) depth++;
    else if (text[i] === close) { depth--; if (depth === 0) return i; }
  }
  return -1;
}

/** Split `text` on commas that are NOT nested inside (), [], {} — so a
 * `path.join(a, b, c)` passed as one argument doesn't get chopped into three. */
function splitTopLevel(text) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') depth--;
    else if (c === ',' && depth === 0) {
      parts.push(text.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(text.slice(start));
  return parts.map((p) => p.trim()).filter(Boolean);
}

/** True if `text` contains a `join(...)`/`resolve(...)` (bare or `path.`-
 * qualified) call whose FIRST argument is a VERIFIED temp/shadow root —
 * either an inline mkdtempSync(...)/tmpdir()/os.tmpdir() call, or a bare
 * identifier already resolved into `tempRootVars` (BUG-744; re-round 3
 * removed the name-only fallback — a name is never enough on its own) —
 * meaning any src-shaped literal segments elsewhere in the call describe a
 * disposable scratch copy, not the real repo source. */
function isTempRootedJoinCall(text, tempRootVars) {
  const m = /(?:\bpath\.)?\b(?:join|resolve)\s*\(/.exec(text);
  if (!m) return false;
  const openIdx = m.index + m[0].length - 1;
  const closeIdx = matchBalanced(text, openIdx, '(', ')');
  if (closeIdx === -1) return false;
  const firstArg = (splitTopLevel(text.slice(openIdx + 1, closeIdx))[0] || '').trim();
  if (!firstArg) return false;
  if (/\b(?:tmpdir|mkdtempSync)\s*\(/.test(firstArg)) return true;
  // BUG-744 re-round P(iii): only a DECLARED, verified temp binding exempts —
  // a bare identifier that merely LOOKS temp-named but was never resolved
  // into tempRootVars (e.g. an undeclared function parameter) does not.
  const bare = /^(\w+)$/.exec(firstArg);
  if (bare && tempRootVars.has(bare[1])) return true;
  return false;
}

/** SRC_MARKER match, minus the temp/shadow-rooted-join exemption. */
function looksLikeSrcPath(text, tempRootVars) {
  if (!SRC_MARKER.test(text)) return false;
  if (isTempRootedJoinCall(text, tempRootVars)) return false;
  return true;
}

function isSrcTarget(target, srcVars, tempRootVars) {
  if (!target) return false;
  if (looksLikeSrcPath(target, tempRootVars)) return true;
  const bare = /^(\w+)$/.exec(target);
  if (bare && srcVars.has(bare[1])) return true;
  const prop = /^(\w+)\.(\w+)$/.exec(target);
  if (prop && srcVars.has(`${prop[1]}.${prop[2]}`)) return true;
  return false;
}

/**
 * Returns true if `fileText` (the source of a test file) appears to write
 * into webconsole/src via writeFileSync/writeFile/copyFileSync/renameSync,
 * directly or through a traced alias / object-property holder / inline
 * multi-argument path.join(...)/resolve(...) / template literal.
 *
 * A file that imports webconsole/testsupport/mutant.mjs is exempted outright
 * (trusted to route mutation through the helper's own shadow-copy safety net).
 */
export function fileWritesIntoSrc(fileText) {
  // BUG-744 re-round P(iv): a block comment (`/* ... */`) can contain what
  // LOOKS like a real import — `/*\nimport { x } from '../testsupport/mutant.mjs';\n*/`
  // — with the exemption regex's line-start anchor still matching the
  // (commented-out) import line inside it. Strip block comments FIRST so the
  // exemption can only fire on a genuine, live import statement.
  const withoutBlockComments = fileText.replace(/\/\*[\s\S]*?\*\//g, '');
  if (USES_MUTANT_HELPER.test(withoutBlockComments)) return false;

  const srcVars = new Set();

  // Bare-identifier alias chain: `const a = <src-like RHS>; const b = a;`
  const declPattern = /(?:const|let|var)\s+(\w+)\s*=\s*([^;\n]+)/g;
  const decls = [...fileText.matchAll(declPattern)].filter(([, , rhs]) => !rhs.trim().startsWith('{'));

  // Temp/shadow root identifiers (BUG-744, re-round 3: NAME is never
  // consulted). A name enters tempRootVars IFF its RHS is a genuine temp
  // binding (mkdtempSync(...)/tmpdir()/os.tmpdir()) — unconditionally — OR
  // it derives TRANSITIVELY from an already-verified temp root via
  // `join(tmpRoot, ...)`/`resolve(tmpRoot, ...)` (isTempRootedJoinCall,
  // reusing tempRootVars-so-far). Multi-round for the same reason the
  // srcVars alias chain below is: `const tmpDir = mkdtempSync(...); const
  // tmpOut = join(tmpDir, 'out');` needs tmpDir resolved before tmpOut can
  // be. Anything else — `process.cwd()`, `path.resolve('..')`,
  // `fileURLToPath(new URL('..', import.meta.url))`, a bare `'../'`, `ROOT`,
  // `REPO_ROOT`, `__dirname`-based joins — is NOT a temp root no matter what
  // its declaring variable is named.
  const tempRootVars = new Set();
  for (let round = 0; round < 4; round++) {
    let changed = false;
    for (const [, name, rhsRaw] of decls) {
      if (tempRootVars.has(name)) continue;
      const rhs = rhsRaw.trim();
      const isGenuineTempBinding = /\b(?:tmpdir|mkdtempSync)\s*\(/.test(rhs);
      const derivesFromTempRoot = isTempRootedJoinCall(rhs, tempRootVars);
      if (isGenuineTempBinding || derivesFromTempRoot) {
        tempRootVars.add(name);
        changed = true;
      }
    }
    if (!changed) break;
  }

  // BUG-744 re-round P(i): a declared name is ALWAYS eligible for src-alias
  // tracing — membership in tempRootVars no longer skips it here, so a
  // temp-hinted name that actually turned out (above) to hold a real src
  // path still gets added to srcVars below.
  for (let round = 0; round < 4; round++) {
    let changed = false;
    for (const [, name, rhsRaw] of decls) {
      if (srcVars.has(name)) continue;
      const rhs = rhsRaw.trim();
      const rhsIsDirectSrcPath = looksLikeSrcPath(rhs, tempRootVars);
      const aliasMatch = /^(\w+)$/.exec(rhs);
      const rhsIsAlias = aliasMatch && srcVars.has(aliasMatch[1]);
      if (rhsIsDirectSrcPath || rhsIsAlias) {
        srcVars.add(name);
        changed = true;
      }
    }
    if (!changed) break;
  }

  // Object-property holders: `const paths = { engine: <src-like value>, ... };`
  // recorded as dotted `name.prop` entries in the SAME traced set.
  const objDeclPattern = /(?:const|let|var)\s+(\w+)\s*=\s*\{/g;
  let om;
  while ((om = objDeclPattern.exec(fileText))) {
    const name = om[1];
    const openIdx = om.index + om[0].length - 1;
    const closeIdx = matchBalanced(fileText, openIdx, '{', '}');
    if (closeIdx === -1) continue;
    const body = fileText.slice(openIdx + 1, closeIdx);
    for (const pair of splitTopLevel(body)) {
      const colonIdx = pair.indexOf(':');
      if (colonIdx === -1) continue;
      const key = pair.slice(0, colonIdx).trim().replace(/^['"]|['"]$/g, '');
      const value = pair.slice(colonIdx + 1).trim();
      const valueIsDirectSrcPath = looksLikeSrcPath(value, tempRootVars);
      const aliasMatch = /^(\w+)$/.exec(value);
      const valueIsAlias = aliasMatch && srcVars.has(aliasMatch[1]);
      if (key && (valueIsDirectSrcPath || valueIsAlias)) srcVars.add(`${name}.${key}`);
    }
  }

  // Every write-ish call's target argument, extracted with a balanced-paren
  // scan (not a comma-truncated regex capture) so a multi-argument
  // path.join(...)/resolve(...) call written INLINE as the argument is seen
  // whole, and a template literal containing a comma inside an interpolation
  // isn't chopped either. copyFileSync/cpSync/renameSync all take
  // (source, destination) — the DESTINATION (argument 1) is the one that
  // matters (BUG-744 P2); the others take the path as argument 0.
  const callPattern = /\b(writeFileSync|writeFile|copyFileSync|renameSync|appendFileSync|cpSync|createWriteStream)\s*\(/g;
  const DEST_IS_ARG1 = new Set(['renameSync', 'copyFileSync', 'cpSync']);
  let cm;
  while ((cm = callPattern.exec(fileText))) {
    const openIdx = cm.index + cm[0].length - 1;
    const closeIdx = matchBalanced(fileText, openIdx, '(', ')');
    if (closeIdx === -1) continue;
    const argsText = fileText.slice(openIdx + 1, closeIdx);
    const args = splitTopLevel(argsText);
    const targetIdx = DEST_IS_ARG1.has(cm[1]) ? 1 : 0;
    const target = (args[targetIdx] || '').trim();
    if (isSrcTarget(target, srcVars, tempRootVars)) return true;
  }
  return false;
}
