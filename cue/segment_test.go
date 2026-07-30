package cue

import (
	"testing"
	"time"

	"github.com/Eyevinn/go-608/cta608"
)

// --- test helpers -----------------------------------------------------------

// whiteRow builds a single white run at column 0 on the given row index.
func whiteRow(index int, text string) cta608.Row {
	return cta608.Row{
		Index:     index,
		Displayed: true,
		Runs:      []cta608.Run{{Column: 0, Text: text, Pen: cta608.Pen{Color: cta608.White}}},
	}
}

// screen assembles a Screen from rows.
func screen(rows ...cta608.Row) cta608.Screen { return cta608.Screen{Rows: rows} }

// cuesEqual compares two cue lists by Start/End and semantic screen equality.
func cuesEqual(a, b []TimedCue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start != b[i].Start || a[i].End != b[i].End {
			return false
		}
		if !screenEqual(a[i].Content, b[i].Content) {
			return false
		}
	}
	return true
}

const s = time.Second

// --- Segment tests ----------------------------------------------------------

// TestSegmentPopOnOneCuePerCaption: a pop-on caption appears, is erased (a gap),
// and a second caption appears — one cue per caption, the gap producing none.
func TestSegmentPopOnOneCuePerCaption(t *testing.T) {
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "HELLO"))},
		{Time: 3 * s, Screen: cta608.Screen{}}, // EDM -> gap
		{Time: 4 * s, Screen: screen(whiteRow(15, "WORLD"))},
		{Time: 6 * s, Screen: cta608.Screen{}}, // EDM -> gap
	}
	got := Segment(changes, SegmentOptions{})
	want := []TimedCue{
		{Start: 1 * s, End: 3 * s, Content: screen(whiteRow(15, "HELLO"))},
		{Start: 4 * s, End: 6 * s, Content: screen(whiteRow(15, "WORLD"))},
	}
	if !cuesEqual(got, want) {
		t.Fatalf("pop-on segmentation:\n got %v\nwant %v", got, want)
	}
}

// TestSegmentRollUpOneCuePerScrollStep: each CR scroll is a displayed-screen
// change, so the visible lines repeat across one cue per step (design note W3).
func TestSegmentRollUpOneCuePerScrollStep(t *testing.T) {
	// Every step here is a scroll, never plain typing, so coalescing does not apply
	// and the cue-per-scroll-step rule is visible on its own.
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "A")), Mode: cta608.RollUp},
		{Time: 2 * s, Screen: screen(whiteRow(14, "A"), whiteRow(15, "B")), Mode: cta608.RollUp},
		{Time: 3 * s, Screen: screen(whiteRow(14, "B"), whiteRow(15, "C")), Mode: cta608.RollUp},
	}
	got := Segment(changes, SegmentOptions{DefaultDur: 1 * s})
	if len(got) != 3 {
		t.Fatalf("want one cue per scroll step (3), got %d: %v", len(got), got)
	}
	// The middle line "B" repeats: bottom of cue 1's successor and top of cue 2.
	if !screenEqual(got[1].Content, screen(whiteRow(14, "A"), whiteRow(15, "B"))) {
		t.Errorf("scroll step 2 content = %v", got[1].Content)
	}
	if got[0].End != 2*s || got[1].Start != 2*s || got[1].End != 3*s {
		t.Errorf("scroll boundaries wrong: %v", got)
	}
	// The dangling final step honors DefaultDur.
	if got[2].Start != 3*s || got[2].End != 4*s {
		t.Errorf("dangling roll-up cue = [%v,%v], want [3s,4s]", got[2].Start, got[2].End)
	}
}

// TestSegmentPaintOnCoalescesBurst: consecutive in-place growth is one cue, spanning
// from the first characters to the erase, and carrying the completed text.
func TestSegmentPaintOnCoalescesBurst(t *testing.T) {
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "PA")), Mode: cta608.PaintOn},
		{Time: 2 * s, Screen: screen(whiteRow(15, "PAINT")), Mode: cta608.PaintOn},
		{Time: 3 * s, Screen: cta608.Screen{}, Mode: cta608.PaintOn},
	}
	want := []TimedCue{
		{Start: 1 * s, End: 3 * s, Content: screen(whiteRow(15, "PAINT"))},
	}
	if got := Segment(changes, SegmentOptions{}); !cuesEqual(got, want) {
		t.Fatalf("paint-on segmentation:\n got %v\nwant %v", got, want)
	}

	// CoalesceNone is the faithful rendering: a cue per displayed-screen change.
	wantNone := []TimedCue{
		{Start: 1 * s, End: 2 * s, Content: screen(whiteRow(15, "PA"))},
		{Start: 2 * s, End: 3 * s, Content: screen(whiteRow(15, "PAINT"))},
	}
	if got := Segment(changes, SegmentOptions{Coalesce: CoalesceNone}); !cuesEqual(got, wantNone) {
		t.Fatalf("paint-on with CoalesceNone:\n got %v\nwant %v", got, wantNone)
	}
}

// TestSegmentEmptyScreenProducesGap: a leading empty screen opens no cue.
func TestSegmentEmptyScreenProducesGap(t *testing.T) {
	changes := []TimedScreen{
		{Time: 0, Screen: cta608.Screen{}}, // empty from the start: no cue
		{Time: 1 * s, Screen: screen(whiteRow(15, "HI"))},
		{Time: 2 * s, Screen: cta608.Screen{}},
	}
	got := Segment(changes, SegmentOptions{})
	if len(got) != 1 {
		t.Fatalf("want exactly one cue, got %d: %v", len(got), got)
	}
	if got[0].Start != 1*s || got[0].End != 2*s {
		t.Errorf("cue window = [%v,%v], want [1s,2s]", got[0].Start, got[0].End)
	}
}

// TestSegmentNoOpChangeCoalesced: a repeated identical screen is not a boundary.
func TestSegmentNoOpChangeCoalesced(t *testing.T) {
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "HI"))},
		{Time: 2 * s, Screen: screen(whiteRow(15, "HI"))}, // identical: no cut
		{Time: 3 * s, Screen: cta608.Screen{}},
	}
	got := Segment(changes, SegmentOptions{})
	if len(got) != 1 || got[0].Start != 1*s || got[0].End != 3*s {
		t.Fatalf("no-op change should coalesce into [1s,3s], got %v", got)
	}
}

// TestSegmentDanglingStreamEnd: a caption still shown at stream end takes
// StreamEnd when set, else Start + DefaultDur.
func TestSegmentDanglingStreamEnd(t *testing.T) {
	changes := []TimedScreen{{Time: 4 * s, Screen: screen(whiteRow(15, "LAST"))}}

	withEnd := Segment(changes, SegmentOptions{StreamEnd: 9 * s, DefaultDur: 2 * s})
	if len(withEnd) != 1 || withEnd[0].End != 9*s {
		t.Errorf("StreamEnd should win: got %v", withEnd)
	}

	withDefault := Segment(changes, SegmentOptions{DefaultDur: 2 * s})
	if len(withDefault) != 1 || withDefault[0].End != 6*s {
		t.Errorf("DefaultDur fallback: want End 6s, got %v", withDefault)
	}

	// A StreamEnd not after Start falls back to the default duration.
	badEnd := Segment(changes, SegmentOptions{StreamEnd: 1 * s, DefaultDur: 2 * s})
	if len(badEnd) != 1 || badEnd[0].End != 6*s {
		t.Errorf("StreamEnd before Start should fall back to DefaultDur: got %v", badEnd)
	}
}

// TestSegmentUnsortedInput: Segment sorts defensively.
func TestSegmentUnsortedInput(t *testing.T) {
	changes := []TimedScreen{
		{Time: 4 * s, Screen: cta608.Screen{}},
		{Time: 1 * s, Screen: screen(whiteRow(15, "HI"))},
	}
	got := Segment(changes, SegmentOptions{})
	if len(got) != 1 || got[0].Start != 1*s || got[0].End != 4*s {
		t.Fatalf("unsorted input should still yield [1s,4s], got %v", got)
	}
}

// TestSegmentEmptyInput: no changes -> no cues.
func TestSegmentEmptyInput(t *testing.T) {
	if got := Segment(nil, SegmentOptions{}); got != nil {
		t.Fatalf("nil input should yield nil, got %v", got)
	}
}

// TestSegmentRollUpCoalescesTyping is the case that motivated Coalesce: roll-up
// writes straight to the displayed screen, so a line arriving two characters at a
// time used to cut a cue per byte pair. Typing must not be a boundary; the scroll
// that follows it must.
func TestSegmentRollUpCoalescesTyping(t *testing.T) {
	ru := func(ms int, rows ...cta608.Row) TimedScreen {
		return TimedScreen{
			Time: time.Duration(ms) * time.Millisecond, Screen: screen(rows...), Mode: cta608.RollUp,
		}
	}
	changes := []TimedScreen{
		// "HELLO" typed onto the base row, two characters at a time.
		ru(1000, whiteRow(15, "HE")),
		ru(1033, whiteRow(15, "HELL")),
		ru(1066, whiteRow(15, "HELLO")),
		// CR scrolls it up and the next line is typed.
		ru(1100, whiteRow(14, "HELLO")),
		ru(1133, whiteRow(14, "HELLO"), whiteRow(15, "WO")),
		ru(1166, whiteRow(14, "HELLO"), whiteRow(15, "WORLD")),
	}

	got := Segment(changes, SegmentOptions{DefaultDur: 1 * s})
	if len(got) != 2 {
		t.Fatalf("want 2 cues (one per scroll step), got %d: %v", len(got), got)
	}
	// Cue 1 starts at the first characters and carries the completed line.
	if got[0].Start != 1000*time.Millisecond || got[0].End != 1100*time.Millisecond {
		t.Errorf("cue 0 = [%v,%v], want [1s,1.1s]", got[0].Start, got[0].End)
	}
	if !screenEqual(got[0].Content, screen(whiteRow(15, "HELLO"))) {
		t.Errorf("cue 0 content = %v, want the completed line", got[0].Content)
	}
	// Cue 2 opens on the scroll and carries the completed two-line window.
	if got[1].Start != 1100*time.Millisecond {
		t.Errorf("cue 1 starts at %v, want the scroll instant 1.1s", got[1].Start)
	}
	if !screenEqual(got[1].Content, screen(whiteRow(14, "HELLO"), whiteRow(15, "WORLD"))) {
		t.Errorf("cue 1 content = %v, want the completed window", got[1].Content)
	}

	// Without coalescing every change is a boundary again.
	if none := Segment(changes, SegmentOptions{Coalesce: CoalesceNone}); len(none) != len(changes) {
		t.Errorf("CoalesceNone gave %d cues, want %d (one per change)", len(none), len(changes))
	}
}

// TestSegmentPopOnNeverCoalesces is the guard that makes coalescing safe. A pop-on
// caption replaced by a longer one on the same row is, by screen alone, identical to
// a line being typed — so gating on the caption mode is the only thing separating
// them. Merging these two would silently lose a caption.
func TestSegmentPopOnNeverCoalesces(t *testing.T) {
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "HELLO")), Mode: cta608.PopOn},
		{Time: 2 * s, Screen: screen(whiteRow(15, "HELLO WORLD")), Mode: cta608.PopOn},
	}
	got := Segment(changes, SegmentOptions{DefaultDur: 1 * s})
	if len(got) != 2 {
		t.Fatalf("pop-on must not coalesce even when the text grows; got %d cues: %v", len(got), got)
	}
	if !screenEqual(got[0].Content, screen(whiteRow(15, "HELLO"))) {
		t.Errorf("cue 0 content = %v, want the first caption intact", got[0].Content)
	}

	// The zero-value Mode is PopOn, so a producer that does not set it keeps the
	// one-cue-per-change behaviour rather than silently coalescing.
	unset := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "HELLO"))},
		{Time: 2 * s, Screen: screen(whiteRow(15, "HELLO WORLD"))},
	}
	if u := Segment(unset, SegmentOptions{DefaultDur: 1 * s}); len(u) != 2 {
		t.Errorf("unset Mode coalesced (%d cues); the zero value must be conservative", len(u))
	}
}

// Growth means the screen gained content in exactly one place. Adding a row counts
// — a two-line paint-on caption is one cue showing both lines, which is what is on
// screen — while overwriting, losing a row, or changing two rows at once do not.
func TestSegmentCoalesceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		second   cta608.Screen
		wantCues int
	}{
		{"grows on one row", screen(whiteRow(15, "ABCD")), 1},
		// A second line of the same paint-on caption: still one cue, both rows.
		{"adds another row", screen(whiteRow(15, "AB"), whiteRow(13, "XY")), 1},
		{"overwrites rather than extends", screen(whiteRow(15, "XY")), 2},
		{"erased", cta608.Screen{}, 1}, // the erase closes the cue and opens none
		// One byte pair writes at most two characters on one row, so two rows
		// changing together cannot be typing.
		{"two rows change at once", screen(whiteRow(15, "ABCD"), whiteRow(14, "Q")), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes := []TimedScreen{
				{Time: 1 * s, Screen: screen(whiteRow(15, "AB")), Mode: cta608.PaintOn},
				{Time: 2 * s, Screen: tc.second, Mode: cta608.PaintOn},
			}
			got := Segment(changes, SegmentOptions{DefaultDur: 1 * s})
			if len(got) != tc.wantCues {
				t.Errorf("got %d cues, want %d: %v", len(got), tc.wantCues, got)
			}
		})
	}
}

// A run's pen must match for growth: a restyle is a change in kind, not more text.
func TestSegmentCoalesceRequiresSamePen(t *testing.T) {
	yellow := cta608.Row{
		Index: 15, Displayed: true,
		Runs: []cta608.Run{{Column: 0, Text: "ABCD", Pen: cta608.Pen{Color: cta608.Yellow}}},
	}
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "AB")), Mode: cta608.PaintOn},
		{Time: 2 * s, Screen: screen(yellow), Mode: cta608.PaintOn},
	}
	if got := Segment(changes, SegmentOptions{DefaultDur: 1 * s}); len(got) != 2 {
		t.Errorf("a recolored row coalesced (%d cues), want 2", len(got))
	}
}
