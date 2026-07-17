package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		desc string
		args []string
		w    io.Writer
		err  bool
	}{
		{desc: "version", args: []string{appName, "-version"}, w: os.Stdout, err: false},
		{desc: "help", args: []string{appName, "-h"}, w: os.Stdout, err: false},
		{desc: "unknown flag", args: []string{appName, "-x"}, w: os.Stdout, err: true},
		{desc: "not implemented", args: []string{appName}, w: os.Stdout, err: true},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			err := run(c.args, c.w)
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
