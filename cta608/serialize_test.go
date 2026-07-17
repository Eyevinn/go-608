package cta608

import (
	"bytes"
	"testing"
)

// TestSerializeAllBytesOddParity checks criterion "every emitted byte has odd
// parity" across every fixture and option combination.
func TestSerializeAllBytesOddParity(t *testing.T) {
	optSet := []SerializeOptions{
		{},
		{Field: 2},
		{Channel: 2},
		{Doubling: DoublingOn},
		{Doubling: DoublingOff},
	}
	for _, tc := range roundTripCases() {
		for _, o := range optSet {
			data := Serialize(tc.tokens, o)
			for i, b := range data {
				if !hasOddParity(b) {
					t.Errorf("%s opts=%+v: byte %d = %#02x lacks odd parity", tc.name, o, i, b)
				}
			}
		}
	}
}

func TestDoublingDefaultsAndOverrides(t *testing.T) {
	eoc := []Token{Command{Op: EOC}}
	single := []byte{0x94, 0x2f} // (0x14,0x2f) forced to odd parity
	double := []byte{0x94, 0x2f, 0x94, 0x2f}

	cases := []struct {
		name string
		opts SerializeOptions
		want []byte
	}{
		{"field1-default-doubles", SerializeOptions{}, double},
		{"field1-explicit-doubles", SerializeOptions{Field: 1}, double},
		{"field2-default-single", SerializeOptions{Field: 2}, single},
		{"doubling-on-overrides-field2", SerializeOptions{Field: 2, Doubling: DoublingOn}, double},
		{"doubling-off-overrides-field1", SerializeOptions{Field: 1, Doubling: DoublingOff}, single},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Serialize(eoc, c.opts)
			if !bytes.Equal(got, c.want) {
				t.Errorf("Serialize(EOC, %+v) = % x, want % x", c.opts, got, c.want)
			}
		})
	}
}

// TestFrameAlignment verifies a dangling character is flushed to its own padded
// pair so a following two-byte control code never straddles a frame pair.
func TestFrameAlignment(t *testing.T) {
	data := Serialize([]Token{Chars{"A"}, Command{Op: EOC}}, SerializeOptions{Doubling: DoublingOff})
	// 'A'=0x41 -> 0xc1; pad 0x00 -> 0x80; EOC (0x14,0x2f) -> (0x94,0x2f)
	want := []byte{0xc1, 0x80, 0x94, 0x2f}
	if !bytes.Equal(data, want) {
		t.Fatalf("got % x, want % x", data, want)
	}
	// The EOC pair starts at an even offset (a whole frame pair).
	if len(data)%2 != 0 {
		t.Fatalf("output length %d not a whole number of pairs", len(data))
	}
}

func TestChannel2SetsHighNibble(t *testing.T) {
	// EOC on channel 2 uses first byte 0x1C (0x14+8).
	got := Serialize([]Token{Command{Op: EOC}}, SerializeOptions{Channel: 2, Doubling: DoublingOff})
	want := []byte{0x1c, 0x2f} // 0x1c already has odd parity (3 bits set); 0x2f likewise
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestUnknownRuneBecomesQuestionMark(t *testing.T) {
	data := Serialize([]Token{Chars{"☃"}}, SerializeOptions{Doubling: DoublingOff}) // snowman ☃
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Token{Chars{"?"}}
	if !tokensEqual(got, want) {
		t.Fatalf("unknown rune round-trip: got %s, want %s", tokStr(got), tokStr(want))
	}
}

// TestExtendedCharBackspaceAndReplace verifies the exact wire shape of an
// extended character: a standard fallback char followed by the two-byte code.
func TestExtendedCharBackspaceAndReplace(t *testing.T) {
	data := Serialize([]Token{Chars{"À"}}, SerializeOptions{Doubling: DoublingOff})
	// fallback 'A' (0x41->0xc1) padded (0x80), then extended code (0x12, 0x30)
	// 0x12 -> 0x92, 0x30 -> 0xb0
	want := []byte{0xc1, 0x80, 0x92, 0xb0}
	if !bytes.Equal(data, want) {
		t.Fatalf("got % x, want % x", data, want)
	}
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(got, []Token{Chars{"À"}}) {
		t.Fatalf("extended round-trip: got %s", tokStr(got))
	}
}
