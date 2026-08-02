package generate

import (
	"fmt"
	"math"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// LineSpec configures one generated caption line: the screen Row (1..15), a
// Color name ("" or "white", "yellow", "green", "blue", "cyan", "red",
// "magenta"), and a Kind that selects the content ("utc" or "media").
type LineSpec struct {
	Row   int
	Color string
	Kind  string
}

// Config is the generator content configuration: the caption lines to render,
// each centered. An empty Config uses DefaultConfig.
type Config struct {
	Lines []LineSpec
}

// DefaultConfig is two centered lines: row 14 UTC RFC3339 (white) and row 15
// media time (yellow) — the wall-clock caption of SPEC §7.
func DefaultConfig() Config {
	return Config{Lines: []LineSpec{
		{Row: 14, Color: "white", Kind: "utc"},
		{Row: 15, Color: "yellow", Kind: "media"},
	}}
}

// Generator produces a wall-clock CTA-608 caption, driven one call per video
// frame via NextFrame(frameWallMS). It builds each upcoming second's caption
// through the cta608 core (CaptionBlock + Encoder) and drives a
// schedule.Scheduler, which drains at most one field-1 pair per frame and pads to
// cc_count; the caller wraps the returned schedule.Frame with the carriage
// package. Pop-on captions are built into non-displayed memory during a second
// and flipped on with a single EOC on that second's last frame — frame-accurate
// and zero-lag, because the EOC pair is scheduled eligible at the flip time.
//
// WithPaintOn swaps that for progressive paint-on: each second opens with a clear
// and then writes its caption straight onto the displayed screen, so the text
// visibly types itself out at the 608 wire rate.
//
// A Generator is not safe for concurrent use; drive it from one goroutine.
type Generator struct {
	fps         float64
	frameDurMS  float64
	buildBudget int // field-1 pairs that fit before the flip frame: round(fps)-1
	cfg         Config
	paintOn     bool // paint the caption progressively instead of popping it on

	sched *schedule.Scheduler
	enc   cta608.Encoder

	frame       int64
	builtForSec int64
	overran     bool
}

// GeneratorOption configures a Generator at construction.
type GeneratorOption func(*Generator)

// WithPaintOn switches the generator from pop-on to paint-on, where each second's
// caption is written directly onto the displayed screen instead of being built in
// non-displayed memory and flipped on.
//
// Because paint-on has no hidden build phase, the wire cadence becomes the
// animation: the second opens with an EDM that clears the screen on its first
// frame, and each following frame carries one byte pair — up to two characters —
// that a decoder renders immediately. The caption therefore types itself out over
// the first ~half of the second (a ~23-pair two-line build is 0.77 s at 30 fps,
// 0.38 s at 60 fps) and then stands complete until the next second's clear.
//
// Each second is painted from a clean screen and re-asserts paint-on mode (RDC),
// so a decoder joining mid-stream is correct from the next second boundary, with
// no reliance on earlier state. The cost relative to pop-on is that a viewer sees
// the caption incomplete for part of every second; the gain is that nothing is
// hidden — every pair on the wire is visible progress.
//
// The one-second budget is unchanged (Overran reports a caption that does not fit
// it), and it is tighter here: the clear costs a pair and nothing may spill past
// the second, since the next second's clear is what ends this one.
func WithPaintOn() GeneratorOption {
	return func(g *Generator) { g.paintOn = true }
}

// NewGenerator returns a Generator for the given frame rate and content config.
// An empty Config uses DefaultConfig. fps must be a broadcast caption rate
// (23.976..60); NewGenerator panics otherwise (via schedule.NewScheduler), whose
// cc_count would fall outside carriage's valid range.
func NewGenerator(fps float64, cfg Config, opts ...GeneratorOption) *Generator {
	if len(cfg.Lines) == 0 {
		cfg = DefaultConfig()
	}
	g := &Generator{
		fps:         fps,
		frameDurMS:  1000.0 / fps,
		buildBudget: int(math.Round(fps)) - 1,
		cfg:         cfg,
		// CC1 only, control-code doubling off so the ~two-line build fits the
		// one-pair-per-frame, one-second refresh budget (SPEC §7).
		sched:       schedule.NewScheduler(fps, schedule.WithDoubling(cta608.DoublingOff)),
		builtForSec: -1,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// NextFrame advances the generator by one video frame presented at wall-clock
// time frameWallMS and returns that frame's {Field1, Field2, CCCount} triple.
// When frameWallMS crosses into a new wall-clock second, the next second's
// caption is built and queued; the returned frame carries at most one field-1
// byte pair (or none), with cc_count padding supplied by carriage.
func (g *Generator) NextFrame(frameWallMS int64) schedule.Frame {
	sec := frameWallMS / 1000
	if sec != g.builtForSec {
		g.builtForSec = sec
		g.buildSecond(sec, frameWallMS)
	}
	f := g.sched.Frame(frameWallMS)
	g.frame++
	return f
}

// Overran reports whether any built second's caption did not fit the one-second,
// one-pair-per-frame budget at this frame rate (the build needs more field-1
// pairs than there are frames before the flip; under WithPaintOn, than there are
// frames in the second). It is sticky once set. A caller can shorten the lines,
// drop a line, or raise the frame rate to clear it.
func (g *Generator) Overran() bool { return g.overran }

// buildSecond builds the caption for the second after sec and schedules it: the
// pop-on build (RCL/ENM + positioned rows) eligible now so it drains one pair per
// frame, and the terminating EOC eligible at the flip time (the second's last
// frame) so the flip is frame-accurate.
//
// Under WithPaintOn it instead paints the caption for sec itself, from nowMS —
// see paintSecond.
func (g *Generator) buildSecond(sec, nowMS int64) {
	mediaMS := int64(math.Round(float64(g.frame) * g.frameDurMS))
	if g.paintOn {
		g.paintSecond(sec, mediaMS, nowMS)
		return
	}
	block := g.block(sec+1, mediaMS+1000)
	toks := g.enc.Apply(block)
	build, eoc := splitEOC(toks)

	if serializedPairs(build) > g.buildBudget {
		g.overran = true
	}

	g.sched.Push(schedule.TimedTokens{TimeMS: nowMS, Field: 1, Tokens: build})
	if len(eoc) > 0 {
		flipMS := (sec+1)*1000 - int64(math.Round(g.frameDurMS))
		g.sched.Push(schedule.TimedTokens{TimeMS: flipMS, Field: 1, Tokens: eoc})
	}
}

// paintSecond schedules second sec's caption as a single paint-on transition
// eligible at nowMS — the second's first frame. The batch is EDM (the clear) +
// RDC + the positioned rows, and it drains one pair per frame, so the screen goes
// blank on the second's first frame and the text then appears two characters at a
// time until it stands complete for the rest of the second.
//
// The caption names the second being painted, not the next one: unlike a pop-on
// flip there is no instant at which the whole caption arrives, so building ahead
// would only put the coming second's text on screen during this one.
func (g *Generator) paintSecond(sec, mediaMS, nowMS int64) {
	toks := append([]cta608.Token{cta608.Command{Op: cta608.EDM}}, paintOnTokens(g.lines(sec, mediaMS))...)
	if serializedPairs(toks) > g.buildBudget {
		g.overran = true
	}
	g.sched.Push(schedule.TimedTokens{TimeMS: nowMS, Field: 1, Tokens: toks})
}

// block compiles the configured lines into a centered pop-on CaptionBlock for the
// given wall-clock second and media time.
func (g *Generator) block(wallSec, mediaMS int64) cta608.CaptionBlock {
	return cta608.CaptionBlock{Lines: g.lines(wallSec, mediaMS), Mode: cta608.PopOn}
}

// lines renders the configured lines, each centered, for the given wall-clock
// second and media time. Lines whose content is empty are dropped.
func (g *Generator) lines(wallSec, mediaMS int64) []cta608.Line {
	lines := make([]cta608.Line, 0, len(g.cfg.Lines))
	for _, ls := range g.cfg.Lines {
		text := content(ls.Kind, wallSec, mediaMS)
		if text == "" {
			continue
		}
		lines = append(lines, cta608.Line{
			Row:   ls.Row,
			Align: cta608.AlignCenter,
			Runs:  []cta608.Run{{Text: text, Pen: cta608.Pen{Color: colorFor(ls.Color)}}},
		})
	}
	return lines
}

// splitEOC separates the terminating EOC command (the pop-on flip) from the build
// tokens, so the build can drain across the second and the EOC can be held for the
// flip frame. A non-pop-on or empty sequence yields a nil eoc.
func splitEOC(toks []cta608.Token) (build, eoc []cta608.Token) {
	if n := len(toks); n > 0 {
		if c, ok := toks[n-1].(cta608.Command); ok && c.Op == cta608.EOC {
			return toks[:n-1], toks[n-1:]
		}
	}
	return toks, nil
}

// content renders one line's text for the given wall-clock second and media time.
func content(kind string, wallSec, mediaMS int64) string {
	switch kind {
	case "utc":
		return time.Unix(wallSec, 0).UTC().Format("2006-01-02T15:04:05Z")
	case "media":
		s := mediaMS / 1000
		return fmt.Sprintf("MEDIA %02d:%02d:%02d", s/3600, (s/60)%60, s%60)
	}
	return ""
}

// colorFor maps a color name to a cta608.Color (unknown/empty -> white).
func colorFor(name string) cta608.Color {
	switch name {
	case "green":
		return cta608.Green
	case "blue":
		return cta608.Blue
	case "cyan":
		return cta608.Cyan
	case "red":
		return cta608.Red
	case "yellow":
		return cta608.Yellow
	case "magenta":
		return cta608.Magenta
	default:
		return cta608.White
	}
}
