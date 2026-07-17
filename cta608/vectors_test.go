package cta608

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readHexVector reads a testdata .hex fixture: whitespace and #-comments are
// ignored, every remaining token is one hex byte.
func readHexVector(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "vectors", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var sb strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, f := range strings.Fields(line) {
			sb.WriteString(f)
		}
	}
	data, err := hex.DecodeString(sb.String())
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return data
}

// TestRawVectors checks that checked-in cc_data vectors decode to the expected
// tokens and re-serialize byte-identically (criterion 2).
func TestRawVectors(t *testing.T) {
	cases := []struct {
		file   string
		opts   SerializeOptions
		tokens []Token
	}{
		{
			file: "popon_hi.hex",
			opts: SerializeOptions{}, // field 1 default (doubling ON)
			tokens: []Token{
				SetMode{Mode: PopOn},
				PAC{Row: 15, Indent: NoIndent, Pen: Pen{Color: White}},
				Chars{"HI"},
				Command{Op: EOC},
			},
		},
		{
			file: "indent_tab_midrow_special_ext.hex",
			opts: SerializeOptions{Doubling: DoublingOff},
			tokens: []Token{
				PAC{Row: 14, Indent: 4, Pen: Pen{Color: White, Underline: true}},
				TabOffset{Columns: 3},
				MidRow{Pen: Pen{Color: Cyan}},
				Chars{"A♪À"},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data := readHexVector(t, c.file)

			got, err := Parse(data, ParseOptions{ValidateParity: true})
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !tokensEqual(got, c.tokens) {
				t.Fatalf("decode mismatch\n got: %s\nwant: %s", tokStr(got), tokStr(c.tokens))
			}

			reser := Serialize(c.tokens, c.opts)
			if !bytes.Equal(reser, data) {
				t.Fatalf("re-serialize not byte-identical\n got: % x\nwant: % x", reser, data)
			}
		})
	}
}
