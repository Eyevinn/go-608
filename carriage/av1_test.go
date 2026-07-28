package carriage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/av1"
	"github.com/Eyevinn/mp4ff/mp4"
)

// The reference metadata OBU from docs/research/av1-metadata-obu-608-layout.md, for
// field 1 = 94 2c, no field 2, cc_count = 3. This is the AV1 counterpart of
// TestSEIWireBytesGolden, and locks the envelope: OBU header, obu_size,
// metadata_type, the T.35/GA94 header, cc_data(), trailing_bits.
func TestMetadataOBUWireBytesGolden(t *testing.T) {
	want := []byte{
		0x2a,       // obu_header: type 5 (OBU_METADATA), obu_has_size_field=1
		0x16,       // obu_size = 22
		0x04,       // metadata_type = 4 (ITU-T T.35), leb128
		0xb5,       // itu_t_t35_country_code (USA)
		0x00, 0x31, // provider_code 0x0031 (ATSC)
		0x47, 0x41, 0x39, 0x34, // user_identifier "GA94"
		0x03,       // user_data_type_code (A/53 captions)
		0xc3, 0xff, // cc_count=3 flags, em_data
		0xfc, 0x94, 0x2c, // field 1: cc_valid=1, cc_type=00
		0xf9, 0x00, 0x00, // field 2: cc_valid=0, cc_type=01
		0xfa, 0x00, 0x00, // DTVCC padding
		0xff, // trailing marker_bits
		0x80, // OBU trailing_bits
	}

	if got := FrameMetadataOBU([]byte{0x94, 0x2c}, nil, 3); !bytes.Equal(got, want) {
		t.Errorf("FrameMetadataOBU = % x\n                want = % x", got, want)
	}

	// The same bytes via the two-step path the mp4io seam uses.
	if got := MetadataOBU(BuildCCData([]byte{0x94, 0x2c}, nil, 3)); !bytes.Equal(got, want) {
		t.Errorf("MetadataOBU = % x\n         want = % x", got, want)
	}
}

// The OBU payload after the metadata_type byte must be byte-identical to the SEI
// message payload for the same cc_data(): only the envelope differs between the two
// codec families. This is what makes BuildCCData codec-free.
func TestMetadataOBUPayloadMatchesSEIPayload(t *testing.T) {
	cc := BuildCCData(f1Pair, f2Pair, 20)

	obu := MetadataOBU(cc)
	obus, err := av1.SplitOBUs(obu)
	if err != nil {
		t.Fatalf("SplitOBUs: %v", err)
	}
	if len(obus) != 1 {
		t.Fatalf("got %d OBUs, want 1", len(obus))
	}
	m, err := av1.ParseMetadataOBU(obus[0].Payload)
	if err != nil {
		t.Fatalf("ParseMetadataOBU: %v", err)
	}
	if m.Type != av1.MetadataTypeITUTT35 {
		t.Fatalf("metadata_type = %v, want ITU-T T.35", m.Type)
	}
	// m.Payload is country_code + itu_t_t35_payload_bytes, which is exactly the
	// SEI message payload.
	if want := SEIMessage(cc).Payload(); !bytes.Equal(m.Payload, want) {
		t.Errorf("OBU T.35 payload = % x\n            want = % x", m.Payload, want)
	}
}

func TestOBUFieldPairsRoundTrip(t *testing.T) {
	cc := BuildCCData(f1Pair, f2Pair, 20)
	sample := append(MetadataOBU(cc), frameOBU(0x42)...)

	f1, f2, err := OBUFieldPairs(sample)
	if err != nil {
		t.Fatalf("OBUFieldPairs: %v", err)
	}
	if !bytes.Equal(f1, f1Pair) {
		t.Errorf("field1 = % x, want % x", f1, f1Pair)
	}
	if !bytes.Equal(f2, f2Pair) {
		t.Errorf("field2 = % x, want % x", f2, f2Pair)
	}
}

// A sample with no caption OBU yields no pairs rather than an error.
func TestOBUFieldPairsNoCaptions(t *testing.T) {
	f1, f2, err := OBUFieldPairs(frameOBU(0x42))
	if err != nil {
		t.Fatalf("OBUFieldPairs: %v", err)
	}
	if f1 != nil || f2 != nil {
		t.Errorf("field1 = % x, field2 = % x, want both nil", f1, f2)
	}
}

// The splice goes after a sequence header and before the frame, and the surrounding
// OBUs keep their order and bytes.
func TestSpliceOBUBeforeFrameOrder(t *testing.T) {
	seqHdr := encodeOBU(av1.OBUSequenceHeader, []byte{0x01, 0x02, 0x03})
	frame := frameOBU(0x42)
	obu := FrameMetadataOBU(f1Pair, nil, 3)

	got, err := SpliceOBUBeforeFrame(append(append([]byte{}, seqHdr...), frame...), obu)
	if err != nil {
		t.Fatalf("SpliceOBUBeforeFrame: %v", err)
	}

	want := concat(seqHdr, obu, frame)
	if !bytes.Equal(got, want) {
		t.Errorf("spliced = % x\n   want = % x", got, want)
	}

	types := obuTypes(t, got)
	wantTypes := []av1.OBUType{av1.OBUSequenceHeader, av1.OBUMetadata, av1.OBUFrame}
	if !equalTypes(types, wantTypes) {
		t.Errorf("OBU types = %v, want %v", types, wantTypes)
	}
}

// An OBU_FRAME_HEADER is an anchor too — a 3-byte show_existing_frame sample is a
// real case in the hierarchical fixture.
func TestSpliceOBUBeforeFrameHeaderAnchor(t *testing.T) {
	hdr := encodeOBU(av1.OBUFrameHeader, []byte{0x88})
	obu := FrameMetadataOBU(f1Pair, nil, 3)

	got, err := SpliceOBUBeforeFrame(hdr, obu)
	if err != nil {
		t.Fatalf("SpliceOBUBeforeFrame: %v", err)
	}
	if want := concat(obu, hdr); !bytes.Equal(got, want) {
		t.Errorf("spliced = % x\n   want = % x", got, want)
	}
}

// Only the first frame OBU is the anchor; hidden reference frames that follow stay
// behind the caption OBU. Sample 2 of the hierarchical fixture is this shape.
func TestSpliceOBUBeforeFrameFirstOfSeveral(t *testing.T) {
	f1, f2, f3 := frameOBU(0x11), frameOBU(0x22), frameOBU(0x33)
	obu := FrameMetadataOBU(f1Pair, nil, 3)

	got, err := SpliceOBUBeforeFrame(concat(f1, f2, f3), obu)
	if err != nil {
		t.Fatalf("SpliceOBUBeforeFrame: %v", err)
	}
	if want := concat(obu, f1, f2, f3); !bytes.Equal(got, want) {
		t.Errorf("spliced = % x\n   want = % x", got, want)
	}
}

// Unlike SpliceSEIBeforeVCL there is deliberately no fallback: a temporal unit
// without a frame OBU is malformed, and appending would hide that.
func TestSpliceOBUBeforeFrameNoAnchor(t *testing.T) {
	seqHdr := encodeOBU(av1.OBUSequenceHeader, []byte{0x01, 0x02, 0x03})
	_, err := SpliceOBUBeforeFrame(seqHdr, FrameMetadataOBU(f1Pair, nil, 3))
	if err == nil {
		t.Fatal("SpliceOBUBeforeFrame succeeded on a sample with no frame OBU, want error")
	}
	if !strings.Contains(err.Error(), "OBU_FRAME") {
		t.Errorf("error = %v, want it to name the missing anchor", err)
	}
}

func TestSpliceOBUBeforeFrameBadSample(t *testing.T) {
	// obu_size claims 200 bytes but only 1 follows.
	bad := []byte{0x32, 0xc8, 0x00}
	if _, err := SpliceOBUBeforeFrame(bad, FrameMetadataOBU(f1Pair, nil, 3)); err == nil {
		t.Fatal("SpliceOBUBeforeFrame succeeded on a corrupt sample, want error")
	}
	if _, _, err := OBUFieldPairs(bad); err == nil {
		t.Fatal("OBUFieldPairs succeeded on a corrupt sample, want error")
	}
}

// The real fixtures: splice one caption OBU into every sample of both av01 files and
// read the pairs back. av01-clean-hierarchical.mp4 is the one that matters — three
// frame OBUs in sample 2, bare frame headers in samples 3 and 5.
func TestSpliceOBUFixtureRoundTrip(t *testing.T) {
	for _, name := range []string{"av01-clean.mp4", "av01-clean-hierarchical.mp4"} {
		t.Run(name, func(t *testing.T) {
			samples := av01FixtureSamples(t, "../testdata/"+name)
			if len(samples) != 5 {
				t.Fatalf("got %d samples, want 5", len(samples))
			}
			for i, data := range samples {
				pair := []byte{0x94, byte(0x20 + i)}
				spliced, err := SpliceOBUBeforeFrame(data, FrameMetadataOBU(pair, nil, 3))
				if err != nil {
					t.Fatalf("sample %d: SpliceOBUBeforeFrame: %v", i+1, err)
				}
				if got, want := len(spliced), len(data)+24; got != want {
					t.Errorf("sample %d: spliced length %d, want %d", i+1, got, want)
				}

				f1, f2, err := OBUFieldPairs(spliced)
				if err != nil {
					t.Fatalf("sample %d: OBUFieldPairs: %v", i+1, err)
				}
				if !bytes.Equal(f1, pair) {
					t.Errorf("sample %d: field1 = % x, want % x", i+1, f1, pair)
				}
				if len(f2) != 0 {
					t.Errorf("sample %d: field2 = % x, want empty", i+1, f2)
				}

				// The original OBUs survive unchanged, in order, with the caption
				// OBU inserted before the first frame.
				assertOriginalOBUsPreserved(t, i+1, data, spliced)
			}
		})
	}
}

// assertOriginalOBUsPreserved checks that spliced is orig with exactly one
// OBU_METADATA added, that the added OBU sits immediately before the first frame
// OBU, and that every other OBU is byte-identical and in the same order.
func assertOriginalOBUsPreserved(t *testing.T, sample int, orig, spliced []byte) {
	t.Helper()
	before, err := av1.SplitOBUs(orig)
	if err != nil {
		t.Fatalf("sample %d: SplitOBUs(orig): %v", sample, err)
	}
	after, err := av1.SplitOBUs(spliced)
	if err != nil {
		t.Fatalf("sample %d: SplitOBUs(spliced): %v", sample, err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("sample %d: %d OBUs after splice, want %d", sample, len(after), len(before)+1)
	}
	kept := make([]av1.OBU, 0, len(before))
	metaAt := -1
	for i, o := range after {
		if o.Header.Type == av1.OBUMetadata && metaAt < 0 {
			metaAt = i
			continue
		}
		kept = append(kept, o)
	}
	if metaAt < 0 {
		t.Fatalf("sample %d: no metadata OBU after splice", sample)
	}
	if next := after[metaAt+1]; next.Header.Type != av1.OBUFrame && next.Header.Type != av1.OBUFrameHeader {
		t.Errorf("sample %d: OBU after the caption OBU is %v, want a frame OBU", sample, next.Header.Type)
	}
	for i := range before {
		if before[i].Header.Type != kept[i].Header.Type || !bytes.Equal(before[i].Payload, kept[i].Payload) {
			t.Errorf("sample %d: OBU %d changed across the splice", sample, i)
		}
	}
}

// --- helpers ---

// frameOBU builds a minimal OBU_FRAME with a one-byte dummy payload. carriage never
// parses the frame body, only its OBU type, so the payload can be arbitrary.
func frameOBU(b byte) []byte { return encodeOBU(av1.OBUFrame, []byte{b}) }

func encodeOBU(t av1.OBUType, payload []byte) []byte {
	return av1.OBU{
		Header:  av1.OBUHeader{Type: t, HasSizeField: true, HeaderSize: 1},
		Payload: payload,
	}.Encode()
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func obuTypes(t *testing.T, data []byte) []av1.OBUType {
	t.Helper()
	obus, err := av1.SplitOBUs(data)
	if err != nil {
		t.Fatalf("SplitOBUs: %v", err)
	}
	types := make([]av1.OBUType, len(obus))
	for i, o := range obus {
		types[i] = o.Header.Type
	}
	return types
}

func equalTypes(a, b []av1.OBUType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// av01FixtureSamples returns the sample data of the single video track of an av01
// fixture, in decode order. It reads the file directly rather than through
// internal/mp4io, which carriage must not depend on.
func av01FixtureSamples(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := mp4.ReadMP4File(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	moov := f.Moov
	if moov == nil && f.Init != nil {
		moov = f.Init.Moov
	}
	if moov == nil || moov.Mvex == nil {
		t.Fatalf("%s: not an initialized fragmented mp4", path)
	}
	trex, ok := moov.Mvex.GetTrex(moov.Traks[0].Tkhd.TrackID)
	if !ok {
		t.Fatalf("%s: no trex", path)
	}
	var out [][]byte
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				t.Fatalf("%s: expanding fragment samples: %v", path, err)
			}
			for _, s := range samples {
				out = append(out, s.Data)
			}
		}
	}
	return out
}
