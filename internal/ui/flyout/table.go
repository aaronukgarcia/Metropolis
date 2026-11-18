package flyout

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// MaxDepth is the most lists one path may open: the bar's drop-down is
// list 1, so Money > Grants > pot > amount is the deepest the criteria need
// (criterion 2: depth over 4 is refused).
const MaxDepth = 4

// Kind says what an Item is. The zero value is invalid on purpose: an Item
// with no kind is refused (criterion 2, "a leaf with no kind").
type Kind int

const (
	// KindSubmenu opens a list of Items.
	KindSubmenu Kind = iota + 1
	// KindAction runs: picking it returns its ActionID to the caller.
	KindAction
	// KindSeparator is a rule drawn between groups. It is never highlighted.
	KindSeparator
)

// Item is one menu entry as the caller writes it. Grey makes it greyed: a
// non-empty Grey is the reason text, drawn in the right-hand column, shown
// on the box footer when the entry is tried, and the entry cannot be picked.
// A greyed submenu can still be opened (its entries are the caller's to grey)
// so the player sees what is coming (criterion 30).
type Item struct {
	// Label is the text shown. Required (not for a separator).
	Label string
	// Letter is the key that jumps to and acts on the entry, matched
	// case-insensitively, unique inside one list. Required (not for a
	// separator); one printable rune.
	Letter rune
	Kind   Kind
	// ActionID is the opaque id returned when a KindAction is picked.
	ActionID string
	// Grey, when non-empty, greys the entry with this reason.
	Grey string
	// Right is the optional right-hand column text (a cost, a key name).
	Right string
	// Items are the entries of a KindSubmenu (at least one selectable).
	Items []Item
}

// entry is an Item after validation, with its widths worked out once.
type entry struct {
	label    string
	letter   rune // lower-cased
	kind     Kind
	action   string
	grey     string
	right    string // text shown in the right column: Grey wins over Right
	sub      *list
	labelW   int
	rightW   int
	letterAt int // rune index of the letter in label, -1 when absent
}

// list is a validated list of entries with the column widths and the first
// and last selectable index, so layout never scans the entries.
type list struct {
	entries []entry
	labelW  int
	rightW  int
	anySub  bool
	first   int
	last    int
}

// Table is a validated menu tree. Build one with NewTable; it is read-only
// afterwards and safe to share between menus.
type Table struct {
	bar  list
	barX []int // x offset of each bar entry from the area's left edge
	barW []int // cell width of each bar entry
}

// letterCols is the cells of an entry's letter column: the upper-case letter
// and one space, drawn before the label so the key shows as plain text.
const letterCols = 2

// barGap is the number of blank cells between two bar entries.
const barGap = 1

// NewTable validates items as the bar (every top-level entry must be a
// submenu) and returns the table, or an error carrying MET-V990 whose cause
// names the first offending entry.
func NewTable(bar []Item) (*Table, error) {
	cid := errs.NewCorrelationID()
	fail := func(path, why string) error {
		return errs.New(ErrInvalidMenu, cid, map[string]any{"cause": path + ": " + why})
	}
	if len(bar) == 0 {
		return nil, fail("bar", "the menu bar has no entries")
	}
	for i := range bar {
		if bar[i].Kind != KindSubmenu {
			return nil, fail(fmt.Sprintf("bar[%d]", i), "a bar entry must be a submenu")
		}
	}
	root, err := buildList(bar, "bar", 0, fail)
	if err != nil {
		return nil, err
	}
	t := &Table{bar: *root}
	t.barX = make([]int, len(root.entries))
	t.barW = make([]int, len(root.entries))
	x := 0
	for i := range root.entries {
		// one cell of padding each side, plus the letter column and its space
		w := root.entries[i].labelW + 2 + letterCols
		t.barX[i] = x
		t.barW[i] = w
		x += w + barGap
	}
	return t, nil
}

// buildList validates and converts one list. depth is the number of lists
// above it (0 for the bar itself, whose drop-downs are list 1).
func buildList(items []Item, path string, depth int, fail func(string, string) error) (*list, error) {
	l := &list{entries: make([]entry, len(items)), first: -1, last: -1}
	seen := make(map[rune]int, len(items))
	for i := range items {
		it := &items[i]
		p := fmt.Sprintf("%s[%d]", path, i)
		if it.Label != "" {
			p = fmt.Sprintf("%s[%d] %q", path, i, it.Label)
		}
		e := &l.entries[i]
		e.kind = it.Kind
		e.letterAt = -1
		switch it.Kind {
		case KindSeparator:
			if it.Label != "" || it.Letter != 0 || it.ActionID != "" || it.Grey != "" || it.Right != "" || len(it.Items) != 0 {
				return nil, fail(p, "a separator carries no label, letter, action, reason, right text or items")
			}
			if i == 0 || i == len(items)-1 {
				return nil, fail(p, "a separator cannot start or end a list")
			}
			if items[i-1].Kind == KindSeparator {
				return nil, fail(p, "two separators in a row")
			}
			continue
		case KindSubmenu, KindAction:
		default:
			return nil, fail(p, "the entry has no valid kind")
		}
		if strings.TrimSpace(it.Label) == "" {
			return nil, fail(p, "the label is empty")
		}
		if !printable(it.Label) || !printable(it.Right) || !printable(it.Grey) {
			return nil, fail(p, "label, right text and reason must be printable text")
		}
		if it.Letter == 0 || !unicode.IsPrint(it.Letter) || unicode.IsSpace(it.Letter) {
			return nil, fail(p, "the letter must be one printable, non-space character")
		}
		lc := unicode.ToLower(it.Letter)
		if prev, dup := seen[lc]; dup {
			return nil, fail(p, fmt.Sprintf("the letter %q is already used by entry %d of the same list", it.Letter, prev))
		}
		seen[lc] = i
		e.label = it.Label
		e.letter = lc
		e.grey = it.Grey
		e.right = it.Right
		if it.Grey != "" {
			e.right = it.Grey
		}
		e.labelW = utf8.RuneCountInString(it.Label)
		e.rightW = utf8.RuneCountInString(e.right)
		for k, r := range []rune(it.Label) {
			if unicode.ToLower(r) == lc {
				e.letterAt = k
				break
			}
		}
		if it.Kind == KindAction {
			if len(it.Items) != 0 {
				return nil, fail(p, "an action cannot also hold a list")
			}
			if it.ActionID == "" && it.Grey == "" {
				return nil, fail(p, "an action needs an action id (or a Grey reason)")
			}
			e.action = it.ActionID
		} else {
			if it.ActionID != "" {
				return nil, fail(p, "a submenu cannot also carry an action id")
			}
			if len(it.Items) == 0 {
				return nil, fail(p, "a submenu with no entries")
			}
			if depth+1 > MaxDepth {
				return nil, fail(p, fmt.Sprintf("lists nested deeper than %d", MaxDepth))
			}
			sub, err := buildList(it.Items, p, depth+1, fail)
			if err != nil {
				return nil, err
			}
			e.sub = sub
			l.anySub = true
		}
		if e.labelW > l.labelW {
			l.labelW = e.labelW
		}
		if e.rightW > l.rightW {
			l.rightW = e.rightW
		}
		if l.first < 0 {
			l.first = i
		}
		l.last = i
	}
	if l.first < 0 {
		return nil, fail(path, "a list with no selectable entry")
	}
	return l, nil
}

func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// BarLen is the number of bar entries.
func (t *Table) BarLen() int { return len(t.bar.entries) }
