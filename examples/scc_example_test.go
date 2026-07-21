package examples

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/scc"
)

// Example_sccReadParseWrite reads a Scenarist SCC file, flattens its entries to
// per-frame byte pairs, parses those into the cta608 token stream, and writes the
// file back out byte-exact. It is the SCC container's whole job: own the text
// structure and timecodes, hand the verbatim 608 bytes to the core, and lose
// nothing on the way back.
func Example_sccReadParseWrite() {
	// A minimal SCC document: a pop-on "HELLO WORLD" caption at 00:00:01:00 and an
	// erase-displayed at 00:00:04:00. The ':' separators and low frame fields make
	// the reader infer the 29.97 NTSC default (non-drop).
	const doc = "Scenarist_SCC V1.0\n" +
		"\n" +
		"00:00:01:00\t9420 9420 94ae 94ae 94e0 94e0 c845 4c4c 4f20 574f 524c c480 942f 942f\n" +
		"\n" +
		"00:00:04:00\t942c 942c\n"

	f, err := scc.Read(strings.NewReader(doc))
	if err != nil {
		panic(err)
	}
	fmt.Printf("fps=%.2f drop=%v entries=%d\n", f.FPS, f.DropFrame, len(f.Entries))

	// Flatten to per-frame pairs (pair i of an entry sits at Frame+i) and feed the
	// concatenated channel-1 bytes to cta608.Parse for tokens with per-frame timing.
	timed := f.TimedPairs()
	fmt.Printf("first pair at frame %d\n", timed[0].Frame)
	var data []byte
	for _, p := range timed {
		data = append(data, p.Pair...)
	}
	tokens, err := cta608.Parse(data, cta608.ParseOptions{ValidateParity: true})
	if err != nil {
		panic(err)
	}
	for _, tok := range tokens {
		fmt.Println(tok)
	}

	// Write the file back and confirm the round-trip is byte-exact.
	var out bytes.Buffer
	if err := scc.Write(&out, f); err != nil {
		panic(err)
	}
	fmt.Printf("byte-exact round-trip: %v\n", out.String() == doc)

	// Output:
	// fps=29.97 drop=false entries=2
	// first pair at frame 30
	// SetMode(pop-on)
	// Command(ENM)
	// PAC(row=15 white)
	// Chars("HELLO WORLD")
	// Command(EOC)
	// Command(EDM)
	// byte-exact round-trip: true
}
