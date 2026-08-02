package generate

import (
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// paintTrace decodes every frame of a unit and returns, per frame, what a viewer
// would see: the concatenation of rows 14 and 15. Idle frames repeat the previous
// frame's screen, which is exactly the point of the trace — a paint-on caption is
// only complete once the last of its pairs has arrived.
func paintTrace(t *testing.T, frames []schedule.Frame) []string {
	t.Helper()
	var dec cta608.Decoder
	out := make([]string, len(frames))
	for i, f := range frames {
		if len(f.Field1) > 0 {
			nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
			fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
			if err != nil {
				t.Fatalf("frame %d FieldPairs: %v", i, err)
			}
			if err := dec.Feed(fld1); err != nil {
				t.Fatalf("frame %d decode: %v", i, err)
			}
		}
		r14, _, _, _ := rowText(dec.Screen(), 14)
		r15, _, _, _ := rowText(dec.Screen(), 15)
		out[i] = r14 + r15
	}
	return out
}

// TestBuildUnitPaintCuesTypesOut is the defining behavior: within a cue's slice the
// screen starts clean and the caption accumulates a couple of characters at a time
// until it is whole, then stands untouched until the next cue clears it. A 2 s
// segment at 30 fps gives two ~1 s cues.
func TestBuildUnitPaintCuesTypesOut(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	frames, err := BuildUnitPaintCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames}, 1000, segCueContent)
	if err != nil {
		t.Fatalf("BuildUnitPaintCues: %v", err)
	}
	if len(frames) != unitFrames {
		t.Fatalf("got %d frames, want %d", len(frames), unitFrames)
	}
	trace := paintTrace(t, frames)

	cues := []struct {
		start, end int
		want       string
	}{
		{0, 30, "14:23:44.000SEG 42"},
		{30, 60, "14:23:45.000SEG 42"},
	}
	for c, cue := range cues {
		if trace[cue.start] != "" {
			t.Errorf("cue %d: frame %d shows %q, want a cleared screen on the cue's first frame",
				c, cue.start, trace[cue.start])
		}
		steps := 0
		for i := cue.start; i < cue.end; i++ {
			// Every frame's screen extends the previous one by at most one byte pair,
			// i.e. two characters — the typewriter property.
			prev := ""
			if i > cue.start {
				prev = trace[i-1]
			}
			if !strings.HasPrefix(cue.want, trace[i]) {
				t.Fatalf("cue %d frame %d: %q is not a prefix of %q", c, i, trace[i], cue.want)
			}
			if n := len(trace[i]) - len(prev); n < 0 || n > 2 {
				t.Errorf("cue %d frame %d: screen grew by %d characters (%q -> %q), want 0..2",
					c, i, n, prev, trace[i])
			} else if n > 0 {
				steps++
			}
		}
		// A whole caption arriving in one step would satisfy the bounds above but is
		// the very thing paint-on is here to avoid.
		if steps < 5 {
			t.Errorf("cue %d painted in %d visible steps, want it spread over many frames", c, steps)
		}
		if trace[cue.end-1] != cue.want {
			t.Errorf("cue %d last frame shows %q, want the complete %q", c, trace[cue.end-1], cue.want)
		}
	}

	// The complete caption must stand for a useful part of the slice, not flash on
	// the final frame: find where each cue reaches its full text.
	for c, cue := range cues {
		done := cue.end
		for i := cue.start; i < cue.end; i++ {
			if trace[i] == cue.want {
				done = i
				break
			}
		}
		t.Logf("cue %d: complete at frame %d of [%d,%d), held for %d frames (%.2f s)",
			c, done, cue.start, cue.end, cue.end-done, float64(cue.end-done)/fps)
		if done >= cue.end-1 {
			t.Errorf("cue %d completes at frame %d, leaving no frame of full display before %d",
				c, done, cue.end)
		}
	}
}

// TestBuildUnitPaintCuesSelfContained checks the property that makes paint-on the
// easy mode for a stateless per-segment server: a unit's frames depend on nothing
// outside the unit, so building it twice is byte-identical, and a decoder that
// starts at this unit (no prior state) sees the same captions as one that has been
// running — no cross-unit preload, as WithFlipAtCueStart needs for pop-on.
func TestBuildUnitPaintCuesSelfContained(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	start := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	u := Unit{Nr: 7, StartMS: start, Frames: unitFrames}

	a, err := BuildUnitPaintCues(fps, u, 1000, segCueContent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildUnitPaintCues(fps, u, 1000, segCueContent)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i].CCCount != b[i].CCCount ||
			string(a[i].Field1) != string(b[i].Field1) || string(a[i].Field2) != string(b[i].Field2) {
			t.Fatalf("frame %d differs between two independent builds of the same unit", i)
		}
	}

	// The first cue of a cold-started unit is fully painted from its own frames.
	trace := paintTrace(t, a)
	if got, want := trace[29], "14:23:44.000SEG 7"; got != want {
		t.Errorf("cold start: end of first cue = %q, want %q", got, want)
	}
}

// TestBuildUnitPaintCuesEmptyCue checks that a cue with no lines is just the clear:
// one EDM on the boundary frame and nothing else, not a bare mode switch.
func TestBuildUnitPaintCuesEmptyCue(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	content := func(u Unit, cueIdx int, cueStartMS int64) UnitCue {
		if cueIdx == 1 {
			return UnitCue{} // clear the caption for the second cue
		}
		return segCueContent(u, cueIdx, cueStartMS)
	}

	frames, err := BuildUnitPaintCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames}, 1000, content)
	if err != nil {
		t.Fatalf("BuildUnitPaintCues: %v", err)
	}
	// EDM on channel 1 is 0x94 0x2c, on cue 1's first frame.
	if got := frames[30].Field1; len(got) != 2 || got[0] != 0x94 || got[1] != 0x2c {
		t.Errorf("frame 30 field 1 = % x, want 94 2c (the clear alone)", got)
	}
	for i := 31; i < unitFrames; i++ {
		if len(frames[i].Field1) != 0 {
			t.Errorf("frame %d carries data after the clear, want idle", i)
		}
	}
	if trace := paintTrace(t, frames); trace[unitFrames-1] != "" {
		t.Errorf("screen shows %q at the end of an empty cue, want it cleared", trace[unitFrames-1])
	}
}

// TestBuildUnitPaintCuesOverran checks the error when a cue cannot finish painting
// inside its slice. Paint-on is tighter than pop-on here: the pairs are the
// animation, so there is nowhere else to put them.
func TestBuildUnitPaintCuesOverran(t *testing.T) {
	_, err := BuildUnitPaintCues(30.0, Unit{Nr: 1, StartMS: 0, Frames: 10}, 1000, segCueContent)
	if err == nil {
		t.Fatal("expected an overrun error for a 10-frame unit, got nil")
	}
	t.Logf("overrun error (expected): %v", err)
}

// TestBuildUnitPaintCuesBadInput covers the guard clauses: frame count, content
// function, and an out-of-range frame rate (errors, never a scheduler panic).
func TestBuildUnitPaintCuesBadInput(t *testing.T) {
	if _, err := BuildUnitPaintCues(30.0, Unit{Nr: 1, Frames: 0}, 1000, segCueContent); err == nil {
		t.Error("expected an error for Frames = 0, got nil")
	}
	if _, err := BuildUnitPaintCues(30.0, Unit{Nr: 1, Frames: 60}, 1000, nil); err == nil {
		t.Error("expected an error for a nil content function, got nil")
	}
	if _, err := BuildUnitPaintCues(5.0, Unit{Nr: 1, Frames: 60}, 1000, segCueContent); err == nil {
		t.Error("expected an fps-range error for 5 fps, got nil")
	}
}

// TestPaintOnTokensEmpty pins the helper's empty case: nothing to paint yields no
// tokens at all, so a cleared cue does not spend a frame on a lone RDC.
func TestPaintOnTokensEmpty(t *testing.T) {
	if toks := paintOnTokens(nil); toks != nil {
		t.Errorf("paintOnTokens(nil) = %v, want nil", toks)
	}
	empty := []cta608.Line{{Row: 15, Runs: []cta608.Run{{Text: ""}}}}
	if toks := paintOnTokens(empty); toks != nil {
		t.Errorf("paintOnTokens(empty runs) = %v, want nil", toks)
	}
	toks := paintOnTokens([]cta608.Line{{Row: 15, Runs: []cta608.Run{{Text: "HI"}}}})
	if len(toks) == 0 {
		t.Fatal("paintOnTokens returned nothing for a real line")
	}
	if m, ok := toks[0].(cta608.SetMode); !ok || m.Mode != cta608.PaintOn {
		t.Errorf("first token = %v, want the paint-on mode entry (RDC)", toks[0])
	}
}
