package generate

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
)

var startWall = time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

func wallAt(frame int, fps float64) int64 {
	return startWall + int64(math.Round(float64(frame)*1000.0/fps))
}

// rowText returns the concatenated text, starting column, and color of a screen
// row, plus whether the row is present.
func rowText(s cta608.Screen, idx int) (text string, col int, color cta608.Color, ok bool) {
	for _, r := range s.Rows {
		if r.Index != idx {
			continue
		}
		col = -1
		for _, run := range r.Runs {
			if col < 0 {
				col = run.Column
				color = run.Pen.Color
			}
			text += run.Text
		}
		return text, col, color, true
	}
	return "", 0, 0, false
}

// TestGeneratorRoundTrip drives the generator at 30 fps and decodes each frame
// through the full carriage + cta608.Decoder path, checking that the pop-on flip
// lands on the last frame of each second and shows the next second's clock.
func TestGeneratorRoundTrip(t *testing.T) {
	const fps = 30.0
	g := NewGenerator(fps, DefaultConfig())
	var dec cta608.Decoder

	type flip struct {
		frame int
		utc   string
		media string
	}
	var flips []flip

	for frame := 0; frame < 3*30; frame++ {
		f := g.NextFrame(wallAt(frame, fps))
		if f.CCCount != 20 {
			t.Fatalf("frame %d cc_count = %d, want 20", frame, f.CCCount)
		}
		if len(f.Field2) != 0 {
			t.Fatalf("frame %d field2 non-empty (CC1-only expected)", frame)
		}
		if l := len(f.Field1); l != 0 && l != 2 {
			t.Fatalf("frame %d field1 = %d bytes, want 0 or 2", frame, l)
		}
		if len(f.Field1) == 0 {
			continue
		}
		// Full round-trip: schedule.Frame -> carriage NAL -> FieldPairs -> Decoder.
		nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
		fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
		if err != nil {
			t.Fatalf("frame %d FieldPairs: %v", frame, err)
		}
		if err := dec.Feed(fld1); err != nil {
			t.Fatalf("frame %d decode: %v", frame, err)
		}
		if dec.Changed() {
			utc, _, _, _ := rowText(dec.Screen(), 14)
			media, _, _, _ := rowText(dec.Screen(), 15)
			flips = append(flips, flip{frame, utc, media})
		}
	}

	want := []flip{
		{29, "2026-07-20T14:23:45Z", "MEDIA 00:00:01"},
		{59, "2026-07-20T14:23:46Z", "MEDIA 00:00:02"},
		{89, "2026-07-20T14:23:47Z", "MEDIA 00:00:03"},
	}
	if len(flips) != len(want) {
		t.Fatalf("got %d flips %+v, want %d", len(flips), flips, len(want))
	}
	for i, w := range want {
		if flips[i] != w {
			t.Errorf("flip %d = %+v, want %+v", i, flips[i], w)
		}
	}

	// Centering + color of the final displayed screen (criterion 2).
	s := dec.Screen()
	if _, col, color, ok := rowText(s, 14); !ok || col != 6 || color != cta608.White {
		t.Errorf("row14 col=%d color=%s (want col 6 white)", col, color)
	}
	if _, col, color, ok := rowText(s, 15); !ok || col != 9 || color != cta608.Yellow {
		t.Errorf("row15 col=%d color=%s (want col 9 yellow)", col, color)
	}
}

func TestGeneratorCCCountPerFPS(t *testing.T) {
	cases := map[float64]int{25: 24, 30: 20, 29.97: 20, 50: 12, 60: 10}
	for fps, want := range cases {
		g := NewGenerator(fps, DefaultConfig())
		f := g.NextFrame(wallAt(0, fps))
		if f.CCCount != want {
			t.Errorf("fps %g: cc_count = %d, want %d", fps, f.CCCount, want)
		}
	}
}

// TestGeneratorCadence checks the one-field-1-pair-per-frame cadence over a
// second: exactly the build pairs plus one EOC are emitted, field 2 stays empty.
func TestGeneratorCadence(t *testing.T) {
	const fps = 30.0
	g := NewGenerator(fps, DefaultConfig())
	pairs := 0
	for frame := 0; frame < 30; frame++ {
		f := g.NextFrame(wallAt(frame, fps))
		if len(f.Field2) != 0 {
			t.Fatalf("frame %d: field2 non-empty", frame)
		}
		if len(f.Field1) == 2 {
			pairs++
		} else if len(f.Field1) != 0 {
			t.Fatalf("frame %d: field1 = %d bytes", frame, len(f.Field1))
		}
	}
	// Two-line default build is ~23 pairs + 1 EOC; must be < 30 and leave idle frames.
	if pairs < 10 || pairs >= 30 {
		t.Errorf("emitted %d field-1 pairs in a 30-frame second, want a modest count < 30", pairs)
	}
	if g.Overran() {
		t.Error("default two-line config overran at 30 fps")
	}
}

// TestGeneratorPaintOn drives the paint-on generator for three seconds at 30 fps
// and checks the animation contract: each second starts with a cleared screen,
// grows by at most one byte pair (two characters) per frame, and stands complete
// well before the next second clears it. The caption names the second it is being
// painted in, unlike the pop-on generator, which paints the second ahead.
func TestGeneratorPaintOn(t *testing.T) {
	const fps = 30.0
	g := NewGenerator(fps, DefaultConfig(), WithPaintOn())
	var dec cta608.Decoder

	screens := make([]string, 3*30)
	for frame := 0; frame < 3*30; frame++ {
		f := g.NextFrame(wallAt(frame, fps))
		if len(f.Field2) != 0 {
			t.Fatalf("frame %d field2 non-empty (CC1-only expected)", frame)
		}
		if len(f.Field1) > 0 {
			nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
			fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
			if err != nil {
				t.Fatalf("frame %d FieldPairs: %v", frame, err)
			}
			if err := dec.Feed(fld1); err != nil {
				t.Fatalf("frame %d decode: %v", frame, err)
			}
		}
		utc, _, _, _ := rowText(dec.Screen(), 14)
		media, _, _, _ := rowText(dec.Screen(), 15)
		screens[frame] = utc + media
	}
	if g.Overran() {
		t.Error("default two-line config overran the paint-on budget at 30 fps")
	}

	want := []string{
		"2026-07-20T14:23:44ZMEDIA 00:00:00",
		"2026-07-20T14:23:45ZMEDIA 00:00:01",
		"2026-07-20T14:23:46ZMEDIA 00:00:02",
	}
	for sec, w := range want {
		start := sec * 30
		if screens[start] != "" {
			t.Errorf("second %d: frame %d shows %q, want the screen cleared on the second's first frame",
				sec, start, screens[start])
		}
		done := -1
		for i := start; i < start+30; i++ {
			if !strings.HasPrefix(w, screens[i]) {
				t.Fatalf("second %d frame %d: %q is not a prefix of %q", sec, i, screens[i], w)
			}
			prev := ""
			if i > start {
				prev = screens[i-1]
			}
			if n := len(screens[i]) - len(prev); n < 0 || n > 2 {
				t.Errorf("second %d frame %d: screen grew by %d characters, want 0..2", sec, i, n)
			}
			if screens[i] == w && done < 0 {
				done = i
			}
		}
		if done < 0 {
			t.Errorf("second %d never reached %q (last was %q)", sec, w, screens[start+29])
			continue
		}
		t.Logf("second %d: complete at frame %d (%.2f s in), held for %d frames",
			sec, done, float64(done-start)/fps, start+30-done)
		if done-start >= 29 {
			t.Errorf("second %d completed at frame %d, leaving no time on screen", sec, done)
		}
	}
}

// TestGeneratorPaintOnOverrunGuard checks that the paint-on budget is reported the
// same way as the pop-on one: a caption that cannot be written within its second.
func TestGeneratorPaintOnOverrunGuard(t *testing.T) {
	g := NewGenerator(30, DefaultConfig(), WithPaintOn())
	g.NextFrame(wallAt(0, 30))
	if g.Overran() {
		t.Error("default config should not overrun at 30 fps")
	}

	big := Config{Lines: []LineSpec{
		{Row: 12, Kind: "utc"}, {Row: 13, Kind: "utc"},
		{Row: 14, Kind: "utc"}, {Row: 15, Kind: "utc"},
	}}
	g2 := NewGenerator(25, big, WithPaintOn())
	g2.NextFrame(wallAt(0, 25))
	if !g2.Overran() {
		t.Error("four-line config should overrun at 25 fps")
	}
}

func TestGeneratorOverrunGuard(t *testing.T) {
	// Default two lines at 30 fps fit comfortably.
	g := NewGenerator(30, DefaultConfig())
	g.NextFrame(wallAt(0, 30))
	if g.Overran() {
		t.Error("default config should not overrun at 30 fps")
	}

	// Four full lines at 25 fps cannot finish building within one second.
	big := Config{Lines: []LineSpec{
		{Row: 12, Kind: "utc"}, {Row: 13, Kind: "utc"},
		{Row: 14, Kind: "utc"}, {Row: 15, Kind: "utc"},
	}}
	g2 := NewGenerator(25, big)
	g2.NextFrame(wallAt(0, 25))
	if !g2.Overran() {
		t.Error("four-line config should overrun at 25 fps")
	}
}
