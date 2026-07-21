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
		{1920, 1000, 2}, // 0.96 s each
		{2002, 1000, 2}, // 1.001 s each
		{2000, 1000, 2},
		{1000, 1000, 1},
		{960, 1000, 1},  // rounds to 1, never 0
		{4000, 1000, 4}, // 1.0 s each
		{3840, 1000, 4}, // 0.96 s each
		{2002, 0, 2},    // targetMS<=0 defaults to 1000
	}
	for _, c := range cases {
		if got := NumCues(c.unitDurMS, c.targetMS); got != c.want {
			t.Errorf("NumCues(%d,%d) = %d, want %d", c.unitDurMS, c.targetMS, got, c.want)
		}
	}
}

// segCueContent formats a probe cue: line 1 = the cue's UTC time (ms precision),
// line 2 = "SEG <segNr>" (constant across the unit's cues — the caller closes
// over segNr). This is the shape livesim2/moqlivemock would pass.
func segCueContent(segNr int) CueContentFunc {
	return func(cueIdx int, cueStartMS int64) UnitCue {
		ts := time.UnixMilli(cueStartMS).UTC().Format("15:04:05.000")
		seg := fmt.Sprintf("SEG %d", segNr)
		white := cta608.Pen{Color: cta608.White}
		yellow := cta608.Pen{Color: cta608.Yellow}
		return UnitCue{Lines: []cta608.Line{
			{Row: 14, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: ts, Pen: white}}},
			{Row: 15, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: seg, Pen: yellow}}},
		}}
	}
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

	frames, err := BuildUnitCues(fps, unitFrames, unitStart, 1000, segCueContent(42))
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
	a, err := BuildUnitCues(fps, unitFrames, start, 1000, segCueContent(7))
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildUnitCues(fps, unitFrames, start, 1000, segCueContent(7))
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

// TestBuildUnitCuesBadFPS checks that an out-of-range frame rate is a returned
// error, not a scheduler panic (5 fps -> cc_count 120, far outside 2..31).
func TestBuildUnitCuesBadFPS(t *testing.T) {
	if _, err := BuildUnitCues(5.0, 60, 0, 1000, segCueContent(1)); err == nil {
		t.Fatal("expected an fps-range error for 5 fps, got nil")
	}
}

// TestBuildUnitCuesOverran checks the error when a build cannot fit its slice
// (here: too few frames per cue for the ~18-pair build).
func TestBuildUnitCuesOverran(t *testing.T) {
	const fps = 30.0
	// 10 frames, N=1 -> one 10-frame slice, far too short for a two-line build.
	_, err := BuildUnitCues(fps, 10, 0, 1000, segCueContent(1))
	if err == nil {
		t.Fatal("expected an overrun error for a 10-frame unit, got nil")
	}
	t.Logf("overrun error (expected): %v", err)
}
