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
	"github.com/Eyevinn/go-608/internal"
	"github.com/Eyevinn/go-608/internal/dump"
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

	var units []dump.Unit
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				return fmt.Errorf("expanding fragment samples: %w", err)
			}
			for _, s := range samples {
				nalus, err := carriage.SampleNALUs(s.Data)
				if err != nil {
					return fmt.Errorf("splitting sample NAL units: %w", err)
				}
				f1, f2, err := carriage.FieldPairs(nalus, track.Codec)
				if err != nil {
					return fmt.Errorf("extracting 608 field pairs: %w", err)
				}
				units = append(units, dump.Unit{Field1: f1, Field2: f2})
			}
		}
	}

	header := fmt.Sprintf("source: %s (codec %s, %d frames, field %d)", path, track.Codec, len(units), field)
	return dump.Write(w, header, "frame", units, field)
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
	units := make([]dump.Unit, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		pair := data[i : i+2]
		u := dump.Unit{}
		if field == 2 {
			u.Field2 = pair
		} else {
			u.Field1 = pair
		}
		units = append(units, u)
	}

	header := fmt.Sprintf("source: %s (%d pairs, field %d)", source, len(units), field)
	return dump.Write(w, header, "pair", units, field)
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
