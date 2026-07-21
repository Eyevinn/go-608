package scc

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ReadOption configures Read (the functional-options pattern, matching
// schedule.Option and cta608's option types).
type ReadOption func(*readConfig)

type readConfig struct {
	fps    float64
	fpsSet bool
}

// WithFPS overrides the reader's frame-rate inference, forcing fps for every
// timecode in the file. Use it for the genuinely ambiguous sparse files — e.g.
// one whose captions all start at a frame field ≤ 24 with ':' separators, where
// 25 (PAL) and a sparse 30-family stream are indistinguishable from the text
// alone (S3). Whether the file is treated as drop-frame still follows the
// separators, and is forced off for rates that cannot be drop-frame.
func WithFPS(fps float64) ReadOption {
	return func(c *readConfig) {
		c.fps = fps
		c.fpsSet = true
	}
}

// Read parses a Scenarist SCC file into an SCCFile, preserving each line as one
// Entry so a subsequent Write reproduces the bytes exactly. It infers the frame
// rate and drop-frame flag from the timecodes (S3): a ';' separator anywhere
// means drop-frame (a fractional NTSC rate), and the maximum line-start frame
// field selects the rate family (≥ 50 → 59.94/60; 30–49 → 50; 25–29 → 29.97/30;
// ≤ 24 → ambiguous). Sparse files are often ambiguous, so inference takes a
// WithFPS override and falls back to 29.97 (the NTSC default) when the signals
// are insufficient. Both ';' and ':' inputs are accepted.
func Read(r io.Reader, opts ...ReadOption) (*SCCFile, error) {
	var cfg readConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// rawEntry keeps the verbatim timecode string and decoded bytes until the
	// frame rate is settled, because the timecode → frame conversion needs it.
	type rawEntry struct {
		tc    string
		bytes []byte
	}
	var raws []rawEntry
	maxFF := -1
	hasSemicolon := false
	headerSeen := false

	sc := bufio.NewScanner(r)
	// SCC caption lines can be long (a whole caption's pairs on one line); lift the
	// scanner's 64 KiB default token cap well above any realistic line.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue // blank separator line
		}
		if !headerSeen {
			if strings.HasPrefix(trimmed, "Scenarist_SCC") {
				headerSeen = true
				continue
			}
			return nil, fmt.Errorf("scc: missing \"Scenarist_SCC\" header (got %q)", trimmed)
		}
		tc, pairs, err := splitEntryLine(trimmed)
		if err != nil {
			return nil, err
		}
		b, err := parsePairs(pairs)
		if err != nil {
			return nil, err
		}
		_, _, _, ff, sepDrop, err := parseTimecodeFields(tc)
		if err != nil {
			return nil, err
		}
		if sepDrop {
			hasSemicolon = true
		}
		if ff > maxFF {
			maxFF = ff
		}
		raws = append(raws, rawEntry{tc: tc, bytes: b})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scc: read: %w", err)
	}
	if !headerSeen {
		return nil, fmt.Errorf("scc: empty input or missing \"Scenarist_SCC\" header")
	}

	fps := cfg.fps
	if !cfg.fpsSet {
		fps = inferFPS(hasSemicolon, maxFF)
	}
	drop := hasSemicolon && supportsDropFrame(fps)

	f := &SCCFile{FPS: fps, DropFrame: drop, Entries: make([]Entry, 0, len(raws))}
	for _, raw := range raws {
		frame, _, err := TimecodeToFrame(raw.tc, fps)
		if err != nil {
			return nil, err
		}
		f.Entries = append(f.Entries, Entry{Frame: frame, Pairs: raw.bytes})
	}
	return f, nil
}

// inferFPS chooses a frame rate from the two sparse-file signals (S3): whether
// any timecode used the ';' drop-frame separator, and the maximum line-start
// frame field seen. A frame field ≥ 25 rules out 25 fps; the higher bands rule
// in the faster families. When the signals cannot separate 25 from a sparse
// 30-family stream (only ':' and every frame field ≤ 24, or a file with no
// entries at all), it falls back to 29.97 — the NTSC default a caller corrects
// with WithFPS.
func inferFPS(hasSemicolon bool, maxFF int) float64 {
	if hasSemicolon {
		// Drop-frame ⇒ fractional NTSC; the frame field separates the two families
		// (29.97 tops out at 29, 59.94 goes to 59).
		if maxFF >= 30 {
			return fpsNTSC60
		}
		return fpsNTSC30
	}
	switch {
	case maxFF >= 50:
		return fpsNTSC60 // 50–59 ⇒ 60-family (non-drop here)
	case maxFF >= 30:
		return 50.0 // 30–49 ⇒ 50
	case maxFF >= 25:
		return fpsNTSC30 // 25–29 ⇒ 30-family
	default:
		return fpsNTSC30 // ≤ 24 (or empty) is ambiguous ⇒ NTSC fallback
	}
}

// splitEntryLine splits an SCC entry line into its leading timecode and the
// remaining hex pair groups. SCC uses a tab after the timecode; accept any run of
// whitespace for robustness.
func splitEntryLine(line string) (tc, pairs string, err error) {
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return "", "", fmt.Errorf("scc: entry line has no byte pairs: %q", line)
	}
	return line[:i], strings.TrimSpace(line[i:]), nil
}

// parsePairs decodes a run of whitespace-separated 4-hex-digit groups (one 608
// byte pair each) into raw bytes, preserving order and parity exactly.
func parsePairs(s string) ([]byte, error) {
	var out []byte
	for _, tok := range strings.Fields(s) {
		if len(tok) != 4 {
			return nil, fmt.Errorf("scc: byte-pair group %q is not 4 hex digits", tok)
		}
		b, err := hex.DecodeString(tok)
		if err != nil {
			return nil, fmt.Errorf("scc: byte-pair group %q: %w", tok, err)
		}
		out = append(out, b...)
	}
	return out, nil
}
