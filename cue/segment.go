package cue

import (
	"sort"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/cta608"
)

// SegmentOptions tunes how Segment resolves a caption that is still displayed
// when the input timeline ends — the one cue with no natural End (design note
// W3). Every other cue is closed by the next displayed-screen change, so these
// options affect only the final, dangling cue.
// Coalesce selects how Segment treats the direct-write caption modes, roll-up and
// paint-on, which write straight to the displayed screen so that every byte pair
// changes it — up to two characters per change.
type Coalesce int

const (
	// CoalesceStructural cuts a cue only at a structural event and not while text
	// is merely being added to a row, so roll-up yields one cue per scroll step and
	// paint-on one per write burst. A period's cue starts at its first change and
	// carries the screen as of its last, so the completed caption is displayed from
	// the moment its first characters appeared — which keeps coverage continuous,
	// where timestamping at completion would leave the typing interval in a gap.
	// This is the default.
	CoalesceStructural Coalesce = iota
	// CoalesceNone cuts a cue at every displayed-screen change, so a direct-write
	// caption arrives two characters at a time, each cue lasting a single frame. It
	// is the faithful rendering of what a viewer sees, and the only mode that needs
	// no lookahead, at the cost of one cue per byte pair.
	CoalesceNone
)

type SegmentOptions struct {
	// Coalesce selects the cue boundary for roll-up and paint-on; see Coalesce.
	// The zero value, CoalesceStructural, coalesces. Pop-on is unaffected either
	// way: its display changes once per caption already.
	Coalesce Coalesce
	// StreamEnd is the absolute end time of the stream. When it is set (> 0) and
	// falls after the dangling cue's Start, it becomes that cue's End — the
	// preferred, caller-authoritative resolution.
	StreamEnd time.Duration
	// DefaultDur is the fallback duration added to the dangling cue's Start when
	// StreamEnd is not usable (unset, or not after Start). The zero value yields
	// a zero-length final cue, so callers that expect a trailing caption should
	// set one of these fields.
	DefaultDur time.Duration
}

// Segment cuts a timeline of displayed-Screen states into a list of TimedCues
// using one unified rule for all caption modes (SPEC §8.2, design note W3):
// every change in the displayed Screen closes the current cue (End = the change
// instant) and, if the new screen is non-empty, opens a new cue (Start = the
// change instant). An empty displayed screen (an erase / EDM) is a gap — it
// closes the open cue and opens none.
//
// The per-mode behaviour falls out of that single rule:
//
//   - Pop-on -> one cue per caption: EOC replaces the display, giving one clean
//     displayed-screen change per caption.
//   - Roll-up -> one cue per scroll step: the base row growing is not a boundary,
//     so a cue spans the typing of one line and the visible lines repeat across
//     successive cues as they move up.
//   - Paint-on -> one cue per write burst, for the same reason.
//
// Roll-up and paint-on write straight to the displayed screen, so without
// coalescing every byte pair is a boundary and a line becomes one cue per two
// characters. opts.Coalesce selects between that faithful rendering
// (CoalesceNone) and the structural default; see Coalesce. Pop-on is unaffected —
// its display changes once per caption, at the EOC.
//
// The input is expected to come from a cta608.Decoder driven by timed byte
// pairs, sampled whenever Decoder.Changed reports a displayed-screen change.
// Segment sorts the input by Time defensively and treats a repeated identical
// screen as a non-boundary (no spurious cut). A caption still displayed at the
// end of the timeline has no natural End and is resolved from opts (W3).
func Segment(changes []TimedScreen, opts SegmentOptions) []TimedCue {
	if len(changes) == 0 {
		return nil
	}

	ordered := make([]TimedScreen, len(changes))
	copy(ordered, changes)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })

	var cues []TimedCue
	var open *TimedCue // the currently displayed cue, or nil during a gap

	prevMode := cta608.PopOn
	for _, ch := range ordered {
		screen := normalizeScreen(ch.Screen)
		if open != nil && screenEqual(open.Content, screen) {
			// The displayed screen did not actually change at this instant, so it
			// is not a segmentation boundary. Coalesce.
			continue
		}
		// Text added to a row of a direct-write caption is not a structural event:
		// extend the open cue's content in place, keeping its Start, so the whole
		// line is one cue rather than one per byte pair. Requiring the mode to be a
		// direct-write one is what stops a pop-on caption replaced by a longer one —
		// indistinguishable from typing, by screen alone — from being merged into it.
		if open != nil && opts.Coalesce == CoalesceStructural &&
			isDirectMode(ch.Mode) && ch.Mode == prevMode && screenGrows(open.Content, screen) {
			open.Content = screen
			prevMode = ch.Mode
			continue
		}
		prevMode = ch.Mode
		// The displayed screen changed: close the open cue at this instant.
		if open != nil {
			open.End = ch.Time
			cues = append(cues, *open)
			open = nil
		}
		// A non-empty new screen opens a fresh cue; an empty screen is a gap.
		if len(screen.Rows) > 0 {
			open = &TimedCue{Start: ch.Time, Content: screen}
		}
	}

	// A caption still displayed when the timeline ends has no closing change.
	if open != nil {
		open.End = danglingEnd(open.Start, opts)
		cues = append(cues, *open)
	}
	return cues
}

// danglingEnd resolves the End of the final, still-displayed cue: the
// caller-supplied StreamEnd when it is set and lands after Start, else
// Start + DefaultDur (design note W3).
func danglingEnd(start time.Duration, opts SegmentOptions) time.Duration {
	if opts.StreamEnd > start {
		return opts.StreamEnd
	}
	return start + opts.DefaultDur
}

// isDirectMode reports whether a caption mode writes to the displayed screen
// directly, rather than building into non-displayed memory and flipping.
func isDirectMode(m cta608.Mode) bool {
	return m == cta608.RollUp || m == cta608.PaintOn
}

// screenGrows reports whether next is prev with text appended to exactly one row —
// the signature of a direct-write caption being typed, as opposed to a structural
// change like a roll-up scroll (rows shift, so a row disappears), an erase (rows
// vanish), or a jump to another row (two rows differ).
func screenGrows(prev, next cta608.Screen) bool {
	p, n := normalizeScreen(prev), normalizeScreen(next)
	byIndex := make(map[int][]cta608.Run, len(n.Rows))
	for _, r := range n.Rows {
		byIndex[r.Index] = r.Runs
	}
	changed := 0
	for _, r := range p.Rows {
		nextRuns, ok := byIndex[r.Index]
		if !ok {
			return false // a row disappeared: a scroll or an erase, not growth
		}
		if runsEqual(r.Runs, nextRuns) {
			continue
		}
		if !runsGrow(r.Runs, nextRuns) {
			return false
		}
		changed++
	}
	inPrev := make(map[int]bool, len(p.Rows))
	for _, r := range p.Rows {
		inPrev[r.Index] = true
	}
	for _, r := range n.Rows {
		if !inPrev[r.Index] {
			changed++ // a row appeared: the first characters on an empty base row
		}
	}
	return changed == 1
}

// runsGrow reports whether next is prev with text appended: every shared run keeps
// its column and pen, the last shared run may be extended, and next may add runs
// beyond it. A nil prev grows into anything, which covers a row's first characters.
func runsGrow(prev, next []cta608.Run) bool {
	if len(next) < len(prev) {
		return false
	}
	for i := range prev {
		p, nr := prev[i], next[i]
		if p.Column != nr.Column || p.Pen != nr.Pen {
			return false
		}
		if i == len(prev)-1 {
			if !strings.HasPrefix(nr.Text, p.Text) {
				return false
			}
			continue
		}
		if p.Text != nr.Text {
			return false
		}
	}
	return true
}
