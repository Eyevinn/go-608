package generate

import (
	"fmt"
	"math"
	"sort"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// This file is the roll-up counterpart of unitcue.go / unitpaint.go. Roll-up shares
// paint-on's animation — it writes to the displayed screen, so the one-pair-per-frame
// drain types the text out two characters at a time — and differs in what happens
// between cues, which is the whole point of the mode:
//
//   - pop-on and paint-on define the entire screen per cue. Every cue starts from
//     nothing (an ENM'd buffer, or an EDM'd display) and what came before is gone.
//   - roll-up defines only the *new* line. A CR scrolls the window up, and the rows
//     above the base row keep the previous cues' lines — held by the decoder, never
//     retransmitted. That history is why live captioning uses roll-up.
//
// So a roll-up unit is self-contained in its *data* (each cue's pairs live inside its
// own slice) but not in its *display*: the rows above the base row are the previous
// unit's work. WithRollUpCarry keeps that continuity; the default clears the window on
// the unit's first frame so the display starts from nothing too. See WithRollUpCarry
// for which to pick.

// RollUpOption configures BuildUnitRollUpCues. It is deliberately a separate type
// from UnitOption: WithFlipAtCueStart is a pop-on concept (there is no flip to move
// in roll-up) and must not be passable here.
type RollUpOption func(*rollUpConfig)

type rollUpConfig struct {
	carry bool
}

// WithRollUpCarry keeps the roll-up window across the unit boundary: the unit's
// first cue scrolls whatever the previous unit left on screen instead of starting
// from an empty window.
//
// This is the trade-off the mode forces, and neither side is free:
//
//   - Default (reset): the unit opens with an EDM on its first frame, so the window
//     is empty and refills as the unit's own cues arrive. The unit is then
//     self-contained in display as well as data — a receiver that joins, seeks, or
//     starts here sees exactly what a receiver that has been running sees, and
//     go-608 can promise that from the unit alone. The cost is visible: a viewer
//     watching continuously sees the window truncate and refill at every unit
//     boundary, so the deepest the window ever gets is the L*N rows a unit writes
//     for itself — L lines per cue over its N = NumCues cues. Two one-second cues of
//     two lines fill a 4-row window exactly; a single-line caption in the same unit
//     reaches only 2 of those 4 rows.
//   - WithRollUpCarry: the window scrolls smoothly across boundaries and the mode
//     behaves as broadcast roll-up does, filling to its full depth. The cost is that
//     the display depends on units played in order: a receiver joining mid-stream
//     sees a partly filled window that completes after ceil(rows/L) cues, and one
//     that seeks sees the pre-seek lines age out over the same span. Both
//     self-correct, and neither is wrong so much as briefly thin.
//
// Reset is the default because it is the only option whose output is a function of
// the unit alone, which is what the per-unit API exists to provide. Choose carry when
// units are served in order to a player that stays joined, and the full window depth
// matters more than a clean segment boundary.
func WithRollUpCarry() RollUpOption {
	return func(c *rollUpConfig) { c.carry = true }
}

// BuildUnitRollUpCues returns the per-frame CTA-608 payload for one unit of video (a
// DASH segment, a MoQ group) at fps, presenting each cue as a roll-up line in a window
// of rows rows (clamped to 2..4). It takes the same arguments as BuildUnitCues and
// BuildUnitPaintCues, plus that window size: the unit splits into
// N = NumCues(unitDurMS, targetPeriodMS) equal cue slices, and content supplies each
// slice's lines.
//
// Each cue is one batch eligible at its slice's first frame: the RU2/3/4 mode entry,
// then per line a CR (which scrolls the window and clears the base row) followed by
// that line's positioned characters. Draining it one pair per frame is the animation —
// the window scrolls, then the new line types itself onto the base row two characters
// at a time and stands there until the next cue scrolls it up.
//
// A cue's lines are written in Row order, bottom line last, so the window ends up laid
// out as the Rows declare and the largest Row is the base row: lines on rows 14 and 15
// leave the first on 14 and the second on 15, the same picture the pop-on and paint-on
// builders produce for the same content. Because every line is its own scroll step, a
// cue of L lines consumes L of the rows rows: rows == L keeps no history at all, and
// holding a whole earlier cue takes rows >= 2*L — see WithRollUp, which describes the
// same arithmetic for the continuous generator. A cue with no lines emits nothing at all,
// leaving the window as it stands (there is no clear to emit — an empty roll-up cue is
// silence, not an erase).
//
// By default the window is cleared on the unit's first frame, which makes the unit
// self-contained in display as well as data; WithRollUpCarry keeps the previous unit's
// lines instead. That choice is the substance of the mode — see WithRollUpCarry.
//
// Frame i of the returned slice is consumed exactly as BuildUnitCues' is — see its
// docs for the carriage call and for the presentation-order rule that decides which
// sample frame i belongs to.
//
// It returns an error if any cue does not finish writing with at least one frame of its
// slice to spare. Roll-up is the most expensive of the three modes per line (the mode
// entry once per cue, plus a CR per line), so this is the mode most likely to need
// shorter lines, a lower update rate, or a higher frame rate.
func BuildUnitRollUpCues(
	fps float64, u Unit, targetPeriodMS int64, rows int, content CueContentFunc,
	opts ...RollUpOption,
) ([]schedule.Frame, error) {
	if u.Frames <= 0 {
		return nil, fmt.Errorf("Unit.Frames must be > 0, got %d", u.Frames)
	}
	if content == nil {
		return nil, fmt.Errorf("content function must not be nil")
	}
	var cfg rollUpConfig
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

	wallAt := func(i int) int64 { return u.StartMS + int64(math.Round(float64(i)*frameDurMS)) }
	// boundary(k) is the first unit-relative frame of cue k; boundary(n)==unitFrames.
	boundary := func(k int) int { return int(math.Round(float64(k) * float64(unitFrames) / float64(n))) }

	for k := 0; k < n; k++ {
		start := boundary(k)
		end := boundary(k + 1)
		cue := content(u, k, wallAt(start))

		toks := rollUpTokens(cue.Lines, rows)
		if k == 0 && !cfg.carry {
			// Reset the window on the unit's first frame, ahead of the first scroll, so
			// the display owes nothing to the previous unit.
			toks = append([]cta608.Token{cta608.Command{Op: cta608.EDM}}, toks...)
		}
		if len(toks) == 0 {
			continue // an empty cue leaves the window untouched
		}

		// The pairs drain on frames start..start+pairs-1, so require the last one to
		// land at least one frame before the next cue's scroll: the finished line is
		// then on the base row for at least one frame before it moves up.
		if pairs := serializedPairs(toks); start+pairs >= end {
			return nil, fmt.Errorf("cue %d roll-up (%d pairs) does not fit its %d-frame slice at %g fps; "+
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

// rollUpTokens compiles one cue's lines into a roll-up transition: the RU2/3/4 mode
// entry, then a CR and the positioned characters for each line, bottom line last.
// Nothing to write yields nil rather than a bare mode entry.
//
// The mode entry rides every cue (one pair) so each cue re-states the window size for
// a receiver that joined since the last one; a decoder already in that mode treats it
// as a no-op on the display, moving only the cursor, which the following CR and PAC
// set anyway.
func rollUpTokens(lines []cta608.Line, rows int) []cta608.Token {
	rows = clampRows(rows)
	ordered := byRow(lines)
	base := baseRow(ordered)
	toks := []cta608.Token{cta608.SetMode{Mode: cta608.RollUp, RollUpRows: rows}}
	for _, ln := range ordered {
		lineToks := rollUpLineTokens(ln, base, rows)
		if len(lineToks) == 0 {
			continue
		}
		toks = append(toks, cta608.Command{Op: cta608.CR})
		toks = append(toks, lineToks...)
	}
	if len(toks) == 1 {
		return nil // only the mode entry: nothing to roll up
	}
	return toks
}

// rollUpLineTokens lowers one line onto the base row of a roll-up window — the PAC
// (plus tab offset and mid-row code that centering and color need) and the characters.
//
// A fresh encoder per line is what makes each line a full write: the CR before it has
// just cleared the base row, so there is no previous content on that row to diff
// against. Its leading mode entry is dropped, because rollUpTokens states the mode once
// per cue rather than once per line.
func rollUpLineTokens(ln cta608.Line, base, rows int) []cta608.Token {
	ln.Row = base
	var enc cta608.Encoder
	toks := enc.Apply(cta608.CaptionBlock{
		Lines: []cta608.Line{ln}, Mode: cta608.RollUp, RollUpRows: rows,
	})
	if len(toks) > 0 {
		if _, ok := toks[0].(cta608.SetMode); ok {
			toks = toks[1:]
		}
	}
	return toks
}

// byRow returns the lines sorted by Row ascending, stably, so a cue is written
// top-to-bottom and its last line lands on the base row. Lines with no explicit Row
// keep their given order, ahead of any positioned line.
func byRow(lines []cta608.Line) []cta608.Line {
	out := make([]cta608.Line, len(lines))
	copy(out, lines)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Row < out[j].Row })
	return out
}

// baseRow returns the roll-up base row for a cue: the largest declared Row, or 15
// (the caption default) when no line names one.
func baseRow(lines []cta608.Line) int {
	base := 0
	for _, ln := range lines {
		if ln.Row > base {
			base = ln.Row
		}
	}
	if base < 1 || base > 15 {
		return 15
	}
	return base
}

// clampRows confines a roll-up window to the 2..4 rows CTA-608 defines, mapping the
// zero value to 2 (the window that fits a two-line caption).
func clampRows(rows int) int {
	if rows < 2 {
		return 2
	}
	if rows > 4 {
		return 4
	}
	return rows
}
