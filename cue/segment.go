package cue

import (
	"sort"
	"time"
)

// SegmentOptions tunes how Segment resolves a caption that is still displayed
// when the input timeline ends — the one cue with no natural End (design note
// W3). Every other cue is closed by the next displayed-screen change, so these
// options affect only the final, dangling cue.
type SegmentOptions struct {
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
//   - Roll-up -> one cue per scroll step: each CR scrolls the window, so the
//     visible lines repeat across successive cues as they move up. Faithful, if
//     verbose (a "coalesce into one cue per utterance" mode was declined in W3
//     in favour of this simpler, correct rule).
//   - Paint-on -> a cue per in-place change.
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

	for _, ch := range ordered {
		screen := normalizeScreen(ch.Screen)
		if open != nil && screenEqual(open.Content, screen) {
			// The displayed screen did not actually change at this instant, so it
			// is not a segmentation boundary. Coalesce.
			continue
		}
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
