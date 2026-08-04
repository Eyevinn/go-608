package generate

import (
	"fmt"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

func TestNumCues(t *testing.T) {
	cases := []struct {
		unitDurMS, targetMS int64
		want                int
	}{
		{1920, 1000, 1}, // truncates to 1 (1.92 s), never 2 x 0.96 s
		{2002, 1000, 2}, // 1.001 s each: a 60-frame group at 30000/1001
		{1001, 1000, 1}, // 1.001 s: a 30-frame group at 30000/1001
		{2000, 1000, 2},
		{1500, 1000, 1}, // 1.5 s, not 2 x 0.75 s
		{1000, 1000, 1},
		{960, 1000, 1},  // shorter than the period: one cue, never 0
		{501, 1000, 1},  // a 30-frame group at 60000/1001
		{4000, 1000, 4}, // 1.0 s each
		{3840, 1000, 3}, // 1.28 s each, not 4 x 0.96 s
		{2002, 0, 2},    // targetMS<=0 defaults to 1000
		{4000, 2000, 2}, // a period need not be a second
		{4000, 1500, 2}, // 2 x 2.0 s, not 3 x 1.33 s
	}
	for _, c := range cases {
		if got := NumCues(c.unitDurMS, c.targetMS); got != c.want {
			t.Errorf("NumCues(%d,%d) = %d, want %d", c.unitDurMS, c.targetMS, got, c.want)
		}
	}
}

// segCueContent formats a probe cue: line 1 = the cue's UTC time (ms precision),
// line 2 = "SEG <u.Nr>" (constant across the unit's cues). One function serves every
// unit, because the unit number arrives as an argument instead of being closed over —
// which is what lets the same func be handed to both BuildUnitCues and
// WithFlipAtCueStart. This is the shape livesim2/moqlivemock would pass.
func segCueContent(u Unit, cueIdx int, cueStartMS int64) UnitCue {
	ts := time.UnixMilli(cueStartMS).UTC().Format("15:04:05.000")
	seg := fmt.Sprintf("SEG %d", u.Nr)
	white := cta608.Pen{Color: cta608.White}
	yellow := cta608.Pen{Color: cta608.Yellow}
	return UnitCue{Lines: []cta608.Line{
		{Row: 14, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: ts, Pen: white}}},
		{Row: 15, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: seg, Pen: yellow}}},
	}}
}

// decodedFlip is one on-screen caption change observed by the decoder.
type decodedFlip struct {
	frame int
	row14 string
	row15 string
}

// decodeFlips drives frames through carriage + cta608.Decoder and returns the
// (frame, row14, row15) of each on-screen change.
func decodeFlips(t *testing.T, frames []schedule.Frame) []decodedFlip {
	t.Helper()
	var dec cta608.Decoder
	var out []decodedFlip
	for i, f := range frames {
		if len(f.Field1) == 0 {
			continue
		}
		nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
		fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
		if err != nil {
			t.Fatalf("frame %d FieldPairs: %v", i, err)
		}
		if err := dec.Feed(fld1); err != nil {
			t.Fatalf("frame %d decode: %v", i, err)
		}
		if dec.Changed() {
			r14, _, _, _ := rowText(dec.Screen(), 14)
			r15, _, _, _ := rowText(dec.Screen(), 15)
			out = append(out, decodedFlip{frame: i, row14: r14, row15: r15})
		}
	}
	return out
}

// TestBuildUnitCuesTwoPerSegment covers a 2 s segment at 30 fps: 60 frames, N=2
// cues of ~1 s, showing the segment's two per-second timestamps and its
// (constant) segment number.
func TestBuildUnitCuesTwoPerSegment(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60 // 2.0 s at 30 fps (a 2.002 s segment rounds to the same 2 cues)
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	frames, err := BuildUnitCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames}, 1000, segCueContent)
	if err != nil {
		t.Fatalf("BuildUnitCues: %v", err)
	}
	if len(frames) != unitFrames {
		t.Fatalf("got %d frames, want %d", len(frames), unitFrames)
	}

	flips := decodeFlips(t, frames)
	for _, fl := range flips {
		t.Logf("flip @frame %d: row14=%q row15=%q", fl.frame, fl.row14, fl.row15)
	}
	if len(flips) != 2 {
		t.Fatalf("got %d flips, want 2 (one per cue)", len(flips))
	}
	// Cue 0 shows the segment's start second; cue 1 shows one second later. Both
	// carry the same segment number. Flips land part-way into each ~30-frame slice
	// (after the build arms), demonstrating the CTA-608 build latency.
	if flips[0].row14 != "14:23:44.000" || flips[0].row15 != "SEG 42" {
		t.Errorf("cue 0 = %q / %q, want 14:23:44.000 / SEG 42", flips[0].row14, flips[0].row15)
	}
	if flips[1].row14 != "14:23:45.000" || flips[1].row15 != "SEG 42" {
		t.Errorf("cue 1 = %q / %q, want 14:23:45.000 / SEG 42", flips[1].row14, flips[1].row15)
	}
	if flips[0].frame <= 0 || flips[0].frame >= 30 {
		t.Errorf("cue 0 flip frame %d, want in (0,30)", flips[0].frame)
	}
	if flips[1].frame <= 30 || flips[1].frame >= 60 {
		t.Errorf("cue 1 flip frame %d, want in (30,60)", flips[1].frame)
	}
	t.Logf("cue arm time: cue0 flips at frame %d (~%.2fs), cue1 at frame %d",
		flips[0].frame, float64(flips[0].frame)/fps, flips[1].frame)
}

// TestBuildUnitCuesSelfContained checks that a unit's output does not depend on
// any other unit: building the same unit twice (as a stateless server would per
// request) yields identical frames.
func TestBuildUnitCuesSelfContained(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	start := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	u := Unit{Nr: 7, StartMS: start, Frames: unitFrames}
	a, err := BuildUnitCues(fps, u, 1000, segCueContent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildUnitCues(fps, u, 1000, segCueContent)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		sameCount := a[i].CCCount == b[i].CCCount
		sameF1 := string(a[i].Field1) == string(b[i].Field1)
		sameF2 := string(a[i].Field2) == string(b[i].Field2)
		if !sameCount || !sameF1 || !sameF2 {
			t.Fatalf("frame %d differs between two independent builds of the same unit", i)
		}
	}
}

// TestBuildUnitCuesFlipAtCueStart checks that WithFlipAtCueStart puts every flip on
// its cue's first frame, so a caption is displayed over exactly the interval its text
// names. Two consecutive units are decoded as one stream because that is the only way
// to observe the cross-unit build: unit A's tail carries the build that unit B flips
// on its own first frame.
func TestBuildUnitCuesFlipAtCueStart(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60 // 2 s at 30 fps -> 2 cues per unit
	unitAStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	unitBStart := unitAStart + 2000

	unitA := Unit{Nr: 42, StartMS: unitAStart, Frames: unitFrames}
	unitB := Unit{Nr: 43, StartMS: unitBStart, Frames: unitFrames}
	unitC := Unit{Nr: 44, StartMS: unitBStart + 2000, Frames: unitFrames}

	a, err := BuildUnitCues(fps, unitA, 1000, segCueContent, WithFlipAtCueStart(unitB, segCueContent))
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}
	b, err := BuildUnitCues(fps, unitB, 1000, segCueContent, WithFlipAtCueStart(unitC, segCueContent))
	if err != nil {
		t.Fatalf("unit B: %v", err)
	}

	flips := decodeFlips(t, append(append([]schedule.Frame{}, a...), b...))
	for _, fl := range flips {
		t.Logf("flip @frame %d: row14=%q row15=%q", fl.frame, fl.row14, fl.row15)
	}
	// Unit A's own first cue was built by the unit before A, which is not part of this
	// stream, so the visible flips are A's second cue and both of B's — each exactly on
	// a cue boundary (frames 30, 60, 90) rather than part-way into the slice.
	want := []decodedFlip{
		{30, "14:23:45.000", "SEG 42"},
		{60, "14:23:46.000", "SEG 43"}, // built in unit A's tail, flipped by unit B
		{90, "14:23:47.000", "SEG 43"},
	}
	if len(flips) != len(want) {
		t.Fatalf("got %d flips, want %d", len(flips), len(want))
	}
	for i, w := range want {
		if flips[i] != w {
			t.Errorf("flip %d = %+v, want %+v", i, flips[i], w)
		}
	}
}

// TestBuildUnitCuesFlipAtCueStartNilNext checks that a nil next leaves the unit's tail
// empty: nothing is preloaded for the following unit, whose first caption is then
// blank until its second cue.
func TestBuildUnitCuesFlipAtCueStartNilNext(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	frames, err := BuildUnitCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames}, 1000,
		segCueContent, WithFlipAtCueStart(Unit{}, nil))
	if err != nil {
		t.Fatalf("BuildUnitCues: %v", err)
	}
	// The last cue flips at frame 30; every frame after it must be idle.
	for i := 31; i < unitFrames; i++ {
		if len(frames[i].Field1) != 0 {
			t.Errorf("frame %d carries %d field-1 bytes, want none (nothing preloaded)", i, len(frames[i].Field1))
		}
	}
}

// TestBuildUnitCuesFlipAtCueStartClear checks that a cleared cue (empty Lines, which
// encodes as a bare EDM with no EOC) stays on its boundary frame: the erase is itself
// the visible change, so it must not be moved ahead of the boundary the way a build is.
func TestBuildUnitCuesFlipAtCueStartClear(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	content := func(u Unit, cueIdx int, cueStartMS int64) UnitCue {
		if cueIdx == 1 {
			return UnitCue{} // clear the caption for the second cue
		}
		return segCueContent(u, cueIdx, cueStartMS)
	}

	frames, err := BuildUnitCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames}, 1000,
		content, WithFlipAtCueStart(Unit{}, nil))
	if err != nil {
		t.Fatalf("BuildUnitCues: %v", err)
	}
	// EDM on channel 1 is 0x94 0x2c, and it belongs on frame 30 (cue 1's first frame).
	if got := frames[30].Field1; len(got) != 2 || got[0] != 0x94 || got[1] != 0x2c {
		t.Errorf("frame 30 field 1 = % x, want 94 2c (EDM on the cue boundary)", got)
	}
	for i := 31; i < unitFrames; i++ {
		if len(frames[i].Field1) != 0 {
			t.Errorf("frame %d carries data after the clear, want idle", i)
		}
	}
}

// TestBuildUnitCuesFlipAtCueStartOverran checks the error when a unit is too short to
// carry the build the next unit needs — silently dropping it would leave that unit
// flipping an unloaded screen, i.e. a caption that never appears.
func TestBuildUnitCuesFlipAtCueStartOverran(t *testing.T) {
	_, err := BuildUnitCues(30.0, Unit{Nr: 1, StartMS: 0, Frames: 10}, 1000, segCueContent,
		WithFlipAtCueStart(Unit{Nr: 2, StartMS: 333, Frames: 10}, segCueContent))
	if err == nil {
		t.Fatal("expected an overrun error for a 10-frame unit, got nil")
	}
	t.Logf("overrun error (expected): %v", err)
}

// TestBuildUnitCuesBadFPS checks that an out-of-range frame rate is a returned
// error, not a scheduler panic (5 fps -> cc_count 120, far outside 2..31).
func TestBuildUnitCuesBadFPS(t *testing.T) {
	if _, err := BuildUnitCues(5.0, Unit{Nr: 1, Frames: 60}, 1000, segCueContent); err == nil {
		t.Fatal("expected an fps-range error for 5 fps, got nil")
	}
}

// TestBuildUnitCuesOverran checks the error when a build cannot fit its slice
// (here: too few frames per cue for the ~18-pair build).
func TestBuildUnitCuesOverran(t *testing.T) {
	const fps = 30.0
	// 10 frames, N=1 -> one 10-frame slice, far too short for a two-line build.
	_, err := BuildUnitCues(fps, Unit{Nr: 1, Frames: 10}, 1000, segCueContent)
	if err == nil {
		t.Fatal("expected an overrun error for a 10-frame unit, got nil")
	}
	t.Logf("overrun error (expected): %v", err)
}

// TestBuildUnitCuesNonContiguousUnits is the reason Unit carries StartMS as its own
// field. Unit B here starts 5 s after unit A even though A is only 2 s long — a
// timeline gap, or equivalently a variable segment duration. Under
// WithFlipAtCueStart, A's tail has to carry the build for B's *actual* first cue, so
// the build must be generated for B's declared start rather than for A's end.
//
// Before Unit existed, the next unit's start was computed as this unit's end, so A
// preloaded a caption reading 14:23:46.000 while B flipped expecting 14:23:49.000 —
// the screen showed A's stale guess. The assertion below is exactly that difference.
func TestBuildUnitCuesNonContiguousUnits(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60 // 2 s at 30 fps
	aStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	bStart := aStart + 5000 // 3 s gap: NOT aStart + 2000

	unitA := Unit{Nr: 42, StartMS: aStart, Frames: unitFrames}
	unitB := Unit{Nr: 43, StartMS: bStart, Frames: unitFrames}
	unitC := Unit{Nr: 44, StartMS: bStart + 2000, Frames: unitFrames}

	a, err := BuildUnitCues(fps, unitA, 1000, segCueContent, WithFlipAtCueStart(unitB, segCueContent))
	if err != nil {
		t.Fatalf("unit A: %v", err)
	}
	b, err := BuildUnitCues(fps, unitB, 1000, segCueContent, WithFlipAtCueStart(unitC, segCueContent))
	if err != nil {
		t.Fatalf("unit B: %v", err)
	}

	flips := decodeFlips(t, append(append([]schedule.Frame{}, a...), b...))
	for _, fl := range flips {
		t.Logf("flip @frame %d: row14=%q row15=%q", fl.frame, fl.row14, fl.row15)
	}
	want := []decodedFlip{
		{30, "14:23:45.000", "SEG 42"},
		// Frame 60 is unit B's first frame. Its build was transmitted in unit A's tail
		// and must name B's start across the gap, not A's end (14:23:46.000).
		{60, "14:23:49.000", "SEG 43"},
		{90, "14:23:50.000", "SEG 43"},
	}
	if len(flips) != len(want) {
		t.Fatalf("got %d flips, want %d", len(flips), len(want))
	}
	for i, w := range want {
		if flips[i] != w {
			t.Errorf("flip %d = %+v, want %+v", i, flips[i], w)
		}
	}
}

// TestBuildUnitCuesNrIndependentOfTime pins the decoupling directly: the same start
// time with a different unit number changes only the number, and the same number at a
// different start changes only the time. Neither is derived from the other, so a
// consumer whose numbering epoch does not begin at t=0 is expressible.
func TestBuildUnitCuesNrIndependentOfTime(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	start := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	// Segment 1000 that starts at t=44 s — nowhere near 1000 * 2 s.
	frames, err := BuildUnitCues(fps, Unit{Nr: 1000, StartMS: start, Frames: unitFrames}, 1000, segCueContent)
	if err != nil {
		t.Fatalf("BuildUnitCues: %v", err)
	}
	flips := decodeFlips(t, frames)
	if len(flips) != 2 {
		t.Fatalf("got %d flips, want 2", len(flips))
	}
	if flips[0].row15 != "SEG 1000" {
		t.Errorf("row15 = %q, want SEG 1000", flips[0].row15)
	}
	if flips[0].row14 != "14:23:44.000" {
		t.Errorf("row14 = %q, want 14:23:44.000 (start is independent of Nr)", flips[0].row14)
	}
}
