package generate

import (
	"fmt"
	"math"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// This file is the paint-on counterpart of unitcue.go: the same per-unit slicing and
// the same Unit / UnitCue / CueContentFunc types, but each cue is written straight
// onto the displayed screen instead of being built in non-displayed memory and
// flipped on.
//
// The difference a viewer sees is that a paint-on caption arrives progressively. A
// pop-on cue is invisible while its ~18 pairs drain and then appears whole; a
// paint-on cue clears the screen on its first frame and then grows two characters
// per frame — the 608 wire rate rendered as a typewriter — standing complete for
// the remainder of its slice. Two characters, not one, because cta608.Serialize
// packs two per byte pair and the scheduler drains one pair per frame; one
// character per frame would mean padding each pair with a null, halving the rate
// for a difference few viewers would notice and a budget most captions would blow.

// BuildUnitPaintCues returns the per-frame CTA-608 payload for one unit of video (a
// DASH segment, a MoQ group) at fps, painting each cue on screen character by
// character. It is the paint-on counterpart of BuildUnitCues and takes the same
// arguments: the unit splits into N = NumCues(unitDurMS, targetPeriodMS) equal cue
// slices, and content supplies each slice's lines.
//
// Each cue is one batch of tokens eligible at its slice's first frame: EDM (clearing
// whatever the previous cue left), RDC (paint-on mode), then the positioned rows.
// The scheduler drains it one byte pair per frame, and a decoder renders each pair as
// it arrives, so within a slice the screen goes blank, fills two characters at a time,
// and then holds until the next slice's clear.
//
// Every cue is self-contained in its own slice, which makes this the simplest unit to
// serve from a stateless per-segment server: no cue's data crosses a unit boundary
// (BuildUnitCues needs WithFlipAtCueStart, and hence the *next* unit, to display a
// caption over exactly the interval it names), each unit opens by clearing the screen
// and re-asserting paint-on mode, and a receiver that joins mid-stream is correct from
// the first cue boundary it sees. The corresponding cost is that a cue's text is only
// complete for the tail of its slice — at 30 fps a two-line caption takes ~0.8 s of a
// ~1 s slice to write out, so paint-on suits a caption whose arrival is the point (a
// visible liveness tell) more than one that must be readable for its whole interval.
//
// Frame i of the returned slice is consumed exactly as BuildUnitCues' is — see its
// docs for the carriage call and for the presentation-order rule that decides which
// sample frame i belongs to.
//
// It returns an error if any cue does not finish painting with at least one frame of
// its slice to spare — shorten the lines, lower the update rate (larger
// targetPeriodMS), or raise fps.
func BuildUnitPaintCues(
	fps float64, u Unit, targetPeriodMS int64, content CueContentFunc,
) ([]schedule.Frame, error) {
	if u.Frames <= 0 {
		return nil, fmt.Errorf("Unit.Frames must be > 0, got %d", u.Frames)
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
	unitFrames := u.Frames
	unitDurMS := int64(math.Round(float64(unitFrames) * frameDurMS))
	n := NumCues(unitDurMS, targetPeriodMS)

	sched := schedule.NewScheduler(fps, schedule.WithDoubling(cta608.DoublingOff))

	wallAt := func(i int) int64 { return u.StartMS + int64(math.Round(float64(i)*frameDurMS)) }
	// boundary(k) is the first unit-relative frame of cue k; boundary(n)==unitFrames.
	boundary := func(k int) int { return int(math.Round(float64(k) * float64(unitFrames) / float64(n))) }

	for k := 0; k < n; k++ {
		start := boundary(k)
		end := boundary(k + 1)
		cue := content(u, k, wallAt(start))

		// The clear leads every cue, including one whose lines are unchanged: a
		// paint-on caption is repainted each slice rather than diffed against the
		// previous one, so what is on screen is always the work of the current cue.
		toks := append([]cta608.Token{cta608.Command{Op: cta608.EDM}}, paintOnTokens(cue.Lines)...)

		// The pairs drain on frames start..start+pairs-1, so require the last one to
		// land at least one frame before the next cue's clear: the fully painted
		// caption is then on screen for at least one frame, rather than being erased
		// the instant it completes.
		if pairs := serializedPairs(toks); start+pairs >= end {
			return nil, fmt.Errorf("cue %d paint (%d pairs) does not fit its %d-frame slice at %g fps; "+
				"shorten lines, lower the update rate, or raise fps", k, pairs, end-start, fps)
		}
		sched.Push(schedule.TimedTokens{TimeMS: wallAt(start), Field: 1, Tokens: toks})
	}

	frames := make([]schedule.Frame, unitFrames)
	for i := 0; i < unitFrames; i++ {
		frames[i] = sched.Frame(wallAt(i))
	}
	return frames, nil
}

// paintOnTokens compiles lines into a paint-on transition written from a clean
// screen: RDC followed by each positioned row. A fresh encoder per call is what
// makes it a full repaint — the caller has just cleared the display, so there is no
// previous screen to diff against, and re-emitting RDC each time keeps every cue
// independently decodable.
//
// Lines with nothing to paint yield nil rather than the encoder's lone mode entry,
// so an empty cue is just the clear.
func paintOnTokens(lines []cta608.Line) []cta608.Token {
	var enc cta608.Encoder
	toks := enc.Apply(cta608.CaptionBlock{Lines: lines, Mode: cta608.PaintOn})
	if len(toks) == 1 {
		if m, ok := toks[0].(cta608.SetMode); ok && m.Mode == cta608.PaintOn {
			return nil
		}
	}
	return toks
}
