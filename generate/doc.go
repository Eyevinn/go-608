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
// Both have variants that write onto the displayed screen instead of flipping a
// caption on, so the text appears two characters per frame — at the 608 wire rate
// — and visibly types itself out:
//
//   - paint-on (WithPaintOn, BuildUnitPaintCues): the second or cue opens with a
//     clear and the caption stands complete until the next clear.
//   - roll-up (WithRollUp, BuildUnitRollUpCues): the window scrolls up instead of
//     clearing and each line is typed onto the bottom row, so previous seconds
//     stay visible above — what live captioning looks like.
//
// See SPEC.md section 4.4 / 7 and docs/design/cea608-wallclock-generation.md.
package generate
