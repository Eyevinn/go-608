// Package generate produces wall-clock CTA-608 captions — the first
// milestone consumed by livesim2 and moqlivemock.
//
// A Generator is driven one call per video frame with the frame's wall-clock
// time (NextFrame(frameWallMS)), which makes it robust to gaps, seeks and
// variable frame rate and makes drop-frame a non-issue. It renders two
// centered lines by default (UTC RFC3339 and media time), builds each second
// ahead into non-displayed memory, and flips it on with a single EOC on the
// last frame of the second for a frame-accurate, zero-lag clock. It builds
// content through the cta608 core and drives a schedule.Scheduler; the caller
// wraps the returned triple with the carriage package.
//
// BuildUnitCues is the segment-oriented counterpart — one call per DASH segment
// or MoQ group rather than one per frame — for a stateless server generating a
// unit's captions from the unit alone.
//
// Both have a paint-on variant (WithPaintOn and BuildUnitPaintCues) that writes
// the caption onto the displayed screen instead of flipping it on: the second (or
// cue) opens with a clear and the text then appears two characters per frame, at
// the 608 wire rate, so the caption visibly types itself out and stands complete
// until the next clear.
//
// See SPEC.md section 4.4 / 7 and docs/design/cea608-wallclock-generation.md.
package generate
