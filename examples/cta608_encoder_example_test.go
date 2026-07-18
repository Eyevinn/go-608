package examples

import (
	"fmt"

	"github.com/Eyevinn/go-608/cta608"
)

// Example_cta608Encoder authors a two-line, bottom-anchored, centered pop-on
// caption as a CaptionBlock, then lets the Encoder diff it against the empty
// display into a token stream. The Encoder is the single per-channel diff
// engine: it lowers each line's absolute columns to PAC indent + Tab Offset and
// compensates the colored line's mid-row cell (SPEC §7), and wraps the build in
// RCL/ENM … EOC. The tokens then serialize to odd-parity cc_data byte pairs.
func Example_cta608Encoder() {
	block := cta608.CaptionBlock{
		Mode:   cta608.PopOn,
		Anchor: cta608.AnchorBottom,
		Lines: []cta608.Line{
			{Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: "HELLO", Pen: cta608.Pen{Color: cta608.White}}}},
			{Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: "WORLD", Pen: cta608.Pen{Color: cta608.Yellow}}}},
		},
	}

	var enc cta608.Encoder // zero value: pop-on, empty display
	tokens := enc.Apply(block)
	for _, tok := range tokens {
		fmt.Println(tok)
	}

	// Serialize with control-code doubling off for a compact, deterministic
	// pair sequence.
	data := cta608.Serialize(tokens, cta608.SerializeOptions{Doubling: cta608.DoublingOff})
	fmt.Printf("cc_data (%d bytes): % x\n", len(data), data)

	// Output:
	// SetMode(pop-on)
	// Command(ENM)
	// PAC(row=14 indent=12 white)
	// Tab(1)
	// Chars("HELLO")
	// PAC(row=15 indent=12 white)
	// MidRow(yellow)
	// Chars("WORLD")
	// Command(EOC)
	// cc_data (26 bytes): 94 20 94 ae 94 d6 97 a1 c8 45 4c 4c 4f 80 94 76 91 2a 57 4f 52 4c c4 80 94 2f
}
