package cue

import (
	"testing"

	"github.com/Eyevinn/go-608/cta608"
)

// decodeToScreens replays compiled token transitions through a Decoder,
// snapshotting the displayed Screen after each transition — the timed-screen
// timeline a 608 consumer would observe.
func decodeToScreens(tts []TimedTokens) []TimedScreen {
	var dec cta608.Decoder
	out := make([]TimedScreen, 0, len(tts))
	for _, tt := range tts {
		dec.Push(tt.Tokens)
		out = append(out, TimedScreen{Time: tt.Time, Screen: dec.Screen()})
	}
	return out
}

// TestCompileRoundTripNonOverlapping is the acceptance round-trip: Compile a set
// of non-overlapping pop-on cues, decode the tokens back into displayed screens,
// re-Segment them, and require the result to be semantically equivalent to the
// originals (SPEC §8.2 round-trip; not byte-exact, by design W1).
func TestCompileRoundTripNonOverlapping(t *testing.T) {
	cues := []TimedCue{
		{Start: 0, End: 2 * s, Content: screen(whiteRow(14, "LINE ONE"), whiteRow(15, "LINE TWO"))},
		{Start: 3 * s, End: 5 * s, Content: screen(whiteRow(15, "SECOND"))}, // a gap at [2s,3s]
		{Start: 5 * s, End: 7 * s, Content: screen(whiteRow(15, "THIRD"))},  // back-to-back with SECOND
	}

	tts := Compile(cues)
	roundTripped := Segment(decodeToScreens(tts), SegmentOptions{})

	if !cuesEqual(roundTripped, cues) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", roundTripped, cues)
	}
}

// TestCompileEmitsPopOnTransitions checks the token shape at each boundary: a
// build+flip (EOC) when a caption appears, and an EDM when the display clears.
func TestCompileEmitsPopOnTransitions(t *testing.T) {
	cues := []TimedCue{{Start: 1 * s, End: 3 * s, Content: screen(whiteRow(15, "HI"))}}
	tts := Compile(cues)

	if len(tts) != 2 {
		t.Fatalf("want two transitions (build, clear), got %d: %v", len(tts), tts)
	}
	if tts[0].Time != 1*s || !hasOp(tts[0].Tokens, cta608.EOC) {
		t.Errorf("first transition should flip on at 1s with EOC: %v", tts[0])
	}
	if tts[1].Time != 3*s || !hasOp(tts[1].Tokens, cta608.EDM) {
		t.Errorf("second transition should clear at 3s with EDM: %v", tts[1])
	}
}

// TestCompileMergeByPositionLaterCueWins: two cues overlap in time and share a
// row; the later-starting cue must win that row (design note W7).
func TestCompileMergeByPositionLaterCueWins(t *testing.T) {
	cues := []TimedCue{
		{Start: 0, End: 4 * s, Content: screen(whiteRow(15, "AAA"))},
		{Start: 1 * s, End: 4 * s, Content: screen(whiteRow(15, "BBB"))}, // later, same row
	}
	screens := decodeToScreens(Compile(cues))

	// The last non-empty displayed screen (while both are active) shows BBB.
	var got cta608.Screen
	for _, ts := range screens {
		if len(ts.Screen.Rows) > 0 {
			got = ts.Screen
		}
	}
	if !screenEqual(got, screen(whiteRow(15, "BBB"))) {
		t.Fatalf("later cue should win the shared row: got %v", got)
	}
}

// TestCompileMergeByPositionUnion: cues on different rows are both visible while
// they overlap (design note W7).
func TestCompileMergeByPositionUnion(t *testing.T) {
	cues := []TimedCue{
		{Start: 0, End: 4 * s, Content: screen(whiteRow(14, "TOP"))},
		{Start: 1 * s, End: 4 * s, Content: screen(whiteRow(15, "BOTTOM"))},
	}
	screens := decodeToScreens(Compile(cues))

	var union cta608.Screen
	for _, ts := range screens {
		if len(ts.Screen.Rows) == 2 {
			union = ts.Screen
		}
	}
	if !screenEqual(union, screen(whiteRow(14, "TOP"), whiteRow(15, "BOTTOM"))) {
		t.Fatalf("overlapping cues on different rows should both show: got %v", union)
	}
}

// TestCompileEmpty: no cues -> no transitions.
func TestCompileEmpty(t *testing.T) {
	if got := Compile(nil); got != nil {
		t.Fatalf("nil cues should compile to nil, got %v", got)
	}
}

// hasOp reports whether toks contains a Command with the given Op.
func hasOp(toks []cta608.Token, op cta608.Op) bool {
	for _, tok := range toks {
		if c, ok := tok.(cta608.Command); ok && c.Op == op {
			return true
		}
	}
	return false
}
