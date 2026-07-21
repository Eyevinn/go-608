package scc

import (
	"reflect"
	"testing"

	"github.com/Eyevinn/go-608/cta608"
)

// TimedPairs assigns pair i of an entry to frame Entry.Frame+i and shares no
// mutable window with the entry.
func TestTimedPairs(t *testing.T) {
	f := &SCCFile{Entries: []Entry{
		{Frame: 100, Pairs: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}},
		{Frame: 200, Pairs: []byte{0x0a, 0x0b}},
	}}
	got := f.TimedPairs()
	want := []TimedPair{
		{Frame: 100, Pair: []byte{0x01, 0x02}},
		{Frame: 101, Pair: []byte{0x03, 0x04}},
		{Frame: 102, Pair: []byte{0x05, 0x06}},
		{Frame: 200, Pair: []byte{0x0a, 0x0b}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TimedPairs() = %v, want %v", got, want)
	}
	// Appending to a returned Pair must not corrupt the next pair's bytes.
	got[0].Pair = append(got[0].Pair, 0xff)
	if f.Entries[0].Pairs[2] != 0x03 {
		t.Errorf("append to a TimedPair clobbered the entry: byte[2] = %#x", f.Entries[0].Pairs[2])
	}
}

// GroupPairs coalesces successive frames into one entry and breaks at idle gaps.
func TestGroupPairs(t *testing.T) {
	in := []TimedPair{
		{Frame: 5, Pair: []byte{0xa0, 0xa1}},
		{Frame: 6, Pair: []byte{0xb0, 0xb1}},
		{Frame: 7, Pair: []byte{0xc0, 0xc1}},
		{Frame: 12, Pair: []byte{0xd0, 0xd1}}, // gap -> new entry
		{Frame: 13, Pair: []byte{0xe0, 0xe1}},
	}
	got := GroupPairs(in)
	want := []Entry{
		{Frame: 5, Pairs: []byte{0xa0, 0xa1, 0xb0, 0xb1, 0xc0, 0xc1}},
		{Frame: 12, Pairs: []byte{0xd0, 0xd1, 0xe0, 0xe1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GroupPairs() = %v, want %v", got, want)
	}
}

// TimedPairs and GroupPairs are inverses for a single contiguous entry.
func TestTimedPairsGroupPairsRoundTrip(t *testing.T) {
	f := &SCCFile{Entries: []Entry{{Frame: 42, Pairs: []byte{1, 2, 3, 4, 5, 6, 7, 8}}}}
	got := GroupPairs(f.TimedPairs())
	if !reflect.DeepEqual(got, f.Entries) {
		t.Fatalf("round trip = %v, want %v", got, f.Entries)
	}
}

// The core acceptance: flattening an SCCFile to TimedPairs and feeding the pairs
// to cta608.Parse recovers the exact token stream, and the pairs carry per-frame
// timing (the first control lands on the entry's frame, successive pairs on the
// following frames).
func TestTimedPairsFeedParse(t *testing.T) {
	tokens := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.Command{Op: cta608.ENM},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HELLO WORLD"},
		cta608.Command{Op: cta608.EOC},
	}
	pairs := cta608.Serialize(tokens, cta608.SerializeOptions{})
	f := &SCCFile{FPS: fpsNTSC30, Entries: []Entry{{Frame: 30, Pairs: pairs}}}

	timed := f.TimedPairs()
	if timed[0].Frame != 30 {
		t.Errorf("first pair frame = %d, want 30", timed[0].Frame)
	}
	// Successive pairs advance one frame each.
	for i := 1; i < len(timed); i++ {
		if timed[i].Frame != timed[i-1].Frame+1 {
			t.Fatalf("pair %d frame = %d, want %d (successive frames)", i, timed[i].Frame, timed[i-1].Frame+1)
		}
	}

	var data []byte
	for _, p := range timed {
		data = append(data, p.Pair...)
	}
	got, err := cta608.Parse(data, cta608.ParseOptions{ValidateParity: true})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(got, tokens) {
		t.Errorf("Parse(TimedPairs) = %v, want %v", got, tokens)
	}
}
