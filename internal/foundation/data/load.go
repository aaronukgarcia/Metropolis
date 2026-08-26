package data

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Filenames for the §24 config set (relative to the resolved data
// directory — see ResolveDataDir).
const (
	FileConsumption   = "consumption.json"
	FileModes         = "modes.json"
	FileBuildings     = "buildings.json"
	FileUnlockTrees   = "unlock_trees.json"
	FileNamingCorpus  = "naming_corpus.json"
	FileSeasonal      = "seasonal.json"
	FileExternalWorld = "external_world.json"
	FilePolicies      = "policies.json"
)

// Load reads, JSON-decodes, and schema-validates one config file at
// path into a freshly zero-valued T, via T's pointer-receiver
// Validator implementation (PT). It is the shared implementation
// behind every per-file loader (LoadConsumption, LoadModes, ...) and
// behind Store's reload path.
//
// Every failure returns a registry-sourced *errs.E (never a panic):
//   - the file does not exist / cannot be read: CodeFileNotFound
//   - the file is not well-formed JSON (syntax error): CodeMalformedJSON
//   - a decoded field has the wrong JSON type: CodeSchemaInvalid, field
//     named from the underlying json.UnmarshalTypeError
//   - the decoded struct's "version" field is missing/zero: CodeMissingVersion
//   - any other Validate() failure: CodeSchemaInvalid, field+rule named
func Load[T any, PT interface {
	*T
	Validator
}](path, correlationID string) (T, error) {
	var zero T

	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(CodeFileNotFound, correlationID, err, map[string]any{
			"path": path,
		})
	}

	// BUG-060 round 2: json.Unmarshal now runs FIRST, before the
	// duplicate-key walk below. Round 1 had the walk run first and return
	// as soon as it found a duplicate, WITHOUT continuing to scan the
	// rest of the document -- so a file with both a genuine duplicate key
	// (earlier in the byte stream) and a later syntax error (e.g. a
	// trailing comma) reported CodeDuplicateKey and masked the file's
	// real CodeMalformedJSON syntax breakage until a second run, after
	// the duplicate was fixed. Unmarshal-first fixes that: any genuine
	// syntax error anywhere in the document fails here and is reported as
	// CodeMalformedJSON immediately, before the duplicate-key walk ever
	// runs. A document with a duplicate key but otherwise-valid JSON
	// syntax still unmarshals successfully here (encoding/json's
	// last-write-wins map behaviour), so the walk below still catches it
	// -- round 1's whole point is preserved.
	//
	// BUG-281 r2: strip-then-strict. The r1 design retried WITHOUT
	// DisallowUnknownFields whenever the FIRST unknown field looked like
	// allowlisted metadata, which turned protection off for the entire
	// document (a "$comment" anywhere before a typo let the typo ride in
	// silently), was order-dependent, and keyed off a name-only,
	// depth-blind allowlist parsed out of an error MESSAGE. The r2 shape
	// never decodes without DisallowUnknownFields:
	//
	//   1. json.Unmarshal the raw bytes into a shadow
	//      map[string]json.RawMessage. Unmarshal (unlike Decoder.Decode)
	//      rejects trailing data after the top-level value, so this step
	//      also restores the CodeMalformedJSON trailing-garbage rejection
	//      the r1 Decoder switch had silently lost.
	//   2. Strip the allowlisted metadata keys from the shadow map:
	//      "$"-prefixed keys at the TOP LEVEL only ("$comment",
	//      "$schema_note" are what the real data/ files use) — and only
	//      those T does NOT itself declare, so a schema that captures its
	//      file's $comment (ExternalWorld, NamingCorpus, UnlockTrees do)
	//      still receives it. Nested metadata is NOT stripped — real
	//      files' nested comment/note/description keys are declared
	//      struct fields, so a nested unknown is always a genuine typo
	//      and always rejects.
	//   3. Re-marshal the stripped map (json.Marshal sorts map keys, so
	//      the result is deterministic) and strict-decode THAT with
	//      DisallowUnknownFields. Every non-metadata unknown field now
	//      errors deterministically regardless of where it sits relative
	//      to any metadata key.
	//
	// extractUnknownFieldName is now used ONLY to name the offending
	// field in the returned error context — never as a security decision.
	var v T
	var shadow map[string]json.RawMessage
	if err := json.Unmarshal(b, &shadow); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			// The document is valid JSON but its top-level value is not an
			// object (every schema T here is a struct, so this can never
			// validate). Report it as the schema mismatch it is.
			return zero, errs.Wrap(CodeSchemaInvalid, correlationID, err, map[string]any{
				"path":  path,
				"field": "(root)",
				"rule":  "type mismatch, want object",
			})
		}
		// Syntax error anywhere in the document — including trailing data
		// after the top-level value ("invalid character ... after
		// top-level value"), which json.Unmarshal rejects and a bare
		// json.Decoder.Decode would not.
		//
		// MET-F602's registered template has a "{cause}" placeholder
		// (BUG-099, shared with MET-E600) — populate it from the JSON
		// decode error's own text so the rendered message actually names
		// the syntax failure instead of leaving the literal "{cause}" in
		// the operator/log-visible text.
		return zero, errs.Wrap(CodeMalformedJSON, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	declared := declaredTopLevelJSONNames(reflect.TypeOf(v))
	for key := range shadow {
		if strings.HasPrefix(key, "$") && !declared[key] {
			delete(shadow, key)
		}
	}
	stripped, err := json.Marshal(shadow)
	if err != nil {
		// Can only happen if the shadow map holds invalid RawMessage,
		// which Unmarshal above has already excluded; fail closed anyway.
		return zero, errs.Wrap(CodeMalformedJSON, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(stripped))
	dec.DisallowUnknownFields()
	if decodeErr := dec.Decode(&v); decodeErr != nil {
		if strings.Contains(decodeErr.Error(), "unknown field") {
			return zero, errs.Wrap(CodeUnknownField, correlationID, decodeErr, map[string]any{
				"path":  path,
				"field": extractUnknownFieldName(decodeErr.Error()),
			})
		}
		var ute *json.UnmarshalTypeError
		if errors.As(decodeErr, &ute) {
			field := ute.Field
			if field == "" {
				field = "(root)"
			}
			return zero, errs.Wrap(CodeSchemaInvalid, correlationID, decodeErr, map[string]any{
				"path":  path,
				"field": field,
				"rule":  "type mismatch, want " + ute.Type.String(),
			})
		}
		return zero, errs.Wrap(CodeMalformedJSON, correlationID, decodeErr, map[string]any{
			"path":  path,
			"cause": decodeErr.Error(),
		})
	}

	// Unmarshal above has already proven b is syntactically valid JSON,
	// so any duplicate key found by walking the raw token stream here
	// (at any nesting depth -- e.g. a duplicate curve name inside
	// seasonal.json's "curves" object) is a genuine semantic problem, not
	// a symptom of a syntax error elsewhere in the file. A walkErr here
	// would mean the walker disagrees with encoding/json about what's
	// valid JSON, which shouldn't happen post-Unmarshal-success; treat it
	// as "no verdict" and fall through to Validate rather than raising a
	// confusing secondary error.
	if dupPath, found, walkErr := findDuplicateKey(b); walkErr == nil && found {
		return zero, errs.Wrap(CodeDuplicateKey, correlationID, errors.New("duplicate key: "+dupPath), map[string]any{
			"path":  path,
			"field": dupPath,
		})
	}

	pv := PT(&v)
	if err := pv.Validate(); err != nil {
		code := CodeSchemaInvalid
		ctx := map[string]any{"path": path}
		var fe *FieldError
		if errors.As(err, &fe) {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
			if fe.Field == "version" {
				code = CodeMissingVersion
			}
		}
		return zero, errs.Wrap(code, correlationID, err, ctx)
	}

	return v, nil
}

// LoadConsumption loads and validates consumption.json from dir.
func LoadConsumption(dir, correlationID string) (Consumption, error) {
	return Load[Consumption, *Consumption](filepath.Join(dir, FileConsumption), correlationID)
}

// LoadModes loads and validates modes.json from dir.
func LoadModes(dir, correlationID string) (Modes, error) {
	return Load[Modes, *Modes](filepath.Join(dir, FileModes), correlationID)
}

// LoadBuildings loads and schema-validates buildings.json from dir. It
// does not cross-check consumptionRef against consumption.json — use
// [LoadBuildingsCatalogue] when that check is wanted (LoadAll performs
// it itself, see below).
func LoadBuildings(dir, correlationID string) (Buildings, error) {
	return Load[Buildings, *Buildings](filepath.Join(dir, FileBuildings), correlationID)
}

// LoadBuildingsCatalogue loads both buildings.json and consumption.json
// from dir and cross-checks every buildings.json entry's consumptionRef
// against consumption.json's Classes map (FEAT-010/data.catalogue
// AC-12), returning a registry-sourced MET-F607 error naming the
// offending entry and field if any reference is dangling. This is the
// canonical way for a caller that only wants the catalogue (rather than
// the full LoadAll aggregate) to get the same cross-file guarantee
// LoadAll provides.
func LoadBuildingsCatalogue(dir, correlationID string) (Buildings, error) {
	b, err := LoadBuildings(dir, correlationID)
	if err != nil {
		return Buildings{}, err
	}
	c, err := LoadConsumption(dir, correlationID)
	if err != nil {
		return Buildings{}, err
	}
	if verr := ValidateConsumptionRefs(&b, &c); verr != nil {
		var fe *FieldError
		ctx := map[string]any{"path": filepath.Join(dir, FileBuildings)}
		if ok := asFieldError(verr, &fe); ok {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
		}
		return Buildings{}, errs.Wrap(CodeBuildingDanglingConsumptionRef, correlationID, verr, ctx)
	}
	return b, nil
}

// asFieldError is a tiny errors.As wrapper kept local to this file so
// load.go's imports stay unchanged (errors.As is already imported here).
func asFieldError(err error, target **FieldError) bool {
	return errors.As(err, target)
}

// LoadUnlockTrees loads and validates unlock_trees.json from dir.
func LoadUnlockTrees(dir, correlationID string) (UnlockTrees, error) {
	return Load[UnlockTrees, *UnlockTrees](filepath.Join(dir, FileUnlockTrees), correlationID)
}

// LoadNamingCorpus loads and validates naming_corpus.json from dir.
func LoadNamingCorpus(dir, correlationID string) (NamingCorpus, error) {
	return Load[NamingCorpus, *NamingCorpus](filepath.Join(dir, FileNamingCorpus), correlationID)
}

// LoadSeasonal loads and validates seasonal.json from dir.
func LoadSeasonal(dir, correlationID string) (Seasonal, error) {
	return Load[Seasonal, *Seasonal](filepath.Join(dir, FileSeasonal), correlationID)
}

// LoadExternalWorld loads and validates external_world.json from dir.
func LoadExternalWorld(dir, correlationID string) (ExternalWorld, error) {
	return Load[ExternalWorld, *ExternalWorld](filepath.Join(dir, FileExternalWorld), correlationID)
}

// LoadPolicies loads and validates policies.json from dir.
func LoadPolicies(dir, correlationID string) (Policies, error) {
	return Load[Policies, *Policies](filepath.Join(dir, FilePolicies), correlationID)
}

// LoadMarketFile loads and validates market.json from dir (MOD-020
// ruling 1 — see market.go's package-level doc comment). Not part of
// the eight-file §24 set LoadAll aggregates; engine.market.Load calls
// this directly.
func LoadMarketFile(dir, correlationID string) (MarketFile, error) {
	return Load[MarketFile, *MarketFile](filepath.Join(dir, FileMarket), correlationID)
}

// LoadLogisticsFile loads and validates logistics.json from dir
// (engine.logistics, MOD-025 — see logistics.go's package-level doc
// comment). Not part of the eight-file §24 set LoadAll aggregates;
// engine.logistics.Load calls this directly, matching engine.market/
// engine.season's precedent for a module-owned loader built on this
// shared generic Load.
func LoadLogisticsFile(dir, correlationID string) (LogisticsFile, error) {
	return Load[LogisticsFile, *LogisticsFile](filepath.Join(dir, FileLogistics), correlationID)
}

// LoadPacing loads and validates pacing.json from dir (FEAT-030 — see
// pacing.go's package-level doc comment). Not part of the eight-file
// §24 set LoadAll aggregates; engine.core's own Load calls this
// directly, matching engine.market/engine.season's precedent for a
// module-owned loader built on this shared generic Load.
func LoadPacing(dir, correlationID string) (Pacing, error) {
	return Load[Pacing, *Pacing](filepath.Join(dir, FilePacing), correlationID)
}

// Config aggregates all eight §24 files loaded by LoadAll. errors.json
// is deliberately excluded (see the package doc and
// foundation.data.md's Out of scope section) — foundation.errors owns
// that loader independently.
type Config struct {
	Consumption   Consumption
	Modes         Modes
	Buildings     Buildings
	UnlockTrees   UnlockTrees
	NamingCorpus  NamingCorpus
	Seasonal      Seasonal
	ExternalWorld ExternalWorld
	Policies      Policies
}

// LoadAll loads and validates all eight §24 files from dir into one
// aggregate Config, for callers (like engine.core's boot sequence)
// that want everything up front. It fails on the first error
// encountered (in the order above), returning that file's
// registry-sourced error unchanged.
func LoadAll(dir, correlationID string) (*Config, error) {
	var c Config
	var err error

	if c.Consumption, err = LoadConsumption(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Modes, err = LoadModes(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Buildings, err = LoadBuildings(dir, correlationID); err != nil {
		return nil, err
	}
	if verr := ValidateConsumptionRefs(&c.Buildings, &c.Consumption); verr != nil {
		var fe *FieldError
		ctx := map[string]any{"path": filepath.Join(dir, FileBuildings)}
		if ok := asFieldError(verr, &fe); ok {
			ctx["field"] = fe.Field
			ctx["rule"] = fe.Rule
		}
		return nil, errs.Wrap(CodeBuildingDanglingConsumptionRef, correlationID, verr, ctx)
	}
	if c.UnlockTrees, err = LoadUnlockTrees(dir, correlationID); err != nil {
		return nil, err
	}
	if c.NamingCorpus, err = LoadNamingCorpus(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Seasonal, err = LoadSeasonal(dir, correlationID); err != nil {
		return nil, err
	}
	if c.ExternalWorld, err = LoadExternalWorld(dir, correlationID); err != nil {
		return nil, err
	}
	if c.Policies, err = LoadPolicies(dir, correlationID); err != nil {
		return nil, err
	}

	return &c, nil
}

// findDuplicateKey walks b's raw JSON token stream (ahead of, and
// independently from, json.Unmarshal) and reports the dotted field path
// of the first object key that occurs twice within the same object,
// anywhere in the document -- BUG-060. The comparison is
// case-insensitive via strings.EqualFold (SEC-056), the same Unicode
// simple fold encoding/json uses to match struct field names, so
// "categories"/"Categories" (and "entries"/"entrieſ") are the same field
// and would silently last-write-wins if this walk compared them
// byte-for-byte. Unmarshaling into a Go map or struct has already thrown
// this information away by the time Validate runs (last occurrence
// silently wins), so this check has to happen on the raw bytes, before
// that collapse occurs.
//
// Known over-approximation (SEC-056, P3, left as-is): the walker folds
// EVERY object key, but encoding/json only case-folds struct FIELD names
// -- map keys and json.RawMessage content are byte-exact. This walker is
// schema-agnostic (it sees only the raw token stream, never the
// destination Go type), so it cannot tell a struct field from a map key
// without a fragile reflection-driven special case; folding everything
// errs on the safe side (rejects a valid-but-case-variant map rather than
// accepting an invalid case-variant struct field).
//
// Returns ("", false, nil) when no duplicate is found. A non-nil error
// means the token walk itself failed (e.g. genuinely malformed JSON);
// callers should treat that as "no verdict" and let json.Unmarshal's own
// CodeMalformedJSON path report the real syntax error, since this
// walker's error messages aren't the registry-sourced ones users expect.
func findDuplicateKey(b []byte) (path string, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return walkForDuplicateKey(dec, "")
}

// walkForDuplicateKey consumes exactly one JSON value from dec (dec must
// be positioned immediately before that value's first token) and
// recurses into objects/arrays looking for a duplicate key. path is the
// dotted/bracketed field path accumulated so far, used to name the
// duplicate in the returned error context.
func walkForDuplicateKey(dec *json.Decoder, path string) (string, bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", false, err
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		// A scalar (string/number/bool/null): nothing further to walk.
		return "", false, nil
	}

	switch delim {
	case '{':
		var seen []string
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return "", false, err
			}
			key, _ := keyTok.(string)

			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			// SEC-056: encoding/json matches struct field names
			// case-insensitively (via its internal foldName /
			// equalFoldRight), so an object carrying both "categories" and
			// "Categories" decodes into the SAME field and silently
			// last-write-wins with no error -- defeating the BUG-060
			// duplicate-key guard this walk provides.
			//
			// Compare pairwise with strings.EqualFold, which implements the
			// SAME Unicode simple case folding encoding/json's field matcher
			// uses: it folds ſ (U+017F long s) ↔ s/S and K (U+212A kelvin
			// sign) ↔ k/K, plus every ASCII case pair. A strings.ToLower map
			// does NOT -- ToLower maps uppercase→lowercase only, so the
			// already-lowercase long-s "ſ" is left untouched and a key like
			// "entrieſ" reads as distinct from "entries" even though
			// encoding/json folds both to the same struct field and silently
			// last-write-wins. The prior-key slice is tiny (a handful of keys
			// per object), so the pairwise scan's cost is negligible.
			for _, seenKey := range seen {
				if strings.EqualFold(key, seenKey) {
					return childPath, true, nil
				}
			}
			seen = append(seen, key)

			dupPath, found, err := walkForDuplicateKey(dec, childPath)
			if err != nil {
				return "", false, err
			}
			if found {
				return dupPath, true, nil
			}
		}
		if _, err := dec.Token(); err != nil { // consume closing '}'
			return "", false, err
		}
		return "", false, nil

	case '[':
		i := 0
		for dec.More() {
			childPath := path + "[" + itoa(i) + "]"
			dupPath, found, err := walkForDuplicateKey(dec, childPath)
			if err != nil {
				return "", false, err
			}
			if found {
				return dupPath, true, nil
			}
			i++
		}
		if _, err := dec.Token(); err != nil { // consume closing ']'
			return "", false, err
		}
		return "", false, nil

	default:
		// '}' or ']' should never be handed to us directly here; treat
		// defensively as "nothing to walk".
		return "", false, nil
	}
}

// declaredTopLevelJSONNames returns the set of top-level JSON field names
// type t (a struct type, possibly behind pointers) declares via its json
// tags (or field names where untagged), following anonymous embedded
// structs the way encoding/json does. The BUG-281 r2 strict loader uses it
// so the pre-decode metadata strip removes ONLY $-prefixed top-level keys
// the schema does not itself capture — a schema that declares "$comment"
// (ExternalWorld, NamingCorpus, UnlockTrees) keeps receiving it, while an
// undeclared "$comment"/"$schema_note" is stripped instead of tripping
// DisallowUnknownFields. A non-struct t yields an empty set (nothing is
// preserved, everything $-prefixed is stripped).
func declaredTopLevelJSONNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}
	collectJSONNames(t, names)
	return names
}

// collectJSONNames walks t's fields into names — split out of
// declaredTopLevelJSONNames so anonymous embedded structs can recurse.
func collectJSONNames(t reflect.Type, names map[string]bool) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			// Embedded struct without an explicit json name: its fields are
			// promoted, encoding/json-style.
			collectJSONNames(f.Type, names)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names[name] = true
	}
}

// extractUnknownFieldName attempts to extract the field name from a
// json.Decoder unknown field error message. The error message is typically
// formatted as "json: unknown field \"fieldName\"" at line X column Y.
// This function extracts "fieldName" if possible, or returns a generic
// fallback if extraction fails.
func extractUnknownFieldName(errMsg string) string {
	// Try to find the pattern: unknown field "..."
	idx := strings.Index(errMsg, "unknown field \"")
	if idx >= 0 {
		start := idx + len("unknown field \"")
		end := strings.Index(errMsg[start:], "\"")
		if end >= 0 {
			return errMsg[start : start+end]
		}
	}
	// Fallback: return generic name if we can't parse it
	return "(unknown)"
}
