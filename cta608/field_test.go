package cta608

import "testing"

func TestDemuxMuxRoundTrip(t *testing.T) {
	ch1 := []Token{
		SetMode{Mode: PopOn},
		PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"CC1 LINE"},
		Command{Op: EOC},
	}
	ch2 := []Token{
		SetMode{Mode: RollUp, RollUpRows: 2},
		PAC{Row: 14, Indent: NoIndent, Pen: Pen{Color: Yellow}},
		Chars{"cc2 line"},
		Command{Op: CR},
	}

	for _, opts := range []SerializeOptions{{}, {Field: 2}, {Doubling: DoublingOff}} {
		field := MuxField(ch1, ch2, opts)
		g1, g2, err := DemuxField(field, ParseOptions{})
		if err != nil {
			t.Fatalf("opts=%+v: DemuxField error: %v", opts, err)
		}
		if !tokensEqual(g1, ch1) {
			t.Errorf("opts=%+v: ch1 mismatch\n got: %s\nwant: %s", opts, tokStr(g1), tokStr(ch1))
		}
		if !tokensEqual(g2, ch2) {
			t.Errorf("opts=%+v: ch2 mismatch\n got: %s\nwant: %s", opts, tokStr(g2), tokStr(ch2))
		}
	}
}

func TestDemuxSingleChannel(t *testing.T) {
	ch1 := []Token{
		SetMode{Mode: PopOn},
		PAC{Row: 1, Indent: NoIndent, Pen: Pen{Color: White}},
		Chars{"only cc1"},
		Command{Op: EOC},
	}
	field := MuxField(ch1, nil, SerializeOptions{})
	g1, g2, err := DemuxField(field, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !tokensEqual(g1, ch1) {
		t.Errorf("ch1 mismatch\n got: %s\nwant: %s", tokStr(g1), tokStr(ch1))
	}
	if len(g2) != 0 {
		t.Errorf("ch2 should be empty, got %s", tokStr(g2))
	}
}
