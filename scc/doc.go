// Package scc reads and writes Scenarist SCC files — a byte-pair container
// for CTA-608 caption data, a sibling of the SEI carriage.
//
// It owns the SCC text-file structure and timecodes only; the cta608 core
// owns all 608 semantics, so round-trips are byte-exact. Canonical time is
// an absolute integer frame number, and timecode conversion implements true
// SMPTE drop-frame for the fractional NTSC rates (the correctness prior-art
// tools lack). The reader infers frame rate from the timecodes with an
// override and a fallback; the writer is deliberately dumb (one entry per
// line, verbatim), leaving grouping policy to the caller.
//
// See SPEC.md section 4.7 / 8.1 and docs/design/cea608-scc-io.md.
package scc
