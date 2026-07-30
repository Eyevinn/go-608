package schedule

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/Eyevinn/go-608/cta608"
)

// collectField1 drives n frames at the given per-frame wall-clock step (ms) and
// returns the concatenated non-empty field-1 pairs plus, per frame, whether a
// field-1 pair was emitted.
func drive(t *testing.T, s *Scheduler, n int, stepMS int64) []Frame {
	t.Helper()
	frames := make([]Frame, n)
	for i := 0; i < n; i++ {
		frames[i] = s.Frame(int64(i) * stepMS)
	}
	return frames
}

func concatField1(frames []Frame) []byte {
	var out []byte
	for _, f := range frames {
		out = append(out, f.Field1...)
	}
	return out
}

func TestCCCountForFPS(t *testing.T) {
	cases := []struct {
		fps  float64
		want int
	}{
		{23.976, 25},
		{24, 25},
		{25, 24},
		{29.97, 20},
		{30, 20},
		{50, 12},
		{59.94, 10},
		{60, 10},
	}
	for _, c := range cases {
		if got := ccCountForFPS(c.fps); got != c.want {
			t.Errorf("ccCountForFPS(%g) = %d, want %d", c.fps, got, c.want)
		}
	}
}

func TestNewSchedulerPanicsOnUnsupportedFPS(t *testing.T) {
	// cc_count = round(600/10) = 60, above carriage's 31 ceiling.
	assertPanics(t, "full policy, fps 10", func() { NewScheduler(10) })
	// cc_count = round(600/1000) = 1, below the two-608-construct floor.
	assertPanics(t, "full policy, fps 1000", func() { NewScheduler(1000) })
	// A supported rate does not panic.
	assertNoPanic(t, "full policy, fps 30", func() { NewScheduler(30) })
	// The minimal policy always uses cc_count 2, so any fps is accepted.
	assertNoPanic(t, "minimal policy, fps 10", func() {
		NewScheduler(10, WithCCCountPolicy(CCCountMinimal))
	})
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic, got none", name)
		}
	}()
	fn()
}

func assertNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", name, r)
		}
	}()
	fn()
}

func TestFrameCCCountReportsFullPerRateByDefault(t *testing.T) {
	cases := []struct {
		fps  float64
		want int
	}{
		{24, 25}, {25, 24}, {30, 20}, {50, 12}, {60, 10},
	}
	for _, c := range cases {
		s := NewScheduler(c.fps)
		if got := s.Frame(0).CCCount; got != c.want {
			t.Errorf("fps %g: Frame().CCCount = %d, want %d", c.fps, got, c.want)
		}
	}
}

func TestCCCountMinimalPolicy(t *testing.T) {
	s := NewScheduler(30, WithCCCountPolicy(CCCountMinimal))
	if got := s.Frame(0).CCCount; got != 2 {
		t.Errorf("minimal policy: CCCount = %d, want 2", got)
	}
	// Draining still works under the minimal policy.
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
	if f := s.Frame(0); len(f.Field1) != 2 || f.CCCount != 2 {
		t.Errorf("minimal policy: got Field1=%x CCCount=%d, want 2-byte pair and CCCount 2", f.Field1, f.CCCount)
	}
}

func TestIdleFieldsYieldEmptyPairs(t *testing.T) {
	s := NewScheduler(30)
	f := s.Frame(0)
	if len(f.Field1) != 0 || len(f.Field2) != 0 {
		t.Errorf("idle frame: Field1=%x Field2=%x, want both empty", f.Field1, f.Field2)
	}
	if f.CCCount != 20 {
		t.Errorf("idle frame at 30fps: CCCount = %d, want 20", f.CCCount)
	}
}

func TestPushEmptyIsNoop(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Tokens: nil})
	if f := s.Frame(0); len(f.Field1) != 0 {
		t.Errorf("empty push then Frame: Field1=%x, want empty", f.Field1)
	}
}

// A pushed control code that is doubled on field 1 occupies two frames (one pair
// per frame), and every emitted pair is exactly two bytes (frame alignment).
func TestOnePairPerFrameAndFrameAlignment(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Command{Op: cta608.EOC}}})

	frames := drive(t, s, 4, 33)
	nonEmpty := 0
	for i, f := range frames {
		if len(f.Field1) == 0 {
			continue
		}
		nonEmpty++
		if len(f.Field1) != 2 {
			t.Errorf("frame %d: Field1 = %x (%d bytes), want a whole 2-byte pair", i, f.Field1, len(f.Field1))
		}
	}
	// EOC on field 1 doubles to two identical pairs → two consecutive frames.
	if nonEmpty != 2 {
		t.Errorf("EOC on field 1: emitted %d field-1 pairs, want 2 (doubled)", nonEmpty)
	}
	if len(frames[0].Field1) != 2 || len(frames[1].Field1) != 2 {
		t.Errorf("doubled control code should drain on frames 0 and 1, got %x %x", frames[0].Field1, frames[1].Field1)
	}
	if len(frames[2].Field1) != 0 {
		t.Errorf("frame 2 should be idle, got %x", frames[2].Field1)
	}
}

func TestFieldTwoUsesField2QueueAndNoDoubling(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Field: 2, Tokens: []cta608.Token{cta608.Command{Op: cta608.EOC}}})

	f0 := s.Frame(0)
	if len(f0.Field1) != 0 {
		t.Errorf("field-2 push leaked to field 1: %x", f0.Field1)
	}
	if len(f0.Field2) != 2 {
		t.Errorf("field-2 push: frame 0 Field2 = %x, want a 2-byte pair", f0.Field2)
	}
	// Doubling is off for field 2 by default, so EOC is a single pair.
	if f1 := s.Frame(33); len(f1.Field2) != 0 {
		t.Errorf("field 2 EOC should not double: frame 1 Field2 = %x, want empty", f1.Field2)
	}
}

// At ≤30 fps both fields may carry a pair in the same frame.
func TestBothFieldsDrainSameFrameAtOrBelow30(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Field: 1, Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
	s.Push(TimedTokens{Field: 2, Tokens: []cta608.Token{cta608.Chars{Text: "CD"}}})

	f := s.Frame(0)
	if len(f.Field1) != 2 || len(f.Field2) != 2 {
		t.Errorf("30fps: want a pair on each field in one frame, got Field1=%x Field2=%x", f.Field1, f.Field2)
	}
}

// Above 30 fps only one 608 pair total may be emitted per frame; field 1 wins.
func TestRateCapSingle608AboveThirty(t *testing.T) {
	for _, fps := range []float64{50, 59.94, 60} {
		s := NewScheduler(fps)
		s.Push(TimedTokens{Field: 1, Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
		s.Push(TimedTokens{Field: 2, Tokens: []cta608.Token{cta608.Chars{Text: "CD"}}})

		f0 := s.Frame(0)
		if len(f0.Field1) != 2 || len(f0.Field2) != 0 {
			t.Errorf("fps %g frame 0: want field 1 only, got Field1=%x Field2=%x", fps, f0.Field1, f0.Field2)
		}
		f1 := s.Frame(1)
		if len(f1.Field1) != 0 || len(f1.Field2) != 2 {
			t.Errorf("fps %g frame 1: want field 2 after field 1 drained, got Field1=%x Field2=%x",
				fps, f1.Field1, f1.Field2)
		}
	}
}

func TestNeverBothFieldsAboveThirty(t *testing.T) {
	s := NewScheduler(60)
	// Saturate both fields with several pairs each.
	for i := 0; i < 5; i++ {
		s.Push(TimedTokens{Field: 1, Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
		s.Push(TimedTokens{Field: 2, Tokens: []cta608.Token{cta608.Chars{Text: "CD"}}})
	}
	for i := 0; i < 20; i++ {
		f := s.Frame(int64(i))
		if len(f.Field1) > 0 && len(f.Field2) > 0 {
			t.Fatalf("frame %d emitted both fields at 60fps: Field1=%x Field2=%x", i, f.Field1, f.Field2)
		}
	}
}

// Pairs tagged in the future are not drained until frameWallMS reaches them;
// meanwhile the field stays idle (empty pairs).
func TestEligibilityGating(t *testing.T) {
	s := NewScheduler(25) // 40 ms/frame
	s.Push(TimedTokens{TimeMS: 1000, Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})

	// Frames before t=1000 must be idle.
	for tMS := int64(0); tMS < 1000; tMS += 40 {
		if f := s.Frame(tMS); len(f.Field1) != 0 {
			t.Fatalf("frame at %dms drained early: %x", tMS, f.Field1)
		}
	}
	// The frame at t=1000 starts draining.
	if f := s.Frame(1000); len(f.Field1) != 2 {
		t.Fatalf("frame at 1000ms: Field1 = %x, want a 2-byte pair", f.Field1)
	}
}

// A pushed stream drains across frames in order and reconstructs, via Parse, to
// the original tokens (Parse collapses the field-1 doubling). This is the core
// "drains in order, frame alignment holds" acceptance check at the token level.
func TestRoundTripThroughParse(t *testing.T) {
	tokens := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HELLO"},
		cta608.MidRow{Pen: cta608.Pen{Color: cta608.Red}},
		cta608.Chars{Text: "WORLD"},
		cta608.Command{Op: cta608.EOC},
	}
	s := NewScheduler(30)
	s.Push(TimedTokens{Tokens: tokens})

	// 30 pairs of headroom is plenty for this ~14-pair stream.
	frames := drive(t, s, 30, 33)
	got, err := cta608.Parse(concatField1(frames), cta608.ParseOptions{ValidateParity: true})
	if err != nil {
		t.Fatalf("Parse of drained pairs: %v", err)
	}
	if !reflect.DeepEqual(got, tokens) {
		t.Errorf("round trip mismatch:\n got  %v\n want %v", got, tokens)
	}

	// The EOC (last two doubled pairs) must land near the end of the stream, not
	// on the first frame — i.e. transitions drain in order.
	if len(frames[0].Field1) == 0 {
		t.Error("first frame should carry the first pair")
	}
}

// Two pushes to the same field concatenate in FIFO order across frames.
func TestTwoPushesDrainInOrder(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Chars{Text: "CD"}}})

	frames := drive(t, s, 4, 33)
	got, err := cta608.Parse(concatField1(frames), cta608.ParseOptions{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []cta608.Token{cta608.Chars{Text: "ABCD"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FIFO order: got %v, want %v", got, want)
	}
}

// The fifo resets its backing slice once fully drained, and keeps working after.
func TestFifoResetsWhenDrained(t *testing.T) {
	s := NewScheduler(30)
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Chars{Text: "AB"}}})
	if f := s.Frame(0); len(f.Field1) != 2 {
		t.Fatalf("first drain: Field1 = %x", f.Field1)
	}
	if s.q1.head != 0 || len(s.q1.pairs) != 0 {
		t.Errorf("after full drain: head=%d len=%d, want both 0", s.q1.head, len(s.q1.pairs))
	}
	// Reuse after reset.
	s.Push(TimedTokens{Tokens: []cta608.Token{cta608.Chars{Text: "CD"}}})
	if f := s.Frame(33); len(f.Field1) != 2 {
		t.Fatalf("drain after reset: Field1 = %x", f.Field1)
	}
}

// A pop-on transition's EOC must land on the pushed time, with the build arriving
// over the preceding frames. This is the FlipOnTime default: a cue's timestamp means
// "show it now", so the caption has to be visible at that frame, not start being
// transmitted then.
func TestFlipOnTimeLandsEOCOnPushedTime(t *testing.T) {
	const fps = 30.0
	toks := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.Command{Op: cta608.ENM},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HELLO"},
		cta608.Command{Op: cta608.EOC},
	}

	for _, tc := range []struct {
		name    string
		timing  FlipTiming
		wantEOC int // frame the EOC (94 2f) is transmitted on
	}{
		// RCL, ENM and the PAC are doubled (2 pairs each) and "HELLO" packs into 3,
		// so the build is 9 pairs. FlipAfterBuild therefore flips 9 frames late,
		// while FlipOnTime backdates the build to land the EOC on frame 30 itself.
		{"FlipOnTime", FlipOnTime, 30},
		{"FlipAfterBuild", FlipAfterBuild, 39},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScheduler(fps, WithFlipTiming(tc.timing))
			s.Push(TimedTokens{TimeMS: 1000, Field: 1, Tokens: toks})

			eocFrame := -1
			var pairs int
			for frame := 0; frame < 120; frame++ {
				f := s.Frame(int64(math.Round(float64(frame) * 1000.0 / fps)))
				if len(f.Field1) != 2 {
					continue
				}
				pairs++
				if f.Field1[0] == 0x94 && f.Field1[1] == 0x2f && eocFrame < 0 {
					eocFrame = frame
				}
			}
			if eocFrame != tc.wantEOC {
				t.Errorf("EOC on frame %d, want %d", eocFrame, tc.wantEOC)
			}
			// Both timings transmit the same bytes; only their placement differs.
			// 9 build pairs + a doubled EOC.
			if pairs != 11 {
				t.Errorf("drained %d pairs, want 11", pairs)
			}
		})
	}
}

// Splitting the transition at the EOC must not change what is transmitted — the two
// timings differ in placement only. If Serialize's null-pair frame alignment behaved
// differently across a split, the pre-roll would silently alter the wire bytes.
func TestFlipOnTimeSameBytesAsFlipAfterBuild(t *testing.T) {
	const fps = 30.0
	toks := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.Command{Op: cta608.ENM},
		cta608.PAC{Row: 14, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.Yellow}},
		cta608.Chars{Text: "ODD LENGTH LINE"}, // odd char count exercises the pad
		cta608.Command{Op: cta608.EOC},
	}
	drain := func(timing FlipTiming) []byte {
		s := NewScheduler(fps, WithFlipTiming(timing))
		s.Push(TimedTokens{TimeMS: 2000, Field: 1, Tokens: toks})
		var out []byte
		for frame := 0; frame < 200; frame++ {
			f := s.Frame(int64(math.Round(float64(frame) * 1000.0 / fps)))
			out = append(out, f.Field1...)
		}
		return out
	}
	on, after := drain(FlipOnTime), drain(FlipAfterBuild)
	if !bytes.Equal(on, after) {
		t.Errorf("FlipOnTime bytes differ from FlipAfterBuild\n on    = % x\n after = % x", on, after)
	}
}

// A transition that is not a pop-on flip — a bare EDM clear, a roll-up CR — is itself
// the visible change, so it must stay on its pushed frame rather than being backdated.
func TestFlipOnTimeDoesNotMoveNonFlipTransitions(t *testing.T) {
	const fps = 30.0
	for _, tc := range []struct {
		name string
		toks []cta608.Token
	}{
		{"bare EDM", []cta608.Token{cta608.Command{Op: cta608.EDM}}},
		{"roll-up CR", []cta608.Token{cta608.Command{Op: cta608.CR}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewScheduler(fps, WithFlipTiming(FlipOnTime))
			s.Push(TimedTokens{TimeMS: 1000, Field: 1, Tokens: tc.toks})
			first := -1
			for frame := 0; frame < 90; frame++ {
				f := s.Frame(int64(math.Round(float64(frame) * 1000.0 / fps)))
				if len(f.Field1) == 2 && first < 0 {
					first = frame
				}
			}
			if first != 30 {
				t.Errorf("first pair on frame %d, want 30 (t=1000ms, not backdated)", first)
			}
		})
	}
}

// When the pre-roll reaches back past the previous transition there is physically no
// room for both flips to be on time. Nothing may be dropped: the queue's in-order
// draining makes the crowded build start as soon as the previous pairs are done, so
// its flip is merely late — degrading to FlipAfterBuild for that one cue.
func TestFlipOnTimeCrowdedPreRollDropsNothing(t *testing.T) {
	const fps = 30.0
	line := func(text string) []cta608.Token {
		return []cta608.Token{
			cta608.SetMode{Mode: cta608.PopOn},
			cta608.Command{Op: cta608.ENM},
			cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
			cta608.Chars{Text: text},
			cta608.Command{Op: cta608.EOC},
		}
	}
	s := NewScheduler(fps, WithFlipTiming(FlipOnTime))
	// Two flips 200 ms apart, each needing ~370 ms of build: they cannot both be on time.
	s.Push(TimedTokens{TimeMS: 1000, Field: 1, Tokens: line("FIRST")})
	s.Push(TimedTokens{TimeMS: 1200, Field: 1, Tokens: line("SECOND")})

	var eocFrames []int
	pairs := 0
	for frame := 0; frame < 200; frame++ {
		f := s.Frame(int64(math.Round(float64(frame) * 1000.0 / fps)))
		if len(f.Field1) != 2 {
			continue
		}
		pairs++
		if f.Field1[0] == 0x94 && f.Field1[1] == 0x2f {
			eocFrames = append(eocFrames, frame)
		}
	}
	// Every pair of both transitions is still transmitted (11 each: 9 build + doubled EOC).
	if pairs != 22 {
		t.Errorf("drained %d pairs, want 22 — nothing may be dropped when the pre-roll is crowded", pairs)
	}
	// Both flips happen, in order, and the second is late rather than lost.
	if len(eocFrames) != 4 { // two flips, EOC doubled
		t.Fatalf("saw EOC on frames %v, want 4 transmissions (two doubled flips)", eocFrames)
	}
	if eocFrames[0] != 30 {
		t.Errorf("first flip on frame %d, want 30 (it had room)", eocFrames[0])
	}
	if eocFrames[2] <= 36 {
		t.Errorf("second flip on frame %d, want later than its 36 (t=1200ms) ideal", eocFrames[2])
	}
}
