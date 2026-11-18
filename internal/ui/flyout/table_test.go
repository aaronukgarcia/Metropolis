package flyout

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestNewTable_AcceptsSample(t *testing.T) {
	tb := mustTable(t, sampleItems())
	if tb.BarLen() != 3 {
		t.Fatalf("BarLen = %d, want 3", tb.BarLen())
	}
}

// bar wraps one list under a single bar entry.
func bar(items ...Item) []Item { return []Item{sub("Menu", 'm', items...)} }

// deep builds a chain of n nested submenus ending in an action.
func deep(n int) Item {
	it := act("Leaf", 'l', "x")
	for i := 0; i < n; i++ {
		it = sub("Level", 'l', it)
	}
	return it
}

// Criterion 2: one broken fixture per rule, each refused with MET-V990.
func TestNewTable_RefusesEachBrokenRule(t *testing.T) {
	cases := []struct {
		name string
		bar  []Item
		want string // part of the cause
	}{
		{"empty bar", nil, "no entries"},
		{"bar entry is an action", []Item{act("A", 'a', "x")}, "must be a submenu"},
		{"duplicate letter", bar(act("One", 'o', "1"), act("Other", 'o', "2")), "already used"},
		{"duplicate letter other case", bar(act("One", 'o', "1"), act("Other", 'O', "2")), "already used"},
		{"empty label", bar(act("", 'e', "1")), "label is empty"},
		{"blank label", bar(act("   ", 'e', "1")), "label is empty"},
		{"no kind", bar(Item{Label: "x", Letter: 'x'}), "no valid kind"},
		{"submenu with no items", bar(Item{Label: "x", Letter: 'x', Kind: KindSubmenu}), "no entries"},
		{"action that also holds a list", bar(Item{Label: "x", Letter: 'x', Kind: KindAction, ActionID: "a", Items: []Item{act("y", 'y', "b")}}), "also hold a list"},
		{"submenu with an action id", bar(Item{Label: "x", Letter: 'x', Kind: KindSubmenu, ActionID: "a", Items: []Item{act("y", 'y', "b")}}), "action id"},
		{"action with no id and no reason", bar(Item{Label: "x", Letter: 'x', Kind: KindAction}), "needs an action id"},
		{"no letter", bar(Item{Label: "x", Kind: KindAction, ActionID: "a"}), "letter"},
		{"space letter", bar(act("x", ' ', "a")), "letter"},
		{"control letter", bar(act("x", '\n', "a")), "letter"},
		{"control character in label", bar(act("a\tb", 'a', "a")), "printable"},
		{"separator first", bar(sep, act("x", 'x', "a")), "separator"},
		{"separator last", bar(act("x", 'x', "a"), sep), "separator"},
		{"two separators", bar(act("x", 'x', "a"), sep, sep, act("y", 'y', "b")), "two separators"},
		{"separator with a label", bar(act("x", 'x', "a"), Item{Kind: KindSeparator, Label: "no"}, act("y", 'y', "b")), "separator"},
		{"only separators cannot be a list", bar(sep), "separator"},
		{"depth over the limit", bar(deep(MaxDepth)), "deeper"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb, err := NewTable(c.bar)
			if err == nil || tb != nil {
				t.Fatalf("NewTable accepted a broken table (err=%v)", err)
			}
			var e *errs.E
			if !errors.As(err, &e) || e.Code != ErrInvalidMenu {
				t.Fatalf("error = %v, want code %s", err, ErrInvalidMenu)
			}
			cause, _ := e.Ctx["cause"].(string)
			if !strings.Contains(cause, c.want) {
				t.Fatalf("cause %q does not mention %q", cause, c.want)
			}
		})
	}
}

// The depth limit is exact: MaxDepth lists is fine, one more is not.
func TestNewTable_DepthLimitIsExact(t *testing.T) {
	// bar entry's list is list 1; deep(n) adds n more lists inside it.
	if _, err := NewTable(bar(deep(MaxDepth - 1))); err != nil {
		t.Fatalf("a path of exactly MaxDepth lists was refused: %v", err)
	}
	if _, err := NewTable(bar(deep(MaxDepth))); err == nil {
		t.Fatal("a path of MaxDepth+1 lists was accepted")
	}
}

// A greyed action may have no action id; a greyed submenu can still hold items.
func TestNewTable_GreyedEntriesAllowed(t *testing.T) {
	if _, err := NewTable(bar(grey("Later", 'l', "not active yet"))); err != nil {
		t.Fatalf("greyed action refused: %v", err)
	}
}

// The table is a copy: changing the caller's slice afterwards changes nothing.
func TestNewTable_CopiesItsInput(t *testing.T) {
	items := sampleItems()
	tb := mustTable(t, items)
	items[0].Label = "Changed"
	items[0].Items[0].Items[0].ActionID = "changed"
	m := New(tb)
	m.Open()
	r := run(m, "b", "h", "d")
	if r.Action != "build:dwelling" {
		t.Fatalf("action = %q, want the original id", r.Action)
	}
}
