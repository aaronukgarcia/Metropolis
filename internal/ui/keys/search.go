package keys

// NameIndex is a caller-supplied searchable name index (AC-8) — this
// package does not own auto-naming data (out of scope, see doc.go); it
// only consumes whatever index the caller provides. Search returns
// matches for query, in the index's own preferred order (typically
// relevance); this package does not re-sort them, since "most relevant
// first" is meaningful ordering information the index alone has, unlike
// Continuations' HUD listing where lexical order is the only sane
// determinism story (AC-15).
type NameIndex interface {
	Search(query string) []string
}

// Search runs query against index (AC-8), remembering the results as
// this KeyGrammar's current search-match set for NextMatch/PrevMatch to
// step through. index.Search is called WITHOUT holding this KeyGrammar's
// lock (a caller-supplied index must never be trusted not to call back
// into this KeyGrammar, e.g. to log via a registered action) — only the
// bookkeeping afterward is locked.
func (g *KeyGrammar) Search(query string, index NameIndex) []string {
	if err := g.checkNotCopied(); err != nil {
		return nil
	}
	matches := index.Search(query)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.searchMatches = matches
	g.searchPos = -1
	return matches
}

// NextMatch steps forward through the current search-match set,
// wrapping. ok is false if there are no matches.
func (g *KeyGrammar) NextMatch() (string, bool) {
	if err := g.checkNotCopied(); err != nil {
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.searchMatches) == 0 {
		return "", false
	}
	g.searchPos = (g.searchPos + 1) % len(g.searchMatches)
	return g.searchMatches[g.searchPos], true
}

// PrevMatch steps backward through the current search-match set,
// wrapping. ok is false if there are no matches.
func (g *KeyGrammar) PrevMatch() (string, bool) {
	if err := g.checkNotCopied(); err != nil {
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.searchMatches) == 0 {
		return "", false
	}
	g.searchPos--
	if g.searchPos < 0 {
		g.searchPos = len(g.searchMatches) - 1
	}
	return g.searchMatches[g.searchPos], true
}
