package cta608

import "testing"

// TestCaptionBlockAnchorRows checks the bottom/top anchoring of auto-placed
// lines onto rows.
func TestCaptionBlockAnchorRows(t *testing.T) {
	white := Pen{Color: White}
	line := func(s string) Line { return Line{Runs: []Run{{Text: s, Pen: white}}} }

	cases := []struct {
		name  string
		block CaptionBlock
		want  []int // row index per resulting row, in order
	}{
		{"bottom-one", CaptionBlock{Lines: []Line{line("A")}}, []int{15}},
		{"bottom-two", CaptionBlock{Lines: []Line{line("A"), line("B")}}, []int{14, 15}},
		{"bottom-three", CaptionBlock{Lines: []Line{line("A"), line("B"), line("C")}}, []int{13, 14, 15}},
		{"top-two", CaptionBlock{Anchor: AnchorTop, Lines: []Line{line("A"), line("B")}}, []int{1, 2}},
		{"explicit-row", CaptionBlock{Lines: []Line{{Runs: []Run{{Text: "A", Pen: white}}, Row: 4}}}, []int{4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.block.Screen()
			if len(s.Rows) != len(c.want) {
				t.Fatalf("got %d rows, want %d", len(s.Rows), len(c.want))
			}
			for i, r := range s.Rows {
				if r.Index != c.want[i] {
					t.Errorf("row %d index = %d, want %d", i, r.Index, c.want[i])
				}
			}
		})
	}
}

// TestCaptionBlockCentering checks that a centered white line and a centered
// colored line land at the same, correct absolute start column (SPEC §7). The
// mid-row compensation is a wire-lowering concern (tested on the Encoder); here
// we assert the resolved Screen columns.
func TestCaptionBlockCentering(t *testing.T) {
	// "HELLO" has width 5 -> left = (32-5)/2 = 13.
	white := CaptionBlock{Lines: []Line{{Runs: []Run{{Text: "HELLO", Pen: Pen{Color: White}}}, Align: AlignCenter}}}
	colored := CaptionBlock{Lines: []Line{{Runs: []Run{{Text: "HELLO", Pen: Pen{Color: Yellow}}}, Align: AlignCenter}}}

	for _, c := range []struct {
		name string
		s    Screen
		col  int
		pen  Pen
	}{
		{"white", white.Screen(), 13, Pen{Color: White}},
		{"colored", colored.Screen(), 13, Pen{Color: Yellow}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if len(c.s.Rows) != 1 || len(c.s.Rows[0].Runs) != 1 {
				t.Fatalf("unexpected shape: %+v", c.s)
			}
			r := c.s.Rows[0].Runs[0]
			if r.Column != c.col {
				t.Errorf("column = %d, want %d", r.Column, c.col)
			}
			if r.Pen != c.pen {
				t.Errorf("pen = %+v, want %+v", r.Pen, c.pen)
			}
			if c.s.Rows[0].Index != 15 {
				t.Errorf("row = %d, want 15", c.s.Rows[0].Index)
			}
		})
	}
}

// TestCaptionBlockAlign checks left/right/center offsets for a known width.
func TestCaptionBlockAlign(t *testing.T) {
	// width-10 text.
	text := "2026-07-17"
	mk := func(a Align) Run {
		b := CaptionBlock{Lines: []Line{{Runs: []Run{{Text: text, Pen: Pen{Color: White}}}, Align: a}}}
		return b.Screen().Rows[0].Runs[0]
	}
	if got := mk(AlignLeft).Column; got != 0 {
		t.Errorf("left column = %d, want 0", got)
	}
	if got := mk(AlignCenter).Column; got != 11 { // (32-10)/2
		t.Errorf("center column = %d, want 11", got)
	}
	if got := mk(AlignRight).Column; got != 22 { // 32-10
		t.Errorf("right column = %d, want 22", got)
	}
}

// TestCaptionBlockMultiRunLine checks that multiple runs on a line keep their
// relative spacing after the alignment offset is applied.
func TestCaptionBlockMultiRunLine(t *testing.T) {
	// Two runs: "AB" at rel col 0, "CD" at rel col 3 (a one-cell gap at col 2).
	// Width = 5. Centered left = (32-5)/2 = 13. So cols 13 and 16.
	block := CaptionBlock{Lines: []Line{{
		Align: AlignCenter,
		Runs: []Run{
			{Column: 0, Text: "AB", Pen: Pen{Color: White}},
			{Column: 3, Text: "CD", Pen: Pen{Color: Red}},
		},
	}}}
	s := block.Screen()
	runs := s.Rows[0].Runs
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].Column != 13 || runs[1].Column != 16 {
		t.Errorf("columns = %d,%d want 13,16", runs[0].Column, runs[1].Column)
	}
}
