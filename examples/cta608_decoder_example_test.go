package examples_test

import (
	"fmt"

	"github.com/Eyevinn/go-608/cta608"
)

// Example_cta608Decode authors a two-line pop-on caption, serializes it to
// cc_data byte pairs, then feeds those bytes to a Decoder to recover the rendered
// Screen — the encode → wire → decode round-trip the core is built around.
func Example_cta608Decode() {
	var enc cta608.Encoder
	toks := enc.SetScreen(cta608.Screen{Rows: []cta608.Row{
		{Index: 14, Runs: []cta608.Run{{Column: 0, Text: "HELLO", Pen: cta608.Pen{Color: cta608.White}}}},
		{Index: 15, Runs: []cta608.Run{{Column: 0, Text: "WORLD", Pen: cta608.Pen{Color: cta608.Yellow}}}},
	}})
	data := cta608.Serialize(toks, cta608.SerializeOptions{})

	var dec cta608.Decoder
	if err := dec.Feed(data); err != nil {
		panic(err)
	}
	for _, r := range dec.Screen().Rows {
		for _, run := range r.Runs {
			fmt.Printf("row %d col %d %s: %q\n", r.Index, run.Column, run.Pen.Color, run.Text)
		}
	}
	// Output:
	// row 14 col 0 white: "HELLO"
	// row 15 col 0 yellow: "WORLD"
}
