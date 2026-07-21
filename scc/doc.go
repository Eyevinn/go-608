// Package scc reads and writes Scenarist SCC files — a byte-pair container for
// CTA-608 caption data, a sibling of the SEI carriage.
//
// It owns the SCC text-file structure and timecodes only; the cta608 core owns
// all 608 semantics, so round-trips are byte-exact. Canonical time is an absolute
// integer frame number counted from 00:00:00:00, and timecode conversion
// implements true SMPTE drop-frame for the fractional NTSC rates (29.97/59.94) —
// the correctness the media-tools/SVTA prior art lacks. The reader infers the
// frame rate from the timecodes with an override (WithFPS) and a 29.97 fallback;
// the writer is deliberately dumb (one entry per line, verbatim), leaving
// pairs-per-line grouping policy to the caller.
//
// The model is a SCCFile of Entries, each an absolute Frame plus its verbatim raw
// byte pairs. Read/Write are the container I/O; FrameToTimecode/TimecodeToFrame
// are the drop-frame-aware conversions; TimedPairs flattens entries into per-frame
// pairs for the core (pair i of an entry lands at Frame+i), and GroupPairs is the
// inverse helper that coalesces a flat scheduled stream back into sparse entries.
//
// Typical read path:
//
//	SCC file --Read--> SCCFile --TimedPairs--> []TimedPair --cta608.Parse--> []Token
//
// Typical write path:
//
//	[]Token --cta608.Serialize--> pairs --(schedule)--> GroupPairs --> Entries --Write--> SCC file
//
// The package imports only cta608 and the standard library. See SPEC.md §4.7 /
// §8.1 and docs/design/cea608-scc-io.md for the full rationale.
package scc
