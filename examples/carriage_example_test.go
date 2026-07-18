package examples_test

import (
	"fmt"

	"github.com/Eyevinn/go-608/carriage"
)

// Example_carriageFrameSEINALU builds one video frame's CEA-608 SEI NAL unit from a
// field-1 byte pair and parses it straight back, recovering the same pair. This is
// the encode → decode round-trip a consumer performs per frame: it prepends a 4-byte
// length to the bare NAL and splices it before the first VCL NALU on the way out,
// and hands a sample's NAL units to FieldPairs on the way in.
func Example_carriageFrameSEINALU() {
	// One field-1 byte pair (here the Resume Caption Loading control code, odd
	// parity already applied) and no field-2 data. ccCount 20 matches a 29.97/30 fps
	// frame (SPEC §5.3); the remaining constructs are DTVCC padding.
	field1 := []byte{0x94, 0x20}

	nalu := carriage.FrameSEINALU(field1, nil, 20, carriage.CodecAVC)
	fmt.Printf("SEI NAL unit: %d bytes, header %#02x\n", len(nalu), nalu[0])

	// Decode: hand the NAL back as one of a sample's NAL units.
	f1, f2, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
	if err != nil {
		panic(err)
	}
	fmt.Printf("field1: % x\n", f1)
	fmt.Printf("field2 empty: %t\n", len(f2) == 0)

	// Output:
	// SEI NAL unit: 75 bytes, header 0x06
	// field1: 94 20
	// field2 empty: true
}
