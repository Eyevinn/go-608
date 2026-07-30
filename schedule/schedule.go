package schedule

import (
	"fmt"
	"math"

	"github.com/Eyevinn/go-608/cta608"
)

// Frame is one video frame's primitive CTA-608 payload — the triple the
// carriage package consumes via carriage.FrameSEINALU(Field1, Field2, CCCount,
// codec). Field1 and Field2 are each either 0 bytes (idle: "no 608 waveform
// this field this frame") or exactly 2 bytes (one 608 byte pair, parity already
// applied by cta608.Serialize). CCCount is the cc_data() construct count for the
// frame; carriage places the (at most two) 608 constructs first and pads the
// remainder to CCCount with DTVCC padding.
type Frame struct {
	Field1  []byte
	Field2  []byte
	CCCount int
}

// CCCountPolicy selects how many cc_data() constructs a Frame declares.
type CCCountPolicy int

const (
	// CCCountFull emits the full per-frame-rate cc_count (round(600/fps), the
	// CTA-708-E §4.3.6 table) and lets carriage pad the surplus with DTVCC
	// padding. Fixed-allocation and the most interoperable — the recommended
	// default (SPEC §5.3).
	CCCountFull CCCountPolicy = iota
	// CCCountMinimal emits the minimal count of 2 — just the two 608 field
	// constructs, with no DTVCC padding. Appropriate for 608-only streams that
	// do not need to reserve a fixed DTVCC allocation.
	CCCountMinimal
)

// FlipTiming selects what a pushed batch's TimeMS means for a pop-on transition —
// one that ends in an EOC, the control code that makes the built caption visible.
//
// A pop-on caption is two transmissions: a build written into non-displayed memory,
// then the EOC that flips it on screen. Both drain at one byte pair per frame, so the
// build occupies real time — ~18 pairs for two lines, 0.6 s at 30 fps — and where it
// sits decides whether TimeMS names the moment the caption *appears* or the moment it
// starts being *sent*.
type FlipTiming int

const (
	// FlipOnTime treats TimeMS as the instant the caption becomes visible: the build
	// is transmitted over the frames *before* TimeMS so its EOC lands on TimeMS. This
	// is the default, because a caption's timestamp almost always means "show it now"
	// — a subtitle cue's start, a clock's second boundary.
	//
	// The build is made eligible pairs*frameDur earlier. Nothing is dropped if that
	// reaches back past the previous batch: the per-field queue drains in order with
	// head-of-line blocking, so a build with no room simply starts as soon as the
	// preceding pairs are done and its flip lands as early as it can — degrading to
	// exactly FlipAfterBuild for that one transition rather than failing.
	FlipOnTime FlipTiming = iota
	// FlipAfterBuild treats TimeMS as the instant transmission starts, so the flip
	// lands however long the build takes after it — 0.2-0.5 s late in practice. This
	// was the only behaviour before v0.8.0; use it to reproduce output from then.
	FlipAfterBuild
)

// Option configures a Scheduler at construction (functional-options pattern).
type Option func(*Scheduler)

// WithFlipTiming sets whether a pop-on batch's TimeMS is when the caption becomes
// visible (FlipOnTime, the default) or when its transmission starts
// (FlipAfterBuild). See FlipTiming.
func WithFlipTiming(t FlipTiming) Option {
	return func(s *Scheduler) { s.flipTiming = t }
}

// WithCCCountPolicy sets the cc_count policy (default CCCountFull).
func WithCCCountPolicy(p CCCountPolicy) Option {
	return func(s *Scheduler) { s.policy = p }
}

// WithChannel sets the in-field data channel (1 or 2) used when serializing
// pushed tokens; it maps onto the caption service (CC1/CC3 on field 1, CC2/CC4
// on field 2). The default is channel 1.
func WithChannel(ch int) Option {
	return func(s *Scheduler) { s.channel = ch }
}

// WithDoubling overrides the control-code doubling policy used when serializing
// pushed tokens. The default (cta608.DoublingDefault) doubles control codes on
// field 1 and not on field 2, per CTA-608-E §D.2.
func WithDoubling(d cta608.Doubling) Option {
	return func(s *Scheduler) { s.doubling = d }
}

// TimedTokens is a wall-time-tagged batch of token transitions to transmit on
// one NTSC field. It is the Scheduler's input unit.
//
// SPEC §4.3 sketches Push(TimedTokens) but names cue.TimedTokens (§4.5), which
// lives in the cue package; importing cue would violate the layering rule (§3:
// schedule imports only cta608). schedule therefore defines its own input type
// so it stays a sibling of cue rather than a dependent. Two deliberate
// deviations from the §4.3 name-sketch: the time is wall-clock milliseconds
// (TimeMS int64, matching Scheduler.Frame's frameWallMS) rather than a
// time.Duration, and a Field selector routes the batch to the right per-field
// queue.
type TimedTokens struct {
	// TimeMS is the wall-clock time (ms) at which these tokens become eligible
	// to transmit. The first frame at or after TimeMS starts draining them.
	TimeMS int64
	// Field is the target NTSC field, 1 or 2; the zero value is treated as 1.
	Field int
	// Tokens is the transition to serialize and enqueue, in order.
	Tokens []cta608.Token
}

// Scheduler maps wall-time-tagged token transitions onto individual video
// frames. It serializes each pushed batch to odd-parity 608 byte pairs, holds
// them in per-field FIFO queues, and drains at most one pair per field per frame
// (a single 608 pair total per frame above 30 fps, per the 608 rate cap),
// padding the rest to cc_count. It is format-agnostic and carriage-free: it
// imports only cta608 and emits the primitive {Field1, Field2, CCCount} triple.
//
// A Scheduler is not safe for concurrent use; drive it from one goroutine.
type Scheduler struct {
	fps       float64
	fullCount int  // cached round(600/fps)
	single608 bool // true above 30 fps: one 608 pair total per frame

	policy     CCCountPolicy
	channel    int
	doubling   cta608.Doubling
	flipTiming FlipTiming

	q1, q2 fifo // per-field pair queues (field 1, field 2)
}

// NewScheduler returns a Scheduler for the given frame rate. Both members of a
// fractional/integer family (e.g. 29.97 and 30) yield the same cc_count. fps is
// expected to be a broadcast caption rate in the 23.976–60 range (SPEC §5.3);
// the resulting cc_count then lands in the 10–25 range that carriage accepts.
//
// Under the default CCCountFull policy, NewScheduler panics if fps is so far
// outside that range that cc_count = round(600/fps) falls outside carriage's
// valid 2..31 window — failing fast at construction rather than deep inside
// carriage.BuildCCData on the first frame.
func NewScheduler(fps float64, opts ...Option) *Scheduler {
	s := &Scheduler{
		fps:        fps,
		policy:     CCCountFull,
		channel:    1,
		doubling:   cta608.DoublingDefault,
		flipTiming: FlipOnTime,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.fullCount = ccCountForFPS(fps)
	if s.policy == CCCountFull && (s.fullCount < 2 || s.fullCount > 31) {
		panic(fmt.Sprintf("schedule: fps %g yields cc_count %d outside the valid 2..31 range; "+
			"use a broadcast caption rate (23.976..60)", fps, s.fullCount))
	}
	// 608 rate cap (SPEC §5.3): ≤30 fps has room for one field-1 and one field-2
	// pair per frame; above 30 fps only one 608 pair per frame fits under the
	// ~119.88 B/s ceiling.
	s.single608 = fps > 30.5
	return s
}

// ccCountForFPS returns cc_count = round(600/fps) (CTA-708-E §4.3.6). The 1.001
// factors of a fractional rate cancel between the bit rate and the frame period,
// so 23.976/24→25, 25→24, 29.97/30→20, 50→12, 59.94/60→10.
func ccCountForFPS(fps float64) int {
	return int(math.Round(600.0 / fps))
}

// Push serializes a batch of token transitions and enqueues the resulting byte
// pairs on the target field's queue, tagged with the batch's eligibility time.
// Serialization (odd parity, control-code doubling, two-per-pair packing,
// null-pair frame alignment, extended-char backspace-and-replace) is owned by
// cta608.Serialize; every emitted unit is therefore a whole 2-byte pair, which
// keeps two-byte control codes frame-aligned once drained one pair per frame.
func (s *Scheduler) Push(t TimedTokens) {
	if len(t.Tokens) == 0 {
		return
	}
	field := t.Field
	if field == 0 {
		field = 1
	}
	// Under FlipOnTime a pop-on transition is enqueued as two runs: the build,
	// backdated so it finishes just before TimeMS, and the EOC at TimeMS itself. A
	// batch with no EOC (a bare EDM clear, a roll-up CR) is the visible change itself
	// and is never backdated — moving it would move the very thing being timed.
	if s.flipTiming == FlipOnTime {
		if build, eoc := splitEOC(t.Tokens); len(eoc) > 0 && len(build) > 0 {
			buildData := s.serialize(build, field)
			preRollMS := int64(math.Round(float64(len(buildData)/2) * 1000.0 / s.fps))
			s.enqueue(buildData, field, t.TimeMS-preRollMS)
			s.enqueue(s.serialize(eoc, field), field, t.TimeMS)
			return
		}
	}
	s.enqueue(s.serialize(t.Tokens, field), field, t.TimeMS)
}

// serialize renders tokens to odd-parity byte pairs under the Scheduler's channel
// and doubling policy.
func (s *Scheduler) serialize(toks []cta608.Token, field int) []byte {
	return cta608.Serialize(toks, cta608.SerializeOptions{
		Field:    field,
		Channel:  s.channel,
		Doubling: s.doubling,
	})
}

// enqueue appends data's byte pairs to the field's queue, all eligible at eligibleMS.
func (s *Scheduler) enqueue(data []byte, field int, eligibleMS int64) {
	q := &s.q1
	if field == 2 {
		q = &s.q2
	}
	for i := 0; i+1 < len(data); i += 2 {
		q.push(queued{b0: data[i], b1: data[i+1], eligibleMS: eligibleMS})
	}
}

// splitEOC splits a transition into its build and a trailing EOC, the control code
// that makes a pop-on caption visible. eoc is nil when the sequence does not end in
// one, which marks a transition whose last token is itself the visible change.
//
// generate has its own copy for the same reason: it is seven lines, and sharing it
// would mean either exporting it from schedule for one caller or growing the cta608
// core's API, both worse than the duplication.
func splitEOC(toks []cta608.Token) (build, eoc []cta608.Token) {
	if n := len(toks); n > 0 {
		if c, ok := toks[n-1].(cta608.Command); ok && c.Op == cta608.EOC {
			return toks[:n-1], toks[n-1:]
		}
	}
	return toks, nil
}

// Frame emits the frame for the video frame presented at frameWallMS. It drains
// at most one eligible pair from each field's queue (a single 608 pair total,
// field 1 first, above 30 fps) and reports the cc_count for the frame. A field
// with no eligible pair this frame yields an empty (0-byte) pair — the distinct
// "no 608 waveform this field this frame" encoding that carriage preserves.
//
// frameWallMS is expected to be non-decreasing across calls; an already-drained
// pair is never re-emitted, so a backward jump only re-gates pairs that are
// still queued.
func (s *Scheduler) Frame(frameWallMS int64) Frame {
	f := Frame{CCCount: s.frameCCCount()}
	if s.single608 {
		// One 608 pair total per frame; field 1 takes priority.
		if p, ok := s.q1.pop(frameWallMS); ok {
			f.Field1 = []byte{p.b0, p.b1}
		} else if p, ok := s.q2.pop(frameWallMS); ok {
			f.Field2 = []byte{p.b0, p.b1}
		}
		return f
	}
	if p, ok := s.q1.pop(frameWallMS); ok {
		f.Field1 = []byte{p.b0, p.b1}
	}
	if p, ok := s.q2.pop(frameWallMS); ok {
		f.Field2 = []byte{p.b0, p.b1}
	}
	return f
}

// frameCCCount resolves the cc_count for one frame under the active policy.
func (s *Scheduler) frameCCCount() int {
	if s.policy == CCCountMinimal {
		return 2 // the two 608 field constructs, no DTVCC padding
	}
	return s.fullCount
}

// queued is one enqueued byte pair plus the wall-clock time at which it becomes
// eligible to transmit.
type queued struct {
	b0, b1     byte
	eligibleMS int64
}

// fifo is a byte-pair queue with a head index. It resets its backing slice once
// fully drained, so the steady state of a generator (push a burst, drain it
// before the next burst) reclaims memory each cycle.
type fifo struct {
	pairs []queued
	head  int
}

func (f *fifo) push(p queued) {
	f.pairs = append(f.pairs, p)
}

// pop removes and returns the head pair when the queue is non-empty and the head
// is eligible at nowMS; otherwise it returns ok=false and leaves the queue
// unchanged (head-of-line blocking preserves in-order draining).
func (f *fifo) pop(nowMS int64) (queued, bool) {
	if f.head >= len(f.pairs) || f.pairs[f.head].eligibleMS > nowMS {
		return queued{}, false
	}
	p := f.pairs[f.head]
	f.head++
	if f.head == len(f.pairs) {
		f.pairs = f.pairs[:0]
		f.head = 0
	}
	return p, true
}
