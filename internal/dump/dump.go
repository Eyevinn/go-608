// Package dump renders the CTA-608 decode stack — field byte pairs, the parsed
// token stream, and the rendered Screen at each displayed change — as a
// deterministic, line-oriented text report. It is the shared debug formatter
// behind go608-info and go608-extract's dump mode, so the two tools emit
// byte-identical output for the same input (SPEC §9, package-layout note P4).
package dump

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/Eyevinn/go-608/cta608"
)

// Unit is one indexed group of decoded byte pairs: an mp4 sample (one video
// frame) or a single 2-byte pair of a raw hex stream. Field1/Field2 are the raw
// 608 bytes (parity preserved) for CC1/CC2 and CC3/CC4 respectively; either may
// be empty for a frame that carries no waveform on that field.
type Unit struct {
	Field1 []byte
	Field2 []byte
}

// Write emits the three fixed sections (field pairs, tokens, screens) for the
// units. header is a one-line source description, idxLabel names the per-unit
// index ("frame" for mp4, "pair" for raw hex), and field (1 or 2) selects which
// field drives the token parse and the Decoder. Output is deterministic (no
// timestamps) so it greps and diffs cleanly.
func Write(w io.Writer, header, idxLabel string, units []Unit, field int) error {
	fmt.Fprintln(w, header)

	fmt.Fprintln(w, "\n== field pairs ==")
	for i, u := range units {
		line := fmt.Sprintf("[%s %d]", idxLabel, i)
		if len(u.Field1) > 0 {
			line += " f1=" + hex.EncodeToString(u.Field1)
		}
		if len(u.Field2) > 0 {
			line += " f2=" + hex.EncodeToString(u.Field2)
		}
		if len(u.Field1) == 0 && len(u.Field2) == 0 {
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

// writeScreen renders the sparse Screen: one line per row (positioned text), then
// a detail line per styled run (its column, pen, and text).
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
func selectField(u Unit, field int) []byte {
	if field == 2 {
		return u.Field2
	}
	return u.Field1
}

// concatField concatenates the chosen field's bytes across all units into one
// channel stream for cta608.Parse.
func concatField(units []Unit, field int) []byte {
	var out []byte
	for _, u := range units {
		out = append(out, selectField(u, field)...)
	}
	return out
}
