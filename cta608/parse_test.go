package cta608

import (
	"errors"
	"testing"
)

func TestParseValidateParityRejectsBadByte(t *testing.T) {
	data := Serialize([]Token{Chars{"Hi"}}, SerializeOptions{Doubling: DoublingOff})
	bad := make([]byte, len(data))
	copy(bad, data)
	bad[0] ^= 0x01 // flip a low bit -> parity flips from odd to even

	_, err := Parse(bad, ParseOptions{ValidateParity: true})
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %v", err)
	}
	if pe.Offset != 0 {
		t.Errorf("ParseError.Offset = %d, want 0", pe.Offset)
	}

	// Without validation the same bytes parse without error (parity stripped).
	if _, err := Parse(bad, ParseOptions{}); err != nil {
		t.Errorf("strip mode should not error, got %v", err)
	}
}

func TestParseCollapsesDoubledControlCodes(t *testing.T) {
	// Two identical adjacent EOC pairs -> one Command{EOC}.
	two := []byte{0x94, 0x2f, 0x94, 0x2f}
	got, err := Parse(two, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(got, []Token{Command{Op: EOC}}) {
		t.Fatalf("doubled EOC: got %s, want [Command(EOC)]", tokStr(got))
	}

	// Three identical pairs -> two commands (first honored, second collapsed,
	// third honored) per CTA-608-E §B.14.
	three := []byte{0x94, 0x2f, 0x94, 0x2f, 0x94, 0x2f}
	got, err = Parse(three, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(got, []Token{Command{Op: EOC}, Command{Op: EOC}}) {
		t.Fatalf("tripled EOC: got %s, want two EOCs", tokStr(got))
	}
}

func TestParseDoesNotCollapseAdjacentSpecialChars(t *testing.T) {
	// Two identical special-char pairs are character data, not a doubled
	// control code: "♪♪" must survive.
	data := []byte{0x91, 0x37, 0x91, 0x37} // (0x11,0x37) x2 -> ♪♪
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(got, []Token{Chars{"♪♪"}}) {
		t.Fatalf("got %s, want [Chars(\"♪♪\")]", tokStr(got))
	}
}

func TestParseSkipsNullPairs(t *testing.T) {
	// A 608 null pair 0x80 0x80 (masks to 0x00 0x00) is a no-op.
	data := []byte{0x80, 0x80}
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("null pair produced tokens: %s", tokStr(got))
	}
}

func TestParseSkipsFieldTwoXDS(t *testing.T) {
	// First byte 0x01..0x0F is field-2 / XDS, out of scope: skipped.
	data := []byte{0x01, 0x82} // 0x01 odd parity, 0x02->0x82
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("XDS produced tokens: %s", tokStr(got))
	}
}

func TestParseOddLengthTrailingByte(t *testing.T) {
	// A trailing lone byte is treated as (b, 0): 'A' with an implicit pad.
	data := []byte{0xc1} // 'A' with odd parity, no partner
	got, err := Parse(data, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(got, []Token{Chars{"A"}}) {
		t.Fatalf("got %s, want [Chars(\"A\")]", tokStr(got))
	}
}
