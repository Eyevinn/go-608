package srt

import (
	"testing"

	"github.com/Eyevinn/go-608/cta608"
)

// TestNearestColor checks the nearest-of-8 quantization (design note W5): the
// eight exact primaries map to themselves and off-palette values snap to the
// closest 608 color.
func TestNearestColor(t *testing.T) {
	cases := []struct {
		v    string
		want cta608.Color
	}{
		{"#ffffff", cta608.White},
		{"#00ff00", cta608.Green},
		{"#0000ff", cta608.Blue},
		{"#00ffff", cta608.Cyan},
		{"#ff0000", cta608.Red},
		{"#ffff00", cta608.Yellow},
		{"#ff00ff", cta608.Magenta},
		{"#000000", cta608.Black},
		{"#010101", cta608.Black},    // almost black
		{"#e60000", cta608.Red},      // dim red -> red
		{"#123456", cta608.Black},    // dark slate -> black
		{"#f00", cta608.Red},         // 3-digit shorthand
		{"#0f0", cta608.Green},       // 3-digit shorthand
		{"red", cta608.Red},          // CSS keyword
		{"lime", cta608.Green},       // CSS keyword aliasing green
		{"aqua", cta608.Cyan},        // CSS keyword aliasing cyan
		{"chartreuse", cta608.White}, // unknown keyword -> white fallback
	}
	for _, c := range cases {
		if got := parseColor(c.v); got != c.want {
			t.Errorf("parseColor(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

// TestColorHex checks the representative hex emitted for each palette color and
// that white/default emit white (folded, since absence renders white).
func TestColorHex(t *testing.T) {
	cases := []struct {
		c    cta608.Color
		want string
	}{
		{cta608.White, "#ffffff"},
		{cta608.Green, "#00ff00"},
		{cta608.Blue, "#0000ff"},
		{cta608.Cyan, "#00ffff"},
		{cta608.Red, "#ff0000"},
		{cta608.Yellow, "#ffff00"},
		{cta608.Magenta, "#ff00ff"},
		{cta608.Black, "#000000"},
		{cta608.ColDefault, "#ffffff"},
	}
	for _, c := range cases {
		if got := colorHex(c.c); got != c.want {
			t.Errorf("colorHex(%v) = %q, want %q", c.c, got, c.want)
		}
	}
}

// TestParseLineNestedTags checks that nested and interleaved inline tags fold into
// the right per-run Pen, including a font color inside italics.
func TestParseLineNestedTags(t *testing.T) {
	runs := parseLine(`<i><font color="#ff0000">red italic</font> plain</i>`)
	if len(runs) != 2 {
		t.Fatalf("run count = %d: %#v", len(runs), runs)
	}
	if runs[0].Text != "red italic" ||
		runs[0].Pen != (cta608.Pen{Color: cta608.Red, Italic: true}) {
		t.Errorf("run0 = %+v", runs[0])
	}
	if runs[1].Text != " plain" ||
		runs[1].Pen != (cta608.Pen{Color: cta608.White, Italic: true}) {
		t.Errorf("run1 = %+v", runs[1])
	}
}
