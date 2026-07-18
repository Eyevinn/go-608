package carriage

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/sei"
)

// A real field-1 pair (RCL control code) and a plausible field-2 pair.
var (
	f1Pair = []byte{0x94, 0x20}
	f2Pair = []byte{0x15, 0x2c}
)

func TestBuildCCDataHeaderAndCount(t *testing.T) {
	const ccCount = 20
	cc := BuildCCData(f1Pair, nil, ccCount)

	// cc_count byte: process_em_data_flag=1, process_cc_data_flag=1,
	// additional_data_flag=0, cc_count in the low 5 bits.
	if got := cc[0]; got != 0xc0|ccCount {
		t.Errorf("cc_count byte = %#02x, want %#02x", got, 0xc0|ccCount)
	}
	if cc[1] != 0xff {
		t.Errorf("em_data byte = %#02x, want 0xff", cc[1])
	}
	// 2 byte header + ccCount*3 construct bytes + 1 trailing marker.
	wantLen := 2 + ccCount*3 + 1
	if len(cc) != wantLen {
		t.Fatalf("len(cc_data) = %d, want %d", len(cc), wantLen)
	}
	if last := cc[len(cc)-1]; last != 0xff {
		t.Errorf("trailing marker = %#02x, want 0xff", last)
	}
}

// The two "nothing here" encodings, plus the 0x8080 null pair, must be distinct
// (acceptance criterion 3).
func TestBuildCCDataDistinctEncodings(t *testing.T) {
	const ccCount = 4
	// field1 = valid real pair, field2 = empty ("no waveform this field").
	cc := BuildCCData(f1Pair, nil, ccCount)
	constructs := cc[2 : len(cc)-1]

	// construct 0: field 1, cc_valid=1, cc_type=00 -> 0xfc + pair bytes.
	if got := constructs[0:3]; !bytes.Equal(got, []byte{0xfc, 0x94, 0x20}) {
		t.Errorf("field-1 valid construct = % x, want fc 94 20", got)
	}
	// construct 1: field 2 empty, cc_valid=0, cc_type=01 -> 0xf9 00 00.
	if got := constructs[3:6]; !bytes.Equal(got, []byte{0xf9, 0x00, 0x00}) {
		t.Errorf("field-2 empty construct = % x, want f9 00 00", got)
	}
	// constructs 2,3: DTVCC padding, cc_valid=0, cc_type=10 -> 0xfa 00 00.
	for i := 2; i < ccCount; i++ {
		if got := constructs[i*3 : i*3+3]; !bytes.Equal(got, []byte{0xfa, 0x00, 0x00}) {
			t.Errorf("padding construct %d = % x, want fa 00 00", i, got)
		}
	}

	// The 0x8080 608 null pair is cc_valid=1 (a live no-op), NOT padding and NOT
	// an empty field.
	ccNull := BuildCCData([]byte{0x80, 0x80}, nil, ccCount)
	if got := ccNull[2:5]; !bytes.Equal(got, []byte{0xfc, 0x80, 0x80}) {
		t.Errorf("0x8080 null-pair construct = % x, want fc 80 80", got)
	}
	// It must differ from both the empty-field and the padding encodings.
	if bytes.Equal(ccNull[2:5], []byte{0xf8, 0x00, 0x00}) {
		t.Error("0x8080 null pair conflated with empty field")
	}
	if bytes.Equal(ccNull[2:5], []byte{0xfa, 0x00, 0x00}) {
		t.Error("0x8080 null pair conflated with DTVCC padding")
	}
}

func TestBuildCCDataFieldTypes(t *testing.T) {
	cc := BuildCCData(f1Pair, f2Pair, 10)
	// field1 valid -> cc_type 00 (0xfc); field2 valid -> cc_type 01 (0xfd).
	if cc[2] != 0xfc {
		t.Errorf("field-1 construct byte = %#02x, want 0xfc", cc[2])
	}
	if cc[5] != 0xfd {
		t.Errorf("field-2 construct byte = %#02x, want 0xfd", cc[5])
	}
}

// Round-trip through mp4ff's parser (acceptance criterion 1). Uses non-null
// pairs, since ParseCEA608 filters out pairs whose low-7-bits are all zero.
func TestBuildCCDataRoundTripParseCEA608(t *testing.T) {
	cc := BuildCCData(f1Pair, f2Pair, 20)
	got1, got2, err := sei.ParseCEA608(cc)
	if err != nil {
		t.Fatalf("ParseCEA608: %v", err)
	}
	if !bytes.Equal(got1, f1Pair) {
		t.Errorf("field1 = % x, want % x", got1, f1Pair)
	}
	if !bytes.Equal(got2, f2Pair) {
		t.Errorf("field2 = % x, want % x", got2, f2Pair)
	}
}

func TestBuildCCDataPanicsOnBadInput(t *testing.T) {
	cases := []struct {
		name    string
		f1, f2  []byte
		ccCount int
	}{
		{"odd field length", []byte{0x94}, nil, 10},
		{"ccCount too small", f1Pair, f2Pair, 1},
		{"ccCount too large", f1Pair, nil, 32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("expected panic for %s", c.name)
				}
			}()
			BuildCCData(c.f1, c.f2, c.ccCount)
		})
	}
}

func TestSEINALURoundTrip(t *testing.T) {
	cc := BuildCCData(f1Pair, f2Pair, 20)
	for _, tc := range []struct {
		name  string
		codec Codec
	}{
		{"AVC", CodecAVC},
		{"HEVC", CodecHEVC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nalu := NALU(tc.codec, SEIMessage(cc))

			var msgs []sei.SEIMessage
			var err error
			switch tc.codec {
			case CodecAVC:
				if avc.GetNaluType(nalu[0]) != avc.NALU_SEI {
					t.Fatalf("NAL type = %s, want SEI", avc.GetNaluType(nalu[0]))
				}
				msgs, err = avc.ParseSEINalu(nalu, nil)
			case CodecHEVC:
				if hevc.GetNaluType(nalu[0]) != hevc.NALU_SEI_PREFIX {
					t.Fatalf("NAL type = %s, want prefix SEI", hevc.GetNaluType(nalu[0]))
				}
				msgs, err = hevc.ParseSEINalu(nalu, nil)
			}
			if err != nil {
				t.Fatalf("ParseSEINalu: %v", err)
			}
			f1, f2 := cea608FromMsgs(t, msgs)
			if !bytes.Equal(f1, f1Pair) || !bytes.Equal(f2, f2Pair) {
				t.Errorf("round-trip fields = % x / % x, want % x / % x", f1, f2, f1Pair, f2Pair)
			}
		})
	}
}

func TestFrameSEINALUMatchesComposition(t *testing.T) {
	want := NALU(CodecAVC, SEIMessage(BuildCCData(f1Pair, f2Pair, 20)))
	got := FrameSEINALU(f1Pair, f2Pair, 20, CodecAVC)
	if !bytes.Equal(got, want) {
		t.Errorf("FrameSEINALU != NALU(codec, SEIMessage(BuildCCData(...)))")
	}
}

// NALU can place a 608 message alongside another SEI message in one NAL unit, and
// FieldPairs still recovers the 608 pairs.
func TestNALUMultipleMessagesInOneNALU(t *testing.T) {
	cc := SEIMessage(BuildCCData(f1Pair, f2Pair, 20))
	// An unregistered (type 5) SEI message sharing the NAL unit.
	other := sei.NewSEIData(sei.SEIUserDataUnregisteredType, bytes.Repeat([]byte{0xa5}, 20))

	nalu := NALU(CodecAVC, other, cc)

	// The single NAL unit carries both messages.
	msgs, err := avc.ParseSEINalu(nalu, nil)
	if err != nil {
		t.Fatalf("ParseSEINalu: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d SEI messages in the NAL unit, want 2", len(msgs))
	}

	// FieldPairs ignores the other message and recovers the 608 pairs.
	f1, f2, err := FieldPairs([][]byte{nalu}, CodecAVC)
	if err != nil {
		t.Fatalf("FieldPairs: %v", err)
	}
	if !bytes.Equal(f1, f1Pair) || !bytes.Equal(f2, f2Pair) {
		t.Errorf("fields = % x / % x, want % x / % x", f1, f2, f1Pair, f2Pair)
	}
}

func TestFieldPairsFromSample(t *testing.T) {
	// Assemble a sample-like NALU list: a dummy VCL NALU plus our SEI NALU.
	seiNALU := FrameSEINALU(f1Pair, f2Pair, 20, CodecAVC)
	vcl := []byte{0x65, 0x88, 0x84, 0x00} // IDR-ish filler, ignored by FieldPairs
	f1, f2, err := FieldPairs([][]byte{vcl, seiNALU}, CodecAVC)
	if err != nil {
		t.Fatalf("FieldPairs: %v", err)
	}
	if !bytes.Equal(f1, f1Pair) {
		t.Errorf("field1 = % x, want % x", f1, f1Pair)
	}
	if !bytes.Equal(f2, f2Pair) {
		t.Errorf("field2 = % x, want % x", f2, f2Pair)
	}
}

// A sample often carries other SEI message types (pic_timing is the common one);
// they must not abort 608 extraction.
func TestFieldPairsIgnoresOtherSEITypes(t *testing.T) {
	// A pic_timing SEI (payloadType 1) whose payload mp4ff cannot decode without an
	// SPS (pict_struct 15 is invalid) — decoding it would error.
	picTiming := []byte{0x06, 0x01, 0x01, 0xf0, 0x80}
	cea := FrameSEINALU(f1Pair, nil, 20, CodecAVC)

	f1, f2, err := FieldPairs([][]byte{picTiming, cea}, CodecAVC)
	if err != nil {
		t.Fatalf("FieldPairs: %v", err)
	}
	if !bytes.Equal(f1, f1Pair) {
		t.Errorf("field1 = % x, want % x", f1, f1Pair)
	}
	if len(f2) != 0 {
		t.Errorf("field2 = % x, want empty", f2)
	}
}

// A truncated type-4 (T.35) SEI payload must be skipped, not panic mp4ff's decoder
// (sei.DecodeUserDataRegisteredSEI reads payload[0:8] unconditionally).
func TestFieldPairsShortT35NoPanic(t *testing.T) {
	// AVC SEI NALU: header 0x06, one type-4 message with a 3-byte payload, trailing bits.
	shortT35 := []byte{0x06, 0x04, 0x03, 0xb5, 0x00, 0x31, 0x80}
	f1, f2, err := FieldPairs([][]byte{shortT35}, CodecAVC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f1) != 0 || len(f2) != 0 {
		t.Errorf("fields = % x / % x, want empty", f1, f2)
	}
}

// A malformed NAL unit shorter than its header must be skipped, not panic.
func TestFieldPairsShortNALUNoPanic(t *testing.T) {
	avcShort := []byte{0x06}  // AVC SEI header, no payload
	hevcShort := []byte{0x4e} // HEVC prefix-SEI, only the first header byte
	if _, _, err := FieldPairs([][]byte{avcShort}, CodecAVC); err != nil {
		t.Errorf("AVC short NALU: unexpected error %v", err)
	}
	if _, _, err := FieldPairs([][]byte{hevcShort}, CodecHEVC); err != nil {
		t.Errorf("HEVC short NALU: unexpected error %v", err)
	}
}

func cea608FromMsgs(t *testing.T, msgs []sei.SEIMessage) (f1, f2 []byte) {
	t.Helper()
	for _, m := range msgs {
		if cea, ok := m.(*sei.CEA608sei); ok {
			f1 = append(f1, cea.Field1...)
			f2 = append(f2, cea.Field2...)
		}
	}
	return f1, f2
}
