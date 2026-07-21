// Package srt is a thin SRT serializer over the cue model: it implements
// cue.Reader and cue.Writer and is the simpler sibling of the webvtt package.
//
// SRT is a header-less ordered cue list with light inline styling and no
// standard positioning. Color maps to <font color>, italic/underline to
// <i>/<u> (bold and background are dropped); placement is dropped on output
// (bottom-centered) and defaults to a bottom-center anchor on input, with no
// positioning extensions invented. All 608<->cue logic lives in the cue
// package.
//
// The entry points are Read (SRT text -> []cue.TimedCue) and Write (the mirror),
// which the value types Reader{} and Writer{} expose as the cue.Reader/cue.Writer
// seam. Read quantizes each <font> color to the nearest of 608's 8 colors and
// anchors the position-less lines to the bottom-center of the grid; Write emits
// the styled text with no positioning. The round-trip is therefore semantic and
// quantized (palette + bottom-center), not byte-exact — but it is stable across a
// read -> write -> read cycle.
//
// See SPEC.md section 4.6 / 8.2 and docs/design/cea608-webvtt-srt-io.md.
package srt
