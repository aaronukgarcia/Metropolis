package news

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file owns the data-driven fact-word list the FactLock requires
// (SEC-148) and its GR#15 data file.
//
// FactLock guards three fact classes: the ordered number sequence, the named
// entity, and the fact-bearing words of the prose. The number and name
// classes are derived from the story's own structure; the fact-word class is
// a vocabulary the engine cannot derive — §29 names the five story
// categories but not the prose words that carry a story's meaning ("deaths"
// vs "births", "March" vs "April"). So the token list is data
// (news-facts.json) rather than a Go literal. The list is a placeholder
// pending Aaron's balance pass — news-facts.json carries a disclosure field
// stating exactly that, and a balance change is a data edit, never a code
// change (the same pattern as salience.json).

//go:embed news-facts.json
var embeddedFactsJSON []byte

// factsFile is news-facts.json's schema: the fact-bearing token list plus a
// non-empty disclosure naming the list as pending tuning.
type factsFile struct {
	Version    int      `json:"version"`
	Tokens     []string `json:"tokens"`
	Disclosure string   `json:"disclosure"`
}

var (
	factWordsOnce sync.Once
	// factWordsSet is the lowercased fact-bearing token set, nil until
	// loaded. factWordsErr is non-nil iff the embedded file failed to load
	// or validate.
	factWordsSet map[string]struct{}
	factWordsErr error
)

// loadFactWordsOnce loads the embedded fact-word list exactly once. It is
// triggered by [New] (so a bad file fails construction, GR#15) and lazily by
// [FactLock] (defense in depth for standalone callers, e.g. tests). On
// success the set is populated; on failure factWordsErr is set and the set
// stays nil, which [FactLock] turns into a fail-closed reject-everything
// stance — a lock that cannot load its vocabulary must never report a
// rewrite safe (GR#17).
func loadFactWordsOnce(correlationID string) {
	factWordsOnce.Do(func() {
		factWordsSet, factWordsErr = loadFactWords(correlationID)
	})
}

// factListInvalid builds the MET-G2305 list-invalid error with the full ctx
// its template renders: field/rule/cause. Every fact-list call site must
// supply all three keys, or a missing one reaches the user as a literal token
// instead of a value (BUG-357: MET-G2305 previously had validators supplying
// only {field,rule} while the template also names {cause}).
func factListInvalid(correlationID, field, rule, cause string) error {
	return errs.New(ErrFactListInvalid, correlationID, map[string]any{
		"field": field,
		"rule":  rule,
		"cause": cause,
	})
}

// loadFactWords unmarshals and validates the embedded news-facts.json,
// returning the lowercased token set. It is deterministic and does not cache
// (the sync.Once in loadFactWordsOnce is the single cache). Every failure is
// a registry-sourced *errs.E (GR#7) — never a silent empty set (GR#15).
func loadFactWords(correlationID string) (map[string]struct{}, error) {
	var ff factsFile
	if err := json.Unmarshal(embeddedFactsJSON, &ff); err != nil {
		// Route Unmarshal errors through the helper to supply full {field,rule,cause} ctx.
		return nil, errs.Wrap(ErrFactListInvalid, correlationID, err, map[string]any{
			"field": "",
			"rule":  "JSON format or schema",
			"cause": err.Error(),
		})
	}
	if ff.Disclosure == "" {
		return nil, factListInvalid(correlationID, "disclosure", "non-empty pending-tuning disclosure required (GR#15)", "")
	}
	if len(ff.Tokens) == 0 {
		return nil, factListInvalid(correlationID, "tokens", "non-empty fact-bearing token list required", "")
	}

	set := make(map[string]struct{}, len(ff.Tokens))
	for _, tok := range ff.Tokens {
		t := strings.ToLower(strings.TrimSpace(tok))
		if t == "" {
			return nil, factListInvalid(correlationID, "tokens", "no empty/whitespace-only token", "")
		}
		if !isLetterWord(t) {
			return nil, factListInvalid(correlationID, "tokens."+tok, "token must be a single whole word of letters (no digits, spaces, or punctuation) so whole-word matching is well-defined", "")
		}
		if _, dup := set[t]; dup {
			return nil, factListInvalid(correlationID, "tokens."+tok, "duplicate token after lowercasing", "")
		}
		set[t] = struct{}{}
	}
	return set, nil
}

// isLetterWord reports whether s is non-empty and every rune is a Unicode
// letter — the same domain wordRe (\pL+) tokenizes, so a fact-word token is
// exactly one prose word and whole-word matching is unambiguous.
func isLetterWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// factWordsIn extracts the set of fact-bearing tokens present in prose, each
// matched as a whole word case-insensitively. Words that are not in the token
// set are ignored; the set membership is a pure map lookup, so the result is
// deterministic (GR#21).
func factWordsIn(prose string, tokens map[string]struct{}) map[string]struct{} {
	found := make(map[string]struct{})
	for _, w := range wordRe.FindAllString(prose, -1) {
		lw := strings.ToLower(w)
		if _, ok := tokens[lw]; ok {
			found[lw] = struct{}{}
		}
	}
	return found
}

// factWordsSurvive reports whether the set of fact-bearing tokens in original
// equals the set in rewritten (SEC-148). A rewrite that drops a fact-word
// ("2 deaths" -> "2"), swaps it ("2 deaths" -> "2 births"), or invents one
// ("record set in March" -> "record set in April") changes the set and fails.
// It is the same exact-set standard as the number and name classes: false
// rejection is harmless (AC-7 falls back to the engine prose), a false
// acceptance is a hallucinated fact.
func factWordsSurvive(original, rewritten string, tokens map[string]struct{}) bool {
	if len(tokens) == 0 {
		// Nothing to check: a story with no fact-bearing words has no
		// fact-word set to preserve (the number/name classes still apply).
		return true
	}
	return sameFactWordSet(factWordsIn(original, tokens), factWordsIn(rewritten, tokens))
}

// sameFactWordSet reports whether a and b hold the same tokens.
func sameFactWordSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
