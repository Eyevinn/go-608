package generate

import (
	"fmt"
	"math"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// This file adds the "per-unit cue" helper — the shared mechanism used by the
// segment-oriented consumers (livesim2 serves one DASH segment per request;
// moqlivemock emits one MoQ group per object; a group corresponds to a segment).
// Unlike Generator, which is a continuous wall-clock clock that builds across
// second boundaries, BuildUnitCues produces a caption for one unit at a time, so a
// stateless per-segment server can generate a segment's captions from the segment
// alone. By default that caption is *self-contained*: every pop-on build and flip
// happens inside the unit, so no cue's data crosses a unit boundary and each unit is
// independently decodable (aside from the first cue's unavoidable build latency).
//
// WithFlipAtCueStart trades that self-containment for display accuracy: each flip
// moves onto its cue's first frame and the build is transmitted over the preceding
// frames, which for a unit's first cue means the previous unit. Use it when the
// caption text refers to its own display interval, since with the default placement
// the caption appears well after the time it shows.
//
// Within a unit the caption updates every targetPeriodMS (≈1 s) or a little slower,
// snapped to an even division of the unit so cues never straddle a unit boundary:
// N = NumCues(unitDurMS, targetPeriodMS) equal slices. By default each cue's pop-on
// build drains from its slice's first frame and flips (EOC) once the build completes,
// so the cue arms over its first ~build frames and then displays for the rest of
// the slice. NB CTA-608 bandwidth: a two-line pop-on build is ~18 byte-pairs,
// i.e. ~0.6 s at 30 fps (~0.3 s at 60 fps), a large share of a ~1 s slice — so a
// cue is armed for much of its slice at low frame rates, and the caption is visible
// only for the remainder. That arming delay is what WithFlipAtCueStart removes, by
// spending the previous slice's frames on the build instead. Either way, a build that
// does not fit the frames available to it is an error (the Overran case).

// Unit identifies one unit of video — a DASH segment, a MoQ group — as three
// independent facts: which unit it is, when it starts, and how long it is in frames.
//
// Nr and StartMS are deliberately separate inputs. A unit's start is *not* assumed to
// be Nr times a fixed duration: segment durations vary (a $Time$-based timeline), a
// timeline can contain gaps, and a numbering epoch need not begin at t=0. Nothing in
// go-608 derives one from the other — Nr is passed through to CueContentFunc untouched
// and never used for timing, while StartMS is the only thing timing is measured from.
type Unit struct {
	Nr      int64 // unit/segment/group number, as the consumer numbers them
	StartMS int64 // wall-clock time of the unit's first frame
	Frames  int   // video frames in the unit
}

// UnitCue is the resolved content of one cue within a unit: the caption lines to
// display for its slice. The caller formats the text (timestamp, segment number,
// …); go-608 owns the pop-on build, the EOC flip, cc_count and (via carriage) the
// SEI carriage. An empty Lines slice clears the caption for that cue's slice (an
// EDM); if nothing is currently displayed it is a no-op.
type UnitCue struct {
	Lines []cta608.Line
}

// CueContentFunc returns the content for cue cueIdx of unit u, whose slice starts at
// wall-clock cueStartMS. It is called once per cue, in order.
//
// Everything the content depends on arrives as an argument, so one CueContentFunc
// serves every unit — including the next unit's first cue under WithFlipAtCueStart,
// which is why u is passed rather than closed over. cueStartMS is u.StartMS advanced
// by the cue's offset within the unit; it is resolved here because the caller has no
// reason to know how the unit was sliced. Keeping the content a pure function of
// (u, cueIdx, cueStartMS) keeps a unit's output independent of any other unit.
type CueContentFunc func(u Unit, cueIdx int, cueStartMS int64) UnitCue

// NumCues returns how many cues of at least targetPeriodMS evenly divide a unit of
// unitDurMS: max(1, unitDurMS/targetPeriodMS) truncated. targetPeriodMS<=0 defaults
// to 1000. E.g. (2002,1000)->2 (1.001 s each), (1001,1000)->1, (1920,1000)->1
// (1.92 s), (1000,1000)->1, (4000,1000)->4.
//
// The division truncates rather than rounds, so a cue is never *shorter* than the
// target period. That asymmetry is deliberate. Content whose resolution is the period
// — a clock labelled in whole seconds, the case go-608 ships — cannot distinguish two
// cues that start inside the same period: they render identically, so the second cue
// has nothing to flip and the caption ends up displayed up to a full period after the
// instant it names. Truncating spends cue rate to avoid that: a 1920 ms segment gets
// one 1.92 s cue that is right when it appears, rather than two 0.96 s cues that are
// most of a second late. Rounding up would also mean a unit whose duration is a whole
// number of periods plus a rounding-up fraction (1500, 2600) silently captions at a
// sub-second rate, which is never what the caller asked for.
//
// A unit shorter than one period still yields one cue — the only case where the trade
// cannot be made, since a cue may not straddle a unit boundary. Its caption then names
// the second its slice starts in, so consecutive short units can repeat a label (only
// the first of them flips) and the clock ticks up to one unit late. Broadcast-rate
// units (a 30-frame group at 60000/1001 is 501 ms) stay within half a second of true.
func NumCues(unitDurMS, targetPeriodMS int64) int {
	if targetPeriodMS <= 0 {
		targetPeriodMS = 1000
	}
	n := int(unitDurMS / targetPeriodMS)
	if n < 1 {
		n = 1
	}
	return n
}

// UnitOption configures BuildUnitCues.
type UnitOption func(*unitConfig)

type unitConfig struct {
	flipAtCueStart bool
	next           Unit
	nextContent    CueContentFunc
}

// WithFlipAtCueStart moves each cue's EOC onto the first frame of its own slice,
// transmitting the pop-on build over the frames *before* the flip instead of after
// the slice starts. The caption is then displayed over exactly the interval its
// content was generated for.
//
// Without this option a cue's build drains from its slice's first frame and the flip
// follows it, so the caption appears ~build-pairs frames into the slice it names —
// for a two-line build that is 0.6–0.75 s of a ~1 s slice. Use this option when the
// caption's content refers to its own display interval (a clock, a segment or group
// number); leave it off when self-contained units matter more (see below).
//
// The cost is that captions no longer fit inside a unit. A cue's build lives in the
// frames preceding its flip, which for the first cue of a unit means the *previous*
// unit. next therefore names the following unit so this unit's tail can carry its
// build: content is called once, as content(next, 0, next.StartMS). Only next.Nr and
// next.StartMS are read — next.Frames is not, since only that unit's first cue is
// needed here.
//
// next.StartMS is taken as given rather than computed as this unit's end. Units are
// not assumed to be contiguous or equal in duration, so a gap or a length change is
// expressed simply by saying where the next unit actually starts. Pass a nil content
// to leave the tail empty, which makes the next unit's first caption blank.
//
// next must be the same Unit the next BuildUnitCues call receives, and with the same
// content function: the build placed in this unit's tail has to match the flip that
// call emits, and both are derived from those two inputs.
//
// Consequences worth knowing:
//   - A receiver that starts, seeks, or joins mid-stream gets a leading EOC without the
//     build that belongs to it, and what it shows for that first cue period depends on
//     its decoder state: a fresh decoder has empty non-displayed memory and shows
//     nothing, while a decoder that keeps 608 state across the discontinuity flips
//     whatever was last preloaded and can show a stale caption. Either way it is correct
//     from the next cue on. Recovering faster belongs to the receiver — a player that
//     resets its 608 state at a discontinuity turns the stale case into the blank one —
//     and a sender cannot help: a sanitising ENM ahead of the EOC would erase the very
//     build about to be flipped. This is also what a stream's very first unit looks
//     like: a generator serving units on demand cannot tell it apart from a join.
//   - A unit's first cue is always encoded from a clean encoder state, so the build
//     placed in the previous unit's tail matches the flip the next unit emits even
//     though the two are produced by separate calls.
func WithFlipAtCueStart(next Unit, content CueContentFunc) UnitOption {
	return func(c *unitConfig) {
		c.flipAtCueStart = true
		c.next = next
		c.nextContent = content
	}
}

// BuildUnitCues returns the per-frame CTA-608 payload for one unit of video (a DASH
// segment, a MoQ group) at fps. It splits the unit into
// N = NumCues(unitDurMS, targetPeriodMS) equal cue slices, calls content for each
// slice's lines, and schedules a pop-on build + EOC flip for each slice.
//
// u carries the unit's number, start time and frame count as independent facts; see
// Unit. Timing comes from u.StartMS alone, and u.Nr is only handed to content.
// Frame i of the returned slice is consumed by
// carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec) for AVC/HEVC, or
// carriage.FrameMetadataOBU(f.Field1, f.Field2, f.CCCount) for AV1.
//
// Frame i belongs to the sample with the i-th smallest presentation time — the i-th
// *displayed* frame of the unit, not the i-th sample in decode order. The two differ
// whenever the video reorders in the container (B-frames in AVC/HEVC; AV1 reorders
// inside the bitstream instead, so for it they always coincide). A caller that walks
// its samples in decode order will scramble the captions, and will not notice by
// reading them back with the same mistake.
//
// By default each cue is self-contained within its slice: the build drains from the
// slice's first frame and the flip follows it. Pass WithFlipAtCueStart to align each
// flip with its slice boundary instead, which trades self-containment for a caption
// that is displayed over exactly the interval it names.
//
// It returns an error if any cue's build does not fit the frames available to it —
// shorten the lines, lower the update rate (larger targetPeriodMS), or raise fps.
// fps must be a broadcast caption rate (23.976–60); schedule.NewScheduler panics
// otherwise.
func BuildUnitCues(
	fps float64, u Unit, targetPeriodMS int64, content CueContentFunc,
	opts ...UnitOption,
) ([]schedule.Frame, error) {
	if u.Frames <= 0 {
		return nil, fmt.Errorf("Unit.Frames must be > 0, got %d", u.Frames)
	}
	if content == nil {
		return nil, fmt.Errorf("content function must not be nil")
	}
	var cfg unitConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	// Validate the frame rate up front and return an error rather than letting
	// schedule.NewScheduler panic: cc_count = round(600/fps) must land in 2..31.
	if cc := int(math.Round(600.0 / fps)); cc < 2 || cc > 31 {
		return nil, fmt.Errorf("fps %.3f yields cc_count %d outside 2..31 (use a 23.976..60 caption rate)", fps, cc)
	}
	frameDurMS := 1000.0 / fps
	unitFrames := u.Frames
	unitDurMS := int64(math.Round(float64(unitFrames) * frameDurMS))
	n := NumCues(unitDurMS, targetPeriodMS)

	sched := schedule.NewScheduler(fps, schedule.WithDoubling(cta608.DoublingOff))
	var enc cta608.Encoder

	wallAt := func(i int) int64 { return u.StartMS + int64(math.Round(float64(i)*frameDurMS)) }
	// boundary(k) is the first unit-relative frame of cue k; boundary(n)==unitFrames.
	boundary := func(k int) int { return int(math.Round(float64(k) * float64(unitFrames) / float64(n))) }

	// prevFlip is the last frame already claimed by a flip, so a following build knows
	// where it may start. Only used when flips are aligned with slice boundaries.
	prevFlip := -1
	for k := 0; k < n; k++ {
		start := boundary(k)
		end := boundary(k + 1)
		cue := content(u, k, wallAt(start))
		toks := enc.Apply(cta608.CaptionBlock{Lines: cue.Lines, Mode: cta608.PopOn})
		if len(toks) == 0 {
			continue // no change from the previous cue: nothing to flip
		}
		buildToks, eocToks := splitEOC(toks)
		pairs := serializedPairs(buildToks)
		eocPairs := serializedPairs(eocToks)

		if cfg.flipAtCueStart {
			// The flip rides the slice's first frame, so the build has to arrive over
			// the frames before it: the tail of the previous slice, or — for the first
			// cue — the previous unit, which is expected to have carried it.
			//
			// A clear (an EDM with no EOC) is itself the visible change, so it stays on
			// the boundary frame rather than being pushed ahead of it.
			if len(eocToks) == 0 {
				sched.Push(schedule.TimedTokens{TimeMS: wallAt(start), Field: 1, Tokens: buildToks})
				prevFlip = start
				continue
			}
			if k > 0 {
				buildStart := start - pairs
				if buildStart <= prevFlip {
					return nil, fmt.Errorf("cue %d build (%d pairs) does not fit the %d frames before its flip "+
						"at %g fps; shorten lines, lower the update rate, or raise fps",
						k, pairs, start-prevFlip-1, fps)
				}
				sched.Push(schedule.TimedTokens{TimeMS: wallAt(buildStart), Field: 1, Tokens: buildToks})
			}
			sched.Push(schedule.TimedTokens{TimeMS: wallAt(start), Field: 1, Tokens: eocToks})
			prevFlip = start
			continue
		}

		// Build + EOC are both eligible at the slice's first frame and drain one
		// pair/frame, so the flip lands at start+pairs. Require it to leave at
		// least one frame of display before the next slice. (Count the EOC in byte
		// pairs, not tokens, so the check stays correct if doubling is ever on.)
		if start+pairs+eocPairs >= end {
			return nil, fmt.Errorf("cue %d build (%d pairs) does not fit its %d-frame slice at %g fps; "+
				"shorten lines, lower the update rate, or raise fps", k, pairs, end-start, fps)
		}
		sched.Push(schedule.TimedTokens{TimeMS: wallAt(start), Field: 1, Tokens: buildToks})
		if len(eocToks) > 0 {
			sched.Push(schedule.TimedTokens{TimeMS: wallAt(start), Field: 1, Tokens: eocToks})
		}
	}

	// Carry the build for the next unit's first cue in this unit's tail, so that unit
	// can flip it on its own first frame. A fresh encoder mirrors the clean state the
	// next call will start from, so the bytes match the flip it will emit.
	//
	// The next unit is described by cfg.next, not computed from this one: its start is
	// whatever the caller says it is, so a gap between units or a change of duration
	// needs no special case here.
	if cfg.flipAtCueStart && cfg.nextContent != nil {
		var nextEnc cta608.Encoder
		nextCue := cfg.nextContent(cfg.next, 0, cfg.next.StartMS)
		toks := nextEnc.Apply(cta608.CaptionBlock{Lines: nextCue.Lines, Mode: cta608.PopOn})
		if buildToks, eocToks := splitEOC(toks); len(eocToks) > 0 {
			pairs := serializedPairs(buildToks)
			buildStart := unitFrames - pairs
			if buildStart <= prevFlip {
				return nil, fmt.Errorf("next unit's first cue build (%d pairs) does not fit the %d frames left "+
					"in this unit at %g fps; shorten lines, lower the update rate, or raise fps",
					pairs, unitFrames-prevFlip-1, fps)
			}
			sched.Push(schedule.TimedTokens{TimeMS: wallAt(buildStart), Field: 1, Tokens: buildToks})
		}
	}

	frames := make([]schedule.Frame, unitFrames)
	for i := 0; i < unitFrames; i++ {
		frames[i] = sched.Frame(wallAt(i))
	}
	return frames, nil
}

// serializedPairs counts the field-1 byte pairs a token sequence serializes to
// (doubling off), i.e. how many frames it takes to drain one pair per frame.
func serializedPairs(toks []cta608.Token) int {
	data := cta608.Serialize(toks, cta608.SerializeOptions{
		Field: 1, Channel: 1, Doubling: cta608.DoublingOff,
	})
	return len(data) / 2
}
