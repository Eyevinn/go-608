package scc

import (
	"strings"
	"testing"
)

// buildSCC assembles a minimal SCC document (header + blank-separated entry
// lines) from raw "timecode\tpairs" lines.
func buildSCC(lines ...string) string {
	var b strings.Builder
	b.WriteString("Scenarist_SCC V1.0\n")
	for _, l := range lines {
		b.WriteString("\n")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// Inference picks the right family from the two sparse-file signals: the ';'
// drop-frame separator and the maximum line-start frame field (S3).
func TestReadInferFPS(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantFPS  float64
		wantDrop bool
	}{
		{"drop 29.97 (semicolon, FF<30)", "00:00:00;20\t8080", fpsNTSC30, true},
		{"drop 59.94 (semicolon, FF>=30)", "00:00:00;45\t8080", fpsNTSC60, true},
		{"non-drop 60-family (FF 50-59)", "00:00:00:55\t8080", fpsNTSC60, false},
		{"non-drop 50 (FF 30-49)", "00:00:00:45\t8080", 50.0, false},
		{"non-drop 30-family (FF 25-29)", "00:00:00:28\t8080", fpsNTSC30, false},
		{"ambiguous fallback (FF<=24, colon)", "00:00:00:10\t8080", fpsNTSC30, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := Read(strings.NewReader(buildSCC(c.line)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if f.FPS != c.wantFPS {
				t.Errorf("FPS = %g, want %g", f.FPS, c.wantFPS)
			}
			if f.DropFrame != c.wantDrop {
				t.Errorf("DropFrame = %v, want %v", f.DropFrame, c.wantDrop)
			}
		})
	}
}

// An empty file (header only) has no signals and takes the 29.97 fallback.
func TestReadEmptyFallsBackTo2997(t *testing.T) {
	f, err := Read(strings.NewReader("Scenarist_SCC V1.0\n"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.FPS != fpsNTSC30 || f.DropFrame {
		t.Errorf("empty file: FPS=%g DropFrame=%v, want %g false", f.FPS, f.DropFrame, fpsNTSC30)
	}
	if len(f.Entries) != 0 {
		t.Errorf("empty file: %d entries, want 0", len(f.Entries))
	}
}

// WithFPS overrides inference. It sets the rate; drop-frame still follows the
// separators and is forced off for a rate that cannot be drop-frame.
func TestReadWithFPSOverride(t *testing.T) {
	// A ':' file that would infer 29.97 is forced to PAL 25 (non-drop).
	f, err := Read(strings.NewReader(buildSCC("00:00:00:10\t8080")), WithFPS(25.0))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.FPS != 25.0 || f.DropFrame {
		t.Errorf("override 25: FPS=%g DropFrame=%v, want 25 false", f.FPS, f.DropFrame)
	}

	// A ';' (drop) file forced to 25 must drop-frame OFF — 25 cannot be drop-frame.
	f, err = Read(strings.NewReader(buildSCC("00:00:00;10\t8080")), WithFPS(25.0))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.DropFrame {
		t.Error("override 25 on a ';' file: DropFrame = true, want false")
	}

	// A ';' file forced to 59.94 keeps drop-frame ON.
	f, err = Read(strings.NewReader(buildSCC("00:00:00;10\t8080")), WithFPS(fpsNTSC60))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.FPS != fpsNTSC60 || !f.DropFrame {
		t.Errorf("override 59.94 on ';' file: FPS=%g DropFrame=%v, want 59.94 true", f.FPS, f.DropFrame)
	}
}

// The override rate is validated against every timecode: a frame field beyond the
// forced rate's nominal is an error (rather than silently mis-scaled).
func TestReadWithFPSFrameFieldOutOfRange(t *testing.T) {
	// FF 40 is fine at the inferred 50 fps, but out of range once forced to 25.
	_, err := Read(strings.NewReader(buildSCC("00:00:00:40\t8080")), WithFPS(25.0))
	if err == nil {
		t.Fatal("Read: nil error, want out-of-range frame field error")
	}
}

// Timecodes convert to the expected absolute frames under the inferred rate.
func TestReadFrameNumbers(t *testing.T) {
	f, err := Read(strings.NewReader(buildSCC(
		"00:00:01:00\t8080", // 1 s at 29.97 nominal 30 -> 30
		"00:00:02:28\t8080", // 2*30+28 -> 88
	)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []int{30, 88}
	for i, e := range f.Entries {
		if e.Frame != want[i] {
			t.Errorf("entry %d frame = %d, want %d", i, e.Frame, want[i])
		}
	}
}

func TestReadErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty input", ""},
		{"missing header", "00:00:00:00\t8080\n"},
		{"malformed timecode", buildSCC("not-a-tc\t8080")},
		{"odd hex length", buildSCC("00:00:00:00\t80")},
		{"non-hex pair", buildSCC("00:00:00:00\t80zz")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Read(strings.NewReader(c.in)); err == nil {
				t.Errorf("Read(%q) = nil error, want error", c.in)
			}
		})
	}
}

// CRLF line endings and multi-pair lines are tolerated on read.
func TestReadToleratesCRLF(t *testing.T) {
	in := "Scenarist_SCC V1.0\r\n\r\n00:00:01:00\t9420 9420 942f 942f\r\n"
	f, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(f.Entries) != 1 || len(f.Entries[0].Pairs) != 8 {
		t.Fatalf("got %d entries, first with %d bytes; want 1 entry, 8 bytes", len(f.Entries), len(f.Entries[0].Pairs))
	}
}
