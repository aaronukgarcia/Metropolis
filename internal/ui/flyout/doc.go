// Package flyout is the fly-out menu engine for the terminal game
// (FEAT-9995385618 lane L1): a menu bar on one row with nested drop-down
// lists, driven by keys and by mouse hit positions.
//
// Module key: ui.flyout (see code.json)
// Spec refs:  docs/planning/acceptance/feat-9995385618-flyout-menu.md
// (criteria 1-26 and 59), Aaron's words "a simple fly out or drop down menu
// fast and nested so comprehensive".
//
// # What is here
//
//   - Table / Item / NewTable: the menu as data, validated once at build.
//     No menu is hand-coded in this package.
//   - Menu: a pure state machine (closed, bar focused, a path of open lists
//     with a highlighted item in each). Press, Click and Hover return a
//     Result; picking an action returns its opaque action id to the caller.
//     No engine, goroutines, clock or I/O.
//   - Menu.Layout and Menu.Draw: boxes placed from the state and the area
//     they may use, each box blanked in full before its text is drawn
//     (BUG-1234 class), scrolling long lists, opening to the left where the
//     right edge would be crossed, never writing outside the buffer.
//   - Layout.Hit: the pure hit test used by Click and Hover.
//
// # One goroutine
//
// A Menu is used from ONE goroutine. It has no locks: the caller serialises
// input and drawing (Press, Click, Hover, Layout and Draw never run at the
// same time on one Menu). A Table is read-only and may be shared.
//
// # Letters are always visible
//
// Every selectable entry shows its key as plain text, upper case, in its own
// column before the label ("►H Housing", " J Jobs"; on the bar "B Build",
// and "[B Build]" when open), so a letter needs no underline or colour to be
// seen and need not occur in the label. The underline on the matching rune of
// the label stays as an extra cue. When a box is cut to fit, the highlight
// marker and the letter column are kept and the label is cut first (after
// the right column).
//
// # Speed
//
// The tree is analysed once in NewTable (column widths, first and last
// selectable entry per list). Press, Layout and Draw then touch only the
// open lists and the rows shown, never the size of the tree, and allocate
// nothing. Menu.Visits counts the entries read so a test can prove this
// without a clock.
//
// # Widths
//
// Widths are counted in runes (every glyph the menu draws is one cell wide;
// the pound sign and the ellipsis are multi-byte but one cell). Wide East
// Asian glyphs are not modelled, as in ui.core.Buffer.
//
// # Keys
//
// Opening puts the focus on the bar (the first entry's list is dropped as a
// preview). A letter, Left or Right picks a bar entry there; Down or Enter
// moves into the dropped list. Inside a list: Up and Down wrap, Right or
// Enter enters a sub-list, Left or Esc goes back one level, Esc at the first
// list closes, a letter jumps to the entry and acts. See Menu.Press.
package flyout
