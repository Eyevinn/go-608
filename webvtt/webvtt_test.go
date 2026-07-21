package webvtt

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
)

// cell is one occupied grid position used for the semantic comparison: the rune
// plus the styling attributes 608 can carry. Spaces are treated as blank cells
// (no cell recorded) because how a run's leading/interior spaces are split off or
// which Pen they nominally carry is not observable — only inked cells and their
// style are. This is exactly the "semantic, quantized, not byte-exact" round-trip
// the WebVTT<->cue mapping promises (SPEC §8.2, design notes W5/W6).
type cell struct {
	r         rune
	color     cta608.Color
	italic    bool
	underline bool
}

// screenGrid renders a Screen to a (row, column) -> cell map, folding the
// default foreground to white and skipping space cells, so two Screens that paint
// the same inked characters with the same style at the same grid positions
// compare equal regardless of run boundaries or space bookkeeping.
func screenGrid(s cta608.Screen) map[[2]int]cell {
	g := map[[2]int]cell{}
	for _, row := range s.Rows {
		for _, rn := range row.Runs {
			col := rn.Column
			for _, ch := range rn.Text {
				if ch != ' ' {
					color := rn.Pen.Color
					if color == cta608.ColDefault {
						color = cta608.White
					}
					g[[2]int{row.Index, col}] = cell{ch, color, rn.Pen.Italic, rn.Pen.Underline}
				}
				col++
			}
		}
	}
	return g
}

// assertSameGrid fails when two Screens differ on any inked cell.
func assertSameGrid(t *testing.T, want, got cta608.Screen) {
	t.Helper()
	wg, gg := screenGrid(want), screenGrid(got)
	if len(wg) != len(gg) {
		t.Errorf("cell count: want %d, got %d\nwant=%+v\ngot=%+v", len(wg), len(gg), wg, gg)
		return
	}
	for pos, wc := range wg {
		if gc, ok := gg[pos]; !ok || gc != wc {
			t.Errorf("cell at row %d col %d: want %+v, got %+v (ok=%v)", pos[0], pos[1], wc, gc, ok)
		}
	}
}

// mkRun is a Run constructor for readable test screens.
func mkRun(col int, text string, pen cta608.Pen) cta608.Run {
	return cta608.Run{Column: col, Text: text, Pen: pen}
}

// TestReadWriteRoundTrip is the headline acceptance test: a cue list with color,
// italic, underline, multi-row, and positioned content survives Write -> Read as
// the same inked grid and the same timing (SPEC §8.2).
func TestReadWriteRoundTrip(t *testing.T) {
	white := cta608.Pen{Color: cta608.White}
	cues := []cue.TimedCue{
		{
			Start: 1 * time.Second, End: 3 * time.Second,
			Content: cta608.Screen{Rows: []cta608.Row{
				{Index: 15, Displayed: true, Runs: []cta608.Run{mkRun(0, "HELLO WORLD", white)}},
			}},
		},
		{
			Start: 4 * time.Second, End: 6*time.Second + 500*time.Millisecond,
			Content: cta608.Screen{Rows: []cta608.Row{
				{Index: 14, Displayed: true, Runs: []cta608.Run{
					mkRun(4, "RED", cta608.Pen{Color: cta608.Red}),
					mkRun(8, "ITAL", cta608.Pen{Color: cta608.White, Italic: true}),
				}},
				{Index: 15, Displayed: true, Runs: []cta608.Run{
					mkRun(4, "YELLOWUNDER", cta608.Pen{Color: cta608.Yellow, Underline: true}),
				}},
			}},
		},
	}

	var buf bytes.Buffer
	if err := Write(&buf, cues); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(cues) {
		t.Fatalf("cue count: want %d, got %d", len(cues), len(got))
	}
	for i := range cues {
		if got[i].Start != cues[i].Start || got[i].End != cues[i].End {
			t.Errorf("cue %d window: want %v-%v, got %v-%v",
				i, cues[i].Start, cues[i].End, got[i].Start, got[i].End)
		}
		assertSameGrid(t, cues[i].Content, got[i].Content)
	}
}

// TestFileRoundTrip reads a fixture, writes it back, and reads it again, proving
// the read/write pair is stable through the WebVTT text form (not only through
// in-memory cues).
func TestFileRoundTrip(t *testing.T) {
	data, err := os.ReadFile("../testdata/webvtt/sample.vtt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	first, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Read #1: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, first); err != nil {
		t.Fatalf("Write: %v", err)
	}
	second, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read #2: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("cue count changed: %d -> %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Start != second[i].Start || first[i].End != second[i].End {
			t.Errorf("cue %d window drifted: %v-%v -> %v-%v",
				i, first[i].Start, first[i].End, second[i].Start, second[i].End)
		}
		assertSameGrid(t, first[i].Content, second[i].Content)
	}
}

// TestReadFixtureStyling checks that the sample fixture's styling and positioning
// land where expected: green/yellow classes, italic/underline, and the position-
// less first cue anchored to the bottom row.
func TestReadFixtureStyling(t *testing.T) {
	data, err := os.ReadFile("../testdata/webvtt/sample.vtt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cues, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(cues) != 3 {
		t.Fatalf("want 3 cues, got %d", len(cues))
	}

	// Cue 1: position-less -> bottom row 15.
	if r := cues[0].Content.Rows[0].Index; r != 15 {
		t.Errorf("position-less cue row: want 15, got %d", r)
	}

	// Cue 2: two rows, green italic on the first, underlined yellow on the second.
	c2 := cues[1].Content
	if len(c2.Rows) != 2 {
		t.Fatalf("cue 2 rows: want 2, got %d", len(c2.Rows))
	}
	var sawGreen, sawItalic, sawYellowUnder bool
	for _, row := range c2.Rows {
		for _, rn := range row.Runs {
			if rn.Pen.Color == cta608.Green {
				sawGreen = true
			}
			if rn.Pen.Italic {
				sawItalic = true
			}
			if rn.Pen.Color == cta608.Yellow && rn.Pen.Underline {
				sawYellowUnder = true
			}
		}
	}
	if !sawGreen || !sawItalic || !sawYellowUnder {
		t.Errorf("cue 2 styling: green=%v italic=%v yellowUnder=%v", sawGreen, sawItalic, sawYellowUnder)
	}

	// Cue 3: line:0% -> top row 1.
	if r := cues[2].Content.Rows[0].Index; r != 1 {
		t.Errorf("line:0%% cue row: want 1, got %d", r)
	}
}

// TestQuantizationAndBoldDrop covers the "styling in" rules on the styled fixture:
// arbitrary CSS colors quantize to the nearest of 8, bold is dropped, and
// italic/underline survive (SPEC §8.2, design note W5).
func TestQuantizationAndBoldDrop(t *testing.T) {
	data, err := os.ReadFile("../testdata/webvtt/styled.vtt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cues, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(cues) != 3 {
		t.Fatalf("want 3 cues, got %d", len(cues))
	}

	// #00a000 and rgb(255,10,0) quantize to Green and Red.
	if got := cues[0].Content.Rows[0].Runs[0].Pen.Color; got != cta608.Green {
		t.Errorf("#00a000 quantized to %v, want green", got)
	}
	var sawWarnRed bool
	for _, rn := range cues[2].Content.Rows[0].Runs {
		if rn.Pen.Color == cta608.Red {
			sawWarnRed = true
		}
	}
	if !sawWarnRed {
		t.Errorf("rgb(255,10,0) did not quantize to red")
	}

	// The bold cue: no run may carry a "bold" flag (there is none in Pen), and
	// the italic/underline runs must survive.
	var sawItalic, sawUnder bool
	for _, rn := range cues[1].Content.Rows[0].Runs {
		if rn.Pen.Italic {
			sawItalic = true
		}
		if rn.Pen.Underline {
			sawUnder = true
		}
	}
	if !sawItalic || !sawUnder {
		t.Errorf("bold cue: italic=%v underline=%v (both want true)", sawItalic, sawUnder)
	}
}

// TestBackgroundRoundTrip checks the best-effort background carriage: a Pen with a
// background color survives Write -> Read via the bg_ class and its ::cue rule.
func TestBackgroundRoundTrip(t *testing.T) {
	cues := []cue.TimedCue{{
		Start: 0, End: time.Second,
		Content: cta608.Screen{Rows: []cta608.Row{{
			Index: 15, Displayed: true,
			Runs: []cta608.Run{mkRun(0, "BG", cta608.Pen{Color: cta608.White, Background: cta608.Black})},
		}}},
	}}
	var buf bytes.Buffer
	if err := Write(&buf, cues); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if bg := got[0].Content.Rows[0].Runs[0].Pen.Background; bg != cta608.Black {
		t.Errorf("background: want black, got %v", bg)
	}
}

func TestParseTimestamp(t *testing.T) {
	cases := map[string]time.Duration{
		"00:00:01.000": time.Second,
		"01:02:03.400": time.Hour + 2*time.Minute + 3*time.Second + 400*time.Millisecond,
		"02:03.400":    2*time.Minute + 3*time.Second + 400*time.Millisecond, // hour-less
		"00:00:00.5":   500 * time.Millisecond,                               // lenient ms
	}
	for in, want := range cases {
		got, err := parseTimestamp(in)
		if err != nil {
			t.Errorf("parseTimestamp(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseTimestamp(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseTimestamp("bogus"); err == nil {
		t.Errorf("parseTimestamp(bogus): want error")
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(time.Hour + 2*time.Minute + 3*time.Second + 400*time.Millisecond)
	if got != "01:02:03.400" {
		t.Errorf("formatTimestamp = %q, want 01:02:03.400", got)
	}
	if got := formatTimestamp(-time.Second); got != "00:00:00.000" {
		t.Errorf("negative formatTimestamp = %q, want 00:00:00.000", got)
	}
}

func TestNearestColor(t *testing.T) {
	cases := map[rgb]cta608.Color{
		{0x00, 0xa0, 0x00}: cta608.Green,   // dark green -> green
		{0xff, 0x0a, 0x00}: cta608.Red,     // almost red -> red
		{0x10, 0x10, 0x10}: cta608.Black,   // near-black -> black
		{0xf0, 0xf0, 0xf0}: cta608.White,   // near-white -> white
		{0x00, 0x00, 0x90}: cta608.Blue,    // navy-ish -> blue
		{0xf0, 0xf0, 0x00}: cta608.Yellow,  // -> yellow
		{0xf0, 0x00, 0xf0}: cta608.Magenta, // -> magenta
	}
	for in, want := range cases {
		if got := nearestColor(in); got != want {
			t.Errorf("nearestColor(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestMissingHeader(t *testing.T) {
	if _, err := Read(bytes.NewBufferString("not a vtt file")); err == nil {
		t.Errorf("Read without WEBVTT header: want error")
	}
}

// TestPositionMapping exercises the line:/position:/align: <-> Row/Column mapping
// in both directions at representative points, confirming it round-trips within
// the coarse-grid quantization tolerance (design note W6).
func TestPositionMapping(t *testing.T) {
	for _, tc := range []struct{ topRow, blockLeft int }{
		{15, 0}, {14, 4}, {1, 16}, {8, 24}, {15, 31},
	} {
		settings := parseSettings(splitFields(formatSettings(tc.topRow, tc.blockLeft)))
		if got := settings.row(1, 0); got != tc.topRow {
			t.Errorf("row round-trip for topRow=%d: got %d", tc.topRow, got)
		}
		// leftColumn with width 1 (align:start ignores width) should recover blockLeft.
		if got := settings.leftColumn(1); got != tc.blockLeft {
			t.Errorf("column round-trip for blockLeft=%d: got %d", tc.blockLeft, got)
		}
	}
}

// splitFields tokenizes a "line:X% position:Y% align:start" string into the
// fields parseSettings expects (it takes the whitespace-separated tokens after
// the "-->").
func splitFields(s string) []string { return strings.Fields(s) }

// TestDecodeEntity covers the character references decodeEntity handles, including
// decimal and hex numeric references (&#39; / &#x41;) and the named/zero-width set.
func TestDecodeEntity(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"&amp;", "&", true},
		{"&lt;x", "<", true},
		{"&nbsp;", " ", true},
		{"&lrm;", "", true},   // zero-width bidi mark: consumed, no text
		{"&#39;", "'", true},  // decimal numeric reference
		{"&#x41;", "A", true}, // hex numeric reference
		{"&#X41;", "A", true}, // hex, uppercase X
		{"&#0;", "", false},   // NUL is not a valid rune to emit
		{"&bogus;", "", false},
		{"&noterm", "", false},
	}
	for _, c := range cases {
		got, _, ok := decodeEntity(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("decodeEntity(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}

	// End to end: a numeric reference in a cue payload decodes to its rune.
	cues, err := Read(bytes.NewBufferString("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nit&#39;s A&#x42;\n"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(cues) != 1 || len(cues[0].Content.Rows) != 1 {
		t.Fatalf("want one cue with one row, got %+v", cues)
	}
	var b strings.Builder
	for _, r := range cues[0].Content.Rows[0].Runs {
		b.WriteString(r.Text)
	}
	if got := b.String(); got != "it's AB" {
		t.Errorf("payload decoded to %q, want %q", got, "it's AB")
	}
}
