package examples

import (
	"fmt"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
)

// lineScreen builds a one-row, white, left-aligned Screen — enough to make the
// cue examples readable without pulling in a serializer.
func lineScreen(row int, text string) cta608.Screen {
	return cta608.Screen{Rows: []cta608.Row{{
		Index:     row,
		Displayed: true,
		Runs:      []cta608.Run{{Column: 0, Text: text, Pen: cta608.Pen{Color: cta608.White}}},
	}}}
}

// screenText joins a Screen's runs into a single string for display.
func screenText(s cta608.Screen) string {
	var parts []string
	for _, r := range s.Rows {
		for _, run := range r.Runs {
			parts = append(parts, run.Text)
		}
	}
	return strings.Join(parts, " / ")
}

// Example_cueSegment shows the 608->text direction. A cta608.Decoder driven by
// timed byte pairs reports a displayed-Screen change whenever the caption
// changes; cue.Segment cuts that timeline into cues with one unified rule — a
// change closes the current cue and opens a new one, an empty screen is a gap,
// and a caption still shown at the end takes a configurable end (SPEC §8.2).
func Example_cueSegment() {
	changes := []cue.TimedScreen{
		{Time: 1 * time.Second, Screen: lineScreen(15, "HELLO")},
		{Time: 3 * time.Second, Screen: cta608.Screen{}},         // erase: gap, no cue
		{Time: 4 * time.Second, Screen: lineScreen(15, "WORLD")}, // still shown at stream end
	}

	// No StreamEnd is known, so the dangling final cue runs for DefaultDur.
	cues := cue.Segment(changes, cue.SegmentOptions{DefaultDur: 2 * time.Second})
	for _, c := range cues {
		fmt.Printf("%v-%v %q\n", c.Start, c.End, screenText(c.Content))
	}

	// Output:
	// 1s-3s "HELLO"
	// 4s-6s "WORLD"
}

// Example_cueCompile shows the text->608 direction. cue.Compile merges any
// overlapping cues by position and drives the core cta608.Encoder diff engine to
// re-flip a pop-on caption at each boundary, emitting wall-time-tagged token
// transitions (SPEC §8.2). Frame scheduling is the schedule package's job.
func Example_cueCompile() {
	cues := []cue.TimedCue{
		{Start: 0, End: 2 * time.Second, Content: lineScreen(15, "HELLO")},
		{Start: 2 * time.Second, End: 4 * time.Second, Content: lineScreen(15, "WORLD")},
	}

	for _, tt := range cue.Compile(cues) {
		fmt.Printf("@%v\n", tt.Time)
		for _, tok := range tt.Tokens {
			fmt.Printf("  %s\n", tok)
		}
	}

	// Output:
	// @0s
	//   SetMode(pop-on)
	//   Command(ENM)
	//   PAC(row=15 white)
	//   Chars("HELLO")
	//   Command(EOC)
	// @2s
	//   SetMode(pop-on)
	//   Command(ENM)
	//   PAC(row=15 white)
	//   Chars("WORLD")
	//   Command(EOC)
	// @4s
	//   Command(EDM)
}
