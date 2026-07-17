// Package cue is the shared timed-text intermediate for go-608 and the one
// place the 608<->cue mapping is written.
//
// A TimedCue is a presentation window whose Content reuses the cta608.Screen
// type, so timed-text formats pivot on positioned, styled rows. Segment cuts
// a stream of displayed-Screen changes into cues (608 -> text); Compile
// merges overlapping cues by position and diffs them into timed token
// transitions (text -> 608, pop-on). The Reader/Writer interface over
// []TimedCue is the published extension seam: WebVTT and SRT ship in-tree and
// TTML or third-party formats plug in without touching this mapping.
//
// See SPEC.md section 4.5 / 8.2 and docs/design/cea608-webvtt-srt-io.md.
package cue
