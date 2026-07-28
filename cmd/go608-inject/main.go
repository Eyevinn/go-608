// Command go608-inject injects CTA-608 as SEI into a fragmented mp4 from WebVTT,
// SRT, or SCC input — the encode-side integration capstone (SPEC §6/§8 §9). It
// wires the encode stack: the timed-text readers → cue.Compile → schedule →
// carriage → an SEI spliced before the first VCL NAL of every frame (via
// internal/mp4io.SpliceFragmented, shared with go608-clock).
//
// Two subtitle paths feed the mp4:
//
//   - WebVTT/SRT: read into cues, compiled to pop-on token transitions, scheduled
//     one pair per frame at the target fps with the chosen cc_count policy.
//   - SCC: injected byte-exactly — its verbatim byte pairs ride frame for frame
//     (SCC frame n → sample n), so the pairs survive the round-trip through
//     carriage untouched; the scheduler is used only to size cc_count.
//
// Format-only conversion (WebVTT/SRT/SCC ⇄ each other, no mp4) is a mode of this
// tool, sharing the exact conversion core (internal/convert) with go608-extract.
//
// Usage:
//
//	go608-inject -i in.mp4 -sub captions.srt -o out.mp4
//	go608-inject -i in.mp4 -sub captions.scc -o out.mp4 -fps 29.97
//	go608-inject -sub captions.srt -o captions.vtt        # format-only, no mp4
//	go608-inject -version
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/internal"
	"github.com/Eyevinn/go-608/internal/convert"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/go-608/scc"
	"github.com/Eyevinn/go-608/schedule"
	"github.com/Eyevinn/mp4ff/mp4"
)

const appName = "go608-inject"

var usg = `%s injects CTA-608 as SEI into a fragmented mp4 from WebVTT / SRT / SCC.
WebVTT/SRT are compiled to pop-on captions; SCC pairs are injected byte-exactly.
Format-only conversion (no mp4) is a mode: omit -i and give a timed-text -o.

Usage of %s:
`

type options struct {
	version bool
	input   string
	sub     string
	output  string
	from    string
	to      string
	fps     float64
	ccCount string
	drop    bool
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options]\n\noptions:\n", appName)
		fs.PrintDefaults()
	}
	opts := options{}
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	fs.StringVar(&opts.input, "i", "", "input fragmented mp4 to inject into (omit for format-only conversion)")
	fs.StringVar(&opts.sub, "sub", "", "subtitle input: a WebVTT, SRT, or SCC file")
	fs.StringVar(&opts.output, "o", "", "output path (an mp4 when injecting, else a timed-text file)")
	fs.StringVar(&opts.from, "from", "", "subtitle format override (webvtt|srt|scc) when the extension is unclear")
	fs.StringVar(&opts.to, "to", "", "format-only output format (webvtt|srt|scc) when the -o extension is unclear")
	fs.Float64Var(&opts.fps, "fps", 30, "frame rate driving cc_count/cadence and SCC framing")
	fs.StringVar(&opts.ccCount, "cc-count", "full", "cc_count policy: full (round(600/fps) padded) or minimal (2)")
	fs.BoolVar(&opts.drop, "drop", false, "write drop-frame timecodes when the format-only output is SCC")
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
	if opts.sub == "" {
		return errors.New("no subtitle input: pass -sub file.(vtt|srt|scc)")
	}
	if opts.fps <= 0 {
		return fmt.Errorf("fps must be positive, got %g", opts.fps)
	}
	if cc := int(math.Round(600.0 / opts.fps)); cc < 2 || cc > 31 {
		return fmt.Errorf("fps %g is outside the broadcast caption range (cc_count %d not in 2..31)", opts.fps, cc)
	}
	policy, err := ccCountPolicy(opts.ccCount)
	if err != nil {
		return err
	}
	subFormat, err := resolveFormat(opts.sub, opts.from)
	if err != nil {
		return err
	}
	convOpts := convert.Options{FPS: opts.fps, DropFrame: opts.drop, CCCount: policy}

	if opts.input == "" {
		return convertOnly(subFormat, opts, convOpts, w)
	}
	return injectMP4(subFormat, opts, policy, convOpts, w)
}

// convertOnly runs the shared conversion core (the mode go608-inject shares with
// go608-extract): read the subtitle into cues and write them out in another
// format, no mp4 involved.
func convertOnly(subFormat convert.Format, opts *options, convOpts convert.Options, w io.Writer) error {
	if opts.output == "" && opts.to == "" {
		return errors.New("format-only mode needs -o file.<ext> or -to webvtt|srt|scc")
	}
	outFormat, out, closeOut, err := openOutput(opts, w)
	if err != nil {
		return err
	}
	defer closeOut()

	r, err := os.Open(opts.sub)
	if err != nil {
		return fmt.Errorf("opening %s: %w", opts.sub, err)
	}
	defer func() { _ = r.Close() }()
	cues, err := convert.ReadCues(subFormat, r, convOpts)
	if err != nil {
		return err
	}
	return convert.WriteCues(outFormat, out, cues, convOpts)
}

// injectMP4 splices the subtitle's 608 into every frame of the input mp4 and
// writes the result to -o. The SEIFunc that SpliceFragmented calls per sample is
// built by the format-specific path: SCC injects verbatim pairs frame for frame,
// WebVTT/SRT drives a schedule.Scheduler fed by cue.Compile.
func injectMP4(
	subFormat convert.Format, opts *options, policy schedule.CCCountPolicy, convOpts convert.Options, w io.Writer,
) error {
	if opts.output == "" {
		return errors.New("no output: pass -o out.mp4")
	}
	f, track, trex, err := openMP4(opts.input)
	if err != nil {
		return err
	}

	var ccFor mp4io.CCDataFunc
	if subFormat == convert.FormatSCC {
		ccFor, err = sccCCDataFunc(opts.sub, opts.fps, policy)
	} else {
		ccFor, err = subtitleCCDataFunc(subFormat, opts, convOpts, track)
	}
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := mp4io.SpliceFragmented(f, track, trex, &buf, ccFor); err != nil {
		return err
	}
	if err := os.WriteFile(opts.output, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", opts.output, err)
	}
	fmt.Fprintf(w, "wrote %s (%d bytes)\n", opts.output, buf.Len())
	return nil
}

// sccCCDataFunc injects an SCC file's byte pairs verbatim, frame for frame: SCC frame
// n rides on the n-th displayed frame's field 1. The scheduler is used only to size
// the per-frame cc_count (nothing is queued on it), so the pairs themselves are
// untouched and survive the round-trip byte-exact.
func sccCCDataFunc(path string, fps float64, policy schedule.CCCountPolicy) (mp4io.CCDataFunc, error) {
	r, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = r.Close() }()
	file, err := scc.Read(r, scc.WithFPS(fps))
	if err != nil {
		return nil, err
	}
	byFrame := map[int][]byte{}
	for _, p := range file.TimedPairs() {
		byFrame[p.Frame] = p.Pair
	}

	sched := schedule.NewScheduler(fps, schedule.WithCCCountPolicy(policy))
	return func(info mp4io.SampleInfo) ([]byte, error) {
		// Frame with an empty queue yields this frame's cc_count and an empty pair;
		// substitute the SCC pair for this frame (or nothing) as field 1.
		fr := sched.Frame(int64(info.Index))
		return carriage.BuildCCData(byFrame[info.Index], nil, fr.CCCount), nil
	}, nil
}

// subtitleCCDataFunc compiles a WebVTT/SRT input to pop-on token transitions and
// schedules them, returning a CCDataFunc that drains the scheduler at each sample's
// media time.
func subtitleCCDataFunc(
	subFormat convert.Format, opts *options, convOpts convert.Options, track mp4io.Track,
) (mp4io.CCDataFunc, error) {
	r, err := os.Open(opts.sub)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", opts.sub, err)
	}
	defer func() { _ = r.Close() }()
	cues, err := convert.ReadCues(subFormat, r, convOpts)
	if err != nil {
		return nil, err
	}

	sched := schedule.NewScheduler(opts.fps, schedule.WithCCCountPolicy(convOpts.CCCount))
	for _, tt := range cue.Compile(cues) {
		sched.Push(schedule.TimedTokens{TimeMS: tt.Time.Milliseconds(), Field: 1, Tokens: tt.Tokens})
	}
	timescale := float64(track.Timescale)
	if timescale <= 0 {
		timescale = opts.fps
	}
	return func(info mp4io.SampleInfo) ([]byte, error) {
		// MediaTime is the presentation time measured from the track origin, so
		// subtitle-file t=0 lands on the first displayed frame whatever absolute
		// timestamps the container starts at.
		mediaMS := int64(math.Round(float64(info.MediaTime) * 1000 / timescale))
		fr := sched.Frame(mediaMS)
		return carriage.BuildCCData(fr.Field1, fr.Field2, fr.CCCount), nil
	}, nil
}

// ccCountPolicy maps the -cc-count flag to a schedule policy.
func ccCountPolicy(s string) (schedule.CCCountPolicy, error) {
	switch s {
	case "full":
		return schedule.CCCountFull, nil
	case "minimal":
		return schedule.CCCountMinimal, nil
	default:
		return 0, fmt.Errorf("-cc-count must be \"full\" or \"minimal\", got %q", s)
	}
}

// resolveFormat picks the subtitle format from an explicit override or extension.
func resolveFormat(path, from string) (convert.Format, error) {
	if from != "" {
		return convert.ParseFormat(from)
	}
	f, ok := convert.FormatFromPath(path)
	if !ok {
		return 0, fmt.Errorf("cannot infer format from %q; add -from webvtt|srt|scc", path)
	}
	return f, nil
}

// openOutput resolves the format-only output format and writer (see go608-extract).
func openOutput(opts *options, w io.Writer) (convert.Format, io.Writer, func(), error) {
	noop := func() {}
	if opts.output == "" {
		f, err := convert.ParseFormat(opts.to)
		return f, w, noop, err
	}
	f, ok := convert.FormatFromPath(opts.output)
	if !ok {
		if opts.to == "" {
			return 0, nil, noop, fmt.Errorf("cannot infer output format from %q; add -to webvtt|srt|scc", opts.output)
		}
		var err error
		if f, err = convert.ParseFormat(opts.to); err != nil {
			return 0, nil, noop, err
		}
	}
	file, err := os.Create(opts.output)
	if err != nil {
		return 0, nil, noop, fmt.Errorf("creating %s: %w", opts.output, err)
	}
	return f, file, func() { _ = file.Close() }, nil
}

// openMP4 reads and decodes a fragmented mp4 and locates its video track.
func openMP4(path string) (*mp4.File, mp4io.Track, *mp4.TrexBox, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, mp4io.Track{}, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return nil, mp4io.Track{}, nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	track, trex, err := mp4io.VideoTrack(f)
	return f, track, trex, err
}
