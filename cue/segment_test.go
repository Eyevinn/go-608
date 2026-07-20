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
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "A"))},
		{Time: 2 * s, Screen: screen(whiteRow(14, "A"), whiteRow(15, "B"))},
		{Time: 3 * s, Screen: screen(whiteRow(14, "B"), whiteRow(15, "C"))},
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

// TestSegmentPaintOnCuePerChange: in-place changes each cut a new cue.
func TestSegmentPaintOnCuePerChange(t *testing.T) {
	changes := []TimedScreen{
		{Time: 1 * s, Screen: screen(whiteRow(15, "PA"))},
		{Time: 2 * s, Screen: screen(whiteRow(15, "PAINT"))},
		{Time: 3 * s, Screen: cta608.Screen{}},
	}
	got := Segment(changes, SegmentOptions{})
	want := []TimedCue{
		{Start: 1 * s, End: 2 * s, Content: screen(whiteRow(15, "PA"))},
		{Start: 2 * s, End: 3 * s, Content: screen(whiteRow(15, "PAINT"))},
	}
	if !cuesEqual(got, want) {
		t.Fatalf("paint-on segmentation:\n got %v\nwant %v", got, want)
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
