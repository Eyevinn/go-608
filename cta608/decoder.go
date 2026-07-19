package cta608

import "sort"

// Decoder is the stateful, per-channel interpreter that turns a 608 token or
// byte stream into the sparse, displayed Screen a consumer renders. It is the
// decode half of the core and the inverse of Encoder. The zero value is a valid
// Decoder in pop-on mode with an empty display.
//
// Feed parses cc_data byte pairs and interprets them; Push interprets an
// already-parsed token stream; Screen returns the current displayed rows.
// Changed reports whether the displayed Screen changed since the previous call —
// the signal cue segmentation pivots on.
//
// The double buffer is modeled as two internal cell grids (displayed and
// non-displayed); pop-on writes to non-displayed and EOC promotes it. XDS is
// dropped by Parse (field-2 control codes never reach interpretation), and text
// mode (TR/RTD) is recognized but its content is not written to the Screen
// (SPEC §1.3).
type Decoder struct {
	mode       Mode
	rollUpRows int
	baseRow    int // roll-up window bottom row (0 = default 15)

	displayed    lineMap // currently shown grid
	nonDisplayed lineMap // pop-on build grid

	cur      dcursor
	textMode bool // TR/RTD entered: caption writes are ignored until a mode command

	notified Screen // displayed snapshot as of the last Changed call
}

// dcell is one character cell: a rune with its Pen. set distinguishes a written
// cell from an empty one (a gap, e.g. where a mid-row code sits).
type dcell struct {
	r   rune
	pen Pen
	set bool
}

// dline is a row of 32 cells (columns 0..31).
type dline [32]dcell

// lineMap maps a 1..15 row index to its cell grid.
type lineMap map[int]*dline

func (m lineMap) line(row int) *dline {
	l := m[row]
	if l == nil {
		l = &dline{}
		m[row] = l
	}
	return l
}

// dcursor is the decoder write head. row 0 means "unpositioned" (writes are
// dropped until a PAC positions the cursor).
type dcursor struct {
	row, col int
	pen      Pen
}

// Feed parses cc_data byte pairs (a single channel's stream) into tokens and
// interprets them, advancing the displayed Screen. Parity is stripped; use Push
// after Parse with ParseOptions if strict parity validation is wanted.
func (d *Decoder) Feed(data []byte) error {
	toks, err := Parse(data, ParseOptions{})
	if err != nil {
		return err
	}
	d.Push(toks)
	return nil
}

// Push interprets an already-parsed token stream.
func (d *Decoder) Push(tokens []Token) {
	for _, t := range tokens {
		d.step(t)
	}
}

// Screen returns a copy of the current displayed rows (sparse, Displayed=true).
func (d *Decoder) Screen() Screen {
	return screenFromLineMap(d.displayed)
}

// Changed reports whether the displayed Screen differs from what it was at the
// previous Changed call (or, on the first call, from an empty screen), then
// records the current screen as the new baseline. It fires exactly when Screen
// output differs, so a mutation that nets to no change does not signal.
func (d *Decoder) Changed() bool {
	cur := d.Screen()
	changed := !screenEqual(cur, d.notified)
	d.notified = cur
	return changed
}

// active returns the grid the cursor currently writes to: the non-displayed grid
// in pop-on mode, the displayed grid in roll-up and paint-on.
func (d *Decoder) active() lineMap {
	if d.mode == PopOn {
		if d.nonDisplayed == nil {
			d.nonDisplayed = lineMap{}
		}
		return d.nonDisplayed
	}
	if d.displayed == nil {
		d.displayed = lineMap{}
	}
	return d.displayed
}

func (d *Decoder) step(t Token) {
	switch tok := t.(type) {
	case SetMode:
		d.textMode = false
		d.mode = tok.Mode
		if tok.Mode == RollUp {
			d.rollUpRows = clamp(tok.RollUpRows, 2, 4)
			if d.baseRow == 0 {
				d.baseRow = 15
			}
			d.cur = dcursor{row: d.baseRow, col: 0, pen: Pen{Color: White}}
		}
		// pop-on / paint-on: a following PAC positions the cursor.
	case PAC:
		if d.textMode {
			return
		}
		d.cur.row = clamp(tok.Row, 1, 15)
		if tok.Indent == NoIndent {
			d.cur.col = 0
		} else {
			d.cur.col = clamp(tok.Indent, 0, 31)
		}
		d.cur.pen = tok.Pen
		if d.mode == RollUp {
			d.baseRow = d.cur.row
		}
	case TabOffset:
		d.cur.col = clamp(d.cur.col+tok.Columns, 0, 31)
	case MidRow:
		// A mid-row code changes the pen and occupies one cell (no visible glyph),
		// so following text starts one column to the right.
		d.cur.pen = tok.Pen
		d.cur.col = clamp(d.cur.col+1, 0, 31)
	case BackgroundAttr:
		if tok.Pen.Color == Black { // black-foreground attribute
			d.cur.pen.Color = Black
			d.cur.pen.Underline = tok.Pen.Underline
		}
		if tok.Pen.Background != ColDefault {
			d.cur.pen.Background = tok.Pen.Background
		}
	case Chars:
		if d.textMode {
			return
		}
		d.writeChars(tok.Text)
	case Command:
		d.command(tok.Op)
	}
}

func (d *Decoder) writeChars(text string) {
	if d.cur.row < 1 || d.cur.row > 15 {
		return
	}
	l := d.active().line(d.cur.row)
	for _, r := range text {
		if d.cur.col < 0 || d.cur.col > 31 {
			break
		}
		l[d.cur.col] = dcell{r: r, pen: d.cur.pen, set: true}
		d.cur.col++
	}
}

func (d *Decoder) command(op Op) {
	switch op {
	case EOC: // flip non-displayed -> displayed, dropping the previous caption
		d.displayed = cloneLineMap(d.nonDisplayed)
	case EDM: // erase displayed memory
		d.displayed = lineMap{}
	case ENM: // erase non-displayed memory
		d.nonDisplayed = lineMap{}
	case CR:
		d.rollUpScroll()
	case BS:
		d.backspace()
	case DER:
		d.deleteToEndOfRow()
	case TR, RTD: // text mode: recognized, content not modeled
		d.textMode = true
	}
	// FON / AOF / AON: flash and reserved codes are not modeled — no state change.
}

// rollUpScroll scrolls the roll-up window up by one row, dropping the top row and
// clearing the base row for new text. CR only scrolls in roll-up mode.
func (d *Decoder) rollUpScroll() {
	if d.mode != RollUp {
		return
	}
	if d.displayed == nil {
		d.displayed = lineMap{}
	}
	n := clamp(d.rollUpRows, 2, 4)
	base := d.baseRow
	if base == 0 {
		base = 15
	}
	for r := base - n + 1; r < base; r++ {
		if r < 1 {
			continue
		}
		if src := d.displayed[r+1]; src != nil {
			cp := *src
			d.displayed[r] = &cp
		} else {
			delete(d.displayed, r)
		}
	}
	delete(d.displayed, base)
	d.cur = dcursor{row: base, col: 0, pen: Pen{Color: White}}
}

func (d *Decoder) backspace() {
	if d.cur.col > 0 {
		d.cur.col--
	}
	if d.cur.row < 1 || d.cur.row > 15 {
		return
	}
	if l := d.active()[d.cur.row]; l != nil {
		l[d.cur.col] = dcell{}
	}
}

func (d *Decoder) deleteToEndOfRow() {
	if d.cur.row < 1 || d.cur.row > 15 {
		return
	}
	l := d.active()[d.cur.row]
	if l == nil {
		return
	}
	for c := d.cur.col; c <= 31; c++ {
		l[c] = dcell{}
	}
}

// --- grid -> Screen ---------------------------------------------------------

func screenFromLineMap(m lineMap) Screen {
	if len(m) == 0 {
		return Screen{}
	}
	idxs := make([]int, 0, len(m))
	for idx := range m {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	var rows []Row
	for _, idx := range idxs {
		runs := runsFromLine(m[idx])
		if len(runs) == 0 {
			continue
		}
		rows = append(rows, Row{Index: idx, Displayed: true, Runs: runs})
	}
	if len(rows) == 0 {
		return Screen{}
	}
	return Screen{Rows: rows}
}

// runsFromLine collapses a cell grid into maximal runs of set cells sharing a
// Pen; an unset cell (a gap) ends the current run.
func runsFromLine(l *dline) []Run {
	var runs []Run
	c := 0
	for c <= 31 {
		if !l[c].set {
			c++
			continue
		}
		start := c
		pen := l[c].pen
		var rs []rune
		for c <= 31 && l[c].set && l[c].pen == pen {
			rs = append(rs, l[c].r)
			c++
		}
		runs = append(runs, Run{Column: start, Text: string(rs), Pen: pen})
	}
	return runs
}

func cloneLineMap(m lineMap) lineMap {
	out := make(lineMap, len(m))
	for idx, l := range m {
		cp := *l
		out[idx] = &cp
	}
	return out
}
