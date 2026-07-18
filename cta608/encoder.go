package cta608

import (
	"sort"
	"strings"
)

// Encoder is the single per-channel diff engine (SPEC §2 invariant 2). It holds
// the current displayed Screen and the caption mode, and turns a target Screen
// into the []Token that transforms current -> target. The zero value is a valid
// Encoder in pop-on mode with an empty display.
//
// SetScreen diffs against a caller-built Screen; Apply compiles a CaptionBlock
// to a target Screen first. All mode-specific token generation lives here:
// pop-on builds into non-displayed memory and flips with EOC; roll-up appends
// to the bottom row and scrolls with CR; paint-on writes changed rows directly.
// Emitted tokens serialize through Serialize to odd-parity, frame-aligned pairs.
type Encoder struct {
	mode       Mode
	rollUpRows int
	current    Screen // the currently displayed rows (normalized, Displayed=true)

	// modeStarted reports whether the entry command for the current roll-up /
	// paint-on mode has already been emitted (so it is not repeated per update).
	modeStarted bool

	// cursor tracks where the last emission left the write head, enabling a
	// minimal roll-up/paint-on delta (append characters without a fresh PAC).
	cur cursor
}

// cursor is the write-head state carried across emissions.
type cursor struct {
	row   int
	col   int
	pen   Pen
	valid bool
}

// SetMode selects the caption mode for subsequent SetScreen/Apply calls. It only
// records state — the RCL/RU/RDC token is emitted by SetScreen as part of a
// spec-correct sequence. rollUpRows is used only for RollUp (clamped to 2..4).
func (e *Encoder) SetMode(m Mode, rollUpRows int) {
	if m != e.mode || rollUpRows != e.rollUpRows {
		e.mode = m
		e.rollUpRows = rollUpRows
		e.modeStarted = false
		e.cur.valid = false
	}
}

// Screen returns a copy of the current displayed Screen (the encoder's mirror of
// what a decoder would show). It is the counterpart of Decoder.Screen().
func (e *Encoder) Screen() Screen {
	return cloneScreen(e.current)
}

// SetScreen diffs the current displayed Screen against target and returns the
// tokens that transform one into the other for the encoder's current mode. An
// unchanged target yields no tokens.
func (e *Encoder) SetScreen(target Screen) []Token {
	target = normalizeScreen(target)
	switch e.mode {
	case RollUp:
		return e.rollUp(target)
	case PaintOn:
		return e.paintOn(target)
	default:
		return e.popOn(target)
	}
}

// Apply sets the mode from the block, compiles it to a target Screen, and diffs.
func (e *Encoder) Apply(b CaptionBlock) []Token {
	e.SetMode(b.Mode, b.RollUpRows)
	return e.SetScreen(b.Screen())
}

// --- pop-on -----------------------------------------------------------------

// popOn rebuilds the caption in non-displayed memory (RCL + ENM + rows) and
// flips it on (EOC). Pop-on is double-buffered, so a change is a full rebuild;
// an unchanged target emits nothing, and clearing emits a single EDM.
func (e *Encoder) popOn(target Screen) []Token {
	if screenEqual(e.current, target) {
		return nil
	}
	if len(target.Rows) == 0 {
		e.current = target
		e.cur.valid = false
		return []Token{Command{Op: EDM}}
	}
	m := e.newEmitter()
	m.add(SetMode{Mode: PopOn}, Command{Op: ENM})
	for _, r := range sortedRows(target) {
		m.emitRow(r.Index, r.Runs)
	}
	m.add(Command{Op: EOC})
	e.store(target, m)
	e.cur.valid = false // next pop-on rebuilds from scratch
	return m.toks
}

// --- roll-up ----------------------------------------------------------------

// rollUp drives a roll-up window. The bottom (base) row is the highest row index
// in target. Transitions are: initial write (RU + base row), append on the base
// row (minimal delta), or scroll (CR + new base row) when the target's upper
// lines match a suffix of the current ones.
func (e *Encoder) rollUp(target Screen) []Token {
	tgt := sortedRows(target)
	m := e.newEmitter()

	if !e.modeStarted {
		m.add(SetMode{Mode: RollUp, RollUpRows: clamp(e.rollUpRows, 2, 4)})
		e.modeStarted = true
		m.cur.valid = false
		e.rollUpRewrite(m, tgt)
		e.store(target, m)
		return m.toks
	}
	if screenEqual(e.current, target) {
		return nil
	}
	if len(tgt) == 0 {
		m.add(Command{Op: EDM})
		m.cur.valid = false
		e.store(target, m)
		return m.toks
	}

	cur := runsOf(sortedRows(e.current))
	tl := runsOf(tgt)
	base := tgt[len(tgt)-1].Index

	switch {
	case isAppend(cur, tl):
		// Only the base row changed (and only by extension): emit its delta.
		m.diffRow(base, lastRuns(cur), tl[len(tl)-1])
	case isScroll(cur, tl):
		m.add(Command{Op: CR})
		m.cur.valid = false
		m.emitRow(base, tl[len(tl)-1])
	default:
		m.add(Command{Op: EDM})
		m.cur.valid = false
		e.rollUpRewrite(m, tgt)
	}
	e.store(target, m)
	return m.toks
}

// rollUpRewrite writes every target line onto the base row top-to-bottom,
// scrolling each up with a CR so the whole window ends in place.
func (e *Encoder) rollUpRewrite(m *emitter, tgt []Row) {
	if len(tgt) == 0 {
		return
	}
	base := tgt[len(tgt)-1].Index
	for i, r := range tgt {
		m.emitRow(base, r.Runs)
		if i < len(tgt)-1 {
			m.add(Command{Op: CR})
			m.cur.valid = false
		}
	}
}

// --- paint-on ---------------------------------------------------------------

// paintOn writes changed rows directly to the displayed screen (RDC on entry).
// Each differing row is emitted from its first changed run; rows that disappear
// are erased with DER.
func (e *Encoder) paintOn(target Screen) []Token {
	m := e.newEmitter()
	if !e.modeStarted {
		m.add(SetMode{Mode: PaintOn})
		e.modeStarted = true
		m.cur.valid = false
	}
	if len(m.toks) == 0 && screenEqual(e.current, target) {
		return nil
	}
	curByRow := rowsByIndex(e.current)
	for _, tr := range sortedRows(target) {
		m.diffRow(tr.Index, curByRow[tr.Index], tr.Runs)
	}
	tgtByRow := rowsByIndex(target)
	for _, cr := range sortedRows(e.current) {
		if _, ok := tgtByRow[cr.Index]; !ok && len(cr.Runs) > 0 {
			m.positionWhite(cr.Index, 0, false)
			m.add(Command{Op: DER})
			m.cur.valid = false
		}
	}
	e.store(target, m)
	return m.toks
}

// --- emission (cursor-tracking token builder) -------------------------------

// emitter accumulates tokens while tracking the write-head cursor, so appends
// can skip a redundant PAC.
type emitter struct {
	toks []Token
	cur  cursor
}

func (e *Encoder) newEmitter() *emitter { return &emitter{cur: e.cur} }

// store records the new displayed screen and the final cursor.
func (e *Encoder) store(target Screen, m *emitter) {
	e.current = target
	e.cur = m.cur
}

func (m *emitter) add(tok ...Token) { m.toks = append(m.toks, tok...) }

// emitRow emits a full row of runs from scratch (used by pop-on and roll-up
// rewrites): the first run forces a reposition, the rest flow via run().
func (m *emitter) emitRow(row int, runs []Run) {
	for i, r := range runs {
		if i == 0 {
			m.cur.valid = false // force a PAC for the row's first run
		}
		m.run(row, r)
	}
}

// diffRow emits the minimal tokens to turn cur runs into tgt runs on row. It
// finds the longest identical run prefix, then handles the tail: a within-run
// text extension (append only the new suffix), a pure append of extra runs (the
// write head is still live at the end of the prefix, so run() continues via
// chars/mid-row), or a changed run (reposition with a PAC for safety, since the
// write head may be stale). Finally, if the row shrank, it erases the trailing
// leftover with a Delete-to-End-of-Row.
func (m *emitter) diffRow(row int, cur, tgt []Run) {
	i := 0
	for i < len(cur) && i < len(tgt) && cur[i] == tgt[i] {
		i++
	}
	switch {
	case i == len(tgt):
		// target is a prefix of current: nothing new to write.
	case i < len(cur) && isExtension(cur[i], tgt[i]):
		// Same run extended: emit only the appended suffix, then any runs after
		// it (all genuine appends — keep the live write head).
		c := cur[i]
		suffix := tgt[i].Text[len(c.Text):]
		m.run(row, Run{Column: c.Column + runeLen(c.Text), Text: suffix, Pen: c.Pen})
		for j := i + 1; j < len(tgt); j++ {
			m.run(row, tgt[j])
		}
	default:
		if i < len(cur) {
			m.cur.valid = false // a run changed: reposition the first re-emitted run
		}
		for j := i; j < len(tgt); j++ {
			m.run(row, tgt[j])
		}
	}
	// If the new content is shorter than what is displayed, erase the leftover
	// tail (stale glyphs to the right of the new content).
	if extent(cur) > extent(tgt) {
		m.positionWhite(row, extent(tgt), false)
		m.add(Command{Op: DER})
		m.cur.valid = false
	}
}

// isExtension reports whether target run t is current run c with more text
// appended at the same column and pen (so only the suffix need be emitted).
func isExtension(c, t Run) bool {
	return c.Column == t.Column && c.Pen == t.Pen &&
		len(t.Text) > len(c.Text) && strings.HasPrefix(t.Text, c.Text)
}

// extent returns the column just past the rightmost glyph of the runs (0 for an
// empty row).
func extent(runs []Run) int {
	end := 0
	for _, r := range runs {
		if e := r.Column + runeLen(r.Text); e > end {
			end = e
		}
	}
	return end
}

// run emits one run at (row, r.Column). It appends characters when the cursor is
// already there with the same pen, uses a mid-row transition when the cursor is
// one cell before it with a different pen, and otherwise repositions with a PAC.
func (m *emitter) run(row int, r Run) {
	switch {
	case m.cur.valid && m.cur.row == row && m.cur.col == r.Column && m.cur.pen == r.Pen:
		m.chars(r.Text)
	case m.cur.valid && m.cur.row == row && m.cur.col == r.Column-1 &&
		m.cur.pen != r.Pen && midRowExpressible(r.Pen):
		m.add(MidRow{Pen: r.Pen})
		m.cur.col++
		m.cur.pen = r.Pen
		m.chars(r.Text)
	default:
		m.positionFor(row, r)
		m.chars(r.Text)
	}
	m.cur.valid = true
}

// chars appends a character token and advances the cursor one cell per rune.
func (m *emitter) chars(text string) {
	if text == "" {
		return
	}
	m.add(Chars{Text: text})
	m.cur.col += runeLen(text)
}

// positionFor emits the PAC (+ Tab, + MidRow) that lands the cursor at the start
// of run r with its pen. A colored/italic run at column > 0 is compensated one
// column left so the mid-row cell lands the text on r.Column (SPEC §7 / W6).
func (m *emitter) positionFor(row int, r Run) {
	c := clamp(r.Column, 0, 31)
	p := r.Pen
	switch {
	case c == 0 && colorPACExpressible(p):
		m.add(PAC{Row: row, Indent: NoIndent, Pen: p})
		m.cur = cursor{row: row, col: 0, pen: p, valid: true}
	case isPlainWhite(p):
		m.positionWhite(row, c, p.Underline)
	default:
		pos := c - 1
		if pos < 0 {
			pos = 0
		}
		m.positionWhite(row, pos, false)
		m.add(MidRow{Pen: p})
		m.cur.col++
		m.cur.pen = p
	}
	if p.Background != ColDefault {
		m.add(BackgroundAttr{Pen: Pen{Background: p.Background}})
	}
}

// positionWhite emits an indent-style PAC (+ Tab Offset) placing a white
// (optionally underlined) cursor at absolute column col.
func (m *emitter) positionWhite(row, col int, underline bool) {
	col = clamp(col, 0, 31)
	indent := col / 4 * 4
	tab := col % 4
	m.add(PAC{Row: row, Indent: indent, Pen: Pen{Color: White, Underline: underline}})
	if tab > 0 {
		m.add(TabOffset{Columns: tab})
	}
	m.cur = cursor{row: row, col: col, pen: Pen{Color: White, Underline: underline}, valid: true}
}

// --- pen predicates ---------------------------------------------------------

// isPlainWhite reports a pen expressible by an indent-style PAC: white (or
// default) foreground, no italics, no background.
func isPlainWhite(p Pen) bool {
	return (p.Color == White || p.Color == ColDefault) && !p.Italic && p.Background == ColDefault
}

// colorPACExpressible reports whether a color-style PAC can carry the pen: no
// background, and italics only in combination with white.
func colorPACExpressible(p Pen) bool {
	if p.Background != ColDefault {
		return false
	}
	if p.Italic && p.Color != White && p.Color != ColDefault {
		return false
	}
	return true
}

// midRowExpressible reports whether a mid-row code can carry the pen (same
// constraints as a color PAC: no background, italics only with white).
func midRowExpressible(p Pen) bool {
	return colorPACExpressible(p)
}

// --- screen helpers ---------------------------------------------------------

// normalizeScreen returns a copy with empty rows dropped, foreground ColDefault
// folded to White, and rows sorted by index, so diffs and cursor comparisons are
// stable. Displayed is forced true (the target is what should be shown).
func normalizeScreen(s Screen) Screen {
	var rows []Row
	for _, r := range s.Rows {
		var runs []Run
		for _, run := range r.Runs {
			if run.Text == "" {
				continue
			}
			if run.Pen.Color == ColDefault {
				run.Pen.Color = White
			}
			runs = append(runs, run)
		}
		if len(runs) == 0 {
			continue
		}
		rows = append(rows, Row{Index: r.Index, Displayed: true, Runs: runs})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	return Screen{Rows: rows}
}

// sortedRows returns the screen's rows sorted ascending by index.
func sortedRows(s Screen) []Row {
	rows := make([]Row, len(s.Rows))
	copy(rows, s.Rows)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	return rows
}

// rowsByIndex maps row index -> that row's runs.
func rowsByIndex(s Screen) map[int][]Run {
	m := make(map[int][]Run, len(s.Rows))
	for _, r := range s.Rows {
		m[r.Index] = r.Runs
	}
	return m
}

// runsOf returns the per-row run slices in row order.
func runsOf(rows []Row) [][]Run {
	out := make([][]Run, len(rows))
	for i, r := range rows {
		out[i] = r.Runs
	}
	return out
}

func lastRuns(lines [][]Run) []Run {
	if len(lines) == 0 {
		return nil
	}
	return lines[len(lines)-1]
}

// screenEqual compares two screens by row index and runs, ignoring the Displayed
// flag (a double-buffer detail the encoder manages internally).
func screenEqual(a, b Screen) bool {
	ra, rb := sortedRows(a), sortedRows(b)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i].Index != rb[i].Index || !runsEqual(ra[i].Runs, rb[i].Runs) {
			return false
		}
	}
	return true
}

func runsEqual(a, b []Run) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isAppend reports that target is current with only its bottom line changed by
// extension (same number of lines, identical upper lines, bottom differs).
func isAppend(cur, tgt [][]Run) bool {
	if len(cur) != len(tgt) || len(tgt) == 0 {
		return false
	}
	for i := 0; i < len(tgt)-1; i++ {
		if !runsEqual(cur[i], tgt[i]) {
			return false
		}
	}
	return !runsEqual(cur[len(cur)-1], tgt[len(tgt)-1])
}

// isScroll reports that target's upper lines equal a suffix of current's lines —
// i.e. current scrolled up by one and a new bottom line was added.
func isScroll(cur, tgt [][]Run) bool {
	u := len(tgt) - 1
	if u < 0 || u > len(cur) {
		return false
	}
	for i := 0; i < u; i++ {
		if !runsEqual(tgt[i], cur[len(cur)-u+i]) {
			return false
		}
	}
	return true
}

// cloneScreen deep-copies a screen (rows and their run slices).
func cloneScreen(s Screen) Screen {
	rows := make([]Row, len(s.Rows))
	for i, r := range s.Rows {
		runs := make([]Run, len(r.Runs))
		copy(runs, r.Runs)
		rows[i] = Row{Index: r.Index, Displayed: r.Displayed, Runs: runs}
	}
	return Screen{Rows: rows}
}
