package cta608

import "unicode/utf8"

// Align is the horizontal placement of a Line within the 32-column caption grid.
// It is authoring sugar: CaptionBlock.Screen() resolves it to absolute Run
// columns (the Screen itself has no notion of alignment).
type Align uint8

// Align values.
const (
	AlignLeft   Align = iota // line starts at column 0 (plus each run's own column)
	AlignCenter              // line is centered in the 32-column grid
	AlignRight               // line ends at column 31
)

// String returns the alignment name.
func (a Align) String() string {
	switch a {
	case AlignLeft:
		return "left"
	case AlignCenter:
		return "center"
	case AlignRight:
		return "right"
	default:
		return "Align(?)"
	}
}

// Anchor is the vertical placement of a CaptionBlock. Lines with an explicit
// Row override it; the rest are laid out on consecutive rows anchored to the
// top or the bottom of the 15-row grid.
type Anchor uint8

// Anchor values.
const (
	AnchorBottom Anchor = iota // lines occupy the bottom rows (…, 14, 15) — the caption default
	AnchorTop                  // lines occupy the top rows (1, 2, …)
)

// String returns the anchor name.
func (a Anchor) String() string {
	switch a {
	case AnchorBottom:
		return "bottom"
	case AnchorTop:
		return "top"
	default:
		return "Anchor(?)"
	}
}

// Line is one authored row of caption text: an ordered set of styled Runs plus
// how the line sits horizontally. A Run's Column here is line-relative (0-based
// from the line's own start); CaptionBlock.Screen() resolves it to an absolute
// screen column from Align. Row is an explicit 1..15 row, or 0 to derive the
// row from the block's Anchor and the line's position in CaptionBlock.Lines.
type Line struct {
	Runs  []Run
	Align Align
	Row   int
}

// CaptionBlock is friendly authoring on top of Screen: a set of Lines placed by
// an Anchor, plus the caption Mode (and RollUpRows for roll-up). CaptionBlock
// itself carries no wire state; Screen() compiles it to a target Screen that the
// Encoder then diffs against the current display. Power users can build a Screen
// directly and skip CaptionBlock entirely.
type CaptionBlock struct {
	Lines      []Line
	Anchor     Anchor
	Mode       Mode
	RollUpRows int
}

// Screen compiles the block into a target Screen: each Line becomes a Row whose
// Runs carry absolute columns (0..31) resolved from the line's Align, and whose
// Index comes from the line's explicit Row or from the Anchor. Empty lines are
// dropped. The columns are what the caption should occupy; the Encoder owns the
// wire lowering to PAC indent + Tab Offset (and the mid-row compensation for a
// colored line, SPEC §7).
func (b CaptionBlock) Screen() Screen {
	m := len(b.Lines)
	var rows []Row
	for i, ln := range b.Lines {
		runs := resolveLine(ln)
		if len(runs) == 0 {
			continue
		}
		row := ln.Row
		if row == 0 {
			if b.Anchor == AnchorTop {
				row = 1 + i
			} else {
				row = 15 - (m - 1) + i
			}
		}
		row = clamp(row, 1, 15)
		rows = append(rows, Row{Index: row, Displayed: true, Runs: runs})
	}
	return Screen{Rows: rows}
}

// resolveLine turns a Line's line-relative runs into absolute-column runs by
// computing the line's visible width and its left edge from the alignment.
func resolveLine(ln Line) []Run {
	if len(ln.Runs) == 0 {
		return nil
	}
	width := 0
	for _, r := range ln.Runs {
		if end := r.Column + runeLen(r.Text); end > width {
			width = end
		}
	}
	var left int
	switch ln.Align {
	case AlignCenter:
		left = (32 - width) / 2
	case AlignRight:
		left = 32 - width
	default: // AlignLeft
		left = 0
	}
	if left < 0 {
		left = 0
	}
	out := make([]Run, 0, len(ln.Runs))
	for _, r := range ln.Runs {
		pen := r.Pen
		if pen.Color == ColDefault {
			pen.Color = White
		}
		out = append(out, Run{Column: clamp(left+r.Column, 0, 31), Text: r.Text, Pen: pen})
	}
	return out
}

// runeLen is the display width of text in columns: one cell per rune (special
// and extended glyphs each occupy a single cell).
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
