/**
 * tools/plan/errors-mojibake-guard.js — BUG-350: data/*.json UTF-8 mojibake
 * signature gate (GR#7, GR#2).
 *
 * On 2026-08-22, data/errors.json on main was found mojibake-corrupted: 542
 * double-encoded runs across 9 character classes (em dash U+2014, section
 * sign U+00A7, plus-minus U+00B1, right single/double quotes U+2019/U+201D,
 * naira U+20A6, multiplication sign U+00D7, bullet U+2022, bullet operator
 * U+2219). Bev repaired the file in merge fd0c5fe. The root-cause tool
 * (an ad-hoc PowerShell `Get-Content`/`Set-Content` round-trip without
 * `-Encoding utf8`, or an equivalent "decode UTF-8 as ANSI/CP-1252 then
 * re-encode as UTF-8" edit) is NOT fixed and will re-corrupt the next time
 * anyone edits data/errors.json (or any other data/*.json) out-of-band
 * instead of through tools/plan/add-error.js.
 *
 * THE MOJIBAKE SIGNATURE
 *   A UTF-8 file misread as CP-1252 turns every non-ASCII byte B into the
 *   CP-1252 character c(B); re-encoding that character as UTF-8 yields the
 *   2-3 byte sequence below. The leading pair of every such double-encoded
 *   run is one of:
 *       C3 82   <-- UTF-8 for U+00C2 (Â) — original first byte was 0xC2
 *                 (the § and ± classes), 110 occurrences in the incident
 *       C3 A2   <-- UTF-8 for U+00E2 (â) — original first byte was 0xE2
 *                 (the em-dash, quote, bullet and naira classes), 426
 *                 occurrences in the incident
 *       C3 83   <-- UTF-8 for U+00C3 (Ã) — original first byte was 0xC3
 *                 (the × class only), 6 occurrences in the incident
 *   Per the BUG-350 spec this guard FAILS on the two signature pairs C3 82
 *   and C3 A2. C3 83 (the multiplication-sign family) is a third, rarer
 *   signature: extend MOJIBAKE_SIGNATURES with { bytes: [0xC3, 0x83] } if
 *   you want full 542/542 coverage — the two pairs below already catch
 *   536/542 (98.9%) of the historical incident.
 *
 * A legitimate single-encoded é (U+00E9, bytes C3 A9) or ä (U+00E4, bytes
 * C3 A4) is NOT in the signature set and never trips this guard. The guard
 * is byte-level: it never parses JSON, so it also fires on a file that is
 * still parseable JSON but whose string values carry the double-encoded
 * bytes (exactly the incident shape — the corrupted file was valid JSON).
 *
 * Usage:
 *   node tools/plan/errors-mojibake-guard.js [--data-dir <dir>] [--json]
 *                                             [--preview-fix <file>]
 *     (no args)   check every data/*.json under <data-dir>; exit 1 on any
 *                 hit, listing each as <file>:<byteOffset> (0-based).
 *     --data-dir  override the directory to scan (default: <repo>/data).
 *     --json      emit a machine-readable JSON report to stdout instead of
 *                 the human listing.
 *     --preview-fix <file>   print the reverse-mojibake-repaired content of
 *                 <file> to stdout. READ-ONLY: never writes to disk. The
 *                 repair (inverse of the corruption) is reconstructed via
 *                 reverseMojibake() and is what the lead would commit.
 *
 * Exit codes: 0 = clean (or preview printed), 1 = findings (or error).
 *
 * Deliberately simple/testable pure functions + a thin CLI wrapper that owns
 * process.exit, following tools/plan/spec-lint.js's convention. The guard
 * NEVER writes data/*.json — those files are the lead's (GR#24); it reports.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_DATA_DIR = path.resolve(__dirname, '..', '..', 'data');

// { bytes: [b0, b1], label } — the BUG-350 signature pairs.
const MOJIBAKE_SIGNATURES = [
  {
    bytes: [0xc3, 0x82],
    label: 'C3 82 = double-encoded U+00C2 (Â) — original UTF-8 first byte 0xC2 (e.g. § U+00A7, ± U+00B1)',
  },
  {
    bytes: [0xc3, 0xa2],
    label: 'C3 A2 = double-encoded U+00E2 (â) — original UTF-8 first byte 0xE2 (e.g. — U+2014, " U+2019, " U+201D, • U+2022, ₦ U+20A6)',
  },
];

// CP-1252 bytes 0x80–0x9F → Unicode (the characters an ANSI misread
// produces). 0x81/0x8D/0x8F/0x90/0x9D are undefined in CP-1252.
const CP1252_SPECIAL = new Map([
  [0x80, 0x20ac], [0x82, 0x201a], [0x83, 0x0192], [0x84, 0x201e],
  [0x85, 0x2026], [0x86, 0x2020], [0x87, 0x2021], [0x88, 0x02c6],
  [0x89, 0x2030], [0x8a, 0x0160], [0x8b, 0x2039], [0x8c, 0x0152],
  [0x8e, 0x017d], [0x91, 0x2018], [0x92, 0x2019], [0x93, 0x201c],
  [0x94, 0x201d], [0x95, 0x2022], [0x96, 0x2013], [0x97, 0x2014],
  [0x98, 0x02dc], [0x99, 0x2122], [0x9a, 0x0161], [0x9b, 0x203a],
  [0x9c, 0x0153], [0x9e, 0x017e], [0x9f, 0x0178],
]);
const UNICODE_TO_CP1252 = new Map();
for (const [byte, cp] of CP1252_SPECIAL) UNICODE_TO_CP1252.set(cp, byte);

// Map a Unicode code point to its CP-1252 byte, or null if unrepresentable.
function cp1252Byte(cp) {
  if (UNICODE_TO_CP1252.has(cp)) return UNICODE_TO_CP1252.get(cp);
  if (cp >= 0x00 && cp <= 0x7f) return cp;          // ASCII is identity
  if (cp >= 0xa0 && cp <= 0xff) return cp;           // Latin-1 supplement is identity in CP-1252
  return null;
}

/**
 * Scan a Buffer for the mojibake signature pairs. Pure — never touches disk.
 * Returns [{ offset, bytes, label }] where offset is the 0-based byte index
 * of the pair's first byte.
 */
function scanBuffer(buf) {
  const hits = [];
  if (!Buffer.isBuffer(buf)) buf = Buffer.from(buf);
  for (let i = 0; i + 1 < buf.length; i++) {
    for (const sig of MOJIBAKE_SIGNATURES) {
      if (buf[i] === sig.bytes[0] && buf[i + 1] === sig.bytes[1]) {
        hits.push({
          offset: i,
          bytes: `${sig.bytes[0].toString(16).toUpperCase().padStart(2, '0')} ` +
                 `${sig.bytes[1].toString(16).toUpperCase().padStart(2, '0')}`,
          label: sig.label,
        });
      }
    }
  }
  return hits;
}

/**
 * Scan every data/*.json under dataDir. Returns
 * [{ file, hits: [...scanBuffer results] }] — files with zero hits still
 * appear with hits: [] so callers can report filesChecked.
 */
function scanDataDir(dataDir) {
  const entries = fs.readdirSync(dataDir).filter((f) => f.endsWith('.json'));
  return entries.map((f) => {
    const file = path.join(dataDir, f);
    const hits = scanBuffer(fs.readFileSync(file));
    return { file, hits };
  });
}

/**
 * Reverse the BUG-350 mojibake: take the double-encoded bytes of a file,
 * decode them as the UTF-8 mojibake text they actually are, then re-encode
 * each character as CP-1252 to recover the ORIGINAL pre-corruption UTF-8
 * bytes. Pure — never touches disk.
 *
 * Throws if the buffer contains a character that is not representable in
 * CP-1252 (i.e. the buffer is not the ANSI-misread shape). For the incident
 * classes (Â/Ã/â + €/" /™/†/—/ˆ + §/±/¦) every character round-trips.
 */
function reverseMojibake(buf) {
  if (!Buffer.isBuffer(buf)) buf = Buffer.from(buf);
  const text = buf.toString('utf8'); // mojibake string, e.g. "â€"" (em dash)
  const bytes = [];
  for (const ch of text) {
    const cp = ch.codePointAt(0);
    const b = cp1252Byte(cp);
    if (b === null) {
      throw new Error(
        `cannot reverse mojibake: U+${cp.toString(16).toUpperCase()} is not representable in CP-1252`
      );
    }
    bytes.push(b);
  }
  return Buffer.from(bytes);
}

/**
 * Run the guard over a data dir. Pure orchestration — the CLI owns
 * process.exit. Returns { findings, totalHits, filesChecked, scannedDir }.
 */
function runCheck({ dataDir = DEFAULT_DATA_DIR } = {}) {
  const results = scanDataDir(dataDir);
  const findings = results.filter((r) => r.hits.length > 0);
  const totalHits = findings.reduce((sum, r) => sum + r.hits.length, 0);
  return {
    findings,
    totalHits,
    filesChecked: results.length,
    scannedDir: dataDir,
  };
}

// ---------------------------------------------------------------------
// CLI wrapper
// ---------------------------------------------------------------------

function usage() {
  return (
    'usage:\n' +
    '  node tools/plan/errors-mojibake-guard.js [--data-dir <dir>] [--json]\n' +
    '                                         [--preview-fix <file>]\n' +
    '  (no args = check data/*.json under the repo data dir; exit 1 on any hit)\n' +
    '  --data-dir <dir>   scan <dir> instead of the default repo data dir\n' +
    '  --json             emit a JSON report to stdout\n' +
    '  --preview-fix <file>  print the reverse-mojibake-repaired content of\n' +
    '                     <file> to stdout (READ-ONLY — never writes)\n'
  );
}

function main(argv) {
  const flags = {};
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--data-dir') {
      flags.dataDir = argv[i + 1];
      i += 1;
    } else if (arg === '--json') {
      flags.json = true;
    } else if (arg === '--preview-fix') {
      flags.previewFix = argv[i + 1];
      i += 1;
    } else if (arg === '--help' || arg === '-h') {
      process.stdout.write(usage());
      return 0;
    } else {
      positional.push(arg);
    }
  }
  if (positional.length > 0) {
    console.error(usage());
    return 2;
  }

  try {
    if (flags.previewFix) {
      const buf = fs.readFileSync(flags.previewFix);
      const repaired = reverseMojibake(buf);
      process.stdout.write(repaired);
      return 0;
    }

    const dataDir = flags.dataDir || DEFAULT_DATA_DIR;
    const report = runCheck({ dataDir });

    if (flags.json) {
      process.stdout.write(JSON.stringify(report, null, 2) + '\n');
    } else if (report.totalHits === 0) {
      console.log(
        `OK: ${report.filesChecked} data/*.json file(s) scanned under ${dataDir} — ` +
          'no C3 82 / C3 A2 mojibake signature found.'
      );
    } else {
      console.error(
        `FAIL: ${report.totalHits} mojibake signature hit(s) across ` +
          `${report.findings.length} file(s) under ${dataDir}:`
      );
      for (const f of report.findings) {
        for (const h of f.hits) {
          const rel = path.relative(process.cwd(), f.file) || f.file;
          console.error(`  ${rel}:${h.offset}  ${h.bytes}`);
        }
      }
      console.error(
        '  -> data/*.json are the lead\'s files (GR#24). Do NOT hand-edit; report to the lead.\n' +
        '     Repair preview for one file: node tools/plan/errors-mojibake-guard.js --preview-fix <file>'
      );
    }
    return report.totalHits > 0 ? 1 : 0;
  } catch (err) {
    console.error(`ERROR: ${err.message}`);
    return 1;
  }
}

module.exports = {
  scanBuffer,
  scanDataDir,
  reverseMojibake,
  runCheck,
  cp1252Byte,
  MOJIBAKE_SIGNATURES,
  DEFAULT_DATA_DIR,
  main,
};

if (require.main === module) {
  process.exit(main(process.argv.slice(2)));
}
