package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mp4Fixture = "../../testdata/carriage-608-avc.mp4"
	sccFixture = "../../testdata/scc/hello-nondrop.scc"
)

func TestRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.vtt")
	cases := []struct {
		desc string
		args []string
		err  bool
	}{
		{"version", []string{appName, "-version"}, false},
		{"help", []string{appName, "-h"}, false},
		{"unknown flag", []string{appName, "-x"}, true},
		{"no input", []string{appName}, true},
		{"no output format", []string{appName, "-i", mp4Fixture}, true},
		{"bad -to", []string{appName, "-i", mp4Fixture, "-to", "bogus"}, true},
		{"dump on non-mp4", []string{appName, "-i", sccFixture, "-dump"}, true},
		{"mp4 -> vtt file", []string{appName, "-i", mp4Fixture, "-o", out}, false},
		{"mp4 -> srt stdout", []string{appName, "-i", mp4Fixture, "-to", "srt"}, false},
		{"scc -> vtt (format-only)", []string{appName, "-i", sccFixture, "-to", "webvtt"}, false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := run(c.args, io.Discard)
			if c.err && err == nil {
				t.Error("expected error, got nil")
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
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), appName) {
		t.Errorf("version output %q missing app name", buf.String())
	}
}

// TestExtractSCCByteExact is acceptance criterion #25.1 (SCC path byte-exact): the
// mp4 fixture's field-1 pairs come out of the SCC path verbatim, not re-compiled.
func TestExtractSCCByteExact(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-i", mp4Fixture, "-to", "scc"}, &buf); err != nil {
		t.Fatalf("extract scc: %v", err)
	}
	// The fixture carries 9420 94ae 9162 c849 942f on successive frames, so the SCC
	// entry must be exactly those pairs (no control-code doubling / re-encoding).
	if !strings.Contains(buf.String(), "9420 94ae 9162 c849 942f") {
		t.Errorf("SCC output not byte-exact to the mp4 field-1 stream:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "9420 9420") {
		t.Errorf("SCC output was re-compiled (doubled controls), not byte-exact:\n%s", buf.String())
	}
}

// TestExtractToCueFormats checks the faithful (quantized) WebVTT and SRT paths
// (acceptance criterion #25.1).
func TestExtractToCueFormats(t *testing.T) {
	for _, tc := range []struct{ to, header string }{
		{"webvtt", "WEBVTT"},
		{"srt", ""},
	} {
		var buf bytes.Buffer
		if err := run([]string{appName, "-i", mp4Fixture, "-to", tc.to}, &buf); err != nil {
			t.Fatalf("extract %s: %v", tc.to, err)
		}
		if tc.header != "" && !strings.HasPrefix(buf.String(), tc.header) {
			t.Errorf("%s output missing %q header:\n%s", tc.to, tc.header, buf.String())
		}
		if !strings.Contains(buf.String(), "HI") {
			t.Errorf("%s output missing the caption text:\n%s", tc.to, buf.String())
		}
	}
}

// TestDumpMatchesInfo is acceptance criterion #25.3: the -dump output matches
// go608-info. Both use internal/dump, so it is identical by construction; assert
// the shared sections and the fixture's first field-1 pair.
func TestDumpMatchesInfo(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-i", mp4Fixture, "-dump"}, &buf); err != nil {
		t.Fatalf("dump: %v", err)
	}
	for _, want := range []string{"== field pairs ==", "f1=9420", "== tokens (field 1) ==", "== screens (field 1) =="} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("dump output missing %q:\n%s", want, buf.String())
		}
	}
}

// TestFormatOnly is acceptance criterion #25.2: convert SCC -> WebVTT with no mp4.
func TestFormatOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{appName, "-i", sccFixture, "-to", "webvtt"}, &buf); err != nil {
		t.Fatalf("format-only: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "WEBVTT") {
		t.Errorf("format-only SCC->WebVTT not valid WebVTT:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "HELLO") {
		t.Errorf("format-only output missing the SCC caption text:\n%s", buf.String())
	}
}

// TestOutputFileWritten checks -o writes a file whose format is inferred from the
// extension.
func TestOutputFileWritten(t *testing.T) {
	out := filepath.Join(t.TempDir(), "cap.srt")
	if err := run([]string{appName, "-i", mp4Fixture, "-o", out}, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading %s: %v", out, err)
	}
	if !strings.Contains(string(data), "-->") || !strings.Contains(string(data), "HI") {
		t.Errorf("written SRT looks wrong:\n%s", data)
	}
}
