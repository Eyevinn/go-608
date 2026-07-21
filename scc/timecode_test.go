package scc

import (
	"strings"
	"testing"
)

// Known-good drop-frame anchor points for 29.97 (nominal 30, drop 2/min). They
// pin the minute boundary (labels ;00 and ;01 skipped), the every-tenth-minute
// exception (no skip at 10:00), and the minute after it (skip resumes at 11:00).
func TestFrameToTimecodeDrop2997(t *testing.T) {
	cases := []struct {
		frame int
		tc    string
	}{
		{0, "00:00:00;00"},
		{1799, "00:00:59;29"},
		{1800, "00:01:00;02"}, // ;00 and ;01 do not exist at a normal minute
		{3598, "00:02:00;02"},
		{17981, "00:09:59;29"},
		{17982, "00:10:00;00"}, // every-tenth minute: no drop, ;00 exists
		{17983, "00:10:00;01"},
		{19781, "00:10:59;29"},
		{19782, "00:11:00;02"}, // drop resumes the minute after the exception
	}
	for _, c := range cases {
		if got := FrameToTimecode(c.frame, fpsNTSC30, true); got != c.tc {
			t.Errorf("FrameToTimecode(%d, 29.97, drop) = %q, want %q", c.frame, got, c.tc)
		}
		frame, drop, err := TimecodeToFrame(c.tc, fpsNTSC30)
		if err != nil {
			t.Fatalf("TimecodeToFrame(%q): %v", c.tc, err)
		}
		if !drop {
			t.Errorf("TimecodeToFrame(%q): drop = false, want true", c.tc)
		}
		if frame != c.frame {
			t.Errorf("TimecodeToFrame(%q) = %d, want %d", c.tc, frame, c.frame)
		}
	}
}

// The 59.94 family drops four labels per minute (nominal 60), with the same
// tenth-minute exception.
func TestFrameToTimecodeDrop5994(t *testing.T) {
	cases := []struct {
		frame int
		tc    string
	}{
		{0, "00:00:00;00"},
		{3599, "00:00:59;59"},
		{3600, "00:01:00;04"}, // ;00..;03 skipped
		{35964, "00:10:00;00"},
	}
	for _, c := range cases {
		if got := FrameToTimecode(c.frame, fpsNTSC60, true); got != c.tc {
			t.Errorf("FrameToTimecode(%d, 59.94, drop) = %q, want %q", c.frame, got, c.tc)
		}
		frame, _, err := TimecodeToFrame(c.tc, fpsNTSC60)
		if err != nil {
			t.Fatalf("TimecodeToFrame(%q): %v", c.tc, err)
		}
		if frame != c.frame {
			t.Errorf("TimecodeToFrame(%q) = %d, want %d", c.tc, frame, c.frame)
		}
	}
}

// Exhaustive round-trip over a range spanning several minute and ten-minute
// boundaries: every frame must survive frame → timecode → frame for the drop
// rates. This is the strongest guard on the conversion's inversibility.
func TestDropFrameRoundTripExhaustive(t *testing.T) {
	for _, fps := range []float64{fpsNTSC30, fpsNTSC60} {
		for frame := 0; frame < 40000; frame++ {
			tc := FrameToTimecode(frame, fps, true)
			if !strings.Contains(tc, ";") {
				t.Fatalf("fps %g frame %d: drop timecode %q lacks ';'", fps, frame, tc)
			}
			got, drop, err := TimecodeToFrame(tc, fps)
			if err != nil {
				t.Fatalf("fps %g frame %d tc %q: %v", fps, frame, tc, err)
			}
			if !drop || got != frame {
				t.Fatalf("fps %g: round trip frame %d -> %q -> (%d, drop=%v)", fps, frame, tc, got, drop)
			}
		}
	}
}

// Non-fractional rates are never drop-frame, even when drop is requested: the
// output uses ':' throughout and the frame field simply counts nominal frames.
func TestNonDropRatesIgnoreDropRequest(t *testing.T) {
	cases := []struct {
		frame int
		fps   float64
		tc    string
	}{
		{25, 25.0, "00:00:01:00"},   // PAL
		{1800, 30.0, "00:01:00:00"}, // exact 30, no drop
		{1800, 25.0, "00:01:12:00"}, // 1800/25 = 72 s
		{24, 24.0, "00:00:01:00"},   // film
		{60, 60.0, "00:00:01:00"},   // exact 60
		{50, 50.0, "00:00:01:00"},   // 50
	}
	for _, c := range cases {
		got := FrameToTimecode(c.frame, c.fps, true) // drop requested but must be ignored
		if strings.Contains(got, ";") {
			t.Errorf("FrameToTimecode(%d, %g, drop) = %q, must not be drop-frame", c.frame, c.fps, got)
		}
		if got != c.tc {
			t.Errorf("FrameToTimecode(%d, %g) = %q, want %q", c.frame, c.fps, got, c.tc)
		}
		frame, drop, err := TimecodeToFrame(got, c.fps)
		if err != nil {
			t.Fatalf("TimecodeToFrame(%q, %g): %v", got, c.fps, err)
		}
		if drop {
			t.Errorf("TimecodeToFrame(%q, %g): drop = true, want false", got, c.fps)
		}
		if frame != c.frame {
			t.Errorf("TimecodeToFrame(%q, %g) = %d, want %d", got, c.fps, frame, c.frame)
		}
	}
}

// A ';' timecode read at a non-drop rate is parsed as non-drop (the separator
// cannot conjure a drop-frame count the rate does not support), and the returned
// drop flag says so.
func TestSemicolonAtNonDropRateIsNonDrop(t *testing.T) {
	frame, drop, err := TimecodeToFrame("00:01:00;00", 25.0)
	if err != nil {
		t.Fatalf("TimecodeToFrame: %v", err)
	}
	if drop {
		t.Error("drop = true at 25 fps, want false")
	}
	if want := (60 * 25); frame != want {
		t.Errorf("frame = %d, want %d", frame, want)
	}
}

func TestTimecodeToFrameErrors(t *testing.T) {
	cases := []string{
		"",
		"00:00:00",       // too few fields
		"00:00:00:00:00", // too many fields
		"aa:00:00:00",    // non-numeric
		"00:00:00:30",    // frame field out of range at 29.97 (max 29)
	}
	for _, tc := range cases {
		if _, _, err := TimecodeToFrame(tc, fpsNTSC30); err == nil {
			t.Errorf("TimecodeToFrame(%q) = nil error, want error", tc)
		}
	}
}
