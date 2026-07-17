package examples

import (
	"fmt"

	"github.com/Eyevinn/go-608/cta608"
)

// Example_cta608RoundTrip builds a pop-on caption as a token stream, serializes
// it to odd-parity cc_data byte pairs, and parses it straight back — the core
// wire round-trip the whole library pivots on.
func Example_cta608RoundTrip() {
	tokens := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HELLO"},
		cta608.MidRow{Pen: cta608.Pen{Color: cta608.Red}},
		cta608.Chars{Text: "WORLD"},
		cta608.Command{Op: cta608.EOC},
	}

	// Field 1, channel 1, control-code doubling on (the standard default).
	data := cta608.Serialize(tokens, cta608.SerializeOptions{})
	fmt.Printf("cc_data (%d bytes): % x\n", len(data), data)

	back, err := cta608.Parse(data, cta608.ParseOptions{})
	if err != nil {
		panic(err)
	}
	for _, tok := range back {
		fmt.Println(tok)
	}

	// Output:
	// cc_data (28 bytes): 94 20 94 20 94 e0 94 e0 c8 45 4c 4c 4f 80 91 a8 91 a8 57 4f 52 4c c4 80 94 2f 94 2f
	// SetMode(pop-on)
	// PAC(row=15 white)
	// Chars("HELLO")
	// MidRow(red)
	// Chars("WORLD")
	// Command(EOC)
}
