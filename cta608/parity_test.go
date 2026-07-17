package cta608

import (
	"math/bits"
	"testing"
)

func TestOddParitySetsHighBitForEvenPopcount(t *testing.T) {
	for v := range 0x80 {
		got := oddParity(byte(v))
		if bits.OnesCount8(got)%2 != 1 {
			t.Fatalf("oddParity(%#02x) = %#02x: popcount not odd", v, got)
		}
		if got&0x7f != byte(v) {
			t.Fatalf("oddParity(%#02x) = %#02x: low 7 bits changed", v, got)
		}
	}
}

func TestOddParityKnownValues(t *testing.T) {
	cases := []struct{ in, want byte }{
		{0x00, 0x80}, // null pad: 0 bits (even) -> set parity bit
		{0x41, 0xc1}, // 'A' 0b0100_0001, 2 bits (even) -> set parity bit
		{0x0f, 0x8f}, // 4 bits (even) -> set parity bit
		{0x7f, 0x7f}, // 7 bits (odd) -> unchanged
		{0x20, 0x20}, // space 0b0010_0000, 1 bit (odd) -> unchanged
	}
	for _, c := range cases {
		if got := oddParity(c.in); got != c.want {
			t.Errorf("oddParity(%#02x) = %#02x, want %#02x", c.in, got, c.want)
		}
	}
}

func TestHasOddParity(t *testing.T) {
	for v := range 0x100 {
		want := bits.OnesCount8(byte(v))%2 == 1
		if got := hasOddParity(byte(v)); got != want {
			t.Errorf("hasOddParity(%#02x) = %v, want %v", v, got, want)
		}
	}
}
