package mp4io

import (
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
)

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
	if track.Codec != CodecAVC {
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
