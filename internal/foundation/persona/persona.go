// Package persona derives a citizen's PRESENTATIONAL identity -- a whimsical
// name, a formatted GUID and a cartoon-avatar recipe -- as pure functions of
// the citizen's uint64 ID (FEAT-2326609842, the citizen dummy-passport).
//
// Citizens carry no name in the engine or in saves (non-goal: no identity
// field is added to ColdRecord). Everything here is therefore a derivation,
// never state: the same ID yields byte-identical output twice, across
// processes, and across save/load, because the only inputs are the ID and the
// fixed tables in this file (AC-2, GR#21). The randomness comes exclusively
// from det.NewStream seeded with PersonaSeed + the ID -- no wall clock, no
// math/rand, no map iteration -- and the whimsy lives in the tables, not in
// any nondeterminism (Aaron's 2026-10-31 ruling: alliterative / occupational-
// pun names).
//
// # Collision odds (honest, birthday-bound)
//
// A name is (first, surname) drawn from the tables below. The draw is NOT
// uniform over the cross product: the surname is picked by one of three equally
// likely modes, and the "alliterative" mode only offers surnames sharing the
// first name's initial. NameSpace() therefore reports the EFFECTIVE space
// (inverse sum of squared per-name probabilities, computed exactly from the
// tables), while DistinctNames() is the raw support size. Two distinct IDs
// collide on NAME with probability exactly 1/NameSpace() each, so among n
// citizens the expected number of colliding pairs is n(n-1)/(2*NameSpace()):
// names are NOT unique and are not meant to be -- the GUID is the identity.
// The GUID is the ID itself, hex-formatted, so it is injective by
// construction. The avatar recipe packs several small attributes plus a
// 64-bit seed; two citizens sharing every attribute still differ in Seed.
package persona

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// PersonaSeed is the fixed "world seed" fed to det.NewStream for every persona
// derivation. It is deliberately NOT the city's world seed: a citizen ID maps
// to the same name in every city and every save, so a passport never changes
// when the city is reloaded or cloned (AC-2 "across save/load").
const PersonaSeed uint64 = 0x4d45545f50455253 // "MET_PERS"

// Avatar attribute cardinalities, shared with the surfaces that draw the
// portrait so a palette index can never exceed its table (GR#3).
const (
	AvatarSkinTones   = 8
	AvatarHairStyles  = 10
	AvatarHairColours = 8
	AvatarEyeColours  = 6
	AvatarOutfits     = 12
	AvatarAccessories = 6
)

// Avatar is the recipe a surface renders as a cartoon portrait. Seed is the
// full 64-bit avatar seed (so two citizens sharing every small attribute still
// differ); the small fields are already reduced into their palette ranges.
type Avatar struct {
	Seed      uint64 `json:"seed"`
	Skin      uint8  `json:"skin"`
	HairStyle uint8  `json:"hairStyle"`
	HairColor uint8  `json:"hairColor"`
	Eyes      uint8  `json:"eyes"`
	Outfit    uint8  `json:"outfit"`
	Accessory uint8  `json:"accessory"`
}

// Persona is the full derived identity.
type Persona struct {
	GUID   string `json:"guid"`
	Name   string `json:"name"`
	Avatar Avatar `json:"avatar"`
}

var firstNames = []string{
	"Abigail", "Barnaby", "Beatrix", "Cedric", "Clementine", "Dorothy", "Darcy", "Edmund",
	"Elspeth", "Florence", "Freddie", "Gertrude", "Gideon", "Hattie", "Horatio", "Ivy",
	"Isidore", "Josephine", "Jasper", "Kitty", "Kenneth", "Lavinia", "Lionel", "Marigold",
	"Mortimer", "Nancy", "Norbert", "Olive", "Orville", "Penelope", "Percy", "Queenie",
	"Quentin", "Rosalind", "Rupert", "Sally", "Septimus", "Tilly", "Thaddeus", "Ursula",
	"Ulysses", "Violet", "Vernon", "Winnie", "Wilbur", "Xanthe", "Xavier", "Yvonne",
	"Yorick", "Zelda", "Zebedee",
}

// alliterativeSurnames is the table the "Barnaby Bumblebee" mode draws from:
// each entry is matched to a first name by initial letter.
var alliterativeSurnames = []string{
	"Appleby", "Aldermoor", "Bumblebee", "Biscuit", "Buttercup", "Bramble", "Crumpet", "Cobblestone",
	"Chutney", "Dumpling", "Dewdrop", "Dabble", "Eggcup", "Elderflower", "Evergreen", "Fiddlesticks",
	"Fudge", "Featherstone", "Gumboot", "Gingersnap", "Goosegrass", "Hobnob", "Hedgerow", "Honeysuckle",
	"Inkwell", "Ironside", "Jellybean", "Jamjar", "Juniper", "Kettleby", "Kipper", "Kumquat",
	"Lollop", "Lavender", "Lamplighter", "Muffin", "Marmalade", "Mudlark", "Nutmeg", "Noodle",
	"Nettle", "Oatcake", "Ovenglove", "Orchard", "Pumpernickel", "Pennywhistle", "Parsnip", "Quibble",
	"Quince", "Quillfeather", "Rhubarb", "Ragamuffin", "Rooster", "Sprocket", "Sixpence", "Shortbread",
	"Teacup", "Thimble", "Tumbleweed", "Umbrella", "Underhill", "Upsy", "Velvet", "Vinegar",
	"Vesper", "Whistlestop", "Wobble", "Waffle", "Xylophone", "Xerxes", "Yarrow", "Yoghurt",
	"Yellowhammer", "Zigzag", "Zucchini", "Zephyr",
}

// punSurnames is the occupational-pun table: a trade, a wink.
var punSurnames = []string{
	"Baker-Fingers", "Breadwinner", "Candlemaker", "Cheesewright", "Cobbler-Knox", "Cooper-Hoops",
	"Dyer-Straits", "Fletcher-Quill", "Fisher-King", "Forger-Ahead", "Gardner-Thyme", "Glazier-Pane",
	"Hammersmith-Jones", "Inkpot-Scribe", "Joiner-Together", "Knitwit", "Ladle-Ward", "Lampwick",
	"Mason-Jarr", "Miller-Time", "Milkfloat", "Needle-Thread", "Pavior-Stone", "Plumb-Bob",
	"Potter-Wheel", "Proofreader-Wright", "Roofer-Madness", "Sawyer-Plank", "Shepherd-Pye", "Smith-Anvil",
	"Spinner-Yarn", "Tanner-Hyde", "Tinker-Bell", "Turner-Lathe", "Wainwright-Wheelie", "Weaver-Loom",
	"Wheelwright-Spokes", "Whitesmith-Spark", "Chandler-Wick", "Thatcher-Straw",
}

// plainSurnames is the third, "just whimsical" mode.
var plainSurnames = []string{
	"Pocketwatch", "Thistlethwaite", "Merriweather", "Higginbottom", "Applegarth", "Bellweather",
	"Cuddlecombe", "Dunderhead", "Fothergill", "Gadabout", "Hogsflesh", "Ickleby",
	"Jollyboat", "Knucklebone", "Longshanks", "Mumblecrust", "Nibblesworth", "Oddfellow",
	"Pottersby", "Quagmire", "Ravenscroft", "Snodgrass", "Tiddlywink", "Underwood",
	"Wigglesworth", "Yeomanry", "Zinnia-Moss", "Crabtree",
}

// alliterIndex maps an upper-case initial to the alliterative surnames
// starting with it. Built once at package load from the (immutable) table, in
// table order -- no map iteration reaches any output (GR#21).
var alliterIndex = func() map[byte][]string {
	m := map[byte][]string{}
	for _, s := range alliterativeSurnames {
		m[s[0]] = append(m[s[0]], s)
	}
	return m
}()

// nameProbabilities is the exact probability of every producible full name,
// mirroring Name's draw: first uniform over firstNames; mode uniform over 3;
// mode 0 a surname uniform over the alliterative surnames sharing the first
// name's initial (or the whole alliterative table when none does), mode 1
// uniform over punSurnames, mode 2 uniform over plainSurnames. Built in table
// order and returned with its keys sorted, so nothing order-dependent reaches
// NameSpace (GR#21).
func nameProbabilities() (keys []string, prob map[string]float64) {
	prob = map[string]float64{}
	pFirst := 1.0 / float64(len(firstNames))
	for _, first := range firstNames {
		cands := alliterIndex[first[0]]
		if len(cands) == 0 {
			cands = alliterativeSurnames
		}
		for _, sur := range cands {
			prob[first+" "+sur] += pFirst / 3 / float64(len(cands))
		}
		for _, sur := range punSurnames {
			prob[first+" "+sur] += pFirst / 3 / float64(len(punSurnames))
		}
		for _, sur := range plainSurnames {
			prob[first+" "+sur] += pFirst / 3 / float64(len(plainSurnames))
		}
	}
	for k := range prob {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, prob
}

// DistinctNames is the number of different full names the tables can produce
// at all (the support of the draw). It is NOT the collision denominator: the
// draw is non-uniform (mode 0 is initial-matched, so a first name with few
// alliterative surnames concentrates its mass), so use NameSpace for odds.
func DistinctNames() int {
	keys, _ := nameProbabilities()
	return len(keys)
}

// NameSpace is the EFFECTIVE name-space size: 1 / sum(p_i^2) over the exact
// per-name probabilities of Name's draw (the inverse Simpson index). Two
// independent citizens share a name with probability exactly 1/NameSpace(), so
// the package doc's n(n-1)/(2*NameSpace()) is the true expected number of
// colliding pairs. For a perfectly uniform draw it equals DistinctNames();
// the alliterative mode makes it smaller (round r1 F1: the previous
// len(first)*len(surnames) claim, 7344, claimed 7344 against a real effective
// space of about 1127 -- predicted collisions were ~6.5x too low; the draw was
// left as is so every golden name pin and the whimsy ruling stand).
func NameSpace() int {
	keys, prob := nameProbabilities()
	var sumSq float64
	for _, k := range keys {
		sumSq += prob[k] * prob[k]
	}
	return int(1/sumSq + 0.5)
}

// guidBrand is the GUID's leading label. Kept as a bare word (no trailing
// hyphen) so the registry source scanner does not mistake the GUID format for a
// dynamically-built MET- error code (BUG-008/BUG-038).
const guidBrand = "MET"

// GUID formats the citizen id as MET-XXXX-XXXX-XXXX-XXXX (the uint64 in
// upper-case hex, four groups of four). Injective: the id is recoverable
// from the string, so two citizens can never share a GUID.
func GUID(id uint64) string {
	return fmt.Sprintf("%s-%04X-%04X-%04X-%04X", guidBrand, (id>>48)&0xffff, (id>>32)&0xffff, (id>>16)&0xffff, id&0xffff)
}

// ParseGUID inverts GUID. ok is false for anything GUID could not have
// produced (wrong shape, lower-case hex, stray characters).
func ParseGUID(s string) (id uint64, ok bool) {
	const guidLen = len(guidBrand) + 1 + 4*4 + 3
	if len(s) != guidLen || !strings.HasPrefix(s, guidBrand+"-") {
		return 0, false
	}
	parts := strings.Split(s[len(guidBrand)+1:], "-")
	if len(parts) != 4 {
		return 0, false
	}
	for _, p := range parts {
		if len(p) != 4 {
			return 0, false
		}
		var v uint64
		for i := 0; i < 4; i++ {
			c := p[i]
			var d uint64
			switch {
			case c >= '0' && c <= '9':
				d = uint64(c - '0')
			case c >= 'A' && c <= 'F':
				d = uint64(c-'A') + 10
			default:
				return 0, false
			}
			v = v<<4 | d
		}
		id = id<<16 | v
	}
	return id, true
}

// Name returns the whimsical deterministic name for id.
func Name(id uint64) string {
	s := det.NewStream(PersonaSeed, id, 0, "persona.name")
	first := firstNames[s.IntN(int64(len(firstNames)))]
	mode := s.IntN(3)
	var sur string
	switch mode {
	case 0:
		cands := alliterIndex[first[0]]
		if len(cands) == 0 {
			sur = alliterativeSurnames[s.IntN(int64(len(alliterativeSurnames)))]
		} else {
			sur = cands[s.IntN(int64(len(cands)))]
		}
	case 1:
		sur = punSurnames[s.IntN(int64(len(punSurnames)))]
	default:
		sur = plainSurnames[s.IntN(int64(len(plainSurnames)))]
	}
	return first + " " + sur
}

// AvatarOf returns the cartoon-avatar recipe for id.
func AvatarOf(id uint64) Avatar {
	s := det.NewStream(PersonaSeed, id, 0, "persona.avatar")
	seed := s.Uint64()
	return Avatar{
		Seed:      seed,
		Skin:      uint8(s.IntN(AvatarSkinTones)),
		HairStyle: uint8(s.IntN(AvatarHairStyles)),
		HairColor: uint8(s.IntN(AvatarHairColours)),
		Eyes:      uint8(s.IntN(AvatarEyeColours)),
		Outfit:    uint8(s.IntN(AvatarOutfits)),
		Accessory: uint8(s.IntN(AvatarAccessories)),
	}
}

// Of derives the whole persona for id.
func Of(id uint64) Persona {
	return Persona{GUID: GUID(id), Name: Name(id), Avatar: AvatarOf(id)}
}

// NOTE (round r1 minor, Aaron ruling pending): rows past 250 saturate at band
// Z, so labels repeat there; a larger label scheme vs accepting it is undecided.
//
// AddressLabel renders a world-grid cell as the house coordinate label
// ("<row letter>,<column>"), the same convention as the webconsole's
// coordLabel(x, y) in webconsole/src/sim/data.ts: the letter is the row band
// (rowBand rows per letter, saturating at Z), the number is x+1.
func AddressLabel(x, y int) string {
	const rowBand = 10 // webconsole/src/sim/data.ts ROW_BAND
	band := y / rowBand
	if band < 0 {
		band = 0
	}
	if band > 25 {
		band = 25
	}
	return fmt.Sprintf("%c,%d", 'A'+band, x+1)
}
