package cta608

import "testing"

// TestCharTablesComplete verifies the flat internal table covers every code in
// 0x20..0xCF exactly once and that the four sets have the expected sizes.
func TestCharTablesComplete(t *testing.T) {
	for code := 0x20; code <= 0xcf; code++ {
		if _, ok := internalToRune[byte(code)]; !ok {
			t.Errorf("internalToRune missing code %#02x", code)
		}
	}
	if len(internalToRune) != 0xcf-0x20+1 {
		t.Errorf("internalToRune has %d entries, want %d", len(internalToRune), 0xcf-0x20+1)
	}
}

// TestReverseIsInverse verifies that decoding then re-encoding a rune yields the
// same internal code, except where two codes share a glyph (the transparent
// space 0x89 collapses onto the standard space 0x20).
func TestReverseIsInverse(t *testing.T) {
	for code := 0x20; code <= 0xcf; code++ {
		r := internalToRune[byte(code)]
		got := runeToInternal[r]
		if byte(code) == 0x89 { // transparent space -> standard space
			if got != 0x20 {
				t.Errorf("rune for 0x89 (%q) reverses to %#02x, want 0x20", r, got)
			}
			continue
		}
		if got != byte(code) {
			t.Errorf("code %#02x -> %q -> %#02x (reverse not inverse)", code, r, got)
		}
	}
}

// TestKnownGlyphs spot-checks a handful of glyphs across the four sets.
func TestKnownGlyphs(t *testing.T) {
	cases := []struct {
		code byte
		r    rune
	}{
		{0x41, 'A'}, // standard ASCII
		{0x2a, 'á'}, // standard substitution
		{0x7f, '█'}, // standard full block
		{0x80, '®'}, // special
		{0x87, '♪'}, // special
		{0x90, 'Á'}, // extended A
		{0xa0, 'À'}, // extended A
		{0xb0, 'Ã'}, // extended B
		{0xc4, 'ß'}, // extended B
		{0xcf, '┛'}, // extended B box drawing
	}
	for _, c := range cases {
		if internalToRune[c.code] != c.r {
			t.Errorf("internalToRune[%#02x] = %q, want %q", c.code, internalToRune[c.code], c.r)
		}
	}
}

// TestExtFallbacksAreStandard verifies every extended-char fallback is itself a
// standard-set rune (so it packs as a plain byte, never recursing).
func TestExtFallbacksAreStandard(t *testing.T) {
	for ic := 0x90; ic <= 0xcf; ic++ {
		r, ok := extFallback[byte(ic)]
		if !ok {
			t.Errorf("extended code %#02x has no fallback", ic)
			continue
		}
		b, ok := runeToInternal[r]
		if !ok || b > 0x7f {
			t.Errorf("fallback %q for %#02x is not a standard-set char (byte %#02x)", r, ic, b)
		}
	}
}

// TestPACRowTablesInvertible verifies the PAC row encode/decode tables are
// mutual inverses across all 15 rows.
func TestPACRowTablesInvertible(t *testing.T) {
	for row := 1; row <= 15; row++ {
		rc := pacRowEncode[row]
		if got := pacRowDecode[rc]; got != row {
			t.Errorf("row %d encodes to %+v which decodes to %d", row, rc, got)
		}
	}
}
