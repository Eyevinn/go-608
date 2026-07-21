package examples

import (
	"fmt"
	"strings"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/srt"
)

// srtCueText flattens a cue's Screen into a single plain string (runs joined in
// row then column order), enough to show what a cue carries without re-serializing
// its styling.
func srtCueText(s cta608.Screen) string {
	var parts []string
	for _, row := range s.Rows {
		for _, run := range row.Runs {
			parts = append(parts, run.Text)
		}
	}
	return strings.Join(parts, "")
}

// Example_srt shows the srt package as a thin, two-way serializer over the cue
// model (SPEC §8.2). It reads an SRT document into []cue.TimedCue — quantizing the
// inline <font color> to the nearest of 608's 8 colors and anchoring the
// position-less text to the bottom of the grid — then writes the cues straight
// back out. Styling survives (color as <font color>, italic as <i>); SRT carries
// no positioning, so none is emitted.
func Example_srt() {
	const doc = "1\n" +
		"00:00:01,000 --> 00:00:03,000\n" +
		`<font color="#ff0000">Red</font> alert` + "\n" +
		"\n" +
		"2\n" +
		"00:00:04,000 --> 00:00:06,000\n" +
		"Plain <i>caption</i>\n"

	cues, err := srt.Read(strings.NewReader(doc))
	if err != nil {
		panic(err)
	}
	for _, c := range cues {
		fmt.Printf("%v-%v %q\n", c.Start, c.End, srtCueText(c.Content))
	}

	fmt.Println("--- written back ---")
	var out strings.Builder
	if err := srt.Write(&out, cues); err != nil {
		panic(err)
	}
	fmt.Print(out.String())

	// Output:
	// 1s-3s "Red alert"
	// 4s-6s "Plain caption"
	// --- written back ---
	// 1
	// 00:00:01,000 --> 00:00:03,000
	// <font color="#ff0000">Red</font> alert
	//
	// 2
	// 00:00:04,000 --> 00:00:06,000
	// Plain <i>caption</i>
}
