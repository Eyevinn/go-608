// Package schedule maps wall-time-tagged CTA-608 token transitions onto
// individual video frames, emitting the primitive {Field1, Field2, CCCount}
// triple that the carriage package consumes.
//
// It owns the cc_count-per-frame-rate policy, the one-pair-per-field-per-
// frame cadence, the 608 rate cap, DTVCC padding to cc_count, and null-pair
// frame alignment. It is format-agnostic and carriage-free (it imports
// neither mp4ff nor the timed-text packages), so it is the shared timing
// layer reused by both the wall-clock generator and the subtitle-compile
// path.
//
// See SPEC.md section 4.3 / 5.3 and docs/design/cea608-wallclock-generation.md.
package schedule
