package cta608

import (
	"fmt"
	"testing"
)

// Reuses the row() and white(col, text) helpers from encoder_test.go.

func scr(rows ...Row) Screen { return Screen{Rows: rows} }

func dumpScreen(s Screen) string {
	out := ""
	for _, r := range sortedRows(s) {
		out += fmt.Sprintf("\n  row %d:", r.Index)
		for _, run := range r.Runs {
			out += fmt.Sprintf(" [%d]%q<%s>", run.Column, run.Text, penString(run.Pen))
		}
	}
	if out == "" {
		return "(empty)"
	}
	return out
}

func assertScreen(t *testing.T, got, want Screen) {
	t.Helper()
	if !screenEqual(got, want) {
		t.Errorf("screen mismatch:\n got:%s\nwant:%s", dumpScreen(got), dumpScreen(want))
	}
}

// --- pop-on -----------------------------------------------------------------

func TestDecoderPopOn(t *testing.T) {
	var d Decoder
	// Build into non-displayed memory; nothing shows until EOC.
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "HI"},
	})
	assertScreen(t, d.Screen(), Screen{}) // not displayed yet

	d.Push([]Token{Command{Op: EOC}})
	assertScreen(t, d.Screen(), scr(row(15, white(0, "HI"))))

	// A second caption replaces the first on EOC (previous caption cleared).
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 14, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "NEXT"},
		Command{Op: EOC},
	})
	assertScreen(t, d.Screen(), scr(row(14, white(0, "NEXT"))))
}

func TestDecoderEDM(t *testing.T) {
	var d Decoder
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "BYE"},
		Command{Op: EOC},
	})
	assertScreen(t, d.Screen(), scr(row(15, white(0, "BYE"))))
	d.Push([]Token{Command{Op: EDM}})
	assertScreen(t, d.Screen(), Screen{})
}

// --- roll-up ----------------------------------------------------------------

func rollUpLine(rowIdx int, text string) []Token {
	return []Token{PAC{Row: rowIdx, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: text}}
}

func TestDecoderRollUp2(t *testing.T) {
	var d Decoder
	d.Push([]Token{SetMode{Mode: RollUp, RollUpRows: 2}})
	d.Push(rollUpLine(15, "L1"))
	assertScreen(t, d.Screen(), scr(row(15, white(0, "L1"))))

	d.Push([]Token{Command{Op: CR}})
	d.Push(rollUpLine(15, "L2"))
	assertScreen(t, d.Screen(), scr(row(14, white(0, "L1")), row(15, white(0, "L2"))))

	// Window is 2 rows: a third line scrolls L1 off the top.
	d.Push([]Token{Command{Op: CR}})
	d.Push(rollUpLine(15, "L3"))
	assertScreen(t, d.Screen(), scr(row(14, white(0, "L2")), row(15, white(0, "L3"))))
}

func TestDecoderRollUp4(t *testing.T) {
	var d Decoder
	d.Push([]Token{SetMode{Mode: RollUp, RollUpRows: 4}})
	for i, s := range []string{"A", "B", "C", "D"} {
		if i > 0 {
			d.Push([]Token{Command{Op: CR}})
		}
		d.Push(rollUpLine(15, s))
	}
	// Four-row window fully populated (rows 12..15).
	assertScreen(t, d.Screen(), scr(
		row(12, white(0, "A")), row(13, white(0, "B")),
		row(14, white(0, "C")), row(15, white(0, "D")),
	))
}

// --- paint-on ---------------------------------------------------------------

func TestDecoderPaintOn(t *testing.T) {
	var d Decoder
	// Paint-on writes directly to the displayed screen — visible immediately.
	d.Push([]Token{
		SetMode{Mode: PaintOn},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "PA"},
	})
	assertScreen(t, d.Screen(), scr(row(15, white(0, "PA"))))
	d.Push([]Token{Chars{Text: "INT"}}) // extend the same row
	assertScreen(t, d.Screen(), scr(row(15, white(0, "PAINT"))))
}

// --- XDS skipped ------------------------------------------------------------

func TestDecoderXDSSkipped(t *testing.T) {
	toks := []Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "HI"},
		Command{Op: EOC},
	}
	clean := Serialize(toks, SerializeOptions{})

	// Splice XDS pairs (first byte 0x01-0x0f) around the caption; Parse drops them.
	var withXDS []byte
	withXDS = append(withXDS, 0x01, 0x25) // XDS class/type pair
	withXDS = append(withXDS, clean...)
	withXDS = append(withXDS, 0x02, 0x42) // more XDS
	withXDS = append(withXDS, 0x0f, 0x40) // XDS end

	var a, b Decoder
	if err := a.Feed(clean); err != nil {
		t.Fatalf("feed clean: %v", err)
	}
	if err := b.Feed(withXDS); err != nil {
		t.Fatalf("feed withXDS: %v", err)
	}
	assertScreen(t, b.Screen(), a.Screen())
	assertScreen(t, b.Screen(), scr(row(15, white(0, "HI"))))
}

// --- text mode --------------------------------------------------------------

func TestDecoderTextModeIgnored(t *testing.T) {
	var d Decoder
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}},
		Command{Op: TR}, Chars{Text: "TEXTMODE"}, // ignored
		Command{Op: EOC},
	})
	assertScreen(t, d.Screen(), Screen{}) // nothing captioned

	// A mode command resumes caption mode; content now shows.
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "OK"},
		Command{Op: EOC},
	})
	assertScreen(t, d.Screen(), scr(row(15, white(0, "OK"))))
}

// --- displayed-change signal ------------------------------------------------

func TestDecoderChangedSignal(t *testing.T) {
	var d Decoder
	// Building non-displayed memory does not change the displayed screen.
	d.Push([]Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "HI"},
	})
	if d.Changed() {
		t.Error("Changed() true while only non-displayed memory changed")
	}
	d.Push([]Token{Command{Op: EOC}})
	if !d.Changed() {
		t.Error("Changed() false after EOC flipped the display")
	}
	if d.Changed() {
		t.Error("Changed() true with no intervening change")
	}
	d.Push([]Token{Command{Op: EDM}})
	if !d.Changed() {
		t.Error("Changed() false after EDM cleared the display")
	}
}

// --- Encoder <-> Decoder round-trip -----------------------------------------

// Now that both halves exist, verify the full core loop:
// target Screen -> Encoder -> Serialize -> Parse -> Decoder -> Screen.
func TestEncoderDecoderRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		mode   Mode
		rows   int
		target Screen
	}{
		{"popon-simple", PopOn, 0, scr(row(15, white(0, "HELLO")))},
		{"popon-two-rows", PopOn, 0, scr(
			row(14, Run{Column: 0, Text: "RED", Pen: Pen{Color: Red}}),
			row(15, white(0, "WHITE")),
		)},
		{"popon-centered-white", PopOn, 0, scr(row(15, white(13, "CENTER")))},
		{"popon-centered-color", PopOn, 0, scr(
			row(15, Run{Column: 14, Text: "HI", Pen: Pen{Color: Blue}}),
		)},
		{"popon-midrow", PopOn, 0, scr(row(15,
			white(0, "AB"),
			Run{Column: 3, Text: "CD", Pen: Pen{Color: Green}},
		))},
		{"rollup3", RollUp, 3, scr(row(14, white(0, "AAA")), row(15, white(0, "BBB")))},
		{"painton", PaintOn, 0, scr(row(15, white(0, "PP")))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var enc Encoder
			enc.SetMode(c.mode, c.rows)
			toks := enc.SetScreen(c.target)
			data := Serialize(toks, SerializeOptions{})
			parsed, err := Parse(data, ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var dec Decoder
			dec.Push(parsed)
			assertScreen(t, dec.Screen(), normalizeScreen(c.target))
		})
	}
}

func TestDecoderFeedParityRoundTrip(t *testing.T) {
	toks := []Token{
		SetMode{Mode: PopOn}, Command{Op: ENM},
		PAC{Row: 15, Indent: 0, Pen: Pen{Color: White}}, Chars{Text: "PARITY"},
		Command{Op: EOC},
	}
	data := Serialize(toks, SerializeOptions{}) // odd parity applied
	var d Decoder
	if err := d.Feed(data); err != nil {
		t.Fatalf("feed: %v", err)
	}
	assertScreen(t, d.Screen(), scr(row(15, white(0, "PARITY"))))
}
