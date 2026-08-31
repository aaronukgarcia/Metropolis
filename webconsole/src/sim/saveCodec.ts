// saveCodec.ts — FEAT-1972079935: compress large localStorage save payloads.
//
// WHY: city-save JSON is thousands of repetitive building objects (near-identical
// keys/shapes over and over), which is exactly the pattern LZ-style compression
// wins big on — Aaron's dogfood city measured ~5-6x on the largest payloads
// (namedSave 1.77 MB -> ~300 KB). localStorage itself never compresses, so the
// only way to fit a real city under the 5 MB quota is to shrink the string we
// hand to setItem.
//
// CODEC: lz-string's compressToUTF16 / decompressFromUTF16. UTF16 is the correct
// variant here specifically BECAUSE localStorage stores UTF-16 code units —
// compressToUTF16's output is safe to round-trip through setItem/getItem (unlike
// the raw compress() variant, which can emit code points that mangle on some
// storage backends). It's synchronous (unlike CompressionStream, which is async
// and would force every save/load call site into a Promise chain), ~4 KB, and has
// zero transitive dependencies.
//
// BACKWARD-COMPAT (critical): encode() prepends a short magic prefix before the
// compressed blob. decode() checks for the prefix: present -> strip + decompress;
// ABSENT -> return the input completely unchanged. That absent-prefix case is
// every save already sitting in a real browser's localStorage from before this
// change landed — plain, uncompressed JSON. decode() must NEVER throw on it, or
// every existing dogfood save (and preWipeArchive, and savepoint) stops loading
// the moment this ships.
// lz-string is a CJS module (`module.exports = LZString`) whose export shape
// Node's ESM interop cannot statically detect as named exports (no static
// `exports.foo = ...` pattern for cjs-module-lexer to find) — import the
// default and pull the two functions we need off it, which works under both
// Node's ESM loader (test runner) and Vite/tsc's bundler resolution (app).
import LZStringDefault from 'lz-string';
const { compressToUTF16, decompressFromUTF16 } = LZStringDefault as unknown as {
  compressToUTF16: (input: string) => string;
  decompressFromUTF16: (compressed: string) => string;
};

/** Magic prefix marking a value as lz-string-compressed (compressToUTF16 output). */
export const LZ_MAGIC = 'LZv1:';

/**
 * Compress a JSON string for localStorage. Prepends LZ_MAGIC so decode() can
 * tell a compressed value apart from a legacy plain-JSON one.
 *
 * Fail-safe: if compression itself throws (should not happen with lz-string,
 * but never trust a library not to), fall back to storing the plain string
 * unprefixed — decode() will then treat it exactly like a legacy value and
 * return it unchanged, so a compression bug degrades to "uncompressed" rather
 * than "unloadable".
 */
export function encode(json: string): string {
  try {
    const compressed = compressToUTF16(json);
    if (typeof compressed !== 'string' || compressed.length === 0) return json;
    return LZ_MAGIC + compressed;
  } catch {
    return json;
  }
}

/**
 * Decompress a value previously written by encode(). If the LZ_MAGIC prefix is
 * absent, the value predates compression (or a fallback path skipped it) —
 * return it UNCHANGED so legacy saves keep loading. Never throws: a corrupt or
 * unexpected compressed blob degrades to returning the raw stored string, and
 * the caller's own JSON.parse (which already has to tolerate corruption today)
 * is the backstop.
 */
export function decode(stored: string): string {
  if (typeof stored !== 'string' || !stored.startsWith(LZ_MAGIC)) return stored;
  try {
    const payload = stored.slice(LZ_MAGIC.length);
    const decompressed = decompressFromUTF16(payload);
    // lz-string returns null/'' on malformed input rather than throwing.
    if (typeof decompressed !== 'string') return stored;
    return decompressed;
  } catch {
    return stored;
  }
}
