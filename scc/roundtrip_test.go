package scc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Eyevinn/go-608/cta608"
)

// The sample fixtures live in the top-level testdata directory (shared across
// packages), one per timecode separator.
var fixtures = []string{
	"hello-nondrop.scc", // ':' non-drop
	"hello-drop.scc",    // ';' true drop-frame, crossing minute and tenth-minute
}

// Read → Write is byte-exact for the committed sample files, for both the ';'
// (drop-frame) and ':' (non-drop) variants. This is the SCC container's headline
// guarantee (SPEC §8.1): the package owns structure and timecodes only, never the
// 608 bytes, so nothing is lost.
func TestReadWriteByteExact(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "testdata", "scc", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			f, err := Read(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			var out bytes.Buffer
			if err := Write(&out, f); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if !bytes.Equal(out.Bytes(), raw) {
				t.Errorf("round trip not byte-exact\n got:\n%q\nwant:\n%q", out.String(), string(raw))
			}
		})
	}
}

// The drop-frame fixture must be recognized as drop-frame at 29.97 from its ';'
// separators alone, and its minute-boundary timecodes must map to the expected
// absolute frames (00:00:58;00 = 1740, 00:01:00;02 = 1800, 00:10:00;00 = 17982).
func TestDropFixtureInference(t *testing.T) {
	path := filepath.Join("..", "testdata", "scc", "hello-drop.scc")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.FPS != fpsNTSC30 || !f.DropFrame {
		t.Errorf("inferred FPS=%g DropFrame=%v, want 29.97 true", f.FPS, f.DropFrame)
	}
	wantFrames := []int{1740, 1800, 17982}
	for i, e := range f.Entries {
		if e.Frame != wantFrames[i] {
			t.Errorf("entry %d frame = %d, want %d", i, e.Frame, wantFrames[i])
		}
	}
}

// The non-drop fixture flattens and parses to its known token stream, confirming
// a real file feeds the cta608 core end to end.
func TestNonDropFixtureParses(t *testing.T) {
	path := filepath.Join("..", "testdata", "scc", "hello-nondrop.scc")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var data []byte
	for _, p := range f.TimedPairs() {
		data = append(data, p.Pair...)
	}
	got, err := cta608.Parse(data, cta608.ParseOptions{ValidateParity: true})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.Command{Op: cta608.ENM},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HELLO WORLD"},
		cta608.Command{Op: cta608.EOC},
		cta608.Command{Op: cta608.EDM},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i].String() {
			t.Errorf("token %d = %v, want %v", i, got[i], want[i])
		}
	}
}
