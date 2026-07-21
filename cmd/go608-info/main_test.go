package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath is the shared fragmented-mp4 fixture (AVC track, CEA-608 SEI) built
// by the carriage tests: field 1 = 9420 94ae 9162 c849 942f (RCL, ENM, PAC, "HI",
// EOC) with one field-2 pair 152c (EDM).
const fixturePath = "../../testdata/carriage-608-avc.mp4"

// hexInput mirrors the fixture's field-1 stream as raw cc_data.
const hexInput = "9420 94ae 9162 c849 942f"

func TestRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.mp4")
	cases := []struct {
		desc string
		args []string
		err  bool
	}{
		{desc: "version", args: []string{appName, "-version"}, err: false},
		{desc: "help", args: []string{appName, "-h"}, err: false},
		{desc: "unknown flag", args: []string{appName, "-x"}, err: true},
		{desc: "no input", args: []string{appName}, err: true},
		{desc: "two inputs", args: []string{appName, "-i", fixturePath, "-hex", hexInput}, err: true},
		{desc: "bad field", args: []string{appName, "-hex", hexInput, "-field", "3"}, err: true},
		{desc: "odd hex", args: []string{appName, "-hex", "94 2"}, err: true},
		{desc: "odd bytes", args: []string{appName, "-hex", "9420 94"}, err: true},
		{desc: "non-hex", args: []string{appName, "-hex", "zzzz"}, err: true},
		{desc: "empty hex", args: []string{appName, "-hex", "   "}, err: true},
		{desc: "missing mp4", args: []string{appName, "-i", missing}, err: true},
		{desc: "not an mp4", args: []string{appName, "-i", "main.go"}, err: true},
		{desc: "hex ok", args: []string{appName, "-hex", hexInput}, err: false},
		{desc: "mp4 ok", args: []string{appName, "-i", fixturePath}, err: false},
		{desc: "field 2 ok", args: []string{appName, "-i", fixturePath, "-field", "2"}, err: false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := run(c.args, io.Discard)
			if c.err && err == nil {
				t.Error("expected error but got nil")
			}
			if !c.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunVersionOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-version"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), appName) {
		t.Errorf("version output %q does not contain app name %q", buf.String(), appName)
	}
}

// TestDumpMP4Fixture is acceptance criterion 1: the fixture dump prints the field
// pairs, token stream, and rendered screens.
func TestDumpMP4Fixture(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-i", fixturePath}, &buf); err != nil {
		t.Fatalf("run -i: %v", err)
	}
	out := buf.String()
	wants := []string{
		"codec AVC", "5 frames", "field 1",
		"== field pairs ==",
		"f1=9420", // RCL pair
		"f2=152c", // the lone field-2 pair is surfaced too
		"== tokens (field 1) ==",
		"SetMode(pop-on)", // RCL
		"Command(ENM)",
		"PAC(row=2 green)",
		`Chars("HI")`,
		"Command(EOC)",
		"== screens (field 1) ==",
		"change 1:",
		"row  2:",
		`"HI"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("mp4 dump missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestDumpHex is acceptance criterion 2: raw cc_data prints tokens + screen with no
// mp4 involved.
func TestDumpHex(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-hex", hexInput}, &buf); err != nil {
		t.Fatalf("run -hex: %v", err)
	}
	out := buf.String()
	wants := []string{
		"raw cc_data (-hex)",
		"5 pairs",
		"[pair 0] f1=9420",
		"SetMode(pop-on)",
		`Chars("HI")`,
		"Command(EOC)",
		"change 1:",
		`"HI"`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("hex dump missing %q\n--- output ---\n%s", w, out)
		}
	}
}

// TestHexParsingForms checks that "0x"-prefixed and comma-separated hex parse to the
// same dump as the plain space-separated form.
func TestHexParsingForms(t *testing.T) {
	forms := []string{
		"9420 94ae 9162 c849 942f",
		"0x94 0x20 0x94 0xae 0x91 0x62 0xc8 0x49 0x94 0x2f",
		"9420,94ae,9162,c849,942f",
		"942094ae9162c849942f",
	}
	var first string
	for i, form := range forms {
		var buf bytes.Buffer
		if err := run([]string{appName, "-hex", form}, &buf); err != nil {
			t.Fatalf("form %d: %v", i, err)
		}
		// Normalize the source header (it echoes the raw form) before comparing.
		out := stripSourceLine(buf.String())
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Errorf("form %d dump differs from form 0\nform: %q", i, form)
		}
	}
}

// TestFieldSelection checks that -field 2 decodes the field-2 stream (the fixture's
// lone 152c pair is EDM), independent of field 1.
func TestFieldSelection(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-i", fixturePath, "-field", "2"}, &buf); err != nil {
		t.Fatalf("run -field 2: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "== tokens (field 2) ==") {
		t.Errorf("field-2 dump missing field-2 tokens header:\n%s", out)
	}
	if !strings.Contains(out, "Command(EDM)") {
		t.Errorf("field-2 dump missing EDM token:\n%s", out)
	}
}

// TestDeterministic is acceptance criterion 3: the dump is stable run to run (no
// timestamps or map-ordering noise).
func TestDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := run([]string{appName, "-i", fixturePath}, &a); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if err := run([]string{appName, "-i", fixturePath}, &b); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if a.String() != b.String() {
		t.Errorf("dump not deterministic across runs:\n--- a ---\n%s\n--- b ---\n%s", a.String(), b.String())
	}
}

// stripSourceLine drops the first (source:) line, whose text echoes the raw input.
func stripSourceLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
