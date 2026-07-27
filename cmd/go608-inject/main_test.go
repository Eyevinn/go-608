package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/internal/convert"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/mp4ff/mp4"
)

// A real 1280x720 AVC SPS/PPS (from mp4ff's initcreator) so the built init parses
// as a valid video track; the per-frame VCL payloads are placeholders.
const (
	synthTimescale = 90000
	avcSPSHex      = "67640020accac05005bb0169e0000003002000000c9c4c000432380008647c12401cb1c31380"
	avcPPSHex      = "68b5df20"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, buildPlainAVC(t, 30, 60), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "c.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:01,000 --> 00:00:02,000\nHI\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	cases := []struct {
		desc string
		args []string
		err  bool
	}{
		{"version", []string{appName, "-version"}, false},
		{"help", []string{appName, "-h"}, false},
		{"unknown flag", []string{appName, "-x"}, true},
		{"no sub", []string{appName, "-i", in, "-o", out}, true},
		{"bad cc-count", []string{appName, "-sub", sub, "-i", in, "-o", out, "-cc-count", "huge"}, true},
		{"fps out of range", []string{appName, "-sub", sub, "-i", in, "-o", out, "-fps", "15"}, true},
		{"inject ok", []string{appName, "-sub", sub, "-i", in, "-o", out, "-fps", "30"}, false},
		{"format-only ok", []string{appName, "-sub", sub, "-to", "webvtt"}, false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := run(c.args, io.Discard)
			if c.err && err == nil {
				t.Error("expected error, got nil")
			}
			if !c.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunVersionOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-version"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), appName) {
		t.Errorf("version output %q missing app name", buf.String())
	}
}

// TestInjectSubtitleRoundTrip is acceptance criterion #26.1: inject an SRT into an
// mp4 and, extracting it back through the same decode stack, recover the cue text.
func TestInjectSubtitleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, buildPlainAVC(t, 30, 180), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "c.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:01,000 --> 00:00:03,000\nHELLO WORLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := run([]string{appName, "-i", in, "-sub", sub, "-o", out, "-fps", "30"}, io.Discard); err != nil {
		t.Fatalf("inject: %v", err)
	}

	cues := decodeMP4Cues(t, out)
	if !cuesContain(cues, "HELLO WORLD") {
		t.Fatalf("injected caption not recovered; cues=%v", cueTexts(cues))
	}
	// The caption should sit near its 1s window (allowing quantization slack).
	for _, c := range cues {
		if strings.Contains(screenText(c), "HELLO") {
			if c.Start < 500*time.Millisecond || c.Start > 1500*time.Millisecond {
				t.Errorf("caption start %v, want ~1s", c.Start)
			}
		}
	}
}

// TestInjectSCCByteExact is acceptance criterion #26.2: an injected SCC file's
// byte pairs survive the round-trip through carriage unchanged.
func TestInjectSCCByteExact(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, buildPlainAVC(t, 30, 120), 0o644); err != nil {
		t.Fatal(err)
	}
	// Frame 10 at 30fps non-drop == timecode 00:00:00:10; five successive pairs.
	sub := filepath.Join(dir, "c.scc")
	sccBody := "Scenarist_SCC V1.0\n\n00:00:00:10\t9420 94ae 9162 c849 942f\n"
	if err := os.WriteFile(sub, []byte(sccBody), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := run([]string{appName, "-i", in, "-sub", sub, "-o", out, "-fps", "30"}, io.Discard); err != nil {
		t.Fatalf("inject scc: %v", err)
	}

	// Read the output mp4's field-1 pair per sample; the SCC pairs must land
	// byte-exact on frames 10..14.
	got := mp4Field1ByFrame(t, out)
	want := map[int]string{10: "9420", 11: "94ae", 12: "9162", 13: "c849", 14: "942f"}
	for frame, hexPair := range want {
		if got[frame] != hexPair {
			t.Errorf("frame %d: got %q, want %q", frame, got[frame], hexPair)
		}
	}
}

// TestFormatOnlyShared checks the format-only mode (shared with go608-extract):
// SRT -> WebVTT with no mp4.
func TestFormatOnlyShared(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "c.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:01,000 --> 00:00:02,000\nHELLO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := run([]string{appName, "-sub", sub, "-to", "webvtt"}, &buf); err != nil {
		t.Fatalf("format-only: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "WEBVTT") || !strings.Contains(buf.String(), "HELLO") {
		t.Errorf("format-only output not valid WebVTT with the cue:\n%s", buf.String())
	}
}

// --- helpers ---

func cueTexts(cues []cue.TimedCue) []string {
	out := make([]string, len(cues))
	for i, c := range cues {
		out[i] = screenText(c)
	}
	return out
}

func cuesContain(cues []cue.TimedCue, want string) bool {
	for _, c := range cues {
		if strings.Contains(screenText(c), want) {
			return true
		}
	}
	return false
}

func screenText(c cue.TimedCue) string {
	var b strings.Builder
	for _, row := range c.Content.Rows {
		for _, run := range row.Runs {
			b.WriteString(run.Text)
		}
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}

// decodeMP4Cues extracts cues from a fragmented mp4 via the shared convert core.
func decodeMP4Cues(t *testing.T, path string) []cue.TimedCue {
	t.Helper()
	f, track, trex := openMP4Test(t, path)
	samples, err := mp4io.Samples(f, trex)
	if err != nil {
		t.Fatal(err)
	}
	units := make([]convert.DecodeUnit, 0, len(samples))
	for _, s := range samples {
		nalus, err := carriage.SampleNALUs(s.Data)
		if err != nil {
			t.Fatal(err)
		}
		f1, _, err := carriage.FieldPairs(nalus, track.Codec)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, convert.DecodeUnit{
			TimeMS: int64(math.Round(float64(s.DecodeTime) * 1000 / float64(track.Timescale))),
			Field1: f1,
		})
	}
	cues, err := convert.CuesFromUnits(units, cue.SegmentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return cues
}

// mp4Field1ByFrame returns each sample's field-1 pair as hex, keyed by frame index.
func mp4Field1ByFrame(t *testing.T, path string) map[int]string {
	t.Helper()
	f, track, trex := openMP4Test(t, path)
	samples, err := mp4io.Samples(f, trex)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for i, s := range samples {
		nalus, err := carriage.SampleNALUs(s.Data)
		if err != nil {
			t.Fatal(err)
		}
		f1, _, err := carriage.FieldPairs(nalus, track.Codec)
		if err != nil {
			t.Fatal(err)
		}
		if len(f1) == 2 {
			out[i] = hex.EncodeToString(f1)
		}
	}
	return out
}

func openMP4Test(t *testing.T, path string) (*mp4.File, mp4io.Track, *mp4.TrexBox) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	track, trex, err := mp4io.VideoTrack(f)
	if err != nil {
		t.Fatal(err)
	}
	return f, track, trex
}

// buildPlainAVC builds a caption-free single-track AVC fragmented mp4 with nFrames
// placeholder samples — the injection target for the round-trip tests.
func buildPlainAVC(t *testing.T, fps float64, nFrames int) []byte {
	t.Helper()
	sps, _ := hex.DecodeString(avcSPSHex)
	pps, _ := hex.DecodeString(avcPPSHex)
	frameDur := uint32(math.Round(synthTimescale / fps))

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(synthTimescale, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		t.Fatal(err)
	}
	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	seg.AddFragment(frag)
	for i := 0; i < nFrames; i++ {
		vcl := []byte{0x41, 0x9a, 0x00, byte(i)}
		flags := mp4.NonSyncSampleFlags
		if i == 0 {
			vcl = []byte{0x65, 0x88, 0x84, 0x00}
			flags = mp4.SyncSampleFlags
		}
		data := carriage.PrefixNALUs(vcl)
		frag.AddFullSample(mp4.FullSample{
			Sample:     mp4.Sample{Flags: flags, Dur: frameDur, Size: uint32(len(data))},
			DecodeTime: uint64(i) * uint64(frameDur),
			Data:       data,
		})
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if err := seg.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
