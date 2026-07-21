package srt

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
)

// readFixture reads one of the testdata/srt sample files into cues.
func readFixture(t *testing.T, name string) []cue.TimedCue {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "testdata", "srt", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	cues, err := Read(f)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return cues
}

// writeString serializes cues to an SRT string, failing the test on error.
func writeString(t *testing.T, cues []cue.TimedCue) string {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b, cues); err != nil {
		t.Fatalf("write: %v", err)
	}
	return b.String()
}

// TestRoundTripSemantic proves the acceptance-criteria round trip: an SRT file
// read into cues, written back out, and read again yields the same cues (semantic,
// quantized), and the serialized text is stable (idempotent) — even though it is
// not required to be byte-exact against the hand-written fixture.
func TestRoundTripSemantic(t *testing.T) {
	for _, name := range []string{"basic.srt", "styled.srt", "messy.srt"} {
		t.Run(name, func(t *testing.T) {
			cues1 := readFixture(t, name)
			if len(cues1) == 0 {
				t.Fatalf("%s: no cues parsed", name)
			}
			srt2 := writeString(t, cues1)

			cues2, err := Read(strings.NewReader(srt2))
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if !reflect.DeepEqual(cues1, cues2) {
				t.Fatalf("cues changed across round trip:\n first=%#v\nsecond=%#v", cues1, cues2)
			}
			// Writing the re-read cues must reproduce the same text (idempotent).
			if srt3 := writeString(t, cues2); srt3 != srt2 {
				t.Fatalf("serialization not idempotent:\n first=%q\nsecond=%q", srt2, srt3)
			}
		})
	}
}

// TestCuesRoundTrip is the mirror direction: cues authored via the core
// CaptionBlock (bottom-anchored, centered — the canonical form Read produces)
// survive Write -> Read unchanged, styling and all.
func TestCuesRoundTrip(t *testing.T) {
	block := cta608.CaptionBlock{
		Anchor: cta608.AnchorBottom,
		Lines: []cta608.Line{{
			Align: cta608.AlignCenter,
			Runs: []cta608.Run{
				{Column: 0, Text: "red ", Pen: cta608.Pen{Color: cta608.Red}},
				{Column: 4, Text: "italic", Pen: cta608.Pen{Color: cta608.White, Italic: true}},
			},
		}},
	}
	cues := []cue.TimedCue{{
		Start:   2 * time.Second,
		End:     5*time.Second + 500*time.Millisecond,
		Content: block.Screen(),
	}}

	srtText := writeString(t, cues)
	got, err := Read(strings.NewReader(srtText))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !reflect.DeepEqual(cues, got) {
		t.Fatalf("cues -> srt -> cues differ:\n want=%#v\n got=%#v\n srt=%q", cues, got, srtText)
	}
}

// TestStylingIn checks the SRT->608 styling map (design note W5): <font color>
// quantizes to the nearest of 8, <i>/<u> are honored, <b> is dropped, and unknown
// tags are stripped while their text survives.
func TestStylingIn(t *testing.T) {
	cues := readFixture(t, "styled.srt")

	// Block 1: a single yellow run.
	runs := onlyRow(t, cues[0].Content).Runs
	if len(runs) != 1 || runs[0].Text != "Yellow headline" || runs[0].Pen.Color != cta608.Yellow {
		t.Fatalf("block1 runs = %#v", runs)
	}
	// Block 2: plain / italic / plain / underline.
	runs = onlyRow(t, cues[1].Content).Runs
	want := []cta608.Pen{
		{Color: cta608.White},
		{Color: cta608.White, Italic: true},
		{Color: cta608.White},
		{Color: cta608.White, Underline: true},
	}
	if len(runs) != len(want) {
		t.Fatalf("block2 run count = %d, want %d (%#v)", len(runs), len(want), runs)
	}
	for i, w := range want {
		if runs[i].Pen != w {
			t.Errorf("block2 run %d pen = %+v, want %+v", i, runs[i].Pen, w)
		}
	}
	if runs[1].Text != "italic" || runs[3].Text != "underline" {
		t.Errorf("block2 styled text = %q / %q", runs[1].Text, runs[3].Text)
	}
}

// TestStylingInMessy checks lenient reading: <b> is dropped (its text folds into
// the surrounding plain run), an arbitrary hex color quantizes to the nearest of
// 8 (here black), unknown markup is stripped, and a non-standard positioning
// suffix on the timing line is ignored (never honored, W6).
func TestStylingInMessy(t *testing.T) {
	cues := readFixture(t, "messy.srt")
	if len(cues) != 2 {
		t.Fatalf("cue count = %d, want 2", len(cues))
	}
	if cues[0].Start != time.Second || cues[0].End != 3*time.Second {
		t.Fatalf("timing = %v..%v, want 1s..3s (trailing coords must be ignored)", cues[0].Start, cues[0].End)
	}
	runs := onlyRow(t, cues[0].Content).Runs
	if len(runs) != 2 {
		t.Fatalf("block1 runs = %#v", runs)
	}
	if runs[0].Text != "This is bold and " || runs[0].Pen != (cta608.Pen{Color: cta608.White}) {
		t.Errorf("bold not dropped/merged: %+v", runs[0])
	}
	if runs[1].Text != "near black" || runs[1].Pen.Color != cta608.Black {
		t.Errorf("hex color not quantized to black: %+v", runs[1])
	}
	stripped := onlyRow(t, cues[1].Content).Runs
	if len(stripped) != 1 || stripped[0].Text != "Unknown markup is stripped" {
		t.Errorf("unknown tag not stripped: %#v", stripped)
	}
}

// TestStylingOut checks the 608->SRT styling map (design note W5): color ->
// <font color>, italic -> <i>, underline -> <u>; the background is dropped and no
// positioning extension is emitted (W6).
func TestStylingOut(t *testing.T) {
	screen := cta608.Screen{Rows: []cta608.Row{{
		Index:     15,
		Displayed: true,
		Runs: []cta608.Run{{
			Column: 0,
			Text:   "styled",
			Pen: cta608.Pen{
				Color:      cta608.Red,
				Italic:     true,
				Underline:  true,
				Background: cta608.Blue, // must be dropped on output
			},
		}},
	}}}
	out := writeString(t, []cue.TimedCue{{End: time.Second, Content: screen}})

	for _, want := range []string{`<font color="#ff0000">`, "<i>", "<u>", "styled", "</u>", "</i>", "</font>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Background and bold never appear; no {\anX}/coordinate hacks are emitted.
	for _, bad := range []string{"<b>", "bgcolor", "background", `{\an`, "X1:", "Y1:"} {
		if strings.Contains(out, bad) {
			t.Errorf("output unexpectedly contains %q:\n%s", bad, out)
		}
	}
}

// TestBottomCenterAnchor confirms SRT->608 anchors multi-line cues to the bottom
// rows (…,14,15) and centers them (design note W6), with no positioning invented.
func TestBottomCenterAnchor(t *testing.T) {
	cues := readFixture(t, "basic.srt")
	rows := cues[1].Content.Rows // the two-line cue
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}
	if rows[0].Index != 14 || rows[1].Index != 15 {
		t.Fatalf("row indices = %d,%d, want 14,15 (bottom anchor)", rows[0].Index, rows[1].Index)
	}
	// Centered: "Second caption" (14 cols) -> left column (32-14)/2 = 9.
	if got := rows[0].Runs[0].Column; got != 9 {
		t.Errorf("row 14 start column = %d, want 9 (centered)", got)
	}
}

// TestTimestamps round-trips durations through the SRT timestamp format and
// checks parse leniency (short fractions, '.' separator) and clamping.
func TestTimestamps(t *testing.T) {
	cases := []struct {
		d    time.Duration
		text string
	}{
		{0, "00:00:00,000"},
		{time.Hour + 2*time.Minute + 3*time.Second + 456*time.Millisecond, "01:02:03,456"},
		{90 * time.Minute, "01:30:00,000"},
	}
	for _, c := range cases {
		if got := formatTimestamp(c.d); got != c.text {
			t.Errorf("formatTimestamp(%v) = %q, want %q", c.d, got, c.text)
		}
		got, err := parseTimestamp(c.text)
		if err != nil || got != c.d {
			t.Errorf("parseTimestamp(%q) = %v, %v; want %v", c.text, got, err, c.d)
		}
	}
	// Negative clamps to zero.
	if got := formatTimestamp(-time.Second); got != "00:00:00,000" {
		t.Errorf("negative not clamped: %q", got)
	}
	// Lenient parse: '.' separator and a short fraction (".5" == 500 ms).
	if got, _ := parseTimestamp("00:00:01.5"); got != 1500*time.Millisecond {
		t.Errorf("lenient parse = %v, want 1.5s", got)
	}
}

// TestReadTolerantInput checks that a UTF-8 BOM and CRLF line endings are handled.
func TestReadTolerantInput(t *testing.T) {
	const doc = "\uFEFF1\r\n00:00:01,000 --> 00:00:02,000\r\nHi\r\n\r\n"
	cues, err := Read(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(cues) != 1 || cues[0].Start != time.Second {
		t.Fatalf("cues = %#v", cues)
	}
	if r := onlyRow(t, cues[0].Content).Runs; len(r) != 1 || r[0].Text != "Hi" {
		t.Fatalf("runs = %#v", r)
	}
}

// TestReadRejectsMalformed confirms a block that has caption text but no timing
// line is a hard error rather than a silent drop.
func TestReadRejectsMalformed(t *testing.T) {
	const doc = "1\nthis block has no arrow line\nmore text\n"
	if _, err := Read(strings.NewReader(doc)); err == nil {
		t.Fatal("expected an error for a block without a timing line")
	}
}

// onlyRow returns the single row of a one-row screen, failing otherwise.
func onlyRow(t *testing.T, s cta608.Screen) cta608.Row {
	t.Helper()
	if len(s.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %#v", len(s.Rows), s.Rows)
	}
	return s.Rows[0]
}
