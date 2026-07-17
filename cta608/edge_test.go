package cta608

import (
	"strings"
	"testing"
)

// TestStringers exercises the String() methods (public debug output) for every
// token kind and the enum default branches.
func TestStringers(t *testing.T) {
	cases := []struct {
		tok  Token
		want string
	}{
		{Chars{"hi"}, `Chars("hi")`},
		{PAC{Row: 3, Indent: NoIndent, Pen: Pen{Color: Red}}, "PAC(row=3 red)"},
		{PAC{Row: 3, Indent: 8, Pen: Pen{Color: White, Underline: true}}, "PAC(row=3 indent=8 white underline)"},
		{MidRow{Pen: Pen{Color: Green, Italic: true}}, "MidRow(green italic)"},
		{TabOffset{Columns: 2}, "Tab(2)"},
		{BackgroundAttr{Pen: Pen{Background: Blue}}, "Background(default bg=blue)"},
		{SetMode{Mode: RollUp, RollUpRows: 3}, "SetMode(roll-up-3)"},
		{SetMode{Mode: PaintOn}, "SetMode(paint-on)"},
		{Command{Op: CR}, "Command(CR)"},
	}
	for _, c := range cases {
		if got := c.tok.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}

	// Enum String default branches.
	if got := Color(200).String(); !strings.HasPrefix(got, "Color(") {
		t.Errorf("Color(200).String() = %q", got)
	}
	if got := Mode(200).String(); !strings.HasPrefix(got, "Mode(") {
		t.Errorf("Mode(200).String() = %q", got)
	}
	if got := Op(200).String(); !strings.HasPrefix(got, "Op(") {
		t.Errorf("Op(200).String() = %q", got)
	}
	// A named color and the default color.
	if White.String() != "white" || ColDefault.String() != "default" {
		t.Errorf("color names wrong: %q %q", White, ColDefault)
	}
}

// TestSerializeClampsOutOfRange verifies out-of-range fields are clamped rather
// than producing invalid bytes.
func TestSerializeClampsOutOfRange(t *testing.T) {
	// Row 99 clamps to 15, indent 99 clamps to 28, tab 9 clamps to 3, roll-up 9
	// clamps to 4.
	toks := []Token{
		PAC{Row: 99, Indent: 99, Pen: Pen{Color: White}},
		TabOffset{Columns: 9},
		SetMode{Mode: RollUp, RollUpRows: 9},
	}
	data := Serialize(toks, SerializeOptions{Doubling: DoublingOff})
	got, err := Parse(data, ParseOptions{ValidateParity: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []Token{
		PAC{Row: 15, Indent: 28, Pen: Pen{Color: White}},
		TabOffset{Columns: 3},
		SetMode{Mode: RollUp, RollUpRows: 4},
	}
	if !tokensEqual(got, want) {
		t.Fatalf("clamp round-trip\n got: %s\nwant: %s", tokStr(got), tokStr(want))
	}
}

// TestPenIsComparable verifies Pen is a comparable value: == is meaningful and
// it works as a map key (the background is a sentinel Color, never a pointer).
func TestPenIsComparable(t *testing.T) {
	a := Pen{Color: Red, Underline: true}
	b := Pen{Color: Red, Underline: true}
	c := Pen{Color: Red, Underline: true, Background: Blue}
	if a != b {
		t.Errorf("equal pens compared unequal")
	}
	if a == c {
		t.Errorf("pens differing only in background compared equal")
	}
	seen := map[Pen]int{a: 1}
	if seen[b] != 1 {
		t.Errorf("Pen not usable as a map key")
	}
}

// TestBackgroundAttrEmptyIsNoOp verifies a BackgroundAttr that expresses neither
// a background nor black-foreground emits nothing.
func TestBackgroundAttrEmptyIsNoOp(t *testing.T) {
	data := Serialize([]Token{BackgroundAttr{}}, SerializeOptions{})
	if len(data) != 0 {
		t.Fatalf("empty BackgroundAttr emitted % x", data)
	}
}
