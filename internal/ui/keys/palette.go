package keys

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ArgKind names an argument's positively-stated allowed domain (AC-9b,
// doc.go's palette argument grammar table).
type ArgKind int

const (
	ArgString ArgKind = iota
	ArgInt
	ArgMoney
	ArgDuration
)

// ArgSpec describes one positional argument a palette CommandSpec
// expects.
type ArgSpec struct {
	Name string
	Kind ArgKind
}

// Money is a parsed monetary amount, in whole units after any K/M
// multiplier suffix has been applied (AC-9b: "5M" -> 5,000,000).
type Money int64

// Duration is a parsed game-duration value: N of Unit ('y' years, 'd'
// days, 'w' weeks, 'm' months) — kept as the original unit rather than
// normalised to a single one, since this package has no calendar model
// of its own (that belongs to the engine module that consumes it).
type Duration struct {
	N    int
	Unit byte
}

// ArgValue is one parsed, typed palette-command argument.
type ArgValue struct {
	Kind     ArgKind
	Str      string
	Int      int64
	Money    Money
	Duration Duration
}

// CommandSpec is one palette command: a name, its positional argument
// list, and the handler to invoke once every argument has validated.
type CommandSpec struct {
	Name string
	Args []ArgSpec
	Run  func(ParsedCommand)
}

// ParsedCommand is ParseCommand's successful result (AC-9): the resolved
// command name plus its typed, positionally-named arguments.
type ParsedCommand struct {
	Name string
	Args map[string]ArgValue
}

// PaletteMatch is one fuzzy-ranked palette listing entry (AC-9): a
// registered mnemonic-path action, or a registered palette-only command,
// with its full path/name rendered beside it as the palette's "doubles
// as the tutorial for the key grammar" contract requires.
type PaletteMatch struct {
	Path  []string
	Name  string
	Score int
}

var (
	moneyPattern    = regexp.MustCompile(`^\d+(\.\d+)?[KkMm]?$`)
	durationPattern = regexp.MustCompile(`^\d+[ydwmYDWM]$`)
	intPattern      = regexp.MustCompile(`^-?\d+$`)
)

// Palette is ui.keys' fuzzy command palette (AC-9, AC-9b). The zero
// value is not ready for use; construct with NewPalette.
type Palette struct {
	mu       sync.Mutex
	grammar  *KeyGrammar
	commands map[string]CommandSpec

	correlationID string
}

// NewPalette constructs a Palette listing grammar's registered mnemonic
// actions (via AllActions) alongside any palette-only commands
// RegisterCommand adds.
func NewPalette(grammar *KeyGrammar, correlationID string) *Palette {
	return &Palette{grammar: grammar, commands: map[string]CommandSpec{}, correlationID: correlationID}
}

// RegisterCommand adds a parameterised palette command (":loan 5M 10y"
// style). Duplicate names are rejected the same way KeyGrammar.Register
// rejects a duplicate mnemonic path.
func (p *Palette) RegisterCommand(spec CommandSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.commands[spec.Name]; exists {
		return errs.New(codeRegisterDuplicate, p.correlationID, map[string]any{"path": []string{spec.Name}, "cause": "palette command already registered"})
	}
	p.commands[spec.Name] = spec
	return nil
}

// Match lists every registered mnemonic action (grammar.AllActions) plus
// every registered palette-only command, ranked by fuzzy score against
// query against their Name, highest first, tied broken by joined path
// (AC-15: deterministic ordering — no map-iteration leakage). An empty
// query matches everything, all tied, so Match("") is a legitimate "list
// every action" call (AC-9's "lists every registered action").
func (p *Palette) Match(query string) []PaletteMatch {
	p.mu.Lock()
	cmdNames := make([]string, 0, len(p.commands))
	for name := range p.commands {
		cmdNames = append(cmdNames, name)
	}
	p.mu.Unlock()
	sort.Strings(cmdNames)

	var candidates []PaletteMatch
	if p.grammar != nil {
		for _, d := range p.grammar.AllActions() {
			candidates = append(candidates, PaletteMatch{Path: d.Path, Name: d.Action.Name})
		}
	}
	for _, name := range cmdNames {
		candidates = append(candidates, PaletteMatch{Path: []string{name}, Name: name})
	}

	out := make([]PaletteMatch, 0, len(candidates))
	for _, c := range candidates {
		score, ok := fuzzyScore(c.Name, query)
		if !ok {
			score, ok = fuzzyScore(strings.Join(c.Path, " "), query)
			if !ok {
				continue
			}
		}
		c.Score = score
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.Join(out[i].Path, "\x1f") < strings.Join(out[j].Path, "\x1f")
	})
	return out
}

// fuzzyScore reports whether every rune of query appears in target, in
// order, case-insensitively (a subsequence match — the standard "fuzzy
// finder" contract), and a deterministic score rewarding contiguous runs
// and an early first match. Pure function, no randomness, no map
// iteration — safe for AC-15's determinism requirement.
func fuzzyScore(target, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	t := []rune(strings.ToLower(target))
	score := 0
	ti := 0
	lastMatch := -2
	for _, qr := range strings.ToLower(query) {
		found := false
		for ; ti < len(t); ti++ {
			if t[ti] == qr {
				if ti == lastMatch+1 {
					score += 3 // contiguous run bonus
				} else {
					score++
				}
				lastMatch = ti
				ti++
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}

// ParseCommand parses input (a leading ':' is stripped if present) into
// a ParsedCommand (AC-9): the first whitespace-delimited field names a
// registered command; remaining fields are validated, in order, against
// that command's ArgSpec list (AC-9b) and rejected — never silently
// coerced or truncated — if they don't match their kind's positive
// grammar (doc.go's table). A rejection names the offending argument and
// the value that failed (MET-U304).
func (p *Palette) ParseCommand(input string) (ParsedCommand, error) {
	input = strings.TrimPrefix(strings.TrimSpace(input), ":")
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return ParsedCommand{}, errs.New(codePaletteUnknownCommand, p.correlationID, map[string]any{"input": input, "name": ""})
	}
	name := fields[0]
	p.mu.Lock()
	spec, ok := p.commands[name]
	p.mu.Unlock()
	if !ok {
		return ParsedCommand{}, errs.New(codePaletteUnknownCommand, p.correlationID, map[string]any{"name": name})
	}

	args := make(map[string]ArgValue, len(spec.Args))
	for i, argSpec := range spec.Args {
		if i+1 >= len(fields) {
			return ParsedCommand{}, errs.New(codePaletteInvalidArg, p.correlationID, map[string]any{
				"command": name, "name": argSpec.Name, "kind": argSpec.Kind, "value": "", "cause": "missing argument",
			})
		}
		raw := fields[i+1]
		val, err := parseArg(argSpec.Kind, raw)
		if err != nil {
			return ParsedCommand{}, errs.New(codePaletteInvalidArg, p.correlationID, map[string]any{
				"command": name, "name": argSpec.Name, "kind": argSpec.Kind, "value": raw,
			})
		}
		args[argSpec.Name] = val
	}
	return ParsedCommand{Name: name, Args: args}, nil
}

func parseArg(kind ArgKind, raw string) (ArgValue, error) {
	switch kind {
	case ArgMoney:
		if !moneyPattern.MatchString(raw) {
			return ArgValue{}, errBadArg
		}
		mult := 1.0
		numPart := raw
		if last := raw[len(raw)-1]; last == 'K' || last == 'k' {
			mult = 1_000
			numPart = raw[:len(raw)-1]
		} else if last == 'M' || last == 'm' {
			mult = 1_000_000
			numPart = raw[:len(raw)-1]
		}
		f, err := strconv.ParseFloat(numPart, 64)
		if err != nil {
			return ArgValue{}, errBadArg
		}
		return ArgValue{Kind: ArgMoney, Str: raw, Money: Money(f * mult)}, nil

	case ArgDuration:
		if !durationPattern.MatchString(raw) {
			return ArgValue{}, errBadArg
		}
		unit := raw[len(raw)-1] | 0x20 // fold to lowercase
		n, err := strconv.Atoi(raw[:len(raw)-1])
		if err != nil {
			return ArgValue{}, errBadArg
		}
		return ArgValue{Kind: ArgDuration, Str: raw, Duration: Duration{N: n, Unit: unit}}, nil

	case ArgInt:
		if !intPattern.MatchString(raw) {
			return ArgValue{}, errBadArg
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return ArgValue{}, errBadArg
		}
		return ArgValue{Kind: ArgInt, Str: raw, Int: n}, nil

	default: // ArgString
		if raw == "" {
			return ArgValue{}, errBadArg
		}
		return ArgValue{Kind: ArgString, Str: raw}, nil
	}
}

var errBadArg = &simpleError{msg: "keys: argument does not match its declared kind's grammar"}
