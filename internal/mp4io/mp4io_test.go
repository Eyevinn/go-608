package mp4io

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/mp4"
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

func TestSampleNALUsTruncated(t *testing.T) {
	// A length prefix that overruns the buffer must error, not panic.
	if _, err := SampleNALUs([]byte{0x00, 0x00, 0x00, 0x08, 0x65, 0x88}); err == nil {
		t.Fatal("expected error for length overrunning sample, got nil")
	}
	if _, err := SampleNALUs([]byte{0x00, 0x00}); err == nil {
		t.Fatal("expected error for truncated length prefix, got nil")
	}
}

func TestSpliceSEIBeforeVCL(t *testing.T) {
	sei := []byte{0x06, 0xaa, 0xbb} // AVC SEI NAL header (type 6) + payload
	cases := []struct {
		name    string
		codec   carriage.Codec
		nalus   [][]byte
		wantIdx int // index the SEI should land at in the result
	}{
		{
			name:    "avc sps then idr",
			codec:   carriage.CodecAVC,
			nalus:   [][]byte{{0x67, 0x10}, {0x65, 0x88}}, // SPS(7), IDR(5)
			wantIdx: 1,
		},
		{
			name:    "avc non-idr first",
			codec:   carriage.CodecAVC,
			nalus:   [][]byte{{0x41, 0x9a}}, // non-IDR(1)
			wantIdx: 0,
		},
		{
			name:    "hevc vps then idr",
			codec:   carriage.CodecHEVC,
			nalus:   [][]byte{{0x40, 0x01}, {0x26, 0x01}}, // VPS(32), IDR_W_RADL(19)
			wantIdx: 1,
		},
		{
			name:    "no vcl -> sei first",
			codec:   carriage.CodecAVC,
			nalus:   [][]byte{{0x67, 0x10}, {0x68, 0x20}}, // SPS, PPS only
			wantIdx: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sample := PrefixNALUs(c.nalus...)
			out, err := SpliceSEIBeforeVCL(sample, sei, c.codec)
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
			if !bytes.Equal(got[c.wantIdx], sei) {
				t.Errorf("SEI landed at wrong index: NAL[%d] = % x, want SEI % x", c.wantIdx, got[c.wantIdx], sei)
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

func TestVideoTrackAVC(t *testing.T) {
	sps, _ := hex.DecodeString("67640020accac05005bb0169e0000003002000000c9c4c000432380008647c12401cb1c31380")
	pps, _ := hex.DecodeString("68b5df20")

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(90000, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		t.Fatalf("SetAVCDescriptor: %v", err)
	}

	track, trex, err := VideoTrack(&mp4.File{Init: init})
	if err != nil {
		t.Fatalf("VideoTrack: %v", err)
	}
	if track.Codec != carriage.CodecAVC {
		t.Errorf("codec = %s, want AVC", track.Codec)
	}
	if track.ID != 1 {
		t.Errorf("track ID = %d, want 1", track.ID)
	}
	if track.Timescale != 90000 {
		t.Errorf("timescale = %d, want 90000", track.Timescale)
	}
	if trex == nil {
		t.Error("trex is nil")
	}
}

func TestVideoTrackErrors(t *testing.T) {
	if _, _, err := VideoTrack(&mp4.File{}); err == nil {
		t.Error("expected error for file with no moov, got nil")
	}
}
