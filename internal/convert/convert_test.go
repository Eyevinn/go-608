package convert

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/scc"
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
