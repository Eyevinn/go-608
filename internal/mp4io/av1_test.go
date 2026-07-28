package mp4io

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/av1"
	"github.com/Eyevinn/mp4ff/mp4"
)

const (
	av01Clean        = "../../testdata/av01-clean.mp4"
	av01Hierarchical = "../../testdata/av01-clean-hierarchical.mp4"
)

// An av01 fixture is recognized as an AV1 video track.
func TestVideoTrackAV1(t *testing.T) {
	for _, path := range []string{av01Clean, av01Hierarchical} {
		t.Run(path, func(t *testing.T) {
			f := readFile(t, path)
			track, trex, err := VideoTrack(f)
			if err != nil {
				t.Fatalf("VideoTrack: %v", err)
			}
			if track.Codec != CodecAV1 {
				t.Errorf("codec = %s, want AV1", track.Codec)
			}
			if _, ok := track.Codec.NALCodec(); ok {
				t.Error("NALCodec reported ok for AV1; AV1 has no NAL framing")
			}
			if trex == nil {
				t.Error("trex is nil")
			}
		})
	}
}

// SpliceFragmented carries cc_data() through an av01 file end to end: every sample
// gains exactly one caption OBU, FieldPairs reads the pairs back, and the file still
// decodes as a fragmented mp4 with its timing intact.
func TestSpliceFragmentedAV1RoundTrip(t *testing.T) {
	for _, path := range []string{av01Clean, av01Hierarchical} {
		t.Run(path, func(t *testing.T) {
			f := readFile(t, path)
			track, trex, err := VideoTrack(f)
			if err != nil {
				t.Fatalf("VideoTrack: %v", err)
			}
			before, _, err := Samples(f, trex)
			if err != nil {
				t.Fatalf("Samples: %v", err)
			}

			pairAt := func(i int) []byte { return []byte{0x94, byte(0x20 + i)} }
			var seen []SampleInfo
			var buf bytes.Buffer
			err = SpliceFragmented(f, track, trex, &buf, func(info SampleInfo) ([]byte, error) {
				seen = append(seen, info)
				return carriage.BuildCCData(pairAt(info.Index), nil, 3), nil
			})
			if err != nil {
				t.Fatalf("SpliceFragmented: %v", err)
			}

			// fn is called once per sample, in presentation order, from a zero origin.
			if len(seen) != len(before) {
				t.Fatalf("fn called %d times, want %d", len(seen), len(before))
			}
			for i, info := range seen {
				if info.Index != i {
					t.Errorf("call %d: Index = %d, want %d", i, info.Index, i)
				}
				if want := before[i].PresentationTime(); info.MediaTime != want {
					// The av01 fixtures start at pts 0, so MediaTime is the raw
					// presentation time.
					t.Errorf("call %d: MediaTime = %d, want %d", i, info.MediaTime, want)
				}
			}

			out := decodeBytes(t, buf.Bytes())
			outTrack, outTrex, err := VideoTrack(out)
			if err != nil {
				t.Fatalf("VideoTrack on output: %v", err)
			}
			after, _, err := Samples(out, outTrex)
			if err != nil {
				t.Fatalf("Samples on output: %v", err)
			}
			if len(after) != len(before) {
				t.Fatalf("%d samples out, want %d", len(after), len(before))
			}
			for i := range after {
				if after[i].DecodeTime != before[i].DecodeTime {
					t.Errorf("sample %d: decode time %d, want %d", i, after[i].DecodeTime, before[i].DecodeTime)
				}
				if got, want := int(after[i].Size), len(after[i].Data); got != want {
					t.Errorf("sample %d: Size %d, want %d", i, got, want)
				}
				f1, f2, err := FieldPairs(after[i].Data, outTrack.Codec)
				if err != nil {
					t.Fatalf("sample %d: FieldPairs: %v", i, err)
				}
				if want := pairAt(i); !bytes.Equal(f1, want) {
					t.Errorf("sample %d: field1 = % x, want % x", i, f1, want)
				}
				if len(f2) != 0 {
					t.Errorf("sample %d: field2 = % x, want empty", i, f2)
				}
				if n := countOBUType(t, after[i].Data, av1.OBUMetadata); n != 1 {
					t.Errorf("sample %d: %d metadata OBUs, want 1", i, n)
				}
			}
		})
	}
}

// A nil return from the CCDataFunc leaves the sample byte-identical — the same
// opt-out the SEI path has.
func TestSpliceFragmentedAV1SkipsNilPayload(t *testing.T) {
	f := readFile(t, av01Clean)
	track, trex, err := VideoTrack(f)
	if err != nil {
		t.Fatalf("VideoTrack: %v", err)
	}
	before, _, err := Samples(f, trex)
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}

	var buf bytes.Buffer
	err = SpliceFragmented(f, track, trex, &buf, func(info SampleInfo) ([]byte, error) {
		if info.Index == 2 {
			return carriage.BuildCCData([]byte{0x94, 0x20}, nil, 3), nil
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("SpliceFragmented: %v", err)
	}

	out := decodeBytes(t, buf.Bytes())
	_, outTrex, err := VideoTrack(out)
	if err != nil {
		t.Fatalf("VideoTrack on output: %v", err)
	}
	after, _, err := Samples(out, outTrex)
	if err != nil {
		t.Fatalf("Samples on output: %v", err)
	}
	for i := range after {
		want := 0
		if i == 2 {
			want = 1
		}
		if n := countOBUType(t, after[i].Data, av1.OBUMetadata); n != want {
			t.Errorf("sample %d: %d metadata OBUs, want %d", i, n, want)
		}
		if i != 2 && !bytes.Equal(after[i].Data, before[i].Data) {
			t.Errorf("sample %d: data changed despite a nil payload", i)
		}
	}
}

// FieldPairs dispatches on the codec: handing an av01 sample to the AVC path must
// fail rather than silently decode the OBU bytes as NAL length prefixes.
func TestFieldPairsCodecDispatch(t *testing.T) {
	sample := append(carriage.MetadataOBU(carriage.BuildCCData([]byte{0x94, 0x2c}, nil, 3)),
		av1.OBU{
			Header:  av1.OBUHeader{Type: av1.OBUFrame, HasSizeField: true, HeaderSize: 1},
			Payload: []byte{0x42},
		}.Encode()...)

	f1, _, err := FieldPairs(sample, CodecAV1)
	if err != nil {
		t.Fatalf("FieldPairs(AV1): %v", err)
	}
	if want := []byte{0x94, 0x2c}; !bytes.Equal(f1, want) {
		t.Errorf("field1 = % x, want % x", f1, want)
	}
	if _, _, err := FieldPairs(sample, CodecAVC); err == nil {
		t.Error("FieldPairs(AVC) accepted an av01 sample, want an error")
	}
}

func TestVideoCodecString(t *testing.T) {
	for _, tc := range []struct {
		codec VideoCodec
		want  string
	}{{CodecAVC, "AVC"}, {CodecHEVC, "HEVC"}, {CodecAV1, "AV1"}, {VideoCodec(9), "VideoCodec(9)"}} {
		if got := tc.codec.String(); got != tc.want {
			t.Errorf("VideoCodec(%d).String() = %q, want %q", int(tc.codec), got, tc.want)
		}
	}
}

// --- helpers ---

func readFile(t *testing.T, path string) *mp4.File {
	t.Helper()
	f, err := mp4.ReadMP4File(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return f
}

func decodeBytes(t *testing.T, data []byte) *mp4.File {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	return f
}

func countOBUType(t *testing.T, sample []byte, typ av1.OBUType) int {
	t.Helper()
	obus, err := av1.SplitOBUs(sample)
	if err != nil {
		t.Fatalf("SplitOBUs: %v", err)
	}
	n := 0
	for _, o := range obus {
		if o.Header.Type == typ {
			n++
		}
	}
	return n
}
