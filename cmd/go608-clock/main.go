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
// -unit-mode switches from the continuous generator (one call per frame) to the
// per-unit API (one call per DASH segment / MoQ group, generate.BuildUnitCues and its
// paint-on and roll-up siblings), with units of -unit-seconds. It exists so the demo
// covers the API a stateless segment server uses, including the cross-unit policies
// that only exist there: "cue-start" moves each pop-on flip onto its cue boundary,
// carrying the build in the previous unit's tail, and "carry" keeps the roll-up window
// across unit boundaries instead of clearing it on each unit's first frame.
//
// Usage:
//
//	go608-clock -o out.mp4 -fps 30 -seconds 5
//	go608-clock -i in.mp4 -o out.mp4 -fps 25
//	go608-clock -o out.mp4 -line 14:white:utc -line 15:yellow:media
//	go608-clock -o out.mp4 -mode paint-on -seconds 5
//	go608-clock -o out.mp4 -mode roll-up3 -seconds 5
//	go608-clock -o out.mp4 -unit-mode cue-start -unit-seconds 2 -seconds 6
//	go608-clock -o out.mp4 -mode roll-up3 -unit-mode carry -unit-seconds 2
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
	"github.com/Eyevinn/go-608/schedule"
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

// Caption modes (-mode) and per-unit placement policies (-unit-mode).
const (
	modePopOn   = "pop-on"
	modePaintOn = "paint-on"
	modeRollUp  = "roll-up"

	unitOff      = ""          // drive the continuous Generator, one call per frame
	unitDefault  = "default"   // per-unit, each unit's cues placed inside it
	unitCueStart = "cue-start" // pop-on only: generate.WithFlipAtCueStart
	unitCarry    = "carry"     // roll-up only: generate.WithRollUpCarry

	// cuePeriodMS is the per-unit caption refresh, matching the continuous
	// generator's one-second clock.
	cuePeriodMS = 1000
)

type options struct {
	version     bool
	output      string
	input       string
	fps         float64
	seconds     float64
	start       string
	mode        string
	unitMode    string
	unitSeconds float64
	lines       lineFlag
}

// captionMode parses -mode into a mode name and, for roll-up, its window size.
// "pop-on" builds each second off-screen and flips it on whole; "paint-on" clears at
// the second boundary and writes the caption onto the screen as it goes, so the text
// visibly types itself out; "roll-up[2-4]" scrolls the window up each second and types
// the new lines onto the bottom row (the mode live captioning uses).
//
// Each line is its own scroll step, so the default two-line caption fills a two-row
// window exactly: plain "roll-up" therefore shows only the current second, "roll-up3"
// keeps the previous second's bottom line, and "roll-up4" keeps the previous second
// whole. See generate.WithRollUp.
func (o *options) captionMode() (mode string, rows int, err error) {
	switch o.mode {
	case "", modePopOn:
		return modePopOn, 0, nil
	case modePaintOn:
		return modePaintOn, 0, nil
	case "roll-up", "roll-up2", "roll-up3", "roll-up4":
		rows = 2
		if n := strings.TrimPrefix(o.mode, "roll-up"); n != "" {
			rows, _ = strconv.Atoi(n) // one of 2, 3, 4 by the case above
		}
		return modeRollUp, rows, nil
	default:
		return "", 0, fmt.Errorf("-mode %q must be %q, %q or \"roll-up[2-4]\"", o.mode, modePopOn, modePaintOn)
	}
}

// genOptions turns -mode into the continuous Generator's options.
func (o *options) genOptions() ([]generate.GeneratorOption, error) {
	mode, rows, err := o.captionMode()
	if err != nil {
		return nil, err
	}
	switch mode {
	case modePaintOn:
		return []generate.GeneratorOption{generate.WithPaintOn()}, nil
	case modeRollUp:
		return []generate.GeneratorOption{generate.WithRollUp(rows)}, nil
	default:
		return nil, nil
	}
}

// checkUnitMode validates -unit-mode against the caption mode: each policy belongs to
// the mode whose cross-unit behaviour it adjusts, so a mismatch is a mistake worth
// naming rather than silently ignoring.
func (o *options) checkUnitMode(mode string) error {
	switch o.unitMode {
	case unitOff:
		return nil
	case unitDefault:
	case unitCueStart:
		if mode != modePopOn {
			return fmt.Errorf("-unit-mode %q applies to -mode %q only (it moves the pop-on flip); got -mode %q",
				unitCueStart, modePopOn, o.mode)
		}
	case unitCarry:
		if mode != modeRollUp {
			return fmt.Errorf("-unit-mode %q applies to -mode roll-up only (it keeps the roll-up window); got -mode %q",
				unitCarry, o.mode)
		}
	default:
		return fmt.Errorf("-unit-mode %q must be %q, %q, %q or %q",
			o.unitMode, unitOff, unitDefault, unitCueStart, unitCarry)
	}
	if o.unitSeconds <= 0 {
		return fmt.Errorf("-unit-seconds must be positive, got %g", o.unitSeconds)
	}
	return nil
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
			"screen) or \"roll-up[2-4]\" (scroll the window up and type onto the bottom row; the default "+
			"two lines fill 2 rows, so use roll-up4 to keep the previous second visible)")
	fs.StringVar(&opts.unitMode, "unit-mode", unitOff,
		"generate one unit (DASH segment / MoQ group) at a time instead of frame by frame: "+
			"\"default\", \"cue-start\" (pop-on flips on the cue boundary) or \"carry\" (roll-up keeps its window)")
	fs.Float64Var(&opts.unitSeconds, "unit-seconds", 2, "unit duration in seconds (-unit-mode only)")
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
	mode, _, err := opts.captionMode()
	if err != nil {
		return err
	}
	if err := opts.checkUnitMode(mode); err != nil {
		return err
	}

	var buf bytes.Buffer
	overran := false
	if opts.input != "" {
		overran, err = spliceInput(opts.input, start, opts, &buf, w)
	} else {
		overran, err = writeSynthetic(start, opts, &buf, w)
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

// capSource yields the 608 payload for the i-th displayed frame. Both output paths
// consume one, so the continuous generator and the per-unit builders are
// interchangeable behind it — which is the point of -unit-mode: the same demo, the
// same decode check, over either API.
type capSource struct {
	frame   func(i int) schedule.Frame
	overran func() bool
}

// newCapSource builds the caption source for a run of nFrames frames: the continuous
// Generator by default, or the per-unit builders under -unit-mode. nFrames must be
// known up front for unit mode, since units tile the whole run.
func newCapSource(start time.Time, o *options, nFrames int) (*capSource, error) {
	startMS := start.UnixMilli()
	if o.unitMode != unitOff {
		frames, err := buildUnitFrames(startMS, o, nFrames)
		if err != nil {
			return nil, err
		}
		idle := schedule.Frame{CCCount: int(math.Round(600.0 / o.fps))}
		return &capSource{
			frame: func(i int) schedule.Frame {
				if i < len(frames) {
					return frames[i]
				}
				return idle // more samples than the run was sized for: no caption data
			},
			overran: func() bool { return false }, // unit mode reports overruns as errors
		}, nil
	}
	genOpts, err := o.genOptions()
	if err != nil {
		return nil, err
	}
	gen := generate.NewGenerator(o.fps, o.lines.config(), genOpts...)
	frameDurMS := 1000.0 / o.fps
	// Drive the caption by frame index at the chosen rate (start + i*frameDur), so the
	// lines stay in lockstep and the clock advances one second per second.
	return &capSource{
		frame: func(i int) schedule.Frame {
			return gen.NextFrame(startMS + int64(math.Round(float64(i)*frameDurMS)))
		},
		overran: gen.Overran,
	}, nil
}

// buildUnitFrames generates the whole run one unit at a time through the per-unit API
// (generate.BuildUnitCues / BuildUnitPaintCues / BuildUnitRollUpCues) and concatenates
// the results — the path a stateless segment server takes, one call per DASH segment or
// MoQ group, exercised here so the demo can decode it back.
//
// Units tile the run from its first frame, every one of them a whole unit: a run that
// does not end on a unit boundary is cut mid-unit, which is what a stream stopping
// mid-segment looks like. Building the last unit short instead would hand a builder a
// slice too small for a caption — a ~23-pair build needs ~23 frames — so any run whose
// length is not a multiple of the unit would fail, which for -i is the common case since
// the sample count is whatever the input has.
//
// Each unit's caption is generated from the unit alone, exactly as a server would, with
// generate.WallClockContent supplying the same lines the continuous generator renders.
func buildUnitFrames(startMS int64, o *options, nFrames int) ([]schedule.Frame, error) {
	mode, rows, err := o.captionMode()
	if err != nil {
		return nil, err
	}
	perUnit := int(math.Round(o.unitSeconds * o.fps))
	if perUnit < 1 {
		return nil, fmt.Errorf("-unit-seconds %g is shorter than one frame at %g fps", o.unitSeconds, o.fps)
	}
	frameDurMS := 1000.0 / o.fps
	content := generate.WallClockContent(o.lines.config(), startMS)
	// unitAt describes the unit starting at frame i: its number, its wall-clock start
	// and a full frame count — full for every unit, including the last (see above). It is
	// also asked for the unit *after* the run's last one, which WithFlipAtCueStart needs
	// to name; only Nr and StartMS are read for that.
	unitAt := func(i int) generate.Unit {
		return generate.Unit{
			Nr:      int64(i / perUnit),
			StartMS: startMS + int64(math.Round(float64(i)*frameDurMS)),
			Frames:  perUnit,
		}
	}

	out := make([]schedule.Frame, 0, nFrames)
	for i := 0; i < nFrames; i += perUnit {
		u := unitAt(i)
		var frames []schedule.Frame
		switch mode {
		case modePaintOn:
			frames, err = generate.BuildUnitPaintCues(o.fps, u, cuePeriodMS, content)
		case modeRollUp:
			var ropts []generate.RollUpOption
			if o.unitMode == unitCarry {
				ropts = append(ropts, generate.WithRollUpCarry())
			}
			frames, err = generate.BuildUnitRollUpCues(o.fps, u, cuePeriodMS, rows, content, ropts...)
		default:
			var uopts []generate.UnitOption
			// Every unit gets the option, including the last: the option is a contract
			// between neighbours — a unit's tail carries the *next* unit's first-cue
			// build, and that unit must be built expecting it, or it transmits the build
			// a second time and flips late. The final unit therefore preloads a build for
			// a unit beyond the run, which nothing flips; that is what the live edge of a
			// stream looks like anyway.
			if o.unitMode == unitCueStart {
				uopts = append(uopts, generate.WithFlipAtCueStart(unitAt(i+perUnit), content))
			}
			frames, err = generate.BuildUnitCues(o.fps, u, cuePeriodMS, content, uopts...)
		}
		if err != nil {
			return nil, fmt.Errorf("unit %d (%d frames from frame %d): %w", u.Nr, u.Frames, i, err)
		}
		out = append(out, frames...)
	}
	if len(out) > nFrames {
		// The run ends inside the last unit: keep the frames the run actually has. Cutting
		// a suffix cannot orphan a flip — an EOC that survives still has its build ahead
		// of it, wherever the mode put it — so the discarded frames only cost the cues
		// that would have followed, and the caption holds what was last flipped.
		out = out[:nFrames]
	}
	return out, nil
}

// writeSynthetic emits a single-track AVC fragmented mp4 whose per-frame samples
// carry the wall-clock caption SEI. It returns whether the caption overran the
// build budget at this frame rate.
func writeSynthetic(start time.Time, o *options, out, status io.Writer) (bool, error) {
	fps := o.fps
	nFrames := int(math.Round(o.seconds * fps))
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

	src, err := newCapSource(start, o, nFrames)
	if err != nil {
		return false, err
	}
	for i := 0; i < nFrames; i++ {
		fr := src.frame(i)
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
	fmt.Fprintf(status, "%s: synthetic AVC, %d frames at %g fps%s\n", appName, nFrames, fps, o.unitNote())
	return src.overran(), nil
}

// unitNote describes the per-unit generation in the status line, or nothing when the
// continuous generator is driving.
func (o *options) unitNote() string {
	if o.unitMode == unitOff {
		return ""
	}
	return fmt.Sprintf(", per-unit (%g s units, -unit-mode %s)", o.unitSeconds, o.unitMode)
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
func spliceInput(inPath string, start time.Time, o *options, out, status io.Writer) (bool, error) {
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
	// Count the input's samples first: under -unit-mode the units have to tile a run
	// whose length is known before any caption is generated.
	samples, _, err := mp4io.Samples(f, trex)
	if err != nil {
		return false, err
	}
	src, err := newCapSource(start, o, len(samples))
	if err != nil {
		return false, err
	}

	frames := 0
	ccFor := func(info mp4io.SampleInfo) ([]byte, error) {
		fr := src.frame(info.Index)
		frames = info.Index + 1
		return carriage.BuildCCData(fr.Field1, fr.Field2, fr.CCCount), nil
	}
	if err := mp4io.SpliceFragmented(f, track, trex, out, ccFor); err != nil {
		return false, err
	}
	fmt.Fprintf(status, "%s: spliced %s into %d %s frames at %g fps%s\n",
		appName, inPath, frames, track.Codec, o.fps, o.unitNote())
	return src.overran(), nil
}
