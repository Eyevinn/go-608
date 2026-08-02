// Command go608-clock generates a wall-clock CTA-608 caption and splices it as
// SEI into a fragmented mp4 — the go-608 first-milestone demo (SPEC §7). It runs
// the whole encode spine end to end: generate.NextFrame → carriage.FrameSEINALU →
// splice the bare SEI NAL (4-byte length prefix) before the first VCL NAL of each
// frame.
//
// Two input modes:
//
//   - Synthetic (default): emit a self-contained single-track AVC fragmented mp4
//     whose per-frame samples carry the caption SEI. The video payloads are
//     placeholder VCL bytes (there is no in-tree H.264 encoder), so the output is
//     a structurally valid fMP4 for round-tripping the 608 — not decodable
//     pictures. This is the deterministic demo/round-trip path.
//   - Input (-i file.mp4): read a single-video-track fragmented mp4 (AVC, HEVC or AV1,
//     auto-detected) and splice the caption SEI into every frame, preserving the
//     original sample timing — captioning real, playable video.
//
// The caption is two centered lines by default (row 14 UTC RFC3339 white, row 15
// media time yellow); override with repeated -line flags. Pass -fps matching the
// media so the wall clock advances one second per second, frame-accurately.
//
// -mode picks how each second reaches the screen: pop-on (the default) builds it
// off-screen and flips it on whole at the second boundary; paint-on clears the
// screen on the boundary and writes the caption straight onto it, two characters
// per frame, so the text visibly types itself out and then stands until the next
// second's clear; roll-up[2-4] scrolls a 2-4 row window up instead of clearing and
// types each second's lines onto the bottom row, leaving the previous seconds
// visible above — the mode live captioning uses.
//
// Usage:
//
//	go608-clock -o out.mp4 -fps 30 -seconds 5
//	go608-clock -i in.mp4 -o out.mp4 -fps 25
//	go608-clock -o out.mp4 -line 14:white:utc -line 15:yellow:media
//	go608-clock -o out.mp4 -mode paint-on -seconds 5
//	go608-clock -o out.mp4 -mode roll-up3 -seconds 5
//	go608-clock -version
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/generate"
	"github.com/Eyevinn/go-608/internal"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/mp4ff/mp4"
)

const appName = "go608-clock"

// Synthetic-mode video parameters. A real 1280x720 AVC SPS/PPS (reused from
// mp4ff's initcreator example) so the init segment parses as a valid video track;
// the per-frame VCL payloads are placeholders (carriage rides the SEI alongside).
const (
	synthTimescale = 90000
	avcSPSHex      = "67640020accac05005bb0169e0000003002000000c9c4c000432380008647c12401cb1c31380"
	avcPPSHex      = "68b5df20"
)

var usg = `%s generates a wall-clock CTA-608 caption and splices it as SEI into a
fragmented mp4 (the go-608 first-milestone demo).

Without -i it emits a self-contained synthetic AVC fMP4 (placeholder video, real
608 SEI); with -i it splices the caption into every frame of an existing
single-video-track fragmented mp4 (AVC, HEVC or AV1), preserving its timing.

Usage of %s:
`

// lineFlag collects repeated -line "row:color:kind" values into a Config.
type lineFlag struct {
	specs []generate.LineSpec
	set   bool
}

func (lf *lineFlag) String() string { return "" }

// Set parses one "row:color:kind" line spec, e.g. "14:white:utc". Row is 1..15,
// kind is "utc" or "media"; color is any 608 color name ("" or unknown falls back
// to white, matching generate).
func (lf *lineFlag) Set(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return fmt.Errorf("line %q must be row:color:kind (e.g. 14:white:utc)", v)
	}
	row, err := strconv.Atoi(parts[0])
	if err != nil || row < 1 || row > 15 {
		return fmt.Errorf("line %q: row must be an integer 1..15", v)
	}
	switch parts[2] {
	case "utc", "media":
	default:
		return fmt.Errorf("line %q: kind must be \"utc\" or \"media\"", v)
	}
	lf.specs = append(lf.specs, generate.LineSpec{Row: row, Color: parts[1], Kind: parts[2]})
	lf.set = true
	return nil
}

// config returns the assembled Config, or generate.DefaultConfig when no -line
// flag was given.
func (lf *lineFlag) config() generate.Config {
	if !lf.set {
		return generate.DefaultConfig()
	}
	return generate.Config{Lines: lf.specs}
}

type options struct {
	version bool
	output  string
	input   string
	fps     float64
	seconds float64
	start   string
	mode    string
	lines   lineFlag
}

// genOptions turns the -mode flag into the generator options, rejecting any other
// value. "pop-on" builds each second off-screen and flips it on whole; "paint-on"
// clears at the second boundary and writes the caption onto the screen as it goes,
// so the text visibly types itself out; "roll-up[2-4]" scrolls the window up each
// second and types the new lines onto the bottom row, keeping the previous seconds
// visible above (the mode live captioning uses). Plain "roll-up" is a two-row
// window, which is what the default two-line caption fills.
func (o *options) genOptions() ([]generate.GeneratorOption, error) {
	switch o.mode {
	case "", "pop-on":
		return nil, nil
	case "paint-on":
		return []generate.GeneratorOption{generate.WithPaintOn()}, nil
	case "roll-up", "roll-up2", "roll-up3", "roll-up4":
		rows := 2
		if n := strings.TrimPrefix(o.mode, "roll-up"); n != "" {
			rows, _ = strconv.Atoi(n) // one of 2, 3, 4 by the case above
		}
		return []generate.GeneratorOption{generate.WithRollUp(rows)}, nil
	default:
		return nil, fmt.Errorf("-mode %q must be \"pop-on\", \"paint-on\" or \"roll-up[2-4]\"", o.mode)
	}
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options]\n\noptions:\n", appName)
		fs.PrintDefaults()
	}

	opts := options{}
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	fs.StringVar(&opts.output, "o", "", "output mp4 path (required)")
	fs.StringVar(&opts.input, "i", "", "input fragmented mp4 to splice into (default: synthetic frames)")
	fs.Float64Var(&opts.fps, "fps", 30, "frame rate driving caption cadence and wall-clock advance")
	fs.Float64Var(&opts.seconds, "seconds", 3, "synthetic-mode duration in seconds (ignored with -i)")
	fs.StringVar(&opts.start, "start", "", "wall-clock start time (RFC3339); default: now (UTC)")
	fs.StringVar(&opts.mode, "mode", "pop-on",
		"caption mode: \"pop-on\" (flip each second on whole), \"paint-on\" (type it out onto a cleared "+
			"screen) or \"roll-up[2-4]\" (scroll the window up and type onto the bottom row)")
	fs.Var(&opts.lines, "line", "caption line \"row:color:kind\" (repeatable; default: 14:white:utc, 15:yellow:media)")

	err := fs.Parse(args[1:])
	return &opts, err
}

func main() {
	if err := run(os.Args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	opts, err := parseOptions(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if opts.version {
		fmt.Fprintf(w, "%s %s\n", appName, internal.GetVersion())
		return nil
	}

	if opts.output == "" {
		return errors.New("no output path: pass -o out.mp4 (or -version)")
	}
	if opts.fps <= 0 {
		return fmt.Errorf("fps must be positive, got %g", opts.fps)
	}
	// The generator's cc_count is round(600/fps) and carriage only accepts 2..31;
	// out-of-range rates would panic deep in schedule.NewScheduler, so reject them
	// here with a clean message (SPEC §5.3: use a broadcast caption rate).
	if cc := int(math.Round(600.0 / opts.fps)); cc < 2 || cc > 31 {
		return fmt.Errorf("fps %g is outside the broadcast caption range "+
			"(cc_count %d not in 2..31); use a rate like 23.976..60", opts.fps, cc)
	}
	start, err := parseStart(opts.start)
	if err != nil {
		return err
	}
	genOpts, err := opts.genOptions()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	overran := false
	if opts.input != "" {
		overran, err = spliceInput(opts.input, opts.fps, start, opts.lines.config(), genOpts, &buf, w)
	} else {
		overran, err = writeSynthetic(opts.fps, opts.seconds, start, opts.lines.config(), genOpts, &buf, w)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.output, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", opts.output, err)
	}
	fmt.Fprintf(w, "wrote %s (%d bytes)\n", opts.output, buf.Len())
	if overran {
		fmt.Fprintf(w, "warning: caption overran the one-second build budget at %g fps; "+
			"shorten the lines, drop a line, or raise the frame rate\n", opts.fps)
	}
	return nil
}

// parseStart parses the -start flag, defaulting to now (UTC) when empty.
func parseStart(s string) (time.Time, error) {
	if s == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing -start %q as RFC3339: %w", s, err)
	}
	return t.UTC(), nil
}

// writeSynthetic emits a single-track AVC fragmented mp4 whose per-frame samples
// carry the wall-clock caption SEI. It returns whether the caption overran the
// build budget at this frame rate.
func writeSynthetic(
	fps, seconds float64, start time.Time, cfg generate.Config, genOpts []generate.GeneratorOption,
	out, status io.Writer,
) (bool, error) {
	nFrames := int(math.Round(seconds * fps))
	if nFrames < 1 {
		nFrames = 1
	}
	frameDur := uint32(math.Round(synthTimescale / fps))

	sps, err := hex.DecodeString(avcSPSHex)
	if err != nil {
		return false, fmt.Errorf("decoding built-in SPS: %w", err)
	}
	pps, err := hex.DecodeString(avcPPSHex)
	if err != nil {
		return false, fmt.Errorf("decoding built-in PPS: %w", err)
	}

	const trackID = uint32(1)
	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(synthTimescale, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		return false, fmt.Errorf("building AVC sample descriptor: %w", err)
	}

	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(1, trackID)
	if err != nil {
		return false, fmt.Errorf("creating fragment: %w", err)
	}
	seg.AddFragment(frag)

	gen := generate.NewGenerator(fps, cfg, genOpts...)
	startMS := start.UnixMilli()
	frameDurMS := 1000.0 / fps
	for i := 0; i < nFrames; i++ {
		wallMS := startMS + int64(math.Round(float64(i)*frameDurMS))
		fr := gen.NextFrame(wallMS)
		sei := carriage.FrameSEINALU(fr.Field1, fr.Field2, fr.CCCount, carriage.CodecAVC)
		data := carriage.PrefixNALUs(sei, dummyVCL(i))
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

	if err := init.Encode(out); err != nil {
		return false, fmt.Errorf("encoding init segment: %w", err)
	}
	if err := seg.Encode(out); err != nil {
		return false, fmt.Errorf("encoding media segment: %w", err)
	}
	fmt.Fprintf(status, "%s: synthetic AVC, %d frames at %g fps\n", appName, nFrames, fps)
	return gen.Overran(), nil
}

// dummyVCL returns the placeholder VCL NAL for synthetic frame i: an AVC IDR slice
// for the first frame (a sync sample), a non-IDR slice otherwise. These are not
// decodable pictures — the SEI is what the demo carries.
func dummyVCL(i int) []byte {
	if i == 0 {
		return []byte{0x65, 0x88, 0x84, 0x00}
	}
	return []byte{0x41, 0x9a, 0x00, byte(i)}
}

// spliceInput reads a single-video-track fragmented mp4 and splices the wall-clock
// caption SEI into every frame, rebuilding each fragment with the grown samples
// and preserving the original decode timing. It returns whether the caption
// overran the build budget at this frame rate.
func spliceInput(
	inPath string, fps float64, start time.Time, cfg generate.Config, genOpts []generate.GeneratorOption,
	out, status io.Writer,
) (bool, error) {
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", inPath, err)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return false, fmt.Errorf("decoding %s: %w", inPath, err)
	}
	track, trex, err := mp4io.VideoTrack(f)
	if err != nil {
		return false, err
	}

	gen := generate.NewGenerator(fps, cfg, genOpts...)
	startMS := start.UnixMilli()
	frameDurMS := 1000.0 / fps
	frames := 0
	// Drive the caption by frame index at the chosen rate (start + i*frameDur), so
	// the two lines stay in lockstep and the clock advances one second per second.
	ccFor := func(info mp4io.SampleInfo) ([]byte, error) {
		wallMS := startMS + int64(math.Round(float64(info.Index)*frameDurMS))
		fr := gen.NextFrame(wallMS)
		frames = info.Index + 1
		return carriage.BuildCCData(fr.Field1, fr.Field2, fr.CCCount), nil
	}
	if err := mp4io.SpliceFragmented(f, track, trex, out, ccFor); err != nil {
		return false, err
	}
	fmt.Fprintf(status, "%s: spliced %s into %d %s frames at %g fps\n", appName, inPath, frames, track.Codec, fps)
	return gen.Overran(), nil
}
