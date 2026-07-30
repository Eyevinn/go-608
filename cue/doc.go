// Package cue is the shared timed-text intermediate for go-608 and the one
// place the 608<->cue mapping is written (SPEC §4.5 / §8.2, design note
// docs/design/cea608-webvtt-srt-io.md).
//
// A TimedCue is a presentation window whose Content reuses the cta608.Screen
// type, so every timed-text format pivots on the same positioned, styled rows
// the core encoder diffs and decoder materializes. The mapping is lossy and
// Screen-mediated — a sibling of the byte-exact SCC and SEI containers — because
// a format's richer grid and palette are quantized to 608's 15x32 grid and 8
// colors at the serializer edge, not carried through the middle.
//
// # Two directions
//
// Segment implements 608->text: it cuts a timeline of displayed-Screen states
// (TimedScreen) into cues with one unified rule for all caption modes — every
// displayed-Screen change closes the current cue and opens a new one, an empty
// screen is a gap, and a caption still shown at end-of-stream takes a
// configurable end (SegmentOptions.StreamEnd, else Start + DefaultDur). Pop-on
// yields one cue per caption, roll-up one cue per scroll step (visible lines
// repeat), paint-on one cue per write burst.
//
// Pop-on gets that for free: it builds into non-displayed memory, so its display
// changes once, at the EOC. Roll-up and paint-on write straight to the displayed
// screen, so every byte pair changes it and the boundary has to be chosen —
// SegmentOptions.Coalesce does that, defaulting to cutting only at structural
// events (a scroll, an erase, a jump to another row) rather than on every two
// characters. CoalesceNone keeps the per-change rendering.
//
// Compile implements text->608: pop-on only. Overlapping cues are merged by
// position at each boundary (the union of active cues' Screens; a later cue wins
// a same-row conflict) and handed to the core cta608.Encoder, whose diff engine
// re-flips the pop-on caption whenever the active set changes. Compile stops at
// wall-time-tagged token transitions (TimedTokens); mapping them onto frames is
// the schedule package's job.
//
// # The published seam
//
// The Reader/Writer interface over []TimedCue is the published extension seam:
// WebVTT and SRT ship in-tree implementing it, and TTML or third-party formats
// plug in with zero change to the mapping in this package.
package cue
