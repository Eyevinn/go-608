// Command go608-extract pulls CTA-608 captions out of a fragmented mp4 and writes
// them as WebVTT, SRT, or SCC — the decode-side integration capstone (SPEC §6/§8
// §9). It wires the already-built decode stack end to end: internal/mp4io +
// carriage.FieldPairs → cta608.Decoder → cue.Segment → the webvtt/srt/scc writers.
//
// Format-only conversion (SCC ↔ WebVTT ↔ SRT with no mp4) is a mode of this tool,
// not a separate binary: when the input is itself a timed-text file, it is read
// through the shared internal/convert core and written back out in another format.
// The same core writes the mp4-derived cues, so both paths share one conversion
// engine (shared with go608-inject).
//
// A -dump mode prints the raw field pairs, token stream, and rendered Screen for a
// 608-bearing input, byte-identical to go608-info (it reuses internal/dump).
//
// Usage:
//
//	go608-extract -i in.mp4 -o out.vtt
//	go608-extract -i in.mp4 -to srt          # to stdout
//	go608-extract -i in.mp4 -o out.scc -fps 29.97 -drop
//	go608-extract -i in.scc -o out.vtt       # format-only, no mp4
//	go608-extract -i in.mp4 -dump            # go608-info-style dump
//	go608-extract -version
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/internal"
	"github.com/Eyevinn/go-608/internal/convert"
	"github.com/Eyevinn/go-608/internal/dump"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/go-608/scc"
	"github.com/Eyevinn/mp4ff/mp4"
)

const appName = "go608-extract"

var usg = `%s pulls CTA-608 out of a fragmented mp4 and writes it as WebVTT, SRT,
or SCC. Format-only conversion (SCC <-> WebVTT <-> SRT, no mp4) is a mode: give a
timed-text file as input. -dump prints the field pairs / tokens / Screen instead.

Usage of %s:
`

type options struct {
	version    bool
	input      string
	output     string
	from       string
	to         string
	fps        float64
	drop       bool
	dumpMode   bool
	streamEnd  time.Duration
	defaultDur time.Duration
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options]\n\noptions:\n", appName)
		fs.PrintDefaults()
	}
	opts := options{}
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	fs.StringVar(&opts.input, "i", "", "input: a fragmented mp4, or a timed-text file for format-only conversion")
	fs.StringVar(&opts.output, "o", "", "output file (format inferred from extension); default stdout with -to")
	fs.StringVar(&opts.from, "from", "", "input format override (webvtt|srt|scc) when the ext is unclear")
	fs.StringVar(&opts.to, "to", "", "output format (webvtt|srt|scc) when writing to stdout / no -o extension")
	fs.Float64Var(&opts.fps, "fps", 30, "frame rate for SCC framing/timecodes")
	fs.BoolVar(&opts.drop, "drop", false, "write drop-frame SCC timecodes")
	fs.BoolVar(&opts.dumpMode, "dump", false, "print the field pairs / tokens / Screen (like go608-info)")
	fs.DurationVar(&opts.streamEnd, "stream-end", 0, "absolute end time for a dangling final cue (e.g. 30s)")
	fs.DurationVar(&opts.defaultDur, "default-dur", 2*time.Second, "fallback duration for a dangling final cue")
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
	if opts.input == "" {
		return errors.New("no input: pass -i (an mp4 or a timed-text file)")
	}
	if opts.fps <= 0 {
		return fmt.Errorf("fps must be positive, got %g", opts.fps)
	}

	convOpts := convert.Options{
		FPS:       opts.fps,
		DropFrame: opts.drop,
		Segment:   cue.SegmentOptions{StreamEnd: opts.streamEnd, DefaultDur: opts.defaultDur},
	}

	mp4Input := !isTimedText(opts.input, opts.from)
	if opts.dumpMode {
		if !mp4Input {
			return errors.New("-dump needs a 608-bearing input (an mp4); a WebVTT/SRT file has no raw pairs")
		}
		return dumpMP4(w, opts.input)
	}

	// Resolve the output format up front so a bad target fails fast; the -o file is
	// created only once the content is ready (below), so a decode error leaves no
	// empty file behind.
	outFormat, err := resolveOutputFormat(opts)
	if err != nil {
		return err
	}

	// Byte-exact path: mp4 SEI and SCC are byte-pair siblings, so 608 → SCC carries
	// the raw wire pairs verbatim rather than decoding and re-compiling (SPEC §8.1).
	if mp4Input && outFormat == convert.FormatSCC {
		pairs, err := pairsFromMP4(opts.input)
		if err != nil {
			return err
		}
		return writeOutput(opts, w, func(out io.Writer) error {
			return convert.WriteSCCPairs(out, pairs, convOpts)
		})
	}

	var cues []cue.TimedCue
	if mp4Input {
		cues, err = cuesFromMP4(opts.input, convOpts)
	} else {
		cues, err = cuesFromTimedText(opts.input, opts.from, convOpts)
	}
	if err != nil {
		return err
	}
	return writeOutput(opts, w, func(out io.Writer) error {
		return convert.WriteCues(outFormat, out, cues, convOpts)
	})
}

// pairsFromMP4 reads a fragmented mp4's per-frame field-1 pairs as raw SCC-style
// timed pairs (frame n = sample n), the byte-exact currency for the 608 → SCC
// path. Frames with no 608 waveform are skipped.
func pairsFromMP4(path string) ([]scc.TimedPair, error) {
	f, track, trex, err := openMP4(path)
	if err != nil {
		return nil, err
	}
	samples, err := mp4io.Samples(f, trex)
	if err != nil {
		return nil, err
	}
	var pairs []scc.TimedPair
	for i, s := range samples {
		nalus, err := mp4io.SampleNALUs(s.Data)
		if err != nil {
			return nil, fmt.Errorf("splitting sample NAL units: %w", err)
		}
		f1, _, err := carriage.FieldPairs(nalus, track.Codec)
		if err != nil {
			return nil, fmt.Errorf("extracting 608 field pairs: %w", err)
		}
		if len(f1) == 2 {
			pairs = append(pairs, scc.TimedPair{Frame: i, Pair: f1})
		}
	}
	return pairs, nil
}

// isTimedText reports whether the input should be read as a timed-text file
// (format-only mode) rather than an mp4: true when an explicit -from is given or
// the extension names a timed-text format.
func isTimedText(path, from string) bool {
	if from != "" {
		return true
	}
	_, ok := convert.FormatFromPath(path)
	return ok
}

// resolveOutputFormat determines the output format from -o's extension or -to,
// without creating any file. With no -o it needs -to (writing to stdout).
func resolveOutputFormat(opts *options) (convert.Format, error) {
	if opts.output == "" {
		if opts.to == "" {
			return 0, errors.New("no output format: pass -o file.<ext> or -to webvtt|srt|scc")
		}
		return convert.ParseFormat(opts.to)
	}
	if f, ok := convert.FormatFromPath(opts.output); ok {
		return f, nil
	}
	if opts.to == "" {
		return 0, fmt.Errorf("cannot infer output format from %q; add -to webvtt|srt|scc", opts.output)
	}
	return convert.ParseFormat(opts.to)
}

// writeOutput runs write against the output sink — the -o file (created only now,
// after the content is ready) or stdout when no -o is given.
func writeOutput(opts *options, w io.Writer, write func(io.Writer) error) error {
	if opts.output == "" {
		return write(w)
	}
	file, err := os.Create(opts.output)
	if err != nil {
		return fmt.Errorf("creating %s: %w", opts.output, err)
	}
	if err := write(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// cuesFromMP4 decodes the 608 in a fragmented mp4 into cues: every sample's
// field-1 pair drives a cta608.Decoder (via the shared convert core) and each
// displayed-Screen change becomes a cue, timed from the sample's decode time.
func cuesFromMP4(path string, opts convert.Options) ([]cue.TimedCue, error) {
	f, track, trex, err := openMP4(path)
	if err != nil {
		return nil, err
	}
	samples, err := mp4io.Samples(f, trex)
	if err != nil {
		return nil, err
	}
	timescale := float64(track.Timescale)
	if timescale <= 0 {
		timescale = opts.FPS // fall back to the flag when the mdhd timescale is absent
	}
	units := make([]convert.DecodeUnit, 0, len(samples))
	for _, s := range samples {
		nalus, err := mp4io.SampleNALUs(s.Data)
		if err != nil {
			return nil, fmt.Errorf("splitting sample NAL units: %w", err)
		}
		f1, _, err := carriage.FieldPairs(nalus, track.Codec)
		if err != nil {
			return nil, fmt.Errorf("extracting 608 field pairs: %w", err)
		}
		units = append(units, convert.DecodeUnit{
			TimeMS: int64(math.Round(float64(s.DecodeTime) * 1000 / timescale)),
			Field1: f1,
		})
	}
	return convert.CuesFromUnits(units, opts.Segment)
}

// cuesFromTimedText reads a timed-text input file into cues (format-only mode).
func cuesFromTimedText(path, from string, opts convert.Options) ([]cue.TimedCue, error) {
	format, err := resolveFormat(path, from)
	if err != nil {
		return nil, err
	}
	r, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = r.Close() }()
	return convert.ReadCues(format, r, opts)
}

// resolveFormat picks the input format from an explicit override or the path's
// extension.
func resolveFormat(path, from string) (convert.Format, error) {
	if from != "" {
		return convert.ParseFormat(from)
	}
	f, ok := convert.FormatFromPath(path)
	if !ok {
		return 0, fmt.Errorf("cannot infer input format from %q; add -from webvtt|srt|scc", path)
	}
	return f, nil
}

// dumpMP4 prints the field-pair / token / Screen dump for an mp4 input, reusing
// internal/dump so the output matches go608-info exactly.
func dumpMP4(w io.Writer, path string) error {
	f, track, trex, err := openMP4(path)
	if err != nil {
		return err
	}
	samples, err := mp4io.Samples(f, trex)
	if err != nil {
		return err
	}
	units := make([]dump.Unit, 0, len(samples))
	for _, s := range samples {
		nalus, err := mp4io.SampleNALUs(s.Data)
		if err != nil {
			return fmt.Errorf("splitting sample NAL units: %w", err)
		}
		f1, f2, err := carriage.FieldPairs(nalus, track.Codec)
		if err != nil {
			return fmt.Errorf("extracting 608 field pairs: %w", err)
		}
		units = append(units, dump.Unit{Field1: f1, Field2: f2})
	}
	header := fmt.Sprintf("source: %s (codec %s, %d frames, field 1)", path, track.Codec, len(units))
	return dump.Write(w, header, "frame", units, 1)
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
