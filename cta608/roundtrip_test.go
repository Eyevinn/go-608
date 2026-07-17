package cta608

import (
	"reflect"
	"testing"
)

// tokensEqual compares two token slices for round-trip assertions.
func tokensEqual(a, b []Token) bool {
	return reflect.DeepEqual(a, b)
}

func tokStr(toks []Token) string {
	s := "["
	for i, t := range toks {
		if i > 0 {
			s += ", "
		}
		s += t.String()
	}
	return s + "]"
}

// roundTripCases covers every token kind and variant. Colors are given as
// White (not ColDefault) and indent PACs as white, matching the wire, so the
// tokens come back identical.
func roundTripCases() []struct {
	name   string
	tokens []Token
} {
	var cases []struct {
		name   string
		tokens []Token
	}
	add := func(name string, toks ...Token) {
		cases = append(cases, struct {
			name   string
			tokens []Token
		}{name, toks})
	}

	// Characters
	add("chars-basic", Chars{"Hello, World!"})
	add("chars-digits-punct", Chars{"12:34 - (a=b)/c;"})
	add("chars-basic-accents", Chars{"café résumé señor ñandú"})
	add("chars-special", Chars{"♪ ½ ® ° ¿ ¢ £ à"})
	add("chars-special-adjacent", Chars{"♪♪"})
	add("chars-extended-A", Chars{"ÀÉÓÜ«»"})
	add("chars-extended-B", Chars{"ÃØ{}\\|"})
	add("chars-mixed-sets", Chars{"A♪ÀzÃ9"})

	// PAC — colors on representative rows
	add("pac-white-row1", PAC{Row: 1, Indent: NoIndent, Pen: Pen{Color: White}})
	add("pac-magenta-ul-row15", PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: Magenta, Underline: true}})
	add("pac-cyan-row11", PAC{Row: 11, Indent: NoIndent, Pen: Pen{Color: Cyan}})
	add("pac-italic-row5", PAC{Row: 5, Indent: NoIndent, Pen: Pen{Color: White, Italic: true}})
	for _, col := range []Color{White, Green, Blue, Cyan, Red, Yellow, Magenta} {
		add("pac-color-"+col.String(), PAC{Row: 4, Indent: NoIndent, Pen: Pen{Color: col}})
	}

	// PAC — every indent (0..28 step 4); indent forces white
	for ind := 0; ind <= 28; ind += 4 {
		add("pac-indent", PAC{Row: 14, Indent: ind, Pen: Pen{Color: White, Underline: ind%8 == 0}})
	}

	// Mid-row
	add("midrow-green", MidRow{Pen: Pen{Color: Green}})
	add("midrow-red-ul", MidRow{Pen: Pen{Color: Red, Underline: true}})
	add("midrow-italic-ul", MidRow{Pen: Pen{Color: White, Italic: true, Underline: true}})

	// Tab offset
	add("tab-1", TabOffset{Columns: 1})
	add("tab-2", TabOffset{Columns: 2})
	add("tab-3", TabOffset{Columns: 3})

	// Background / foreground-black attributes
	add("bg-blue", BackgroundAttr{Pen: Pen{Background: Blue}})
	add("bg-black", BackgroundAttr{Pen: Pen{Background: Black}})
	add("bg-transparent", BackgroundAttr{Pen: Pen{Background: Transparent}})
	add("fg-black", BackgroundAttr{Pen: Pen{Color: Black}})
	add("fg-black-ul", BackgroundAttr{Pen: Pen{Color: Black, Underline: true}})

	// Mode switches
	add("mode-popon", SetMode{Mode: PopOn})
	add("mode-ru2", SetMode{Mode: RollUp, RollUpRows: 2})
	add("mode-ru3", SetMode{Mode: RollUp, RollUpRows: 3})
	add("mode-ru4", SetMode{Mode: RollUp, RollUpRows: 4})
	add("mode-painton", SetMode{Mode: PaintOn})

	// Every miscellaneous command
	for _, op := range []Op{EOC, EDM, ENM, CR, BS, DER, TR, RTD, FON, AOF, AON} {
		add("cmd-"+op.String(), Command{Op: op})
	}

	// Realistic multi-token sequences
	add("popon-caption",
		SetMode{Mode: PopOn},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"HELLO"},
		MidRow{Pen: Pen{Color: Red}},
		Chars{"WORLD"},
		Command{Op: EOC},
	)
	add("rollup-caption",
		SetMode{Mode: RollUp, RollUpRows: 3},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"live line"},
		Command{Op: CR},
	)
	add("centered-colored-line",
		PAC{Row: 14, Indent: 8, Pen: Pen{Color: White}},
		TabOffset{Columns: 2},
		MidRow{Pen: Pen{Color: Yellow}},
		Chars{"2026-07-17"},
	)
	add("dangling-char-before-ctrl",
		Chars{"ABC"}, // odd length -> a dangling char flushed before EOC
		Command{Op: EOC},
	)
	add("extended-after-pac",
		PAC{Row: 3, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"À la"},
	)

	return cases
}

func TestRoundTrip(t *testing.T) {
	opts := []struct {
		name string
		s    SerializeOptions
		p    ParseOptions
	}{
		{"field1-default-doubling-on", SerializeOptions{}, ParseOptions{}},
		{"field1-doubling-off", SerializeOptions{Doubling: DoublingOff}, ParseOptions{}},
		{"field2-default-doubling-off", SerializeOptions{Field: 2}, ParseOptions{}},
		{"field1-validate-parity", SerializeOptions{}, ParseOptions{ValidateParity: true}},
		{"channel2", SerializeOptions{Channel: 2}, ParseOptions{}},
	}
	for _, tc := range roundTripCases() {
		for _, o := range opts {
			t.Run(tc.name+"/"+o.name, func(t *testing.T) {
				data := Serialize(tc.tokens, o.s)
				if len(data)%2 != 0 {
					t.Fatalf("serialized length %d is odd (not whole pairs)", len(data))
				}
				got, err := Parse(data, o.p)
				if err != nil {
					t.Fatalf("Parse error: %v", err)
				}
				if !tokensEqual(got, tc.tokens) {
					t.Fatalf("round-trip mismatch\n got: %s\nwant: %s\nbytes: % x",
						tokStr(got), tokStr(tc.tokens), data)
				}
			})
		}
	}
}
