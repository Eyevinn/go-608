package cta608

// This file ports the CTA-608-E character, PAC, and mid-row tables verbatim.
// Character glyphs follow CTA-608-E Tables 49, 50 and 5–10 (cross-checked
// against the DASH-IF media-tools byte_to_utf8 table, which agrees entry for
// entry — see docs/research/prior-art-608.md §5.1). PAC row codes follow
// Table 53 and mid-row codes Table 51.
//
// Internal codes are a flat 0x20..0xCF index over the four sets:
//   0x20..0x7F  standard set (modified ASCII, Table 50)
//   0x80..0x8F  special set  (transmitted 0x11 + 0x30..0x3F, Table 49)
//   0x90..0xAF  extended set A (transmitted 0x12 + 0x20..0x3F, Tables 5–7)
//   0xB0..0xCF  extended set B (transmitted 0x13 + 0x20..0x3F, Tables 8–10)
// Extended sets (>= 0x90) use backspace-and-replace; special chars do not.

// basicSubst holds the standard-set slots (0x20..0x7F) that differ from ASCII
// (CTA-608-E Table 50). Every other code in 0x20..0x7F is plain ASCII.
var basicSubst = map[byte]rune{
	0x2a: 'á',
	0x5c: 'é',
	0x5e: 'í',
	0x5f: 'ó',
	0x60: 'ú',
	0x7b: 'ç',
	0x7c: '÷',
	0x7d: 'Ñ',
	0x7e: 'ñ',
	0x7f: '█', // full block (U+2588)
}

// specialChars maps internal codes 0x80..0x8F to runes (Table 49). Index i is
// internal code 0x80+i. 0x89 is the transparent space, rendered as a space.
var specialChars = [16]rune{
	'®', '°', '½', '¿', '™', '¢', '£', '♪',
	'à', ' ', 'è', 'â', 'ê', 'î', 'ô', 'û',
}

// extCharsA maps internal codes 0x90..0xAF to runes (extended set A, Tables
// 5–7: Spanish, Miscellaneous, French). Index i is internal code 0x90+i.
var extCharsA = [32]rune{
	'Á', 'É', 'Ó', 'Ú', 'Ü', 'ü', '‘', '¡',
	'*', '’', '━', '©', '℠', '•', '“', '”',
	'À', 'Â', 'Ç', 'È', 'Ê', 'Ë', 'ë', 'Î',
	'Ï', 'ï', 'Ô', 'Ù', 'ù', 'Û', '«', '»',
}

// extCharsB maps internal codes 0xB0..0xCF to runes (extended set B, Tables
// 8–10: Portuguese, German, Danish). Index i is internal code 0xB0+i.
var extCharsB = [32]rune{
	'Ã', 'ã', 'Í', 'Ì', 'ì', 'Ò', 'ò', 'Õ',
	'õ', '{', '}', '\\', '^', '_', '|', '∼',
	'Ä', 'ä', 'Ö', 'ö', 'ß', '¥', '¤', '┃',
	'Å', 'å', 'Ø', 'ø', '┏', '┓', '┗', '┛',
}

// extFallback maps an extended internal code (0x90..0xCF) to the standard-set
// ASCII rune emitted before its two-byte code (backspace-and-replace). Every
// value here must itself be a standard-set rune so it packs as a plain byte.
var extFallback = map[byte]rune{
	// set A (0x90..0xAF)
	0x90: 'A', 0x91: 'E', 0x92: 'O', 0x93: 'U', 0x94: 'U', 0x95: 'u',
	0x96: '\'', 0x97: '!', 0x98: '?', 0x99: '\'', 0x9a: '-', 0x9b: 'C',
	0x9c: 'S', 0x9d: '?', 0x9e: '"', 0x9f: '"',
	0xa0: 'A', 0xa1: 'A', 0xa2: 'C', 0xa3: 'E', 0xa4: 'E', 0xa5: 'E',
	0xa6: 'e', 0xa7: 'I', 0xa8: 'I', 0xa9: 'i', 0xaa: 'O', 0xab: 'U',
	0xac: 'u', 0xad: 'U', 0xae: '<', 0xaf: '>',
	// set B (0xB0..0xCF)
	0xb0: 'A', 0xb1: 'a', 0xb2: 'I', 0xb3: 'I', 0xb4: 'i', 0xb5: 'O',
	0xb6: 'o', 0xb7: 'O', 0xb8: 'o', 0xb9: '(', 0xba: ')', 0xbb: '/',
	0xbc: '?', 0xbd: '?', 0xbe: '?', 0xbf: '-',
	0xc0: 'A', 0xc1: 'a', 0xc2: 'O', 0xc3: 'o', 0xc4: 's', 0xc5: 'Y',
	0xc6: '?', 0xc7: '?', 0xc8: 'A', 0xc9: 'a', 0xca: 'O', 0xcb: 'o',
	0xcc: '?', 0xcd: '?', 0xce: '?', 0xcf: '?',
}

// internalToRune / runeToInternal are the forward and reverse character maps,
// built once from the tables above. Standard-set codes win reverse collisions
// (e.g. the transparent space 0x89 and the standard space 0x20 both map to
// ' ', but ' ' encodes back to 0x20).
var internalToRune, runeToInternal = buildCharTables()

func buildCharTables() (map[byte]rune, map[rune]byte) {
	fwd := make(map[byte]rune, 0xD0)
	rev := make(map[rune]byte, 0xD0)
	put := func(code byte, r rune) {
		fwd[code] = r
		if _, seen := rev[r]; !seen {
			rev[r] = code
		}
	}
	// Standard set first so it wins reverse collisions.
	for code := 0x20; code <= 0x7f; code++ {
		if r, ok := basicSubst[byte(code)]; ok {
			put(byte(code), r)
		} else {
			put(byte(code), rune(code))
		}
	}
	for i, r := range specialChars {
		put(byte(0x80+i), r)
	}
	for i, r := range extCharsA {
		put(byte(0x90+i), r)
	}
	for i, r := range extCharsB {
		put(byte(0xb0+i), r)
	}
	return fwd, rev
}

// wireColors maps a wire color index (0..6) to a Color; used by PAC and mid-row
// codes. Index 7 is white-italics, handled separately.
var wireColors = [7]Color{White, Green, Blue, Cyan, Red, Yellow, Magenta}

// wireColorIndex returns the 0..6 wire index for a foreground Color. ColDefault
// maps to white (0); colors without a foreground code map to 0 as a fallback.
func wireColorIndex(c Color) int {
	switch c {
	case Green:
		return 1
	case Blue:
		return 2
	case Cyan:
		return 3
	case Red:
		return 4
	case Yellow:
		return 5
	case Magenta:
		return 6
	default: // White, ColDefault, and any non-foreground color
		return 0
	}
}

// bgColorFromWireIndex maps a background wire index (0..7) to a Color
// (White..Black). Transparent is a separate code, not part of this range.
var bgColorFromWireIndex = [8]Color{White, Green, Blue, Cyan, Red, Yellow, Magenta, Black}

// bgWireIndex returns the 0..7 wire index for a background Color (White..Black),
// or -1 if the color has no background code in the 0x10-range.
func bgWireIndex(c Color) int {
	if c >= White && c <= Black {
		return int(c) - int(White)
	}
	return -1
}

// pacRowCode is the first-byte encoding of a PAC row for channel 1 (Table 53).
// upper selects the 0x60-range second byte (the upper of the two rows sharing a
// first byte); the lower row uses the 0x40-range.
type pacRowCode struct {
	byte1 byte
	upper bool
}

// pacRowEncode is indexed by 1-based row (1..15); index 0 is unused.
var pacRowEncode = [16]pacRowCode{
	1:  {0x11, false},
	2:  {0x11, true},
	3:  {0x12, false},
	4:  {0x12, true},
	5:  {0x15, false},
	6:  {0x15, true},
	7:  {0x16, false},
	8:  {0x16, true},
	9:  {0x17, false},
	10: {0x17, true},
	11: {0x10, false},
	12: {0x13, false},
	13: {0x13, true},
	14: {0x14, false},
	15: {0x14, true},
}

// pacRowDecode inverts pacRowEncode: (channel-1 first byte, upper) -> row.
var pacRowDecode = func() map[pacRowCode]int {
	m := make(map[pacRowCode]int, 15)
	for row := 1; row <= 15; row++ {
		m[pacRowEncode[row]] = row
	}
	return m
}()
