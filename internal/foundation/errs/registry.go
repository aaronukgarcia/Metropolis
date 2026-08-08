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
// a single uppercase layer letter followed by three digits.
var codeFormat = regexp.MustCompile(`^MET-[A-Z]\d{3}$`)

var (
	regOnce    sync.Once
	regData    map[string]registryEntry
	regLoadErr error
)

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
// silently hide a duplicate — see decodeCodes), and every entry has all
// four required fields populated.
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
	path, err := resolveRegistryPath()
	if err != nil {
		return nil, fmt.Errorf("resolve registry path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	codes, dupes, err := decodeCodes(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(dupes) > 0 {
		return nil, fmt.Errorf("duplicate error codes in %s: %s", path, strings.Join(dupes, ", "))
	}

	for code, entry := range codes {
		if !codeFormat.MatchString(code) {
			return nil, fmt.Errorf("invalid code format %q in %s (want MET-<layer><NNN>)", code, path)
		}
		if entry.Severity == "" || entry.Module == "" || entry.Message == "" || entry.Remedy == "" {
			return nil, fmt.Errorf("code %q in %s is missing a required field (severity/module/message/remedy)", code, path)
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
