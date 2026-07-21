// Command go608-info dumps the CTA-608 decode stack for a fragmented mp4 or raw
// cc_data byte pairs: the per-unit field byte pairs, the parsed token stream, and
// the rendered Screen at each displayed change. It is the thinnest consumer of the
// decode spine (carriage.FieldPairs → cta608.Parse / cta608.Decoder) and a
// diagnostic used across the go-608 tooling (SPEC §9).
//
// Two input modes:
//
//   - mp4 (-i file.mp4): decode a fragmented mp4, locate its single video track,
//     and for each sample extract the field-1/field-2 608 byte pairs from the
//     CEA-608 SEI (via internal/mp4io + carriage.FieldPairs).
//   - raw cc_data (-hex "9420 94ae ..." or -cc-file pairs.txt): decode a hex
//     byte-pair stream directly, no mp4 needed.
//
// The selected field (default field 1 / CC1) drives the token parse and the
// Decoder; both fields' bytes are always listed. Output is line-oriented and
// deterministic (no timestamps) so it greps and diffs cleanly.
//
// Usage:
//
//	go608-info -i captions.mp4
//	go608-info -i captions.mp4 -field 2
//	go608-info -hex "9420 94ae 9162 c849 942f"
//	go608-info -cc-file pairs.txt
//	go608-info -version
package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/internal"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/mp4ff/mp4"
)

const appName = "go608-info"

var usg = `%s dumps the CTA-608 field pairs, token stream, and rendered Screen of a
fragmented mp4 or a raw cc_data byte-pair stream (a debug tool).

Provide exactly one input: -i (an mp4), -hex (inline hex byte pairs), or -cc-file
(a file of hex byte pairs). The selected -field (default 1) drives the token parse
and the Decoder; both fields are always listed for an mp4.

Usage of %s:
`

// unit is one indexed group of decoded byte pairs: an mp4 sample (one video frame)
// or a single 2-byte pair of a raw hex stream. field1/field2 are the raw 608 bytes
// (parity preserved) for CC1/CC2 and CC3/CC4 respectively.
type unit struct {
	field1 []byte
	field2 []byte
}

type options struct {
	version bool
	input   string
	hexIn   string
	ccFile  string
	field   int
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options]\n\noptions:\n", appName)
		fs.PrintDefaults()
	}

	opts := options{}
	fs.BoolVar(&opts.version, "version", false, "print version and exit")
	fs.StringVar(&opts.input, "i", "", "input fragmented mp4 to dump")
	fs.StringVar(&opts.hexIn, "hex", "", "raw cc_data byte pairs as hex, e.g. \"9420 94ae ...\"")
	fs.StringVar(&opts.ccFile, "cc-file", "", "file of raw cc_data byte pairs as hex")
	fs.IntVar(&opts.field, "field", 1, "608 field to decode into tokens/Screen (1 or 2)")

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

	if opts.field != 1 && opts.field != 2 {
		return fmt.Errorf("-field must be 1 or 2, got %d", opts.field)
	}

	nInputs := 0
	for _, s := range []string{opts.input, opts.hexIn, opts.ccFile} {
		if s != "" {
			nInputs++
		}
	}
	switch nInputs {
	case 0:
		return errors.New("no input: pass one of -i, -hex, or -cc-file (or -version)")
	case 1:
	default:
		return errors.New("choose exactly one input: -i, -hex, or -cc-file")
	}

	switch {
	case opts.input != "":
		return dumpMP4(w, opts.input, opts.field)
	case opts.hexIn != "":
		return dumpHex(w, "raw cc_data (-hex)", opts.hexIn, opts.field)
	default:
		raw, err := os.ReadFile(opts.ccFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", opts.ccFile, err)
		}
		return dumpHex(w, fmt.Sprintf("raw cc_data (%s)", opts.ccFile), string(raw), opts.field)
	}
}

// dumpMP4 reads a fragmented mp4, extracts the 608 field pairs from every sample's
// CEA-608 SEI, and writes the field-pair / token / Screen dump.
func dumpMP4(w io.Writer, path string, field int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	track, trex, err := mp4io.VideoTrack(f)
	if err != nil {
		return err
	}

	var units []unit
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				return fmt.Errorf("expanding fragment samples: %w", err)
			}
			for _, s := range samples {
				nalus, err := mp4io.SampleNALUs(s.Data)
				if err != nil {
					return fmt.Errorf("splitting sample NAL units: %w", err)
				}
				f1, f2, err := carriage.FieldPairs(nalus, track.Codec)
				if err != nil {
					return fmt.Errorf("extracting 608 field pairs: %w", err)
				}
				units = append(units, unit{field1: f1, field2: f2})
			}
		}
	}

	header := fmt.Sprintf("source: %s (codec %s, %d frames, field %d)", path, track.Codec, len(units), field)
	return dump(w, header, "frame", units, field)
}

// dumpHex parses a raw hex byte-pair stream (each 2 bytes is one pair of the chosen
// field) and writes the field-pair / token / Screen dump.
func dumpHex(w io.Writer, source, hexData string, field int) error {
	data, err := parseHexBytes(hexData)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("no cc_data bytes in input")
	}
	if len(data)%2 != 0 {
		return fmt.Errorf("raw cc_data must be whole 2-byte 608 pairs, got %d bytes", len(data))
	}
	units := make([]unit, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		pair := data[i : i+2]
		u := unit{}
		if field == 2 {
			u.field2 = pair
		} else {
			u.field1 = pair
		}
		units = append(units, u)
	}

	header := fmt.Sprintf("source: %s (%d pairs, field %d)", source, len(units), field)
	return dump(w, header, "pair", units, field)
}

// dump writes the three fixed sections (field pairs, tokens, screens) for the units.
// idxLabel names the per-unit index ("frame" for mp4, "pair" for raw hex).
func dump(w io.Writer, header, idxLabel string, units []unit, field int) error {
	fmt.Fprintln(w, header)

	fmt.Fprintln(w, "\n== field pairs ==")
	for i, u := range units {
		line := fmt.Sprintf("[%s %d]", idxLabel, i)
		if len(u.field1) > 0 {
			line += " f1=" + hex.EncodeToString(u.field1)
		}
		if len(u.field2) > 0 {
			line += " f2=" + hex.EncodeToString(u.field2)
		}
		if len(u.field1) == 0 && len(u.field2) == 0 {
			line += " (none)"
		}
		fmt.Fprintln(w, line)
	}

	stream := concatField(units, field)
	fmt.Fprintf(w, "\n== tokens (field %d) ==\n", field)
	toks, err := cta608.Parse(stream, cta608.ParseOptions{})
	if err != nil {
		return fmt.Errorf("parsing field %d tokens: %w", field, err)
	}
	if len(toks) == 0 {
		fmt.Fprintln(w, "(none)")
	}
	for i, t := range toks {
		fmt.Fprintf(w, "[%d] %s\n", i, t.String())
	}

	fmt.Fprintf(w, "\n== screens (field %d) ==\n", field)
	var dec cta608.Decoder
	changes := 0
	for i, u := range units {
		data := selectField(u, field)
		if len(data) == 0 {
			continue
		}
		if err := dec.Feed(data); err != nil {
			return fmt.Errorf("decoding field %d at %s %d: %w", field, idxLabel, i, err)
		}
		if dec.Changed() {
			changes++
			fmt.Fprintf(w, "[%s %d] change %d:\n", idxLabel, i, changes)
			writeScreen(w, dec.Screen())
		}
	}
	if changes == 0 {
		fmt.Fprintln(w, "(no displayed changes)")
	}
	return nil
}

// writeScreen renders the sparse Screen: one line per row (positioned text), then a
// detail line per styled run (its column, pen, and text).
func writeScreen(w io.Writer, s cta608.Screen) {
	if len(s.Rows) == 0 {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	for _, row := range s.Rows {
		fmt.Fprintf(w, "  row %2d: %q\n", row.Index, rowLine(row))
		for _, r := range row.Runs {
			fmt.Fprintf(w, "    col %2d %s: %q\n", r.Column, penStr(r.Pen), r.Text)
		}
	}
}

// rowLine renders a row's runs as a single positioned string: runs placed at their
// absolute columns with gaps filled by spaces, trimmed on the right.
func rowLine(row cta608.Row) string {
	var b []rune
	for _, r := range row.Runs {
		for len(b) < r.Column {
			b = append(b, ' ')
		}
		b = b[:r.Column]
		b = append(b, []rune(r.Text)...)
	}
	return strings.TrimRight(string(b), " ")
}

// penStr renders a Pen compactly, omitting default/false attributes (mirrors the
// cta608 internal pen formatter).
func penStr(p cta608.Pen) string {
	var b strings.Builder
	b.WriteString(p.Color.String())
	if p.Italic {
		b.WriteString(" italic")
	}
	if p.Underline {
		b.WriteString(" underline")
	}
	if p.Background != cta608.ColDefault {
		fmt.Fprintf(&b, " bg=%s", p.Background)
	}
	return b.String()
}

// selectField returns the raw bytes of the chosen field (1 or 2) for a unit.
func selectField(u unit, field int) []byte {
	if field == 2 {
		return u.field2
	}
	return u.field1
}

// concatField concatenates the chosen field's bytes across all units into one
// channel stream for cta608.Parse.
func concatField(units []unit, field int) []byte {
	var out []byte
	for _, u := range units {
		out = append(out, selectField(u, field)...)
	}
	return out
}

// parseHexBytes decodes a whitespace/comma-separated hex byte-pair string (with
// optional "0x" prefixes) into raw bytes.
func parseHexBytes(s string) ([]byte, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	})
	var sb strings.Builder
	for _, f := range fields {
		f = strings.TrimPrefix(f, "0x")
		f = strings.TrimPrefix(f, "0X")
		sb.WriteString(f)
	}
	h := sb.String()
	if len(h)%2 != 0 {
		return nil, fmt.Errorf("hex input has an odd number of digits (%d)", len(h))
	}
	data, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("parsing hex bytes: %w", err)
	}
	return data, nil
}
