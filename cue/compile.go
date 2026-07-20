package cue

import (
	"sort"
	"time"

	"github.com/Eyevinn/go-608/cta608"
)

// Compile turns a cue list into wall-time-tagged token transitions that render
// it as pop-on captions (SPEC §8.2, design notes W4/W7/W9).
//
// Every cue compiles to pop-on: built in non-displayed memory and flipped on at
// its Start, replaced or cleared at its End. Roll-up authoring is out of scope,
// so a roll-up -> text -> 608 round-trip lands as pop-on — accepted lossy
// behaviour (W4).
//
// Overlapping cues are merged by position (W7). Timed text allows several cues
// on screen at once, but 608 has exactly one displayed screen, so at each cue
// start/end boundary the target Screen is the union of all currently-active
// cues' Screens, placed by their row positions; a same-row conflict is resolved
// by cue order — the cue that starts later wins that row (ties keep input
// order). That target is handed to the core cta608.Encoder, whose diff engine
// re-flips the pop-on caption whenever the active set changes, so Compile reuses
// the one diff engine rather than adding parallel logic.
//
// Compile stops at timed tokens: mapping them onto specific frames with
// per-frame cc_count and field cadence is the schedule package's job (W9).
// Boundaries where the merged screen does not change emit nothing (the encoder
// returns no tokens for an unchanged target), so the result is minimal. The
// final boundary clears the display (an EDM), so the output is self-terminating
// and never leaves a dangling caption.
func Compile(cues []TimedCue) []TimedTokens {
	if len(cues) == 0 {
		return nil
	}

	// Order cues by Start (stable) so "the later cue" in a same-row conflict is
	// the one that starts later, with equal starts keeping input order. mergeAt
	// then writes rows in this order, letting a later cue overwrite a shared row.
	ordered := make([]TimedCue, len(cues))
	copy(ordered, cues)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })

	var enc cta608.Encoder // zero value: pop-on, empty display
	var out []TimedTokens
	for _, t := range boundaryTimes(ordered) {
		target := mergeAt(ordered, t)
		toks := enc.SetScreen(target)
		if len(toks) == 0 {
			continue // the merged screen did not change at this boundary
		}
		out = append(out, TimedTokens{Time: t, Tokens: toks})
	}
	return out
}

// boundaryTimes returns the sorted, de-duplicated set of every cue Start and End
// — the only instants at which the active set (and therefore the merged screen)
// can change.
func boundaryTimes(cues []TimedCue) []time.Duration {
	seen := make(map[time.Duration]struct{}, 2*len(cues))
	var ts []time.Duration
	add := func(t time.Duration) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			ts = append(ts, t)
		}
	}
	for _, c := range cues {
		add(c.Start)
		add(c.End)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	return ts
}

// mergeAt builds the target Screen active at instant t: the union, by row, of
// every cue whose window contains t (Start <= t < End). cues must be ordered by
// Start so that iterating in slice order lets a later-starting cue overwrite a
// row it shares with an earlier one (the W7 "later cue wins" rule).
func mergeAt(cues []TimedCue, t time.Duration) cta608.Screen {
	byRow := make(map[int]cta608.Row)
	for _, c := range cues {
		if c.Start > t || t >= c.End {
			continue
		}
		for _, r := range c.Content.Rows {
			if len(r.Runs) == 0 {
				continue
			}
			byRow[r.Index] = r
		}
	}
	if len(byRow) == 0 {
		return cta608.Screen{}
	}
	idxs := make([]int, 0, len(byRow))
	for idx := range byRow {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	rows := make([]cta608.Row, 0, len(idxs))
	for _, idx := range idxs {
		rows = append(rows, byRow[idx])
	}
	return cta608.Screen{Rows: rows}
}
