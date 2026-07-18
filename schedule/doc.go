// Package schedule maps wall-time-tagged CTA-608 token transitions onto
// individual video frames, emitting the primitive {Field1, Field2, CCCount}
// triple that the carriage package consumes.
//
// It owns the cc_count-per-frame-rate policy, the one-pair-per-field-per-frame
// cadence, and the 608 rate cap; it keeps two-byte control codes frame-aligned
// and reports the cc_count that carriage then pads to (the DTVCC padding and the
// null-pair insertion themselves live in carriage and cta608.Serialize). It is
// format-agnostic and carriage-free — it imports neither mp4ff nor the timed-text
// packages, only cta608 — so it is the shared timing layer reused by both the
// wall-clock generator and the subtitle-compile path.
//
// # Model
//
// A Scheduler holds a FIFO byte-pair queue per NTSC field. Push serializes a
// batch of token transitions with cta608.Serialize (which owns odd parity,
// control-code doubling, two-per-pair packing, and frame alignment) and appends
// the resulting 2-byte pairs to the target field's queue, tagged with a
// wall-clock eligibility time. Frame(frameWallMS) drains at most one eligible
// pair per field — a single 608 pair total above 30 fps, per the rate cap — and
// reports the frame's cc_count; carriage places the 608 constructs first and
// pads the rest.
//
// Because Serialize emits whole 2-byte pairs and Frame drains whole pairs, a
// two-byte control code never straddles a frame boundary; any intra-stream null
// padding needed for alignment is already inserted by Serialize. An idle field
// yields a 0-byte pair (the "no 608 waveform this field this frame" encoding),
// kept distinct from the 2-byte 608 null pair (0x80 0x80) and from carriage's
// DTVCC padding.
//
// # cc_count
//
// cc_count = round(600/fps) (CTA-708-E §4.3.6): 23.976/24→25, 25→24, 29.97/30→20,
// 50→12, 59.94/60→10. CCCountFull (the recommended default) emits that full
// per-rate count and lets carriage pad the surplus with DTVCC padding;
// CCCountMinimal emits just the two 608 field constructs.
//
// # Layering note
//
// SPEC §4.3 sketches Push(TimedTokens) using cue.TimedTokens (§4.5). Importing
// cue would break the layering rule (§3), so schedule defines its own
// TimedTokens with a wall-clock millisecond timestamp and a field selector. See
// the TimedTokens doc comment.
//
// See SPEC.md §4.3 / §5.3 and docs/design/cea608-wallclock-generation.md.
package schedule
