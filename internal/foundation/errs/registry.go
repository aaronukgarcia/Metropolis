package errs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// registryEntry is one entry under the "codes" key of data/errors.json.
type registryEntry struct {
	Severity string `json:"severity"`
	Module   string `json:"module"`
	Message  string `json:"message"`
	Remedy   string `json:"remedy"`
}

// codeFormat is the MET-<layer><NNN> format documented in data/errors.json:
// a single uppercase layer letter followed by three or four digits. The
// format was widened from three digits on 2026-08-14 (BUG-234): the
// three-digit namespace was exhausted and minting a fresh layer letter
// per module was the wrong answer, so four-digit codes are now accepted
// while every existing three-digit code stays valid unchanged.
var codeFormat = regexp.MustCompile(`^MET-[A-Z]\d{3,4}$`)

// tokenFormat is the only template-token shape renderTemplate (errs.go) can
// substitute: a plain identifier (letters, digits, underscore), looked up in
// the error's ctx (plus the always-resolvable "code"/"correlationId"). A
// token with anything else — a Go-style format verb ({x!q}), prose wrapped in
// braces ({finding, reason}), or an empty brace pair ({}) — is left as a
// visible literal, so it renders verbatim to users. BUG-357 step 2: the
// registry now rejects malformed tokens at load, so a code that will render
// "{token!q}" to a user is a load failure, not a silent cosmetic defect.
var tokenFormat = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// malformedTemplateToken returns the first `{...}` token in tmpl that is not a
// plain identifier, or "" when every token is well-formed. Mirrors
// renderTemplate's brace scan exactly (a '{' without a closing '}' is not a
// token and is left alone). An empty token is reported as "{}" — the empty
// string must stay the unambiguous "no malformed token" sentinel.
func malformedTemplateToken(tmpl string) string {
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			continue
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 {
			continue // no closing brace — renderTemplate leaves it literal too
		}
		token := tmpl[i+1 : i+j]
		if token == "" {
			return "{}"
		}
		if !tokenFormat.MatchString(token) {
			return token
		}
		i += j
	}
	return ""
}

var (
	regOnce    sync.Once
	regData    map[string]registryEntry
	regLoadErr error
)

// registryFailureKind classifies WHY the registry could not yield a usable
// code map, so construct() (errs.go) can map each mode to its distinct fatal
// registry code (BUG-279). Before this classification every registry failure
// collapsed to the single "unregistered code" fallback (MET-F003, severity
// "error"), leaving MET-F001/MET-F002 defined-but-unreachable and silently
// downgrading a whole-registry outage to a single-typo-level error.
type registryFailureKind int

const (
	// registryLoadFailed → MET-F001: the registry could not be loaded at all
	// (path unresolved, file unreadable, or bytes unparseable as JSON) — there
	// is no registry to speak of.
	registryLoadFailed registryFailureKind = iota
	// registryValidationFailed → MET-F002: the registry loaded and parsed, but
	// an entry violates the schema (duplicate code, bad code format, missing
	// required field, or a malformed template token) — the file is present but
	// not trustworthy.
	registryValidationFailed
)

// registryError wraps a doLoadRegistry failure with its classification and the
// resolved registry path (empty when resolution itself failed). construct()
// uses the kind to choose MET-F001 vs MET-F002 and the path/cause to fill their
// {path}/{cause} message templates. It Unwraps to the underlying cause so
// errors.Is/As against the wrapped error keep working.
type registryError struct {
	kind registryFailureKind
	path string
	err  error
}

func (e *registryError) Error() string {
	if e.err == nil {
		return "registry failure (no underlying cause)"
	}
	return e.err.Error()
}
func (e *registryError) Unwrap() error { return e.err }

// registryPathEnv is the override environment variable documented in the
// package doc and in data/errors.json's resolution order.
const registryPathEnv = "METROPOLIS_ERRORS_PATH"

// relRegistryPath is the registry file location relative to the repo root.
const relRegistryPath = "data/errors.json"

// loadRegistry loads and validates data/errors.json exactly once per
// process (subsequent calls return the cached result or cached error).
//
// Resolution order:
//  1. $METROPOLIS_ERRORS_PATH, if set — used verbatim, no further search.
//  2. Walking upward from the running executable's directory, looking for
//     data/errors.json at each level.
//  3. Walking upward from the current working directory, looking for
//     data/errors.json at each level (this is what makes `go test` work
//     from any package directory: the search walks up until it finds the
//     repo root, or gives up at the filesystem root).
//
// Validation performed on load: every code matches MET-<layer><NNN>,
// every code is unique (JSON's own object-key collapsing would otherwise
// silently hide a duplicate — see decodeCodes), every entry has all
// four required fields populated, and every message/remedy template token is
// a plain identifier (BUG-357 step 2 — malformed tokens render literally).
func loadRegistry() (map[string]registryEntry, error) {
	regOnce.Do(func() {
		regData, regLoadErr = doLoadRegistry()
	})
	return regData, regLoadErr
}

// resetRegistryForTest clears the sync.Once-cached registry state so
// tests can exercise different METROPOLIS_ERRORS_PATH values / registry
// contents in isolation. Test-only; never called from production code.
func resetRegistryForTest() {
	regOnce = sync.Once{}
	regData = nil
	regLoadErr = nil
}

func doLoadRegistry() (map[string]registryEntry, error) {
	// BUG-279: each failure is tagged registryLoadFailed (→ MET-F001) or
	// registryValidationFailed (→ MET-F002). The dividing line is "could we
	// turn the bytes into a code map at all?" — path/read/parse are LOAD
	// failures (no usable registry); schema checks over a parsed map are
	// VALIDATION failures (a present-but-untrustworthy registry).
	path, err := resolveRegistryPath()
	if err != nil {
		return nil, &registryError{kind: registryLoadFailed, path: "", err: fmt.Errorf("resolve registry path: %w", err)}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &registryError{kind: registryLoadFailed, path: path, err: fmt.Errorf("read %s: %w", path, err)}
	}

	codes, dupes, err := decodeCodes(data)
	if err != nil {
		return nil, &registryError{kind: registryLoadFailed, path: path, err: fmt.Errorf("parse %s: %w", path, err)}
	}
	if len(dupes) > 0 {
		return nil, &registryError{kind: registryValidationFailed, path: path, err: fmt.Errorf("duplicate error codes in %s: %s", path, strings.Join(dupes, ", "))}
	}

	for code, entry := range codes {
		if !codeFormat.MatchString(code) {
			return nil, &registryError{kind: registryValidationFailed, path: path, err: fmt.Errorf("invalid code format %q in %s (want MET-<layer><NNN>, three or four digits)", code, path)}
		}
		if entry.Severity == "" || entry.Module == "" || entry.Message == "" || entry.Remedy == "" {
			return nil, &registryError{kind: registryValidationFailed, path: path, err: fmt.Errorf("code %q in %s is missing a required field (severity/module/message/remedy)", code, path)}
		}
		// BUG-357 step 2: reject template tokens that renderTemplate can never
		// substitute. A malformed token ({x!q}, prose-in-braces, {}) is a
		// literal that reaches the user verbatim — a load-time failure beats a
		// cosmetic defect that ships. Both message and remedy are scanned;
		// remedy prose occasionally borrows brace syntax descriptively (e.g.
		// Go composite literals), and those sites are reworded in the data.
		for _, field := range []struct{ name, text string }{
			{"message", entry.Message},
			{"remedy", entry.Remedy},
		} {
			if bad := malformedTemplateToken(field.text); bad != "" {
				return nil, &registryError{kind: registryValidationFailed, path: path, err: fmt.Errorf("code %q in %s has a malformed template token %q in %s — tokens must be plain identifiers (no format verbs, no brace-wrapped prose, no empty {})", code, path, bad, field.name)}
			}
		}
	}

	return codes, nil
}

func resolveRegistryPath() (string, error) {
	if p := os.Getenv(registryPathEnv); p != "" {
		return p, nil
	}

	if exe, err := os.Executable(); err == nil {
		if p, ok := findUpward(filepath.Dir(exe), relRegistryPath); ok {
			return p, nil
		}
	}

	if wd, err := os.Getwd(); err == nil {
		if p, ok := findUpward(wd, relRegistryPath); ok {
			return p, nil
		}
	}

	return "", fmt.Errorf("%s not found via %s, executable directory, or working-directory search", relRegistryPath, registryPathEnv)
}

// findUpward looks for rel joined onto start, then each successive parent
// directory, until found or the filesystem root is reached.
func findUpward(start, rel string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// decodeCodes parses the top-level "codes" object of an errors.json file
// using a streaming token decoder rather than a plain map unmarshal, so
// that a duplicate key (which encoding/json would otherwise silently
// resolve to "last value wins") is instead reported as a validation
// failure. Returns the resolved code map and any keys seen more than once.
func decodeCodes(data []byte) (map[string]registryEntry, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	if _, err := expectDelim(dec, '{'); err != nil {
		return nil, nil, err
	}

	var codesRaw json.RawMessage
	found := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected non-string key token %v", keyTok)
		}
		if key == "codes" {
			if err := dec.Decode(&codesRaw); err != nil {
				return nil, nil, fmt.Errorf("decode codes value: %w", err)
			}
			found = true
			continue
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, nil, fmt.Errorf("skip %q value: %w", key, err)
		}
	}
	if !found {
		return nil, nil, fmt.Errorf(`top-level "codes" object not found`)
	}

	dec2 := json.NewDecoder(bytes.NewReader(codesRaw))
	if _, err := expectDelim(dec2, '{'); err != nil {
		return nil, nil, fmt.Errorf("codes value is not an object: %w", err)
	}

	seen := map[string]bool{}
	var dupes []string
	codes := map[string]registryEntry{}
	for dec2.More() {
		keyTok, err := dec2.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected non-string code key token %v", keyTok)
		}
		var entry registryEntry
		if err := dec2.Decode(&entry); err != nil {
			return nil, nil, fmt.Errorf("decode entry %q: %w", key, err)
		}
		if seen[key] {
			dupes = append(dupes, key)
		}
		seen[key] = true
		codes[key] = entry
	}

	return codes, dupes, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) (json.Delim, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != want {
		return 0, fmt.Errorf("expected delimiter %q, got %v", want, tok)
	}
	return d, nil
}
