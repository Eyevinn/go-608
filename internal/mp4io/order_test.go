package mp4io

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/mp4"
)

// The B-frame fixtures (#54): 30 frames of real libx264 / libx265 with -bf 3, so
// composition offsets are non-zero and the first presentation time is 1024 rather
// than 0. Both properties are load-bearing here — the first exercises the ordering
// rule, the second the origin.
const (
	bframesAVC  = "../../testdata/bframes-avc.mp4"
	bframesHEVC = "../../testdata/bframes-hevc.mp4"
)

// The fixtures must actually reorder, or every assertion below passes vacuously —
// which is exactly how the bug survived against the composition-offset-free AVC
// fixture.
func TestBFrameFixturesReorder(t *testing.T) {
	for _, path := range []string{bframesAVC, bframesHEVC} {
		t.Run(path, func(t *testing.T) {
			f := readFile(t, path)
			_, trex, err := VideoTrack(f)
			if err != nil {
				t.Fatalf("VideoTrack: %v", err)
			}
			samples, origin, err := Samples(f, trex)
			if err != nil {
				t.Fatalf("Samples: %v", err)
			}
			if len(samples) != 30 {
				t.Fatalf("got %d samples, want 30", len(samples))
			}
			if origin == 0 {
				t.Error("track origin is 0; the fixture no longer exercises the timing origin")
			}
			reordered := false
			for i, s := range samples {
				if s.CompositionTimeOffset != 0 {
					reordered = true
				}
				if i > 0 && samples[i-1].PresentationTime() >= s.PresentationTime() {
					t.Fatalf("sample %d is not after sample %d in presentation time", i, i-1)
				}
			}
			if !reordered {
				t.Error("no composition offsets; the fixture no longer exercises the ordering rule")
			}
			// Presentation order must genuinely differ from decode order.
			if samples[0].DecodeTime == 0 && samples[1].DecodeTime == 512 {
				t.Error("presentation order equals decode order; the fixture no longer reorders")
			}
		})
	}
}

// The k-th caption payload must land on the k-th *displayed* frame, and come back
// out on it. Before the fix the payloads were assigned in decode order, so this
// round-trip agreed with itself while every third-party decoder saw them permuted.
func TestSpliceFragmentedPresentationOrder(t *testing.T) {
	for _, path := range []string{bframesAVC, bframesHEVC} {
		t.Run(path, func(t *testing.T) {
			f := readFile(t, path)
			track, trex, err := VideoTrack(f)
			if err != nil {
				t.Fatalf("VideoTrack: %v", err)
			}

			// A distinct pair per presentation index, and a record of the media time
			// the payload was chosen at.
			pairAt := func(i int) []byte { return []byte{0x80 | byte(i), 0x41} }
			mediaTimes := map[string]int64{}
			var buf bytes.Buffer
			err = SpliceFragmented(f, track, trex, &buf, func(info SampleInfo) ([]byte, error) {
				pair := pairAt(info.Index)
				mediaTimes[string(pair)] = info.MediaTime
				return carriage.BuildCCData(pair, nil, 3), nil
			})
			if err != nil {
				t.Fatalf("SpliceFragmented: %v", err)
			}

			out := decodeBytes(t, buf.Bytes())
			outTrack, outTrex, err := VideoTrack(out)
			if err != nil {
				t.Fatalf("VideoTrack on output: %v", err)
			}
			samples, origin, err := Samples(out, outTrex)
			if err != nil {
				t.Fatalf("Samples on output: %v", err)
			}

			for i, s := range samples {
				f1, _, err := FieldPairs(s.Data, outTrack.Codec)
				if err != nil {
					t.Fatalf("displayed frame %d: FieldPairs: %v", i, err)
				}
				want := pairAt(i)
				if !bytes.Equal(f1, want) {
					t.Fatalf("displayed frame %d carries % x, want % x "+
						"(payloads are not in presentation order)", i, f1, want)
				}
				// The media time the payload was scheduled at is the sample's own
				// presentation time measured from the track origin, so the write and
				// read sides share one clock.
				if got, want := mediaTimes[string(f1)], s.PresentationTime()-origin; got != want {
					t.Errorf("displayed frame %d: scheduled at media time %d, read back at %d", i, got, want)
				}
			}

			// The first displayed frame is at media time 0 even though the container
			// starts at pts 1024 — the C2 origin rule.
			if got := mediaTimes[string(pairAt(0))]; got != 0 {
				t.Errorf("first displayed frame is at media time %d, want 0", got)
			}
		})
	}
}

// Decode order is what gets written: the output's sample sequence must still be the
// input's, or the file no longer decodes.
func TestSpliceFragmentedPreservesDecodeOrder(t *testing.T) {
	f := readFile(t, bframesAVC)
	track, trex, err := VideoTrack(f)
	if err != nil {
		t.Fatalf("VideoTrack: %v", err)
	}
	var buf bytes.Buffer
	err = SpliceFragmented(f, track, trex, &buf, func(info SampleInfo) ([]byte, error) {
		return carriage.BuildCCData([]byte{0x94, 0x20}, nil, 3), nil
	})
	if err != nil {
		t.Fatalf("SpliceFragmented: %v", err)
	}

	in := readFile(t, bframesAVC)
	_, inTrex, err := VideoTrack(in)
	if err != nil {
		t.Fatalf("VideoTrack on a fresh read: %v", err)
	}
	out := decodeBytes(t, buf.Bytes())
	_, outTrex, err := VideoTrack(out)
	if err != nil {
		t.Fatalf("VideoTrack on output: %v", err)
	}

	before := decodeOrderTimes(t, in, inTrex)
	after := decodeOrderTimes(t, out, outTrex)
	if len(before) != len(after) {
		t.Fatalf("%d samples out, want %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("decode-order sample %d: (dts, cto) = %v, want %v", i, after[i], before[i])
		}
	}
}

type sampleTiming struct {
	decodeTime uint64
	cto        int32
}

// decodeOrderTimes returns each sample's timing in container (decode) order, which
// Samples deliberately no longer exposes.
func decodeOrderTimes(t *testing.T, f *mp4.File, trex *mp4.TrexBox) []sampleTiming {
	t.Helper()
	var out []sampleTiming
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				t.Fatalf("expanding fragment samples: %v", err)
			}
			for _, s := range samples {
				out = append(out, sampleTiming{s.DecodeTime, s.CompositionTimeOffset})
			}
		}
	}
	return out
}
