package cta608

import (
	"fmt"
	"strings"
)

// Color is a CTA-608 color attribute. It is a small enum (never a string) so
// that Pen stays a comparable value. The zero value ColDefault renders as
// white in the foreground and as "no background" for a background color.
type Color uint8

// Color values. The order mirrors the wire color index used by PAC and mid-row
// codes (white..magenta), with Black and Transparent appended for backgrounds.
const (
	ColDefault  Color = iota // foreground -> white; background -> none
	White                    // 1
	Green                    // 2
	Blue                     // 3
	Cyan                     // 4
	Red                      // 5
	Yellow                   // 6
	Magenta                  // 7
	Black                    // 8 (background, or the black-foreground attribute)
	Transparent              // 9 (transparent background)
)

// String returns the lowercase color name, matching the ubiquitous language.
func (c Color) String() string {
	switch c {
	case ColDefault:
		return "default"
	case White:
		return "white"
	case Green:
		return "green"
	case Blue:
		return "blue"
	case Cyan:
		return "cyan"
	case Red:
		return "red"
	case Yellow:
		return "yellow"
	case Magenta:
		return "magenta"
	case Black:
		return "black"
	case Transparent:
		return "transparent"
	default:
		return fmt.Sprintf("Color(%d)", uint8(c))
	}
}

// Pen is the styling applied to a run of characters: a foreground Color, the
// Italic and Underline flags, and an optional Background Color. It is a
// comparable value struct — == works and there is no aliasing — because the
// background is a sentinel Color (Black/Transparent/ColDefault), not a pointer.
type Pen struct {
	Color      Color
	Italic     bool
	Underline  bool
	Background Color
}

// Mode is a caption mode. It is per-channel state carried by SetMode, not a
// property of Screen or Row. Text mode is not modeled (SPEC §1.3).
type Mode uint8

// Mode values.
const (
	PopOn   Mode = iota // Resume Caption Loading (RCL)
	RollUp              // Roll-Up (RU2/RU3/RU4); the row count lives in SetMode.RollUpRows
	PaintOn             // Resume Direct Captioning (RDC)
)

// String returns the mode name.
func (m Mode) String() string {
	switch m {
	case PopOn:
		return "pop-on"
	case RollUp:
		return "roll-up"
	case PaintOn:
		return "paint-on"
	default:
		return fmt.Sprintf("Mode(%d)", uint8(m))
	}
}

// Run is a maximal contiguous span of characters sharing one Pen, positioned at
// an absolute Column (0..31). It is part of the sparse, derived Screen.
type Run struct {
	Column int
	Text   string
	Pen    Pen
}

// Row is one line of the sparse Screen: a 1..15 Index, a Displayed flag
// modeling 608's double buffer, and its character Runs.
type Row struct {
	Index     int
	Displayed bool
	Runs      []Run
}

// Screen is the sparse, derived display state: only the non-empty Rows. It is
// materialized from the token stream (decode) and diffed against (encode); this
// package (the wire boundary) defines the type but does not interpret it.
type Screen struct {
	Rows []Row
}

// Op identifies a miscellaneous two-byte control command carried by Command.
// Mode-setting commands (RCL/RU2-4/RDC) are modeled by SetMode, and Tab Offset
// by TabOffset, so they are not Op values.
type Op uint8

// Op values — the miscellaneous control commands (CTA-608-E §7 / Annex F).
const (
	EOC Op = iota // End Of Caption (flip non-displayed -> displayed)
	EDM           // Erase Displayed Memory
	ENM           // Erase Non-displayed Memory
	CR            // Carriage Return
	BS            // Backspace
	DER           // Delete to End of Row
	TR            // Text Restart (mode switch recognized; text content not modeled)
	RTD           // Resume Text Display
	FON           // Flash On
	AOF           // reserved (historically Alarm Off)
	AON           // reserved (historically Alarm On)
)

// String returns the mnemonic for the op.
func (o Op) String() string {
	switch o {
	case EOC:
		return "EOC"
	case EDM:
		return "EDM"
	case ENM:
		return "ENM"
	case CR:
		return "CR"
	case BS:
		return "BS"
	case DER:
		return "DER"
	case TR:
		return "TR"
	case RTD:
		return "RTD"
	case FON:
		return "FON"
	case AOF:
		return "AOF"
	case AON:
		return "AON"
	default:
		return fmt.Sprintf("Op(%d)", uint8(o))
	}
}

// NoIndent is the sentinel PAC.Indent value marking a color-style PAC (one that
// carries a foreground color/italic rather than an indent). A PAC with
// Indent >= 0 is an indent-style PAC, which the standard forces to white.
const NoIndent = -1

// Token is the public sum type of the wire-faithful 608 token stream — the
// spine both Serialize and Parse pivot on. One token corresponds to (at most)
// one on-the-wire command, so a []Token round-trips bytes exactly. The
// unexported token() marker keeps the set closed to this package.
type Token interface {
	token()
	fmt.Stringer
}

// Chars is a run of character data (mirrors a display character-run). Extended
// and special glyphs are just runes in Text; the wire boundary owns their
// two-byte encoding and backspace-and-replace.
type Chars struct {
	Text string
}

// PAC is a Preamble Address Code: it positions the cursor on Row (1..15) and
// sets a base Pen. A color-style PAC has Indent == NoIndent and uses Pen.Color/
// Pen.Italic; an indent-style PAC has Indent in {0,4,..,28} and is forced to
// white (only Pen.Underline is honored).
type PAC struct {
	Row    int
	Indent int
	Pen    Pen
}

// MidRow is a mid-row style change (Table 51): it starts a new character-run
// with the given Pen (color/underline, or italics at the top values).
type MidRow struct {
	Pen Pen
}

// TabOffset shifts the cursor right by Columns (1..3) within a row.
type TabOffset struct {
	Columns int
}

// BackgroundAttr sets a background color, transparent background, or the
// black-foreground attribute (CTA-608-E background/foreground codes). Exactly
// one of these is expressed per token via Pen: Pen.Background set (a color or
// Transparent) is a background code; Pen.Color == Black is the black-foreground
// code (with Pen.Underline).
type BackgroundAttr struct {
	Pen Pen
}

// SetMode switches the caption mode: PopOn (RCL), RollUp (RU2/RU3/RU4, with
// RollUpRows in {2,3,4}), or PaintOn (RDC).
type SetMode struct {
	Mode       Mode
	RollUpRows int
}

// Command is a miscellaneous two-byte control command (see Op).
type Command struct {
	Op Op
}

func (Chars) token()          {}
func (PAC) token()            {}
func (MidRow) token()         {}
func (TabOffset) token()      {}
func (BackgroundAttr) token() {}
func (SetMode) token()        {}
func (Command) token()        {}

// String renders the character text quoted for readable test output.
func (c Chars) String() string { return fmt.Sprintf("Chars(%q)", c.Text) }

func (p PAC) String() string {
	if p.Indent == NoIndent {
		return fmt.Sprintf("PAC(row=%d %s)", p.Row, penString(p.Pen))
	}
	return fmt.Sprintf("PAC(row=%d indent=%d %s)", p.Row, p.Indent, penString(p.Pen))
}

func (m MidRow) String() string         { return fmt.Sprintf("MidRow(%s)", penString(m.Pen)) }
func (t TabOffset) String() string      { return fmt.Sprintf("Tab(%d)", t.Columns) }
func (b BackgroundAttr) String() string { return fmt.Sprintf("Background(%s)", penString(b.Pen)) }

func (s SetMode) String() string {
	if s.Mode == RollUp {
		return fmt.Sprintf("SetMode(roll-up-%d)", s.RollUpRows)
	}
	return fmt.Sprintf("SetMode(%s)", s.Mode)
}

func (c Command) String() string { return fmt.Sprintf("Command(%s)", c.Op) }

// penString renders a Pen compactly, omitting default/false attributes.
func penString(p Pen) string {
	var b strings.Builder
	b.WriteString(p.Color.String())
	if p.Italic {
		b.WriteString(" italic")
	}
	if p.Underline {
		b.WriteString(" underline")
	}
	if p.Background != ColDefault {
		fmt.Fprintf(&b, " bg=%s", p.Background)
	}
	return b.String()
}
