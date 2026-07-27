package carriage

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"os"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// -update regenerates the committed testdata fixture (golden-file pattern):
//
//	go test ./carriage/ -run TestCarriageMP4FixtureRoundTrip -update
var update = flag.Bool("update", false, "regenerate the testdata fMP4 fixture")

// The fixture is shared with go608-info / go608-extract (#24/#25), so it lives in
// the top-level testdata directory rather than carriage/testdata.
const fixturePath = "../testdata/carriage-608-avc.mp4"

// A real 1280x720 AVC SPS/PPS pair, reused from mp4ff's examples/initcreator so the
// init segment parses as a valid video track. The sample payloads themselves are
// dummy VCL bytes — carriage only cares about the SEI NAL that rides alongside them.
const (
	fixtureSPShex = "67640020accac05005bb0169e0000003002000000c9c4c000432380008647c12401cb1c31380"
	fixturePPShex = "68b5df20"
)

type frameCC struct {
	f1, f2 []byte
}

// A short CC1 pop-on-style sequence, one 608 pair per frame, with a single field-2
// pair partway through to prove both fields round-trip.
var fixtureFrames = []frameCC{
	{f1: []byte{0x94, 0x20}},                         // RCL
	{f1: []byte{0x94, 0xae}},                         // ENM
	{f1: []byte{0x91, 0x62}, f2: []byte{0x15, 0x2c}}, // a char pair + a field-2 pair
	{f1: []byte{0xc8, 0x49}},                         // 'H','I'
	{f1: []byte{0x94, 0x2f}},                         // EOC
}

func fixtureExpected() (f1, f2 []byte) {
	for _, fr := range fixtureFrames {
		f1 = append(f1, fr.f1...)
		f2 = append(f2, fr.f2...)
	}
	return f1, f2
}

// TestCarriageMP4FixtureRoundTrip builds (with -update) and reads back the shared
// fragmented-mp4 fixture, recovering the field pairs from each sample's NAL units via
// FieldPairs (acceptance criterion 4).
func TestCarriageMP4FixtureRoundTrip(t *testing.T) {
	if *update {
		if err := os.WriteFile(fixturePath, buildFixtureMP4(t), 0o644); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
		t.Logf("wrote %s", fixturePath)
	}

	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("reading fixture (run with -update to create it): %v", err)
	}

	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	moov := f.Moov
	if moov == nil && f.Init != nil {
		moov = f.Init.Moov
	}
	if moov == nil {
		t.Fatal("no moov in decoded fixture")
	}
	trex, ok := moov.Mvex.GetTrex(1)
	if !ok {
		t.Fatal("no trex for track 1")
	}

	var gotF1, gotF2 []byte
	nSamples := 0
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				t.Fatalf("GetFullSamples: %v", err)
			}
			for _, s := range samples {
				nalus, err := avc.GetNalusFromSample(s.Data)
				if err != nil {
					t.Fatalf("GetNalusFromSample: %v", err)
				}
				f1, f2, err := FieldPairs(nalus, CodecAVC)
				if err != nil {
					t.Fatalf("FieldPairs: %v", err)
				}
				gotF1 = append(gotF1, f1...)
				gotF2 = append(gotF2, f2...)
				nSamples++
			}
		}
	}

	if nSamples != len(fixtureFrames) {
		t.Errorf("decoded %d samples, want %d", nSamples, len(fixtureFrames))
	}
	wantF1, wantF2 := fixtureExpected()
	if !bytes.Equal(gotF1, wantF1) {
		t.Errorf("field1 = % x, want % x", gotF1, wantF1)
	}
	if !bytes.Equal(gotF2, wantF2) {
		t.Errorf("field2 = % x, want % x", gotF2, wantF2)
	}
}

// buildFixtureMP4 synthesizes an init segment + one media segment for a single AVC
// track whose samples each carry a CTA-608 SEI NAL (from FrameSEINALU) ahead of a
// dummy VCL NAL. It is the only place carriage touches mp4ff's mp4 package, and only
// in tests.
func buildFixtureMP4(t *testing.T) []byte {
	t.Helper()
	sps, err := hex.DecodeString(fixtureSPShex)
	if err != nil {
		t.Fatalf("decoding SPS: %v", err)
	}
	pps, err := hex.DecodeString(fixturePPShex)
	if err != nil {
		t.Fatalf("decoding PPS: %v", err)
	}

	const trackID = uint32(1)
	const timescale = uint32(90000)
	const frameDur = uint32(3000) // 30 fps at 90000

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(timescale, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		t.Fatalf("SetAVCDescriptor: %v", err)
	}

	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(1, trackID)
	if err != nil {
		t.Fatalf("CreateFragment: %v", err)
	}
	seg.AddFragment(frag)

	for i, fr := range fixtureFrames {
		seiNALU := FrameSEINALU(fr.f1, fr.f2, 20, CodecAVC)
		vcl := []byte{0x41, 0x9a, 0x00, byte(i)}
		flags := mp4.NonSyncSampleFlags
		if i == 0 {
			vcl = []byte{0x65, 0x88, 0x84, 0x00}
			flags = mp4.SyncSampleFlags
		}
		data := lengthPrefixedNALUs(seiNALU, vcl)
		frag.AddFullSample(mp4.FullSample{
			Sample: mp4.Sample{
				Flags: flags,
				Dur:   frameDur,
				Size:  uint32(len(data)),
			},
			DecodeTime: uint64(i) * uint64(frameDur),
			Data:       data,
		})
	}

	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatalf("init.Encode: %v", err)
	}
	if err := seg.Encode(&buf); err != nil {
		t.Fatalf("seg.Encode: %v", err)
	}
	return buf.Bytes()
}

// lengthPrefixedNALUs concatenates NAL units into an AVC sample: a 4-byte big-endian
// length prefix before each NAL. This is the exact inverse of avc.GetNalusFromSample.
func lengthPrefixedNALUs(nalus ...[]byte) []byte {
	var buf bytes.Buffer
	var l [4]byte
	for _, n := range nalus {
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		buf.Write(l[:])
		buf.Write(n)
	}
	return buf.Bytes()
}
