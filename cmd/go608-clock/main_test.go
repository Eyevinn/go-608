package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/mp4ff/mp4"
)

// startStr is a whole-second-aligned wall-clock start, so the per-second pop-on
// flips land on exact frame boundaries (frame round(fps)-1, then every round(fps)).
const startStr = "2026-07-20T12:00:00Z"

func TestRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.mp4")
	cases := []struct {
		desc string
		args []string
		err  bool
	}{
		{desc: "version", args: []string{appName, "-version"}, err: false},
		{desc: "help", args: []string{appName, "-h"}, err: false},
		{desc: "unknown flag", args: []string{appName, "-x"}, err: true},
		{desc: "missing output", args: []string{appName}, err: true},
		{desc: "bad start", args: []string{appName, "-o", out, "-start", "not-a-time"}, err: true},
		{desc: "bad line", args: []string{appName, "-o", out, "-line", "99:white:utc"}, err: true},
		{desc: "fps out of caption range", args: []string{appName, "-o", out, "-fps", "15"}, err: true},
		{desc: "bad mode", args: []string{appName, "-o", out, "-mode", "roll-up"}, err: true},
		{
			desc: "synthetic ok",
			args: []string{appName, "-o", out, "-fps", "30", "-seconds", "1", "-start", startStr},
			err:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := run(c.args, io.Discard)
			if c.err && err == nil {
				t.Error("expected error but got nil")
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
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), appName) {
		t.Errorf("version output %q does not contain app name %q", buf.String(), appName)
	}
}

// TestSyntheticWallClock is the acceptance test (criteria 1 & 2): the emitted
// mp4's 608 SEI, extracted via carriage.FieldPairs + Decoder, shows the two-line
// wall clock advancing one second per second, frame-accurately, at fps 25 and 30.
func TestSyntheticWallClock(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, startStr)
	base := start.Unix()

	for _, fps := range []float64{25, 30} {
		t.Run(fmt.Sprintf("fps%g", fps), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "clock.mp4")
			fpsArg := strconv.FormatFloat(fps, 'g', -1, 64)
			args := []string{appName, "-o", out, "-fps", fpsArg, "-seconds", "4", "-start", startStr}
			if err := run(args, io.Discard); err != nil {
				t.Fatalf("run: %v", err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading output: %v", err)
			}

			flips := decodeFlips(t, data, carriage.CodecAVC)
			if len(flips) < 3 {
				t.Fatalf("got %d caption flips, want at least 3", len(flips))
			}

			fpsInt := int(math.Round(fps))
			for k, fl := range flips {
				// Frame-accuracy: flip k lands on the last frame of its second.
				wantFrame := (k+1)*fpsInt - 1
				if fl.frame != wantFrame {
					t.Errorf("flip %d at frame %d, want %d (fps %g)", k, fl.frame, wantFrame, fps)
				}
				// One second per second: the UTC line shows consecutive seconds.
				wantUTC := time.Unix(base+int64(k)+1, 0).UTC().Format("2006-01-02T15:04:05Z")
				if fl.utc != wantUTC {
					t.Errorf("flip %d UTC = %q, want %q", k, fl.utc, wantUTC)
				}
				// The media line advances and is present.
				if !strings.HasPrefix(fl.media, "MEDIA ") {
					t.Errorf("flip %d media = %q, want a MEDIA hh:mm:ss line", k, fl.media)
				}
			}
		})
	}
}

// TestSyntheticPaintOn checks the -mode paint-on path end to end: each second the
// caption is erased on the boundary frame and then written onto the screen a couple
// of characters at a time, instead of appearing whole on one flip.
func TestSyntheticPaintOn(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, startStr)
	out := filepath.Join(t.TempDir(), "paint.mp4")
	args := []string{appName, "-o", out, "-fps", "30", "-seconds", "3", "-start", startStr, "-mode", "paint-on"}
	if err := run(args, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	flips := decodeFlips(t, data, carriage.CodecAVC)
	// A pop-on run of the same length yields 3 changes (one flip per second); painting
	// changes the screen on most frames of each second.
	if len(flips) < 30 {
		t.Fatalf("got %d screen changes in 3 s of paint-on, want many (one per pair)", len(flips))
	}
	// Each second opens with a cleared screen and closes with the complete caption
	// for that same second (paint-on shows the second it is painted in).
	for sec := 0; sec < 3; sec++ {
		wantUTC := time.Unix(start.Unix()+int64(sec), 0).UTC().Format("2006-01-02T15:04:05Z")
		var cleared bool
		var last flip
		for _, fl := range flips {
			if fl.frame < sec*30 || fl.frame >= (sec+1)*30 {
				continue
			}
			if fl.frame == sec*30 && fl.utc == "" && fl.media == "" {
				cleared = true
			}
			last = fl
		}
		if sec > 0 && !cleared {
			t.Errorf("second %d: no clear on its first frame (frame %d)", sec, sec*30)
		}
		if last.utc != wantUTC {
			t.Errorf("second %d ends showing %q, want %q", sec, last.utc, wantUTC)
		}
		if !strings.HasPrefix(last.media, "MEDIA ") {
			t.Errorf("second %d media = %q, want a MEDIA hh:mm:ss line", sec, last.media)
		}
	}
}

// TestLineConfigHonored checks that a custom -line config drives which rows carry
// the caption (acceptance criterion 2: "honors the line Config").
func TestLineConfigHonored(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cfg.mp4")
	args := []string{appName, "-o", out, "-fps", "30", "-seconds", "2", "-start", startStr, "-line", "5:white:utc"}
	if err := run(args, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, _ := os.ReadFile(out)
	flips := decodeFlips(t, data, carriage.CodecAVC)
	if len(flips) == 0 {
		t.Fatal("no caption flips decoded")
	}
	last := flips[len(flips)-1]
	if last.rows[5] == "" {
		t.Errorf("row 5 empty; configured single UTC line on row 5 not honored (rows=%v)", last.rows)
	}
	if last.rows[14] != "" || last.rows[15] != "" {
		t.Errorf("default rows 14/15 present despite custom config (rows=%v)", last.rows)
	}
}

// TestOverrunReported checks that content exceeding the one-second build budget is
// reported (acceptance criterion 2: "reports overrun if content doesn't fit").
func TestOverrunReported(t *testing.T) {
	out := filepath.Join(t.TempDir(), "overrun.mp4")
	// Four full lines cannot build within one second at 25 fps (budget 24 pairs).
	args := []string{appName, "-o", out, "-fps", "25", "-seconds", "2", "-start", startStr,
		"-line", "12:white:utc", "-line", "13:white:utc", "-line", "14:white:utc", "-line", "15:yellow:media"}
	var buf bytes.Buffer
	if err := run(args, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(buf.String(), "overran") {
		t.Errorf("expected an overrun warning, got: %q", buf.String())
	}
}

// TestInputSplice exercises the -i path end to end: splice the caption into a
// caption-free single-track AVC fMP4 and read the wall clock back out.
func TestInputSplice(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, buildPlainAVC(t, 30, 90), 0o644); err != nil {
		t.Fatalf("writing input: %v", err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := run([]string{appName, "-i", in, "-o", out, "-fps", "30", "-start", startStr}, io.Discard); err != nil {
		t.Fatalf("run -i: %v", err)
	}

	data, _ := os.ReadFile(out)
	flips := decodeFlips(t, data, carriage.CodecAVC)
	if len(flips) < 2 {
		t.Fatalf("got %d flips from spliced input, want at least 2", len(flips))
	}
	start, _ := time.Parse(time.RFC3339, startStr)
	wantUTC := time.Unix(start.Unix()+1, 0).UTC().Format("2006-01-02T15:04:05Z")
	if flips[0].utc != wantUTC {
		t.Errorf("first spliced flip UTC = %q, want %q", flips[0].utc, wantUTC)
	}
	// The spliced sample must still contain its original VCL NAL (video preserved).
	f, _ := mp4.DecodeFile(bytes.NewReader(data))
	_, trex, _ := mp4io.VideoTrack(f)
	samples, _ := f.Segments[0].Fragments[0].GetFullSamples(trex)
	nalus, _ := carriage.SampleNALUs(samples[0].Data)
	if len(nalus) < 2 {
		t.Errorf("spliced sample has %d NAL units, want >= 2 (SEI + VCL)", len(nalus))
	}
}

// --- test helpers ---

// flip is one decoded caption change: the frame index it occurred on and the text
// of the tracked rows.
type flip struct {
	frame int
	utc   string
	media string
	rows  map[int]string
}

// decodeFlips replays the output mp4 frame by frame, feeding each sample's field-1
// pair to a cta608.Decoder and recording every displayed-Screen change.
func decodeFlips(t *testing.T, data []byte, codec carriage.Codec) []flip {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	_, trex, err := mp4io.VideoTrack(f)
	if err != nil {
		t.Fatalf("VideoTrack: %v", err)
	}

	var dec cta608.Decoder
	var flips []flip
	frame := 0
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				t.Fatalf("GetFullSamples: %v", err)
			}
			for _, s := range samples {
				nalus, err := carriage.SampleNALUs(s.Data)
				if err != nil {
					t.Fatalf("SampleNALUs: %v", err)
				}
				f1, _, err := carriage.FieldPairs(nalus, codec)
				if err != nil {
					t.Fatalf("FieldPairs: %v", err)
				}
				if err := dec.Feed(f1); err != nil {
					t.Fatalf("Decoder.Feed: %v", err)
				}
				if dec.Changed() {
					screen := dec.Screen()
					flips = append(flips, flip{
						frame: frame,
						utc:   rowText(screen, 14),
						media: rowText(screen, 15),
						rows:  allRowText(screen),
					})
				}
				frame++
			}
		}
	}
	return flips
}

// rowText returns the concatenated, trimmed character text of the given screen row.
func rowText(s cta608.Screen, index int) string {
	for _, r := range s.Rows {
		if r.Index == index {
			var b strings.Builder
			for _, run := range r.Runs {
				b.WriteString(run.Text)
			}
			return strings.TrimSpace(b.String())
		}
	}
	return ""
}

// allRowText maps each present row index to its text.
func allRowText(s cta608.Screen) map[int]string {
	m := make(map[int]string, len(s.Rows))
	for _, r := range s.Rows {
		m[r.Index] = rowText(s, r.Index)
	}
	return m
}

// buildPlainAVC builds a single-track AVC fragmented mp4 with nFrames placeholder
// video samples and NO caption SEI — the input fixture for the -i splice test.
func buildPlainAVC(t *testing.T, fps float64, nFrames int) []byte {
	t.Helper()
	sps, _ := hex.DecodeString(avcSPSHex)
	pps, _ := hex.DecodeString(avcPPSHex)
	frameDur := uint32(math.Round(synthTimescale / fps))

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(synthTimescale, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		t.Fatalf("SetAVCDescriptor: %v", err)
	}
	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(1, 1)
	if err != nil {
		t.Fatalf("CreateFragment: %v", err)
	}
	seg.AddFragment(frag)
	for i := 0; i < nFrames; i++ {
		data := carriage.PrefixNALUs(dummyVCL(i))
		flags := mp4.NonSyncSampleFlags
		if i == 0 {
			flags = mp4.SyncSampleFlags
		}
		frag.AddFullSample(mp4.FullSample{
			Sample:     mp4.Sample{Flags: flags, Dur: frameDur, Size: uint32(len(data))},
			DecodeTime: uint64(i) * uint64(frameDur),
			Data:       data,
		})
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatalf("init.Encode: %v", err)
	}
	if err := seg.Encode(&buf); err != nil {
		t.Fatalf("seg.Encode: %v", err)
	}
	return buf.Bytes()
}
