package carriage

import (
	"bytes"
	"testing"
)

func TestPrefixSampleRoundTrip(t *testing.T) {
	nalus := [][]byte{
		{0x67, 0x64, 0x00, 0x20}, // SPS-ish
		{0x68, 0xb5, 0xdf},       // PPS-ish
		{0x65, 0x88, 0x84, 0x00}, // IDR-ish
	}
	sample := PrefixNALUs(nalus...)
	got, err := SampleNALUs(sample)
	if err != nil {
		t.Fatalf("SampleNALUs: %v", err)
	}
	if len(got) != len(nalus) {
		t.Fatalf("got %d NAL units, want %d", len(got), len(nalus))
	}
	for i := range nalus {
		if !bytes.Equal(got[i], nalus[i]) {
			t.Errorf("NAL %d = % x, want % x", i, got[i], nalus[i])
		}
	}
}

func TestSampleNALUsErrors(t *testing.T) {
	cases := []struct {
		name   string
		sample []byte
	}{
		{"length overruns sample", []byte{0x00, 0x00, 0x00, 0x08, 0x65, 0x88}},
		{"truncated length prefix", []byte{0x00, 0x00}},
		{"zero-length NAL unit", []byte{0x00, 0x00, 0x00, 0x00}},
		{"trailing partial prefix", []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x00, 0x00}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SampleNALUs(c.sample); err == nil {
				t.Errorf("expected an error for %s, got nil", c.name)
			}
		})
	}
}

func TestSampleNALUsEmpty(t *testing.T) {
	got, err := SampleNALUs(nil)
	if err != nil {
		t.Fatalf("SampleNALUs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d NAL units from an empty sample, want 0", len(got))
	}
}

func TestSpliceSEIBeforeVCL(t *testing.T) {
	seiNALU := []byte{0x06, 0xaa, 0xbb} // AVC SEI NAL header (type 6) + payload
	cases := []struct {
		name    string
		codec   Codec
		nalus   [][]byte
		wantIdx int // index the SEI should land at in the result
	}{
		{
			name:    "avc sps then idr",
			codec:   CodecAVC,
			nalus:   [][]byte{{0x67, 0x10}, {0x65, 0x88}}, // SPS(7), IDR(5)
			wantIdx: 1,
		},
		{
			name:    "avc non-idr first",
			codec:   CodecAVC,
			nalus:   [][]byte{{0x41, 0x9a}}, // non-IDR(1)
			wantIdx: 0,
		},
		{
			name:    "avc aud sps pps then idr",
			codec:   CodecAVC,
			nalus:   [][]byte{{0x09, 0x10}, {0x67, 0x10}, {0x68, 0x20}, {0x65, 0x88}}, // AUD, SPS, PPS, IDR
			wantIdx: 3,
		},
		{
			name:    "hevc vps then idr",
			codec:   CodecHEVC,
			nalus:   [][]byte{{0x40, 0x01}, {0x26, 0x01}}, // VPS(32), IDR_W_RADL(19)
			wantIdx: 1,
		},
		{
			// No picture to precede: the SEI goes last so the existing NAL order —
			// here a parameter-set-only sample — is left untouched.
			name:    "no vcl -> sei last",
			codec:   CodecAVC,
			nalus:   [][]byte{{0x67, 0x10}, {0x68, 0x20}}, // SPS, PPS only
			wantIdx: 2,
		},
		{
			// An SEI already in the sample is preserved, and ours still lands ahead
			// of the picture.
			name:    "existing sei preserved",
			codec:   CodecAVC,
			nalus:   [][]byte{{0x06, 0x01, 0x02}, {0x65, 0x88}}, // SEI, IDR
			wantIdx: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := PrefixNALUs(c.nalus...)
			out, err := SpliceSEIBeforeVCL(sample, seiNALU, c.codec)
			if err != nil {
				t.Fatalf("SpliceSEIBeforeVCL: %v", err)
			}
			got, err := SampleNALUs(out)
			if err != nil {
				t.Fatalf("SampleNALUs(out): %v", err)
			}
			if len(got) != len(c.nalus)+1 {
				t.Fatalf("got %d NAL units, want %d", len(got), len(c.nalus)+1)
			}
			if !bytes.Equal(got[c.wantIdx], seiNALU) {
				t.Errorf("SEI landed at wrong index: NAL[%d] = % x, want SEI % x", c.wantIdx, got[c.wantIdx], seiNALU)
			}
			// The original NAL units must all survive, in order.
			rest := make([][]byte, 0, len(got)-1)
			for i, n := range got {
				if i == c.wantIdx {
					continue
				}
				rest = append(rest, n)
			}
			for i, n := range c.nalus {
				if !bytes.Equal(rest[i], n) {
					t.Errorf("original NAL %d not preserved: got % x, want % x", i, rest[i], n)
				}
			}
		})
	}
}

// A corrupt sample must surface as an error from the splice, not a panic or a
// silently dropped caption.
func TestSpliceSEIBeforeVCLBadSample(t *testing.T) {
	_, err := SpliceSEIBeforeVCL([]byte{0x00, 0x00, 0x00, 0x40, 0x65}, []byte{0x06, 0x01}, CodecAVC)
	if err == nil {
		t.Fatal("expected an error for a sample whose NAL length overruns it, got nil")
	}
}

// The whole point of the splice is that FieldPairs can read the captions back out of
// the spliced sample.
func TestSpliceSEIBeforeVCLRoundTripsThroughFieldPairs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		codec Codec
		vcl   []byte
	}{
		{"AVC", CodecAVC, []byte{0x65, 0x88, 0x84, 0x00}},
		{"HEVC", CodecHEVC, []byte{0x26, 0x01, 0xaf, 0x00}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := PrefixNALUs(tc.vcl)
			out, err := SpliceSEIBeforeVCL(sample, FrameSEINALU(f1Pair, f2Pair, 20, tc.codec), tc.codec)
			if err != nil {
				t.Fatalf("SpliceSEIBeforeVCL: %v", err)
			}
			nalus, err := SampleNALUs(out)
			if err != nil {
				t.Fatalf("SampleNALUs: %v", err)
			}
			if !IsVCL(nalus[1], tc.codec) {
				t.Errorf("NAL after the SEI is not the VCL NAL: % x", nalus[1])
			}
			f1, f2, err := FieldPairs(nalus, tc.codec)
			if err != nil {
				t.Fatalf("FieldPairs: %v", err)
			}
			if !bytes.Equal(f1, f1Pair) || !bytes.Equal(f2, f2Pair) {
				t.Errorf("fields = % x / % x, want % x / % x", f1, f2, f1Pair, f2Pair)
			}
		})
	}
}

func TestIsVCL(t *testing.T) {
	cases := []struct {
		name  string
		nalu  []byte
		codec Codec
		want  bool
	}{
		{"avc idr", []byte{0x65, 0x88}, CodecAVC, true},
		{"avc non-idr", []byte{0x41, 0x9a}, CodecAVC, true},
		{"avc sps", []byte{0x67, 0x10}, CodecAVC, false},
		{"avc sei", []byte{0x06, 0x01}, CodecAVC, false},
		{"hevc idr", []byte{0x26, 0x01}, CodecHEVC, true},
		{"hevc vps", []byte{0x40, 0x01}, CodecHEVC, false},
		{"hevc prefix sei", []byte{0x4e, 0x01}, CodecHEVC, false},
		{"empty nalu", nil, CodecAVC, false},
		{"unknown codec", []byte{0x65, 0x88}, Codec(99), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsVCL(c.nalu, c.codec); got != c.want {
				t.Errorf("IsVCL(% x, %s) = %v, want %v", c.nalu, c.codec, got, c.want)
			}
		})
	}
}
