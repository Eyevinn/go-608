package generate

import (
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// rollTrace decodes every frame and returns, per frame, the text of the roll-up
// window's rows from the top row down to base. Idle frames repeat the previous
// frame's window.
func rollTrace(t *testing.T, frames []schedule.Frame, base, rows int) [][]string {
	t.Helper()
	var dec cta608.Decoder
	out := make([][]string, len(frames))
	for i, f := range frames {
		if len(f.Field1) > 0 {
			nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
			fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
			if err != nil {
				t.Fatalf("frame %d FieldPairs: %v", i, err)
			}
			if err := dec.Feed(fld1); err != nil {
				t.Fatalf("frame %d decode: %v", i, err)
			}
		}
		win := make([]string, rows)
		for r := 0; r < rows; r++ {
			win[r], _, _, _ = rowText(dec.Screen(), base-rows+1+r)
		}
		out[i] = win
	}
	return out
}

// TestBuildUnitRollUpCuesScrollsAndTypes is the defining behavior: each cue scrolls
// the window up and types its lines onto the base row, two characters per frame,
// while the previous lines stay visible above. A 2 s segment at 30 fps with the
// two-line probe content gives two cues of two scroll steps each.
func TestBuildUnitRollUpCuesScrollsAndTypes(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	const base, rows = 15, 3
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	frames, err := BuildUnitRollUpCues(fps, Unit{Nr: 42, StartMS: unitStart, Frames: unitFrames},
		1000, rows, segCueContent)
	if err != nil {
		t.Fatalf("BuildUnitRollUpCues: %v", err)
	}
	if len(frames) != unitFrames {
		t.Fatalf("got %d frames, want %d", len(frames), unitFrames)
	}
	// The default resets the window: EDM (0x94 0x2c) on the unit's first frame.
	if got := frames[0].Field1; len(got) != 2 || got[0] != 0x94 || got[1] != 0x2c {
		t.Errorf("frame 0 field 1 = % x, want 94 2c (the window reset)", got)
	}

	trace := rollTrace(t, frames, base, rows)
	if w := trace[0]; w[0] != "" || w[1] != "" || w[2] != "" {
		t.Errorf("frame 0 window = %q, want it cleared", w)
	}

	// The base row either grows by up to one byte pair (two characters) or is emptied
	// by a scroll — never anything else.
	for i := 1; i < unitFrames; i++ {
		prev, cur := trace[i-1][rows-1], trace[i][rows-1]
		switch {
		case cur == "": // a scroll cleared the base row
		case strings.HasPrefix(cur, prev) && len(cur)-len(prev) <= 2:
		default:
			t.Fatalf("frame %d base row went %q -> %q, want a ≤2-character extension or a scroll",
				i, prev, cur)
		}
	}

	// Settled window at the end of each cue. Cue 0 fills the bottom two rows; cue 1
	// pushes cue 0's lines up, so the window holds the last three lines written.
	cases := []struct {
		frame int
		want  []string
	}{
		{29, []string{"", "14:23:44.000", "SEG 42"}},
		{59, []string{"SEG 42", "14:23:45.000", "SEG 42"}},
	}
	for _, c := range cases {
		got := trace[c.frame]
		for r := range got {
			if got[r] != c.want[r] {
				t.Errorf("frame %d window = %q, want %q", c.frame, got, c.want)
				break
			}
		}
		t.Logf("frame %d window: %q", c.frame, got)
	}

	// Roll-up never erases mid-unit: once a cue has typed its lines they only move up.
	steps := 0
	for i := 1; i < unitFrames; i++ {
		if len(trace[i][rows-1]) > len(trace[i-1][rows-1]) {
			steps++
		}
	}
	if steps < 10 {
		t.Errorf("base row grew on %d frames, want the text spread over many frames", steps)
	}
}

// TestBuildUnitRollUpCuesCarry covers the cross-unit question directly: by default a
// unit clears the window on its first frame, so its display owes nothing to the unit
// before it; WithRollUpCarry keeps the previous unit's lines and scrolls them.
func TestBuildUnitRollUpCuesCarry(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	const base, rows = 15, 3
	aStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	unitA := Unit{Nr: 42, StartMS: aStart, Frames: unitFrames}
	unitB := Unit{Nr: 43, StartMS: aStart + 2000, Frames: unitFrames}

	build := func(u Unit, opts ...RollUpOption) []schedule.Frame {
		t.Helper()
		frames, err := BuildUnitRollUpCues(fps, u, 1000, rows, segCueContent, opts...)
		if err != nil {
			t.Fatalf("unit %d: %v", u.Nr, err)
		}
		return frames
	}

	// Reset (default): unit B opens with an EDM, so the window is empty on its first
	// frame even though unit A left three lines on screen.
	stream := append(build(unitA), build(unitB)...)
	trace := rollTrace(t, stream, base, rows)
	if w := trace[59]; w[1] == "" || w[2] == "" {
		t.Fatalf("end of unit A window = %q, want it filled", w)
	}
	if w := trace[60]; w[0] != "" || w[1] != "" || w[2] != "" {
		t.Errorf("reset: unit B's first frame window = %q, want it cleared", w)
	}

	// Carry: no EDM, and unit A's lines are still there to be scrolled up.
	carried := append(build(unitA, WithRollUpCarry()), build(unitB, WithRollUpCarry())...)
	if got := carried[60].Field1; len(got) == 2 && got[0] == 0x94 && got[1] == 0x2c {
		t.Error("carry: unit B still starts with an EDM, want the window kept")
	}
	ctrace := rollTrace(t, carried, base, rows)
	if w := ctrace[60]; w[2] == "" {
		t.Errorf("carry: unit B's first frame window = %q, want unit A's lines still shown", w)
	}
	// One cue later the window holds unit A's last line above unit B's first.
	if w := ctrace[89]; w[0] == "" && w[1] == "" {
		t.Errorf("carry: window at frame 89 = %q, want scrolled history", w)
	}
	t.Logf("carry: unit B frame 0 window %q, frame 29 %q", ctrace[60], ctrace[89])
}

// TestBuildUnitRollUpCuesSelfContained checks that a unit's frames depend on nothing
// outside the unit in either mode — the same unit built twice is byte-identical. With
// carry the *display* continues across the boundary, but the emitted data does not
// change, which is what lets a stateless server serve either way.
func TestBuildUnitRollUpCuesSelfContained(t *testing.T) {
	const fps = 30.0
	u := Unit{Nr: 7, StartMS: time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli(), Frames: 60}
	for _, tc := range []struct {
		name string
		opts []RollUpOption
	}{
		{"reset", nil},
		{"carry", []RollUpOption{WithRollUpCarry()}},
	} {
		a, err := BuildUnitRollUpCues(fps, u, 1000, 3, segCueContent, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		b, err := BuildUnitRollUpCues(fps, u, 1000, 3, segCueContent, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		for i := range a {
			if a[i].CCCount != b[i].CCCount ||
				string(a[i].Field1) != string(b[i].Field1) || string(a[i].Field2) != string(b[i].Field2) {
				t.Fatalf("%s: frame %d differs between two independent builds of the same unit", tc.name, i)
			}
		}
	}
}

// TestBuildUnitRollUpCuesRowOrder checks that lines are written bottom-last regardless
// of the order they arrive in, so the window ends up laid out as the Rows declare and
// the largest Row is the base row.
func TestBuildUnitRollUpCuesRowOrder(t *testing.T) {
	const fps = 30.0
	white := cta608.Pen{Color: cta608.White}
	// Deliberately reversed: the bottom line (row 15) is given first.
	content := func(u Unit, cueIdx int, cueStartMS int64) UnitCue {
		return UnitCue{Lines: []cta608.Line{
			{Row: 15, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: "BOTTOM", Pen: white}}},
			{Row: 14, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: "TOP", Pen: white}}},
		}}
	}
	frames, err := BuildUnitRollUpCues(fps, Unit{Nr: 1, StartMS: 0, Frames: 30}, 1000, 2, content)
	if err != nil {
		t.Fatalf("BuildUnitRollUpCues: %v", err)
	}
	trace := rollTrace(t, frames, 15, 2)
	if got := trace[29]; got[0] != "TOP" || got[1] != "BOTTOM" {
		t.Errorf("window = %q, want [TOP BOTTOM] (rows 14/15 as declared)", got)
	}
}

// TestBuildUnitRollUpCuesEmptyCue checks that a cue with no lines emits nothing: the
// window keeps what it has rather than scrolling a blank line in or erasing.
func TestBuildUnitRollUpCuesEmptyCue(t *testing.T) {
	const fps = 30.0
	const unitFrames = 60
	content := func(u Unit, cueIdx int, cueStartMS int64) UnitCue {
		if cueIdx == 1 {
			return UnitCue{}
		}
		return segCueContent(u, cueIdx, cueStartMS)
	}
	frames, err := BuildUnitRollUpCues(fps, Unit{Nr: 42, StartMS: 0, Frames: unitFrames}, 1000, 2, content)
	if err != nil {
		t.Fatalf("BuildUnitRollUpCues: %v", err)
	}
	for i := 30; i < unitFrames; i++ {
		if len(frames[i].Field1) != 0 {
			t.Errorf("frame %d carries data for an empty cue, want idle", i)
		}
	}
	trace := rollTrace(t, frames, 15, 2)
	if got, want := trace[unitFrames-1][1], "SEG 42"; got != want {
		t.Errorf("base row = %q after an empty cue, want the previous cue's %q kept", got, want)
	}
}

// TestBuildUnitRollUpCuesOverran checks the error when a cue cannot finish writing
// inside its slice. Roll-up is the most expensive mode per line — the mode entry plus
// a CR per line on top of the text.
func TestBuildUnitRollUpCuesOverran(t *testing.T) {
	_, err := BuildUnitRollUpCues(30.0, Unit{Nr: 1, StartMS: 0, Frames: 10}, 1000, 2, segCueContent)
	if err == nil {
		t.Fatal("expected an overrun error for a 10-frame unit, got nil")
	}
	t.Logf("overrun error (expected): %v", err)
}

// TestBuildUnitRollUpCuesBadInput covers the guard clauses.
func TestBuildUnitRollUpCuesBadInput(t *testing.T) {
	if _, err := BuildUnitRollUpCues(30.0, Unit{Nr: 1, Frames: 0}, 1000, 2, segCueContent); err == nil {
		t.Error("expected an error for Frames = 0, got nil")
	}
	if _, err := BuildUnitRollUpCues(30.0, Unit{Nr: 1, Frames: 60}, 1000, 2, nil); err == nil {
		t.Error("expected an error for a nil content function, got nil")
	}
	if _, err := BuildUnitRollUpCues(5.0, Unit{Nr: 1, Frames: 60}, 1000, 2, segCueContent); err == nil {
		t.Error("expected an fps-range error for 5 fps, got nil")
	}
}

// TestRollUpTokens pins the token shape: the mode entry once per cue, then a CR and
// the positioned text per line, and nothing at all when there is nothing to write.
func TestRollUpTokens(t *testing.T) {
	if toks := rollUpTokens(nil, 2); toks != nil {
		t.Errorf("rollUpTokens(nil) = %v, want nil", toks)
	}
	empty := []cta608.Line{{Row: 15, Runs: []cta608.Run{{Text: ""}}}}
	if toks := rollUpTokens(empty, 2); toks != nil {
		t.Errorf("rollUpTokens(empty runs) = %v, want nil", toks)
	}

	toks := rollUpTokens([]cta608.Line{
		{Row: 14, Runs: []cta608.Run{{Text: "A"}}},
		{Row: 15, Runs: []cta608.Run{{Text: "B"}}},
	}, 3)
	m, ok := toks[0].(cta608.SetMode)
	if !ok || m.Mode != cta608.RollUp || m.RollUpRows != 3 {
		t.Fatalf("first token = %v, want SetMode{RollUp, 3}", toks[0])
	}
	crs, modes := 0, 0
	for _, tok := range toks[1:] {
		switch tk := tok.(type) {
		case cta608.Command:
			if tk.Op == cta608.CR {
				crs++
			}
		case cta608.SetMode:
			modes++
		}
	}
	if crs != 2 {
		t.Errorf("got %d CRs, want one per line (2)", crs)
	}
	if modes != 0 {
		t.Errorf("got %d extra mode entries, want the mode stated once per cue", modes)
	}
	// Both lines are written to the base row (the largest declared Row).
	for _, tok := range toks {
		if p, ok := tok.(cta608.PAC); ok && p.Row != 15 {
			t.Errorf("PAC row = %d, want the base row 15", p.Row)
		}
	}
}

// TestClampRows pins the 2..4 window (the zero value being the two-row window).
func TestClampRows(t *testing.T) {
	for in, want := range map[int]int{-1: 2, 0: 2, 1: 2, 2: 2, 3: 3, 4: 4, 5: 4, 99: 4} {
		if got := clampRows(in); got != want {
			t.Errorf("clampRows(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestBaseRow pins the base-row rule: the largest declared Row, or 15 when unset.
func TestBaseRow(t *testing.T) {
	cases := []struct {
		lines []cta608.Line
		want  int
	}{
		{nil, 15},
		{[]cta608.Line{{Row: 0}}, 15},
		{[]cta608.Line{{Row: 14}, {Row: 15}}, 15},
		{[]cta608.Line{{Row: 4}, {Row: 3}}, 4},
		{[]cta608.Line{{Row: 99}}, 15},
	}
	for _, c := range cases {
		if got := baseRow(c.lines); got != c.want {
			t.Errorf("baseRow(%v) = %d, want %d", c.lines, got, c.want)
		}
	}
}
