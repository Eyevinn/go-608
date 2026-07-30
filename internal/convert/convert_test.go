package convert

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/scc"
	"github.com/Eyevinn/go-608/schedule"
)

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"webvtt": FormatWebVTT, "vtt": FormatWebVTT, ".vtt": FormatWebVTT,
		"srt": FormatSRT, ".srt": FormatSRT,
		"scc": FormatSCC, "SCC": FormatSCC, ".scc": FormatSCC,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = (%v, %v), want (%v, nil)", in, got, err, want)
		}
	}
	if _, err := ParseFormat("mkv"); err == nil {
		t.Error("ParseFormat(mkv): want error")
	}
	if f, ok := FormatFromPath("caps.srt"); !ok || f != FormatSRT {
		t.Errorf("FormatFromPath(caps.srt) = (%v,%v)", f, ok)
	}
	if _, ok := FormatFromPath("movie.mp4"); ok {
		t.Error("FormatFromPath(movie.mp4): want ok=false")
	}
}

// TestWriteSCCPairsByteExact confirms the byte-pair path carries wire bytes
// verbatim (successive frames coalesce into one entry).
func TestWriteSCCPairsByteExact(t *testing.T) {
	pairs := []scc.TimedPair{
		{Frame: 0, Pair: []byte{0x94, 0x20}},
		{Frame: 1, Pair: []byte{0xc8, 0x49}},
		{Frame: 2, Pair: []byte{0x94, 0x2f}},
	}
	var buf bytes.Buffer
	if err := WriteSCCPairs(&buf, pairs, Options{FPS: 30}); err != nil {
		t.Fatalf("WriteSCCPairs: %v", err)
	}
	if !strings.Contains(buf.String(), "9420 c849 942f") {
		t.Errorf("SCC not byte-exact:\n%s", buf.String())
	}
	if err := WriteSCCPairs(&buf, pairs, Options{}); err == nil {
		t.Error("WriteSCCPairs with fps 0: want error")
	}
}

// TestCueRoundTripWebVTT round-trips cues through the WebVTT writer/reader and
// checks the caption text and window survive the shared core (semantic).
func TestCueRoundTripWebVTT(t *testing.T) {
	cues := []cue.TimedCue{{
		Start: time.Second,
		End:   3 * time.Second,
		Content: cta608.Screen{Rows: []cta608.Row{{
			Index: 15, Displayed: true,
			Runs: []cta608.Run{{Column: 0, Text: "HELLO", Pen: cta608.Pen{Color: cta608.White}}},
		}}},
	}}
	var buf bytes.Buffer
	if err := WriteCues(FormatWebVTT, &buf, cues, Options{}); err != nil {
		t.Fatalf("WriteCues: %v", err)
	}
	got, err := ReadCues(FormatWebVTT, bytes.NewReader(buf.Bytes()), Options{})
	if err != nil {
		t.Fatalf("ReadCues: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d cues, want 1", len(got))
	}
	if text := rowsText(got[0].Content); text != "HELLO" {
		t.Errorf("round-trip text = %q, want HELLO", text)
	}
	if got[0].Start != time.Second || got[0].End != 3*time.Second {
		t.Errorf("round-trip window = %v..%v, want 1s..3s", got[0].Start, got[0].End)
	}
}

// TestWriteCuesSCCCompilePath checks the lossy cue -> SCC path emits a valid SCC
// document (the WebVTT/SRT -> SCC direction, distinct from WriteSCCPairs).
func TestWriteCuesSCCCompilePath(t *testing.T) {
	cues := []cue.TimedCue{{
		Start: time.Second, End: 2 * time.Second,
		Content: cta608.Screen{Rows: []cta608.Row{{
			Index: 15, Displayed: true,
			Runs: []cta608.Run{{Column: 0, Text: "HI", Pen: cta608.Pen{Color: cta608.White}}},
		}}},
	}}
	var buf bytes.Buffer
	if err := WriteCues(FormatSCC, &buf, cues, Options{FPS: 30}); err != nil {
		t.Fatalf("WriteCues SCC: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "Scenarist_SCC V1.0") {
		t.Errorf("not a valid SCC document:\n%s", buf.String())
	}
	if err := WriteCues(FormatSCC, &bytes.Buffer{}, cues, Options{}); err == nil {
		t.Error("WriteCues SCC with fps 0: want error")
	}
}

func rowsText(s cta608.Screen) string {
	var b strings.Builder
	for _, r := range s.Rows {
		for _, run := range r.Runs {
			b.WriteString(run.Text)
		}
	}
	return b.String()
}

// TestCuesFromUnitsExtendedChars guards the conversion path that exposed the
// incremental-decode bug: CuesFromUnits feeds the decoder one pair per unit to keep
// per-frame timing, and an extended character's fallback and glyph land in different
// units. "CAFÉ" used to come out as "CAFEÉ" in every mp4/SCC -> WebVTT conversion of
// accented text.
func TestCuesFromUnitsExtendedChars(t *testing.T) {
	const text = "CAFÉ ÀU LAIT"
	var enc cta608.Encoder
	data := cta608.Serialize(enc.Apply(cta608.CaptionBlock{
		Mode: cta608.PopOn, Anchor: cta608.AnchorBottom,
		Lines: []cta608.Line{{
			Align: cta608.AlignLeft,
			Runs:  []cta608.Run{{Text: text, Pen: cta608.Pen{Color: cta608.White}}},
		}},
	}), cta608.SerializeOptions{})

	// One pair per frame at 30 fps, exactly as the mp4 and SCC read paths do.
	units := make([]DecodeUnit, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, DecodeUnit{
			TimeMS: int64(i/2) * 1000 / 30,
			Field1: data[i : i+2],
		})
	}
	cues, err := CuesFromUnits(units, cue.SegmentOptions{DefaultDur: 2 * time.Second})
	if err != nil {
		t.Fatalf("CuesFromUnits: %v", err)
	}
	if len(cues) == 0 {
		t.Fatal("no cues decoded")
	}
	if got := rowsText(cues[len(cues)-1].Content); got != text {
		t.Errorf("decoded %q, want %q", got, text)
	}
}

// TestSCCTextSCCRoundTripTiming is the payoff of the FlipOnTime default, measured on
// the real fixture. SCC and WebVTT time captions by different conventions: an SCC
// entry's timecode is when its first byte pair is *transmitted*, while a WebVTT cue's
// start is when the caption is *visible*. They differ by exactly the pop-on build, so
// pre-rolling the build makes the two conventions agree and SCC -> text -> SCC an
// identity on the timecode.
//
// With FlipAfterBuild the returned entry is late by the build length (12 frames here),
// which is what shipped before v0.8.0.
func TestSCCTextSCCRoundTripTiming(t *testing.T) {
	const sccIn = "Scenarist_SCC V1.0\n\n" +
		"00:00:01:00\t9420 9420 94ae 94ae 94e0 94e0 c845 4c4c 4f20 574f 524c c480 942f 942f\n\n" +
		"00:00:04:00\t942c 942c\n"

	for _, tc := range []struct {
		name    string
		timing  schedule.FlipTiming
		wantTC  string
		wantVTT time.Duration // the cue start the VTT step reports
	}{
		// The EOC is the 13th of 14 pairs, so the caption becomes visible 12 frames
		// (400 ms at 30 fps) after the entry's timecode. That display time is what
		// WebVTT must carry, and pre-rolling maps it back onto the original timecode.
		{"FlipOnTime", schedule.FlipOnTime, "00:00:01:00", 1400 * time.Millisecond},
		{"FlipAfterBuild", schedule.FlipAfterBuild, "00:00:01:12", 1400 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{FPS: 30, FlipTiming: tc.timing}

			cues, err := ReadCues(FormatSCC, strings.NewReader(sccIn), opts)
			if err != nil {
				t.Fatalf("ReadCues SCC: %v", err)
			}
			if len(cues) != 1 {
				t.Fatalf("got %d cues, want 1", len(cues))
			}
			// Reading is timing-policy-free: it reports when the decoder shows the
			// caption, which is the build's end either way.
			if cues[0].Start != tc.wantVTT {
				t.Errorf("cue start = %v, want %v", cues[0].Start, tc.wantVTT)
			}
			if cues[0].End != 4*time.Second {
				t.Errorf("cue end = %v, want 4s (the EDM lands exactly)", cues[0].End)
			}

			var buf bytes.Buffer
			if err := WriteCues(FormatSCC, &buf, cues, opts); err != nil {
				t.Fatalf("WriteCues SCC: %v", err)
			}
			if !strings.Contains(buf.String(), tc.wantTC+"\t") {
				t.Errorf("round-tripped SCC lacks timecode %s:\n%s", tc.wantTC, buf.String())
			}
		})
	}
}

// The whole SCC document must come back byte-identical under the default timing —
// pairs and timecodes — so the only asymmetry left in SCC -> text -> SCC is the
// terminating EDM that cue.Compile always appends.
func TestSCCTextSCCRoundTripByteExact(t *testing.T) {
	const sccIn = "Scenarist_SCC V1.0\n\n" +
		"00:00:01:00\t9420 9420 94ae 94ae 94e0 94e0 c845 4c4c 4f20 574f 524c c480 942f 942f\n\n" +
		"00:00:04:00\t942c 942c\n"
	opts := Options{FPS: 30}

	cues, err := ReadCues(FormatSCC, strings.NewReader(sccIn), opts)
	if err != nil {
		t.Fatalf("ReadCues: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteCues(FormatSCC, &buf, cues, opts); err != nil {
		t.Fatalf("WriteCues: %v", err)
	}
	norm := func(s string) []string {
		var out []string
		for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				out = append(out, ln)
			}
		}
		return out
	}
	got, want := norm(buf.String()), norm(sccIn)
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), buf.String())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

// TestCuesFromUnitsCoalescesRollUp checks the mode reaches cue.Segment. Roll-up
// writes straight to the displayed screen, so without the mode every byte pair would
// cut a cue and an 11-character line would arrive as six of them.
func TestCuesFromUnitsCoalescesRollUp(t *testing.T) {
	var enc cta608.Encoder
	enc.SetMode(cta608.RollUp, 3)
	var data []byte
	for _, w := range [][]string{{"HELLO THERE"}, {"HELLO THERE", "SECOND LINE"}} {
		var lines []cta608.Line
		base := 15 - len(w) + 1
		for j, txt := range w {
			lines = append(lines, cta608.Line{
				Row: base + j, Align: cta608.AlignLeft,
				Runs: []cta608.Run{{Text: txt, Pen: cta608.Pen{Color: cta608.White}}},
			})
		}
		data = append(data, cta608.Serialize(enc.Apply(cta608.CaptionBlock{
			Mode: cta608.RollUp, RollUpRows: 3, Lines: lines,
		}), cta608.SerializeOptions{})...)
	}

	units := make([]DecodeUnit, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, DecodeUnit{TimeMS: int64(i/2) * 1000 / 30, Field1: data[i : i+2]})
	}

	coalesced, err := CuesFromUnits(units, cue.SegmentOptions{DefaultDur: 2 * time.Second})
	if err != nil {
		t.Fatalf("CuesFromUnits: %v", err)
	}
	if len(coalesced) != 2 {
		t.Errorf("got %d cues, want 2 (one per scroll step): %v", len(coalesced), cueTexts(coalesced))
	}
	if got := rowsText(coalesced[0].Content); got != "HELLO THERE" {
		t.Errorf("cue 0 = %q, want the completed line %q", got, "HELLO THERE")
	}

	perChange, err := CuesFromUnits(units, cue.SegmentOptions{
		DefaultDur: 2 * time.Second, Coalesce: cue.CoalesceNone,
	})
	if err != nil {
		t.Fatalf("CuesFromUnits (CoalesceNone): %v", err)
	}
	if len(perChange) <= len(coalesced) {
		t.Errorf("CoalesceNone gave %d cues, want more than the coalesced %d",
			len(perChange), len(coalesced))
	}
}

// cueTexts renders a cue list's text for failure messages.
func cueTexts(cues []cue.TimedCue) []string {
	out := make([]string, 0, len(cues))
	for _, c := range cues {
		out = append(out, rowsText(c.Content))
	}
	return out
}
