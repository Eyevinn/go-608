package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The av01 fixtures are only 5 frames (167 ms), which is too short for a pop-on
// caption to build and display — so the CLI-level AV1 assertions are made on the
// byte pairs, not on recovered cues. SCC gives exact control over which pair lands
// on which frame, which is what needs proving: the caption OBU goes into every
// sample of a real AV1 bitstream, including the awkward ones, and comes back out.
const (
	av01Clean        = "../../testdata/av01-clean.mp4"
	av01Hierarchical = "../../testdata/av01-clean-hierarchical.mp4"
)

// go608-inject accepts an av01 input and carries SCC pairs through it byte-exact,
// frame for frame — the AV1 counterpart of TestInjectSCCByteExact.
func TestInjectAV1SCCByteExact(t *testing.T) {
	for _, fixture := range []string{av01Clean, av01Hierarchical} {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			dir := t.TempDir()
			sub := filepath.Join(dir, "c.scc")
			// Five pairs on frames 0..4, one per sample of the fixture.
			sccBody := "Scenarist_SCC V1.0\n\n00:00:00:00\t9420 94ae 9162 c849 942f\n"
			if err := os.WriteFile(sub, []byte(sccBody), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out.mp4")
			args := []string{appName, "-i", fixture, "-sub", sub, "-o", out, "-fps", "30"}
			if err := run(args, io.Discard); err != nil {
				t.Fatalf("inject scc into av01: %v", err)
			}

			got := mp4Field1ByFrame(t, out)
			want := map[int]string{0: "9420", 1: "94ae", 2: "9162", 3: "c849", 4: "942f"}
			for frame, hexPair := range want {
				if got[frame] != hexPair {
					t.Errorf("frame %d: got %q, want %q", frame, got[frame], hexPair)
				}
			}
			if len(got) != len(want) {
				t.Errorf("got pairs on %d frames, want %d", len(got), len(want))
			}
		})
	}
}

// A subtitle input works too, and the injected file is still a decodable av01 track
// that go608-extract can read back.
func TestInjectAV1SubtitlePairsLandOnFrames(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "c.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:00,167\nHI\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	args := []string{appName, "-i", av01Hierarchical, "-sub", sub, "-o", out, "-fps", "30"}
	if err := run(args, io.Discard); err != nil {
		t.Fatalf("inject srt into av01: %v", err)
	}

	// Every sample carries a caption OBU: the first pair is the RCL control code the
	// pop-on build opens with. (The fixture is far too short for the caption to
	// finish building, so no cue is expected — only the wire pairs.)
	got := mp4Field1ByFrame(t, out)
	if len(got) == 0 {
		t.Fatal("no field-1 pairs recovered from the injected av01 file")
	}
	if want := "9420"; got[0] != want {
		t.Errorf("frame 0: got %q, want %q (RCL)", got[0], want)
	}
}

// The track codec is reported as AV1, not silently mistaken for a NAL-framed one.
func TestInjectAV1TrackDetection(t *testing.T) {
	_, track, _ := openMP4Test(t, av01Clean)
	if got := track.Codec.String(); got != "AV1" {
		t.Errorf("codec = %s, want AV1", got)
	}
	if _, ok := track.Codec.NALCodec(); ok {
		t.Error("NALCodec reported ok for an av01 track")
	}
}
