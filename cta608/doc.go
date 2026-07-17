// Package cta608 is the pure core of go-608: the CTA-608 / CEA-608 caption
// model and its wire boundary.
//
// The spine is a wire-faithful Token stream. Serialize turns tokens into
// odd-parity cc_data byte pairs and Parse turns byte pairs back into tokens,
// so a []Token round-trips bytes exactly. A sparse, derived Screen (Rows of
// styled character Runs, not a 15x32 grid) is the rendered display state: a
// Decoder builds it up by interpreting tokens, and an Encoder diffs against
// it to produce tokens. CaptionBlock is a friendly authoring type that
// compiles to a target Screen.
//
// The package is a dependency-free leaf: the character, PAC, parity and
// roll-up tables are unexported. Wire concerns (parity, control-code
// doubling, two-per-pair packing, null-pair frame alignment) live in the
// Serialize/Parse boundary, and timing lives entirely outside the core.
//
// See SPEC.md section 4.1 and docs/design/cea608-core-model.md.
package cta608
