// Package cta608 is the pure core of go-608: the CTA-608 / CEA-608 caption
// model and its wire boundary.
//
// The spine is a wire-faithful Token stream — Chars, PAC, MidRow, TabOffset,
// BackgroundAttr, SetMode and Command, closed under the Token interface, one
// token per on-the-wire command. Serialize turns a []Token into odd-parity
// cc_data byte pairs and Parse turns byte pairs back into tokens, so a []Token
// round-trips bytes exactly. That one boundary owns every byte concern: odd
// parity, control-code doubling (on for field 1, off for field 2 by default),
// two-characters-per-pair packing, null-pair frame alignment of two-byte
// control codes, and the extended-character backspace-and-replace. ParseOptions
// chooses whether to validate or strip parity; SerializeOptions selects the
// field, channel, and doubling policy.
//
// DemuxField and MuxField are the thin field layer above the per-channel core:
// they split and join the two in-field data channels by the control byte's
// high nibble.
//
// Screen, Row, Run, Pen and Color are the sparse, derived display value types
// (styled character runs, not a 15x32 grid). Pen is a comparable value (== is
// meaningful) because a background is a sentinel Color, never a pointer.
//
// Encoder is the single per-channel diff engine: it holds the currently
// displayed Screen and, for a target Screen, emits the []Token that transforms
// current into target. SetScreen diffs a caller-built Screen; Apply first
// compiles a CaptionBlock. All mode-specific token generation lives here —
// pop-on builds into non-displayed memory and flips with EOC, roll-up appends to
// the bottom row and scrolls with CR, paint-on writes changed rows directly —
// and the diff bottoms out at the character-run within a row, so appending to a
// roll-up line emits only the new characters. CaptionBlock is friendly authoring
// on top of Screen: Lines placed by an Anchor with per-line Align, compiled by
// Screen() to a target Screen whose Runs carry absolute columns. The Encoder
// lowers those columns to PAC indent + Tab Offset, compensating one column for
// the mid-row cell of a colored line (SPEC §7). Backgrounds are emitted
// best-effort as a BackgroundAttr at a run's start.
//
// Decoder is the inverse: the stateful, per-channel interpreter that turns a
// token or byte stream into the displayed Screen. Feed parses cc_data bytes and
// interprets them; Push interprets a token stream; Screen returns the displayed
// rows; Changed reports whether the displayed Screen changed since the previous
// call — the signal timed-text cue segmentation pivots on. It models 608's double
// buffer with an internal displayed and non-displayed grid (pop-on writes to
// non-displayed and EOC promotes it), scrolls the roll-up window on CR, and
// writes paint-on rows straight to the display. XDS is dropped by Parse and text
// mode (TR/RTD) is recognized but not rendered (SPEC §1.3).
//
// The package is a dependency-free leaf: the character (Tables 49, 50, 5–10),
// PAC (Table 53), mid-row (Table 51) and parity tables are unexported, and
// timing lives entirely outside the core.
//
// See SPEC.md sections 4.1 and 5.4/5.5, and docs/design/cea608-core-model.md.
package cta608
