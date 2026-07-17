// Package go608 is the documentation landing page for the go-608 module — a
// Go library for CTA-608 / CEA-608 captions: encode + decode, cc_data + SEI
// carriage per ATSC A/53, wall-clock caption generation, and timed-text
// (SCC / WebVTT / SRT) I/O.
//
// The module has no importable root package (the last path element "go-608"
// is not a valid Go identifier). This file exists only to provide a godoc
// landing page. Import the cooperating packages directly:
//
//   - cta608   the pure core: Token stream, Screen, Serialize/Parse, Decoder/Encoder
//   - carriage cc_data / T.35 / SEI / NAL carriage (wraps github.com/Eyevinn/mp4ff)
//   - schedule timed tokens to per-frame {field1, field2, ccCount}
//   - generate wall-clock caption generation (the first milestone)
//   - scc      Scenarist SCC read/write (byte-exact, true SMPTE drop-frame)
//   - cue      shared timed-cue intermediate and the 608<->cue mapping
//   - webvtt   WebVTT serializer over the cue model
//   - srt      SRT serializer over the cue model
//
// See SPEC.md at the repository root for the full design specification and
// docs/design/ for the per-decision rationale.
package go608
