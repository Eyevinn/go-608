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
		{desc: "bad mode", args: []string{appName, "-o", out, "-mode", "crawl"}, err: true},
		{desc: "bad roll-up window", args: []string{appName, "-o", out, "-mode", "roll-up5"}, err: true},
		{desc: "bad unit-mode", args: []string{appName, "-o", out, "-unit-mode", "bogus"}, err: true},
		{
			desc: "cue-start needs pop-on",
			args: []string{appName, "-o", out, "-unit-mode", "cue-start", "-mode", "paint-on"},
			err:  true,
		},
		{
			desc: "carry needs roll-up",
			args: []string{appName, "-o", out, "-unit-mode", "carry", "-mode", "pop-on"},
			err:  true,
		},
		{
			desc: "unit-seconds must be positive",
			args: []string{appName, "-o", out, "-unit-mode", "default", "-unit-seconds", "0"},
			err:  true,
		},
		{
			desc: "unit-seconds shorter than a frame",
			args: []string{appName, "-o", out, "-unit-mode", "default", "-unit-seconds", "0.001"},
			err:  true,
		},
		{
			desc: "unit mode ok",
			args: []string{appName, "-o", out, "-fps", "30", "-seconds", "4", "-start", startStr,
				"-unit-mode", "default", "-unit-seconds", "2"},
			err: false,
		},
		{
			desc: "roll-up ok",
			args: []string{appName, "-o", out, "-fps", "30", "-seconds", "1", "-start", startStr,
				"-mode", "roll-up3"},
			err: false,
		},
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

// TestSyntheticRollUp checks the -mode roll-up path end to end: the window scrolls
// each second and the new lines are typed onto the bottom row, with the previous
// second still visible above and nothing ever erased.
func TestSyntheticRollUp(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, startStr)
	out := filepath.Join(t.TempDir(), "roll.mp4")
	args := []string{appName, "-o", out, "-fps", "30", "-seconds", "3", "-start", startStr, "-mode", "roll-up3"}
	if err := run(args, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	flips := decodeFlips(t, data, carriage.CodecAVC)
	if len(flips) < 30 {
		t.Fatalf("got %d screen changes in 3 s of roll-up, want many (one per pair)", len(flips))
	}
	// The last state of second 1 has that second's lines on rows 14/15 and second 0's
	// bottom line scrolled up to row 13 — the history roll-up keeps and never resends.
	var last flip
	for _, fl := range flips {
		if fl.frame >= 30 && fl.frame < 60 {
			last = fl
		}
	}
	wantUTC := time.Unix(start.Unix()+1, 0).UTC().Format("2006-01-02T15:04:05Z")
	if last.rows[14] != wantUTC {
		t.Errorf("row 14 = %q at the end of second 1, want %q", last.rows[14], wantUTC)
	}
	if !strings.HasPrefix(last.rows[15], "MEDIA ") {
		t.Errorf("row 15 = %q, want the media line on the base row", last.rows[15])
	}
	if last.rows[13] == "" {
		t.Errorf("row 13 empty, want second 0's last line scrolled up (rows=%v)", last.rows)
	}
}

// clockMP4 runs the tool into a temp file and returns the bytes.
func clockMP4(t *testing.T, extra ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clock.mp4")
	args := append([]string{appName, "-o", out, "-fps", "30", "-seconds", "4", "-start", startStr}, extra...)
	if err := run(args, io.Discard); err != nil {
		t.Fatalf("run %v: %v", extra, err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	return data
}

// TestUnitModeCueStartFlipsOnBoundaries is the regression test for the per-unit
// pop-on path: with -unit-mode cue-start every flip must land on a cue boundary
// (frames 30, 60, 90 at 30 fps), including the flip at frame 60 that opens the second
// unit — its build was transmitted in the first unit's tail.
//
// The bug this pins: WithFlipAtCueStart is a contract between neighbouring units, so a
// unit whose successor is not given the option transmits that successor's build a
// second time and the flip lands a whole build late (frame 83 rather than 60 here). It
// was caught by decoding the output with ffmpeg, not by the library's own tests.
func TestUnitModeCueStartFlipsOnBoundaries(t *testing.T) {
	data := clockMP4(t, "-unit-mode", "cue-start", "-unit-seconds", "2")

	flips := decodeFlips(t, data, carriage.CodecAVC)
	// Unit 0's first cue has no preceding unit to carry its build, so its EOC flips an
	// empty buffer and nothing shows until the next cue — the documented cold start.
	want := []struct {
		frame int
		utc   string
	}{
		{30, "2026-07-20T12:00:01Z"},
		{60, "2026-07-20T12:00:02Z"}, // built in unit 0's tail, flipped by unit 1
		{90, "2026-07-20T12:00:03Z"},
	}
	if len(flips) != len(want) {
		for _, fl := range flips {
			t.Logf("flip @frame %d: %q / %q", fl.frame, fl.utc, fl.media)
		}
		t.Fatalf("got %d flips, want %d (one per cue boundary)", len(flips), len(want))
	}
	for i, w := range want {
		if flips[i].frame != w.frame || flips[i].utc != w.utc {
			t.Errorf("flip %d = frame %d %q, want frame %d %q", i, flips[i].frame, flips[i].utc, w.frame, w.utc)
		}
	}
}

// TestUnitModeRollUpResetVsCarry checks the two cross-unit roll-up policies through
// the whole tool: reset clears the window on the second unit's first frame, carry
// keeps it and scrolls instead.
func TestUnitModeRollUpResetVsCarry(t *testing.T) {
	const boundary = 60 // frames per 2 s unit at 30 fps

	// emptyAt reports whether the screen is blank at any change on the given frame.
	emptyAt := func(flips []flip, frame int) bool {
		for _, fl := range flips {
			if fl.frame == frame && len(fl.rows) == 0 {
				return true
			}
		}
		return false
	}
	// filledAt returns the row count at the last change at or before frame.
	filledAt := func(flips []flip, frame int) int {
		n := 0
		for _, fl := range flips {
			if fl.frame <= frame {
				n = len(fl.rows)
			}
		}
		return n
	}

	reset := decodeFlips(t, clockMP4(t, "-mode", "roll-up3", "-unit-mode", "default", "-unit-seconds", "2"),
		carriage.CodecAVC)
	if !emptyAt(reset, boundary) {
		t.Errorf("reset: no cleared screen on frame %d, want the window reset at the unit boundary", boundary)
	}

	carry := decodeFlips(t, clockMP4(t, "-mode", "roll-up3", "-unit-mode", "carry", "-unit-seconds", "2"),
		carriage.CodecAVC)
	if emptyAt(carry, boundary) {
		t.Errorf("carry: screen cleared on frame %d, want the window kept across the unit boundary", boundary)
	}
	if n := filledAt(carry, boundary+1); n < 2 {
		t.Errorf("carry: %d rows shown just after the boundary, want the previous unit's lines scrolled up", n)
	}
}

// TestUnitModePaintOn checks the per-unit paint-on path end to end: the caption is
// typed out within each cue and every second gets its own.
func TestUnitModePaintOn(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, startStr)
	flips := decodeFlips(t, clockMP4(t, "-mode", "paint-on", "-unit-mode", "default", "-unit-seconds", "2"),
		carriage.CodecAVC)
	if len(flips) < 40 {
		t.Fatalf("got %d screen changes in 4 s of per-unit paint-on, want many (one per pair)", len(flips))
	}
	for sec := 0; sec < 4; sec++ {
		var last flip
		for _, fl := range flips {
			if fl.frame >= sec*30 && fl.frame < (sec+1)*30 {
				last = fl
			}
		}
		want := time.Unix(start.Unix()+int64(sec), 0).UTC().Format("2006-01-02T15:04:05Z")
		if last.utc != want {
			t.Errorf("second %d ends showing %q, want %q", sec, last.utc, want)
		}
	}
}

// TestUnitModeMatchesGeneratorPopOn pins the two APIs against each other: the per-unit
// pop-on builder with its flips on cue boundaries shows the same captions as the
// continuous generator, which also flips on the second boundary. The wire bytes differ
// (the unit path splits at unit boundaries), the captions do not.
func TestUnitModeMatchesGeneratorPopOn(t *testing.T) {
	cont := decodeFlips(t, clockMP4(t), carriage.CodecAVC)
	unit := decodeFlips(t, clockMP4(t, "-unit-mode", "cue-start", "-unit-seconds", "2"), carriage.CodecAVC)

	// The continuous generator flips at the last frame of each second (29, 59, 89) for
	// the *next* second; the per-unit cue-start path flips on the boundary (30, 60, 90)
	// for the second it names. Same captions, one frame apart.
	//
	// The continuous run has one flip more: on its last frame it flips the caption for
	// the second after the run ends (pop-on builds ahead), which the per-unit run has no
	// slice for. Compare the flips they share.
	if len(cont) < len(unit) || len(unit) == 0 {
		t.Fatalf("continuous gave %d flips, per-unit %d", len(cont), len(unit))
	}
	for i := range unit {
		if cont[i].utc != unit[i].utc || cont[i].media != unit[i].media {
			t.Errorf("flip %d: continuous %q/%q vs per-unit %q/%q",
				i, cont[i].utc, cont[i].media, unit[i].utc, unit[i].media)
		}
		if unit[i].frame != cont[i].frame+1 {
			t.Errorf("flip %d: per-unit frame %d, continuous %d (want one frame later)",
				i, unit[i].frame, cont[i].frame)
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
