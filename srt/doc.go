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
// See SPEC.md section 4.6 / 8.2 and docs/design/cea608-webvtt-srt-io.md.
package srt
