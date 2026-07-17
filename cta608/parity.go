package cta608

import "math/bits"

// oddParity forces b to odd parity: it keeps the low 7 bits and sets bit 7 iff
// that makes the total number of set bits odd (CTA-608-E §D.2 — all transmitted
// bytes shall be odd parity). A null low byte 0x00 becomes 0x80.
func oddParity(b byte) byte {
	v := b & 0x7f
	if bits.OnesCount8(v)%2 == 0 {
		return v | 0x80
	}
	return v
}

// hasOddParity reports whether b already has odd parity (an odd number of set
// bits across all 8 bits). Parse uses it when ParseOptions.ValidateParity is set.
func hasOddParity(b byte) bool {
	return bits.OnesCount8(b)%2 == 1
}
