package cta608

import (
	"testing"
)

// A Decoder is fed one byte pair per video frame in every timed path (that is what
// preserves per-frame timing), so incremental feeding must decode identically to
// feeding the whole buffer. Two 608 constructs straddle a pair boundary and used to
// break across it: a doubled control code and an extended character's fallback.
//
// feedModes returns the displayed screen after feeding data whole and pair by pair.
func feedModes(t *testing.T, data []byte) (whole, perPair Screen) {
	t.Helper()
	var w Decoder
	if err := w.Feed(data); err != nil {
		t.Fatalf("whole Feed: %v", err)
	}
	var p Decoder
	for i := 0; i+1 < len(data); i += 2 {
		if err := p.Feed(data[i : i+2]); err != nil {
			t.Fatalf("Feed at pair %d: %v", i/2, err)
		}
	}
	return w.Screen(), p.Screen()
}

func screenText(s Screen) string {
	out := ""
	for _, r := range s.Rows {
		for _, run := range r.Runs {
			out += run.Text
		}
	}
	return out
}

// An extended character is transmitted as a fallback char pair followed by the
// two-byte extended code, and the receiver backspaces over the fallback. Fed one
// pair per frame the two land in different Feed calls, so the backspace has to reach
// a character already written to the screen — it used to be silently skipped,
// leaving the fallback behind ("CAFÉ" decoding as "CAFEÉ"). This affects every mode,
// pop-on included.
func TestFeedIncrementalExtendedChars(t *testing.T) {
	const text = "CAFÉ ÀU LAIT" // É and À are extended set A
	var enc Encoder
	data := Serialize(enc.Apply(CaptionBlock{
		Mode: PopOn, Anchor: AnchorBottom,
		Lines: []Line{{Align: AlignLeft, Runs: []Run{{Text: text, Pen: Pen{Color: White}}}}},
	}), SerializeOptions{})

	whole, perPair := feedModes(t, data)
	if got := screenText(whole); got != text {
		t.Errorf("whole-buffer decode = %q, want %q", got, text)
	}
	if got := screenText(perPair); got != text {
		t.Errorf("pair-by-pair decode = %q, want %q", got, text)
	}
}

// Serialize doubles control codes on field 1, and a decoder must act on the first
// copy and ignore the second. Roll-up's CR is the case where getting that wrong is
// visible: an uncollapsed second CR scrolls the window twice, dropping a line. The
// collapse used to reset on every Feed call, so a doubled CR split across frames
// over-scrolled.
func TestFeedIncrementalRollUpDoubledCR(t *testing.T) {
	windows := [][]string{{"AAA"}, {"AAA", "BBB"}, {"AAA", "BBB", "CCC"}}
	var enc Encoder
	enc.SetMode(RollUp, 3)
	var data []byte
	for _, w := range windows {
		var lines []Line
		base := 15 - len(w) + 1
		for j, txt := range w {
			lines = append(lines, Line{Row: base + j, Align: AlignLeft,
				Runs: []Run{{Text: txt, Pen: Pen{Color: White}}}})
		}
		data = append(data, Serialize(enc.Apply(CaptionBlock{
			Mode: RollUp, RollUpRows: 3, Lines: lines,
		}), SerializeOptions{})...)
	}

	whole, perPair := feedModes(t, data)
	want := map[int]string{13: "AAA", 14: "BBB", 15: "CCC"}
	for name, s := range map[string]Screen{"whole-buffer": whole, "pair-by-pair": perPair} {
		got := map[int]string{}
		for _, r := range s.Rows {
			for _, run := range r.Runs {
				got[r.Index] += run.Text
			}
		}
		if len(got) != len(want) {
			t.Errorf("%s: %d rows displayed, want %d (%v)", name, len(got), len(want), got)
		}
		for row, text := range want {
			if got[row] != text {
				t.Errorf("%s: row %d = %q, want %q", name, row, got[row], text)
			}
		}
	}
}

// The same continuity is required of every control code, not just CR: a doubled EOC
// split across Feed calls must flip once. Pop-on hid this because a second flip of
// the same buffer is a no-op on the displayed screen; assert the token-level effect
// so a regression cannot hide behind that idempotence.
func TestFeedIncrementalDoubledControlCollapses(t *testing.T) {
	// RCL doubled, then a doubled CR, as Serialize would emit them.
	data := Serialize([]Token{
		SetMode{Mode: RollUp, RollUpRows: 2},
		Command{Op: CR},
	}, SerializeOptions{})

	var p parser
	var got []Token
	for i := 0; i+1 < len(data); i += 2 {
		toks, err := p.parse(data[i:i+2], ParseOptions{})
		if err != nil {
			t.Fatalf("parse at pair %d: %v", i/2, err)
		}
		got = append(got, toks...)
	}
	if len(got) != 2 {
		t.Fatalf("incremental parse yielded %d tokens, want 2 (each doubled pair collapsed): %v", len(got), got)
	}
	if _, ok := got[0].(SetMode); !ok {
		t.Errorf("token 0 = %T, want SetMode", got[0])
	}
	if c, ok := got[1].(Command); !ok || c.Op != CR {
		t.Errorf("token 1 = %v, want Command(CR)", got[1])
	}
}

// Parse itself is unchanged: it starts from a zero parser, so a whole-buffer parse
// keeps its previous behaviour and callers doing Parse-then-Push are unaffected.
func TestParseRemainsStateless(t *testing.T) {
	data := Serialize([]Token{
		SetMode{Mode: RollUp, RollUpRows: 2},
		Command{Op: CR},
	}, SerializeOptions{})
	first, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("Parse is not repeatable: %d then %d tokens", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("token %d differs between calls: %v vs %v", i, first[i], second[i])
		}
	}
}
