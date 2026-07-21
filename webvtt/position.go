package webvtt

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// cueSettings is the subset of WebVTT cue settings this serializer maps onto the
// 608 grid: line: (vertical) and position:/align: (horizontal). Everything else a
// cue may carry (size:, region:, vertical:) is deliberately dropped — 608's
// coarse 15x32 grid and bottom-anchored model cannot hold it, so carrying it
// would be false fidelity (SPEC §8.2, design note W6). Percentages run 0..100.
//
// The mapping is quantized and therefore only approximately invertible: several
// nearby WebVTT positions collapse onto the same grid Row/Column, which is the
// accepted lossy behaviour of W6 (contrast the byte-exact SCC/SEI containers).
type cueSettings struct {
	// linePct is a percentage line: value (0 = top of frame, 100 = bottom); set
	// iff linePctSet. WebVTT line: may be either a percentage or a snap-to-lines
	// integer, so the two are tracked separately.
	linePct    float64
	linePctSet bool
	// lineNum is an integer (snap-to-lines) line: value; set iff lineNumSet. A
	// non-negative value counts rows from the top (0 = first row), a negative one
	// from the bottom (-1 = last row), matching the WebVTT line-number semantics.
	lineNum    int
	lineNumSet bool
	// positionPct is the horizontal position: percentage of the box alignment
	// point; set iff positionSet. When unset WebVTT centers the cue (50%).
	positionPct float64
	positionSet bool
	// align is the text alignment keyword (start|center|end|left|right); the empty
	// string means unset, which WebVTT treats as center.
	align string
}

// parseSettings turns the whitespace-separated "key:value" tokens that follow a
// cue's "-->" timing into a cueSettings. Unknown keys are ignored (dropped, per
// W6) and any ",<alignment>" suffix on line:/position: — e.g. "position:40%,line-left"
// — is discarded, because 608 has no notion of a secondary alignment axis.
func parseSettings(fields []string) cueSettings {
	var s cueSettings
	for _, f := range fields {
		key, val, ok := strings.Cut(f, ":")
		if !ok {
			continue
		}
		val, _, _ = strings.Cut(val, ",") // drop the optional ",<alignment>" suffix
		switch key {
		case "line":
			if pct, ok := parsePercent(val); ok {
				s.linePct, s.linePctSet = pct, true
			} else if n, err := strconv.Atoi(val); err == nil {
				s.lineNum, s.lineNumSet = n, true
			}
		case "position":
			if pct, ok := parsePercent(val); ok {
				s.positionPct, s.positionSet = pct, true
			}
		case "align":
			s.align = val
		}
	}
	return s
}

// parsePercent parses a "NN%" value, returning false when the suffix is absent
// (so a bare line-number integer can be told apart from a percentage).
func parsePercent(val string) (float64, bool) {
	if !strings.HasSuffix(val, "%") {
		return 0, false
	}
	p, err := strconv.ParseFloat(strings.TrimSuffix(val, "%"), 64)
	if err != nil {
		return 0, false
	}
	return p, true
}

// row maps a cue's line: setting to a 1..15 grid Row for the lineIndex-th payload
// line of an nLines-line cue. WebVTT places the first line at line: and stacks the
// rest downward, so lineIndex shifts the base row down. A percentage is quantized
// across the 14 gaps between the 15 rows; a snap-to-lines integer maps directly;
// and a position-less cue is bottom-anchored (its last line lands on row 15) —
// the default bottom anchor of W6. The result is clamped to the grid.
func (s cueSettings) row(nLines, lineIndex int) int {
	var base int
	switch {
	case s.linePctSet:
		base = int(math.Round(s.linePct/100*14)) + 1
	case s.lineNumSet:
		if s.lineNum >= 0 {
			base = s.lineNum + 1 // line 0 -> row 1 (top)
		} else {
			base = 16 + s.lineNum // line -1 -> row 15 (bottom)
		}
	default:
		base = 15 - (nLines - 1) // bottom-anchored: last line on row 15
	}
	return clampInt(base+lineIndex, 1, 15)
}

// leftColumn maps a cue's position:/align: settings to the 0..31 grid column at
// which a payload line of the given rendered width begins. position: locates the
// box's alignment point (default center at 50%); align: selects which point that
// is — start/left uses the left edge, end/right the right edge, and center (the
// WebVTT default) the middle. Subtracting the appropriate share of the width
// yields the left edge, quantized and clamped to the grid (W6).
func (s cueSettings) leftColumn(width int) int {
	pos := 50.0
	if s.positionSet {
		pos = s.positionPct
	}
	anchor := pos / 100 * gridCols
	var left float64
	switch s.align {
	case "start", "left":
		left = anchor
	case "end", "right":
		left = anchor - float64(width)
	default: // center, middle, or unset -> centered on the anchor
		left = anchor - float64(width)/2
	}
	return clampInt(int(math.Round(left)), 0, gridCols-1)
}

// formatSettings emits the "line: position: align:" string for a cue whose
// topmost row is topRow and whose leftmost column across all rows is blockLeft.
// It is the inverse of row/leftColumn for the writer's own output: the top row
// becomes a line: percentage across the 14 inter-row gaps, and the block's left
// edge becomes a position: percentage with align:start. Reading this back
// reproduces the same Row/Column (within the shared quantization), which is what
// makes the WebVTT<->cue round-trip stable even though it is not byte-exact (W6).
func formatSettings(topRow, blockLeft int) string {
	linePct := int(math.Round(float64(topRow-1) / 14 * 100))
	posPct := int(math.Round(float64(blockLeft) / gridCols * 100))
	return fmt.Sprintf("line:%d%% position:%d%% align:start", linePct, posPct)
}

// gridCols is the 608 column count (0..31) used as the horizontal quantization
// denominator; rows use 1..15 directly.
const gridCols = 32

// clampInt constrains v to the inclusive [lo, hi] range.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
