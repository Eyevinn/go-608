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
// second boundaries, BuildUnitCues produces a *self-contained* caption for one
// unit: every pop-on build and flip happens inside the unit, so no cue's data
// crosses a unit boundary and each unit is independently decodable (aside from
// the first cue's unavoidable build latency). This lets a stateless per-segment
// server generate a segment's captions from the segment alone.
//
// Within a unit the caption updates roughly every targetPeriodMS (≈1 s), snapped
// to an even division of the unit so cues never straddle a unit boundary:
// N = NumCues(unitDurMS, targetPeriodMS) equal slices. Each cue's pop-on build
// drains from its slice's first frame and flips (EOC) once the build completes,
// so the cue arms over its first ~build frames and then displays for the rest of
// the slice. NB CTA-608 bandwidth: a two-line pop-on build is ~18 byte-pairs,
// i.e. ~0.6 s at 30 fps (~0.3 s at 60 fps), a large share of a ~1 s slice — so a
// cue is armed for much of its slice at low frame rates. If a build does not fit
// its slice, BuildUnitCues returns an error (the Overran case).

// UnitCue is the resolved content of one cue within a unit: the caption lines to
// display for its slice. The caller formats the text (timestamp, segment number,
// …); go-608 owns the pop-on build, the EOC flip, cc_count and (via carriage) the
// SEI carriage. An empty Lines slice clears the caption for that cue's slice (an
// EDM); if nothing is currently displayed it is a no-op.
type UnitCue struct {
	Lines []cta608.Line
}

// CueContentFunc returns the content for cue cueIdx of a unit, whose slice starts
// at wall-clock cueStartMS. It is called once per cue, in order. Consumers close
// over per-unit facts (segment number, timescale, …) and format the lines here;
// keeping the content a pure function of (cueIdx, cueStartMS) keeps a unit's
// output independent of any other unit.
type CueContentFunc func(cueIdx int, cueStartMS int64) UnitCue

// NumCues returns how many ≈targetPeriodMS cues evenly divide a unit of
// unitDurMS: max(1, round(unitDurMS/targetPeriodMS)). targetPeriodMS<=0 defaults
// to 1000. E.g. (1920,1000)->2 (0.96 s each), (2002,1000)->2 (1.001 s each),
// (1000,1000)->1, (4000,1000)->4.
func NumCues(unitDurMS, targetPeriodMS int64) int {
	if targetPeriodMS <= 0 {
		targetPeriodMS = 1000
	}
	n := int(math.Round(float64(unitDurMS) / float64(targetPeriodMS)))
	if n < 1 {
		n = 1
	}
	return n
}

// BuildUnitCues returns the per-frame CTA-608 payload for one unit of unitFrames
// video frames at fps, the unit starting at wall-clock unitStartMS. It splits the
// unit into N = NumCues(unitDurMS, targetPeriodMS) equal cue slices, calls content
// for each slice's lines, and schedules a self-contained pop-on build + EOC flip
// within each slice. Frame i of the returned slice is consumed by
// carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec).
//
// It returns an error if any cue's build does not fit its slice — shorten the
// lines, lower the update rate (larger targetPeriodMS), or raise fps. fps must be
// a broadcast caption rate (23.976–60); schedule.NewScheduler panics otherwise.
func BuildUnitCues(
	fps float64, unitFrames int, unitStartMS, targetPeriodMS int64, content CueContentFunc,
) ([]schedule.Frame, error) {
	if unitFrames <= 0 {
		return nil, fmt.Errorf("unitFrames must be > 0, got %d", unitFrames)
	}
	if content == nil {
		return nil, fmt.Errorf("content function must not be nil")
	}
	// Validate the frame rate up front and return an error rather than letting
	// schedule.NewScheduler panic: cc_count = round(600/fps) must land in 2..31.
	if cc := int(math.Round(600.0 / fps)); cc < 2 || cc > 31 {
		return nil, fmt.Errorf("fps %.3f yields cc_count %d outside 2..31 (use a 23.976..60 caption rate)", fps, cc)
	}
	frameDurMS := 1000.0 / fps
	unitDurMS := int64(math.Round(float64(unitFrames) * frameDurMS))
	n := NumCues(unitDurMS, targetPeriodMS)

	sched := schedule.NewScheduler(fps, schedule.WithDoubling(cta608.DoublingOff))
	var enc cta608.Encoder

	wallAt := func(i int) int64 { return unitStartMS + int64(math.Round(float64(i)*frameDurMS)) }
	// boundary(k) is the first unit-relative frame of cue k; boundary(n)==unitFrames.
	boundary := func(k int) int { return int(math.Round(float64(k) * float64(unitFrames) / float64(n))) }

	for k := 0; k < n; k++ {
		start := boundary(k)
		end := boundary(k + 1)
		cue := content(k, wallAt(start))
		toks := enc.Apply(cta608.CaptionBlock{Lines: cue.Lines, Mode: cta608.PopOn})
		if len(toks) == 0 {
			continue // no change from the previous cue: nothing to flip
		}
		buildToks, eocToks := splitEOC(toks)
		pairs := serializedPairs(buildToks)
		eocPairs := serializedPairs(eocToks)
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
