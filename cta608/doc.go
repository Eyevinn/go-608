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
// meaningful) because a background is a sentinel Color, never a pointer. This
// package defines those types; the stateful Decoder and Encoder that interpret
// tokens into a Screen and diff a Screen back into tokens, and the CaptionBlock
// authoring helper, arrive in later tickets.
//
// The package is a dependency-free leaf: the character (Tables 49, 50, 5–10),
// PAC (Table 53), mid-row (Table 51) and parity tables are unexported, and
// timing lives entirely outside the core.
//
// See SPEC.md sections 4.1 and 5.4/5.5, and docs/design/cea608-core-model.md.
package cta608
