package examples

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/webvtt"
)

// Example_webvttRead shows the WebVTT -> cue direction. webvtt.Read parses the
// WEBVTT document into []cue.TimedCue, quantizing the STYLE-class color to the 608
// palette and the line:/position: settings to the 15x32 grid (SPEC §8.2). All the
// 608 mapping lives in the cue package; webvtt only serializes.
func Example_webvttRead() {
	const doc = `WEBVTT

STYLE
::cue(.green) { color: #00ff00; }

00:00:01.000 --> 00:00:03.000 line:100% position:0% align:start
<c.green>HELLO</c> <i>WORLD</i>
`
	cues, err := webvtt.Read(strings.NewReader(doc))
	if err != nil {
		panic(err)
	}
	for _, c := range cues {
		fmt.Printf("%v-%v\n", c.Start, c.End)
		for _, row := range c.Content.Rows {
			for _, r := range row.Runs {
				fmt.Printf("  row=%d col=%d %q color=%s italic=%v\n",
					row.Index, r.Column, r.Text, r.Pen.Color, r.Pen.Italic)
			}
		}
	}

	// Output:
	// 1s-3s
	//   row=15 col=0 "HELLO" color=green italic=false
	//   row=15 col=5 " " color=white italic=false
	//   row=15 col=6 "WORLD" color=white italic=true
}

// Example_webvttWrite shows the cue -> WebVTT direction. webvtt.Write serializes a
// cue list, emitting the WEBVTT header, a STYLE block for every color class used,
// and one positioned cue block per TimedCue (SPEC §8.2).
func Example_webvttWrite() {
	cues := []cue.TimedCue{{
		Start: 1 * time.Second, End: 3 * time.Second,
		Content: cta608.Screen{Rows: []cta608.Row{{
			Index: 15, Displayed: true,
			Runs: []cta608.Run{
				{Column: 0, Text: "HELLO ", Pen: cta608.Pen{Color: cta608.White}},
				{Column: 6, Text: "RED", Pen: cta608.Pen{Color: cta608.Red}},
			},
		}}},
	}}

	var buf bytes.Buffer
	if err := webvtt.Write(&buf, cues); err != nil {
		panic(err)
	}
	fmt.Print(buf.String())

	// Output:
	// WEBVTT
	//
	// STYLE
	// ::cue(.red) { color: #ff0000; }
	//
	// 1
	// 00:00:01.000 --> 00:00:03.000 line:100% position:0% align:start
	// HELLO <c.red>RED</c>
}
