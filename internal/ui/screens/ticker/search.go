package ticker

import (
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// Archive search (TIK-4/TIK-8). The searchable history archive reuses
// ui.keys' "/" NameIndex search convention — name-substring match with
// n/N stepping — rather than a bespoke full-text query language, per
// ASM-254 (GR#3: one search primitive, no second query parser). The
// compile-time assertion below proves the reuse is require/import, not a
// transcription of the interface.
var _ keys.NameIndex = (*Screen)(nil)

// matchStories returns the archive stories whose §20 Name contains query
// as a case-insensitive substring, preserving archive order (so matches
// are deterministic, not map-iteration-order-dependent — GR#21). The
// case-insensitivity is the natural reading of UI-SPEC §3's worked
// example ("`/pent` cycles Pent Lane, Pent Way, Pent Stream" — a
// lowercase query matching capitalised names). An empty/whitespace query
// matches nothing (never "everything"), so the "no matches" state is
// honest rather than the accidental result of matching on "".
func matchStories(stories []Story, query string) []Story {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	out := make([]Story, 0)
	for _, st := range stories {
		if strings.Contains(strings.ToLower(st.Name), q) {
			out = append(out, st)
		}
	}
	return out
}

// Search implements keys.NameIndex for the archive: it returns the Names
// of the archive stories matching query (name-substring match), in
// archive order. It is the seam ui.keys' KeyGrammar.Search drives with
// the "/" key; this package does not own the key binding or the grammar,
// only the index (mirrors keys.doc.go's "this package only consumes
// whatever index the caller provides" from the other side of the seam).
func (s *Screen) Search(query string) []string {
	// SEC-020: Search is the exported entry point that satisfies
	// keys.NameIndex (the compile-time assertion above), so a struct copy
	// could be called on Search directly — it gets its own guard even
	// though the work is delegated to SearchStories, which also guards
	// (mirrors demo.Screen.SubscribeAll/menu.Screen.SubscribeSession's
	// identical "guard the exported entry point, not only the guarded
	// method it delegates to" precedent).
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Search"}); err != nil {
		return nil
	}
	stories := s.SearchStories(query)
	names := make([]string, len(stories))
	for i, st := range stories {
		names[i] = st.Name
	}
	return names
}

// SearchStories runs the name-substring search over the archive and
// records the result as this screen's current search selection, returning
// the matched stories in archive order. Recording the selection here (the
// screen, not the grammar) is what lets the render path draw the matched
// rows and let NextMatch/PrevMatch step them — the same n/N convention
// ui.keys' KeyGrammar.NextMatch/PrevMatch implement, mirrored so the
// screen is self-contained and testable without the grammar wired in.
func (s *Screen) SearchStories(query string) []Story {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SearchStories"}); err != nil {
		return nil
	}
	s.mu.Lock()
	matches := matchStories(s.archive, query)
	s.searchQuery = query
	s.searchMatches = matches
	s.searchPos = -1
	s.searchActive = true
	s.mu.Unlock()
	// Return a copy so a caller cannot mutate the screen's stored
	// selection slice (which Search()/CurrentMatch/NextMatch read back).
	out := make([]Story, len(matches))
	copy(out, matches)
	return out
}

// NextMatch steps forward through the current search-result set, wrapping,
// mirroring keys.KeyGrammar.NextMatch's exact semantics. ok is false when
// there are no matches (in which case the returned Story is the zero
// value). The first NextMatch after a SearchStories returns the first
// match (searchPos starts at -1 and wraps to 0).
func (s *Screen) NextMatch() (Story, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "NextMatch"}); err != nil {
		return Story{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.searchMatches) == 0 {
		return Story{}, false
	}
	s.searchPos = (s.searchPos + 1) % len(s.searchMatches)
	return s.searchMatches[s.searchPos], true
}

// PrevMatch steps backward through the current search-result set,
// wrapping, mirroring keys.KeyGrammar.PrevMatch's exact semantics. ok is
// false when there are no matches. The first PrevMatch after a
// SearchStories wraps to the last match (searchPos starts at -1 and
// wraps to len-1).
func (s *Screen) PrevMatch() (Story, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "PrevMatch"}); err != nil {
		return Story{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.searchMatches) == 0 {
		return Story{}, false
	}
	s.searchPos--
	if s.searchPos < 0 {
		s.searchPos = len(s.searchMatches) - 1
	}
	return s.searchMatches[s.searchPos], true
}

// CurrentMatch returns the currently-selected search match, without
// stepping. When searchPos is -1 (a search has been run but n/N not yet
// pressed), it returns the first match — the same "first match is the
// default selection" a fresh keys.KeyGrammar.Search implies.
func (s *Screen) CurrentMatch() (Story, bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CurrentMatch"}); err != nil {
		return Story{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.searchMatches) == 0 {
		return Story{}, false
	}
	pos := s.searchPos
	if pos < 0 {
		pos = 0
	}
	return s.searchMatches[pos], true
}

// SearchActive reports whether a search has been run since construction
// (TIK-8's discriminator: "no matches" — a search ran and matched zero —
// is distinguishable from "still loading" — no archive data yet — and
// from "no search yet").
func (s *Screen) SearchActive() bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SearchActive"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searchActive
}

// SearchMatchedCount returns how many archive stories the last search
// matched. Zero while no search has been run (SearchActive()==false).
func (s *Screen) SearchMatchedCount() int {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SearchMatchedCount"}); err != nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.searchMatches)
}
