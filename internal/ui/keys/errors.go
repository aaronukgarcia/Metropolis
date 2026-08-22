package keys

// Registry error codes for ui.keys (MOD-011). Range: U300-U399, claimed
// in data/errors.json's "ranges.reserved" table (third U-layer
// sub-range — U000-U099/U100-U199/U200-U299 already belong to
// ui.core/ui.screen.map/ui.screen.debug). Checked against both the table
// AND a live source scan (`grep -rn "MET-U3" internal/ cmd/`) before
// claiming, per BUG-008's lesson. Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields
// (GR#7).
const (
	// codeRegisterDuplicate: Register was called twice with the identical
	// mnemonic path (AC-14).
	codeRegisterDuplicate = "MET-U300"

	// codeRegisterPrefixConflict: Register was called with a path that is
	// either a strict prefix of an already-registered longer path, or
	// would itself become a strict prefix of one once an already-terminal
	// node is extended (AC-14b, ASM-118).
	codeRegisterPrefixConflict = "MET-U301"

	// codeKeymapMalformed: a keymap profile file failed to parse/validate
	// against the documented schema; the grammar falls back to the
	// shipped default profile (AC-13).
	codeKeymapMalformed = "MET-U302"

	// codeKeymapUnknownAction: a well-formed keymap profile entry's target
	// mnemonic path names no action this KeyGrammar has Register()ed;
	// that one entry is rejected, the rest of the profile still loads
	// (AC-11b).
	codeKeymapUnknownAction = "MET-U303"

	// codePaletteInvalidArg: a parameterised palette command's argument
	// did not match its declared positive-grammar domain (AC-9b).
	codePaletteInvalidArg = "MET-U304"

	// codeInvalidMarkID: SetMark/GetMark was called with an identifier
	// outside the 12-slot a-l addressing space (AC-7).
	codeInvalidMarkID = "MET-U305"

	// codeReservedToken: Register or RegisterGlobal was called with a
	// token this package reserves for a built-in, unconditionally
	// intercepted key (Esc, ".", "u", "U" — see grammar.go's Feed doc
	// comment) — registering an action under one of these tokens would be
	// permanently unreachable, since Feed never lets the trie/global
	// lookup see them (AC-2b's "invariant enforced, not stated" applied
	// to this package's own reserved-key contract).
	codeReservedToken = "MET-U306"

	// codePaletteUnknownCommand: ParseCommand was given a command name no
	// RegisterCommand call has registered.
	codePaletteUnknownCommand = "MET-U307"

	// codeGrammarCopied: a *KeyGrammar method was called on a struct copy
	// of the value NewKeyGrammar constructed (SEC-020-class guard, mirrors
	// harness.uitest's Harness/harness.replay's Recorder).
	codeGrammarCopied = "MET-U308"

	// codeRegisterEmptyPath: Register was called with an empty mnemonic
	// path. This is NOT a prefix conflict (MET-U301) — there is no other
	// path to conflict with — so it gets its own code rather than reusing
	// codeRegisterPrefixConflict with a fabricated conflictsWith (which the
	// MET-U301 template would render as a literal {conflictsWith}).
	codeRegisterEmptyPath = "MET-U309"
)
