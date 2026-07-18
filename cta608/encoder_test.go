package cta608

import (
	"bytes"
	"testing"
)

// row is a small helper to build a Screen row for the tests.
func row(index int, runs ...Run) Row { return Row{Index: index, Runs: runs} }

func white(col int, text string) Run { return Run{Column: col, Text: text, Pen: Pen{Color: White}} }

// assertTokens fails unless got equals want, printing both readably.
func assertTokens(t *testing.T, got, want []Token) {
	t.Helper()
	if !tokensEqual(got, want) {
		t.Fatalf("token mismatch\n got: %s\nwant: %s", tokStr(got), tokStr(want))
	}
}

// TestPopOnFullBuild: SetScreen from an empty display produces a full pop-on
// caption — RCL, ENM, positioned rows, EOC (acceptance criterion 3/4).
func TestPopOnFullBuild(t *testing.T) {
	var e Encoder // zero value: pop-on
	target := Screen{Rows: []Row{
		row(15, white(0, "HELLO"), Run{Column: 6, Text: "WORLD", Pen: Pen{Color: Red}}),
	}}
	got := e.SetScreen(target)
	want := []Token{
		SetMode{Mode: PopOn},
		Command{Op: ENM},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"HELLO"},
		MidRow{Pen: Pen{Color: Red}},
		Chars{"WORLD"},
		Command{Op: EOC},
	}
	assertTokens(t, got, want)

	// Re-applying the same screen is a no-op.
	if again := e.SetScreen(target); len(again) != 0 {
		t.Fatalf("unchanged SetScreen emitted %s", tokStr(again))
	}

	// The emitted tokens serialize to whole, odd-parity pairs.
	data := Serialize(got, SerializeOptions{})
	if len(data)%2 != 0 {
		t.Fatalf("serialized length %d not whole pairs", len(data))
	}
	for i, b := range data {
		if !hasOddParity(b) {
			t.Fatalf("byte %d = %#02x lacks odd parity", i, b)
		}
	}
}

// TestPopOnClear: clearing a displayed pop-on caption emits a single EDM.
func TestPopOnClear(t *testing.T) {
	var e Encoder
	e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HI"))}})
	got := e.SetScreen(Screen{})
	assertTokens(t, got, []Token{Command{Op: EDM}})
}

// TestCenteredColoredLine checks the SPEC §7 lowering: a centered colored line
// is PAC(indent, white) -> Tab -> MidRow(color) -> chars, compensated one column
// so the text lands on its centered absolute column (acceptance criterion 2).
func TestCenteredColoredLine(t *testing.T) {
	var e Encoder
	block := CaptionBlock{
		Mode:  PopOn,
		Lines: []Line{{Align: AlignCenter, Runs: []Run{{Text: "2026-07-17", Pen: Pen{Color: Yellow}}}}},
	}
	got := e.Apply(block)
	// width 10 -> centered start column 11; colored -> PAC/Tab land column 10,
	// MidRow shifts to 11: indent 8 + Tab 2 = 10.
	want := []Token{
		SetMode{Mode: PopOn},
		Command{Op: ENM},
		PAC{Row: 15, Indent: 8, Pen: Pen{Color: White}},
		TabOffset{Columns: 2},
		MidRow{Pen: Pen{Color: Yellow}},
		Chars{"2026-07-17"},
		Command{Op: EOC},
	}
	assertTokens(t, got, want)
}

// TestCenteredWhiteLine checks a centered white line uses an indent PAC + Tab,
// no mid-row (acceptance criterion 2).
func TestCenteredWhiteLine(t *testing.T) {
	var e Encoder
	block := CaptionBlock{
		Mode:  PopOn,
		Lines: []Line{{Align: AlignCenter, Runs: []Run{{Text: "HELLO", Pen: Pen{Color: White}}}}},
	}
	got := e.Apply(block)
	// width 5 -> centered start column 13 = indent 12 + Tab 1.
	want := []Token{
		SetMode{Mode: PopOn},
		Command{Op: ENM},
		PAC{Row: 15, Indent: 12, Pen: Pen{Color: White}},
		TabOffset{Columns: 1},
		Chars{"HELLO"},
		Command{Op: EOC},
	}
	assertTokens(t, got, want)
}

// TestRollUpAppendAndScroll exercises the roll-up transitions: enter + first
// line, a minimal append on the base row, and a scroll (CR + new base line).
func TestRollUpAppendAndScroll(t *testing.T) {
	var e Encoder
	e.SetMode(RollUp, 3)

	// Enter roll-up and write the first line.
	got := e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HELLO"))}})
	assertTokens(t, got, []Token{
		SetMode{Mode: RollUp, RollUpRows: 3},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"HELLO"},
	})

	// Append " WORLD" to the base row -> only the new characters (criterion:
	// appending emits only the new run, not a full rebuild).
	got = e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HELLO WORLD"))}})
	assertTokens(t, got, []Token{Chars{" WORLD"}})

	// Scroll: the base line moves up to row 14 and a new base line appears.
	got = e.SetScreen(Screen{Rows: []Row{
		row(14, white(0, "HELLO WORLD")),
		row(15, white(0, "NEXT")),
	}})
	assertTokens(t, got, []Token{
		Command{Op: CR},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"NEXT"},
	})
}

// TestRollUpNewRunAppend checks that appending a differently-styled run to the
// base row emits just a mid-row transition + the new chars.
func TestRollUpNewRunAppend(t *testing.T) {
	var e Encoder
	e.SetMode(RollUp, 2)
	e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HELLO"))}})

	got := e.SetScreen(Screen{Rows: []Row{row(15,
		white(0, "HELLO"),
		Run{Column: 6, Text: "RED", Pen: Pen{Color: Red}},
	)}})
	assertTokens(t, got, []Token{
		MidRow{Pen: Pen{Color: Red}},
		Chars{"RED"},
	})
}

// TestPaintOn checks a paint-on target writes rows directly (RDC on entry, no
// EOC) and that an incremental change touches only the changed row.
func TestPaintOn(t *testing.T) {
	var e Encoder
	e.SetMode(PaintOn, 0)

	got := e.SetScreen(Screen{Rows: []Row{
		row(14, white(0, "ONE")),
		row(15, white(0, "TWO")),
	}})
	assertTokens(t, got, []Token{
		SetMode{Mode: PaintOn},
		PAC{Row: 14, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"ONE"},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"TWO"},
	})

	// Extend only row 15: emit only the appended suffix (no RDC, no row 14).
	got = e.SetScreen(Screen{Rows: []Row{
		row(14, white(0, "ONE")),
		row(15, white(0, "TWOX")),
	}})
	assertTokens(t, got, []Token{Chars{"X"}})
}

// TestPaintOnShrink checks that shortening a row erases the leftover tail with a
// Delete-to-End-of-Row so stale glyphs do not linger.
func TestPaintOnShrink(t *testing.T) {
	var e Encoder
	e.SetMode(PaintOn, 0)
	e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HELLO WORLD"))}})

	got := e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HELLO"))}})
	want := []Token{
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"HELLO"},
		PAC{Row: 15, Indent: 4, Pen: Pen{Color: White}}, // reposition at col 5 (indent 4 + Tab 1)
		TabOffset{Columns: 1},
		Command{Op: DER},
	}
	assertTokens(t, got, want)
}

// TestReposition checks that changing a row's position/content re-emits that
// row from a fresh PAC (pop-on rebuild).
func TestReposition(t *testing.T) {
	var e Encoder
	e.SetScreen(Screen{Rows: []Row{row(15, white(4, "LEFT"))}})

	// A new caption at a different row and indent: full pop-on rebuild.
	got := e.SetScreen(Screen{Rows: []Row{row(13, white(8, "MOVED"))}})
	want := []Token{
		SetMode{Mode: PopOn},
		Command{Op: ENM},
		PAC{Row: 13, Indent: 8, Pen: Pen{Color: White}},
		Chars{"MOVED"},
		Command{Op: EOC},
	}
	assertTokens(t, got, want)
}

// TestEncoderScreenMirror checks the encoder mirrors the displayed screen like
// a decoder would (normalized rows), so callers can inspect current state.
func TestEncoderScreenMirror(t *testing.T) {
	var e Encoder
	e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HI"))}})
	s := e.Screen()
	if len(s.Rows) != 1 || s.Rows[0].Index != 15 || len(s.Rows[0].Runs) != 1 ||
		s.Rows[0].Runs[0].Text != "HI" {
		t.Fatalf("mirror = %+v", s)
	}
}

// TestSerializeEmittedRollUp checks the emitted roll-up tokens serialize to the
// expected odd-parity byte pairs (a wire-level spot check without a Decoder).
func TestSerializeEmittedRollUp(t *testing.T) {
	var e Encoder
	e.SetMode(RollUp, 2)
	got := e.SetScreen(Screen{Rows: []Row{row(15, white(0, "HI"))}})
	// Doubling off for a compact, deterministic pair sequence.
	data := Serialize(got, SerializeOptions{Doubling: DoublingOff})
	// RU2 = (0x14,0x25); PAC row15 white color-style = (0x14,0x60); 'H'=0x48,'I'=0x49.
	want := []byte{
		oddParity(0x14), oddParity(0x25),
		oddParity(0x14), oddParity(0x60),
		oddParity(0x48), oddParity(0x49),
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("serialized roll-up\n got: % x\nwant: % x", data, want)
	}
}
