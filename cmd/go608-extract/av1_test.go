package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/internal/mp4io"
	"github.com/Eyevinn/mp4ff/mp4"
)

const av01Fixture = "../../testdata/av01-clean-hierarchical.mp4"

// The five field-1 pairs carried by the av01 input these tests read back — the same
// RCL / ENM / char-pair / EOC sequence the AVC fixtures use.
var av01Pairs = [][]byte{
	{0x94, 0x20}, {0x94, 0xae}, {0x91, 0x62}, {0xc8, 0x49}, {0x94, 0x2f},
}

// go608-extract reads an av01 input, closing the CLI loop. The fixture is 5 frames,
// too short for a pop-on caption to finish building, so the assertion is on the wire
// pairs (via -dump) rather than on recovered cues.
func TestExtractAV1Dump(t *testing.T) {
	in := captionedAV01(t)

	var buf bytes.Buffer
	if err := run([]string{appName, "-i", in, "-dump"}, &buf); err != nil {
		t.Fatalf("dump av01: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "codec AV1") {
		t.Errorf("dump header does not report AV1:\n%s", got)
	}
	for _, want := range []string{"f1=9420", "f1=94ae", "f1=9162", "f1=c849", "f1=942f"} {
		if !strings.Contains(got, want) {
			t.Errorf("dump output missing %q:\n%s", want, got)
		}
	}
}

// The byte-exact 608 → SCC path works for av01 too: the pairs come back out in the
// frames they went in on.
func TestExtractAV1SCCByteExact(t *testing.T) {
	in := captionedAV01(t)

	var buf bytes.Buffer
	if err := run([]string{appName, "-i", in, "-to", "scc"}, &buf); err != nil {
		t.Fatalf("extract scc from av01: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "Scenarist_SCC V1.0") {
		t.Errorf("not valid SCC:\n%s", got)
	}
	if want := "9420 94ae 9162 c849 942f"; !strings.Contains(got, want) {
		t.Errorf("SCC output missing %q:\n%s", want, got)
	}
}

// captionedAV01 writes a copy of the av01 fixture carrying one known field-1 pair per
// sample, and returns its path. It goes through the same mp4io seam go608-inject uses,
// without needing the other command's main package.
func captionedAV01(t *testing.T) string {
	t.Helper()
	f, err := mp4.ReadMP4File(av01Fixture)
	if err != nil {
		t.Fatalf("reading %s: %v", av01Fixture, err)
	}
	track, trex, err := mp4io.VideoTrack(f)
	if err != nil {
		t.Fatalf("VideoTrack: %v", err)
	}
	var buf bytes.Buffer
	err = mp4io.SpliceFragmented(f, track, trex, &buf, func(info mp4io.SampleInfo) ([]byte, error) {
		if info.Index >= len(av01Pairs) {
			return nil, nil
		}
		return carriage.BuildCCData(av01Pairs[info.Index], nil, 3), nil
	})
	if err != nil {
		t.Fatalf("SpliceFragmented: %v", err)
	}
	path := filepath.Join(t.TempDir(), "av01-608.mp4")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
