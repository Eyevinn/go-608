package srt

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
)

// Reader is the SRT implementation of cue.Reader: it deserializes an SRT document
// into the shared []cue.TimedCue. It is a stateless value type (the parse holds no
// configuration), so Reader{} is the in-tree implementor the cue seam names
// (design note W8). ReadCues simply delegates to the package-level Read.
type Reader struct{}

// Writer is the SRT implementation of cue.Writer: it serializes a []cue.TimedCue
// out as an SRT document. Like Reader it is a stateless value type; WriteCues
// delegates to the package-level Write.
type Writer struct{}

// Compile-time proof that the value types satisfy the published cue seam, so a
// caller can pass srt.Reader{} / srt.Writer{} anywhere a cue.Reader / cue.Writer
// is expected (SPEC §4.6, design note W8).
var (
	_ cue.Reader = Reader{}
	_ cue.Writer = Writer{}
)

// ReadCues implements cue.Reader.
func (Reader) ReadCues(r io.Reader) ([]cue.TimedCue, error) { return Read(r) }

// WriteCues implements cue.Writer.
func (Writer) WriteCues(w io.Writer, cues []cue.TimedCue) error { return Write(w, cues) }

// Read parses an SRT document into the shared cue list (SPEC §4.6 / §8.2). SRT is
// a header-less, ordered list of numbered blocks — an index line, a
// "HH:MM:SS,mmm --> HH:MM:SS,mmm" timing line, and one or more text lines,
// separated by blank lines. Each block becomes one TimedCue whose Content is a
// cta608.Screen built from the text lines: inline styling is quantized to the 608
// palette (design note W5) and, because SRT carries no positioning, the lines are
// anchored to the bottom of the grid and centered (design note W6). A UTF-8 BOM
// and CRLF/CR line endings are tolerated. All 608<->cue logic lives in the cue
// package; Read only turns SRT text into TimedCues.
func Read(r io.Reader) ([]cue.TimedCue, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("srt: read: %w", err)
	}
	text := string(data)
	text = strings.TrimPrefix(text, "\uFEFF") // drop a leading UTF-8 BOM
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var cues []cue.TimedCue
	for _, block := range splitBlocks(text) {
		c, ok, err := parseBlock(block)
		if err != nil {
			return nil, err
		}
		if ok {
			cues = append(cues, c)
		}
	}
	return cues, nil
}

// Write serializes a cue list as an SRT document (SPEC §4.6 / §8.2). Blocks are
// numbered from 1 in slice order; each cue's window becomes a
// "HH:MM:SS,mmm --> HH:MM:SS,mmm" timing line and its Screen rows become text
// lines (top row first). Styling is emitted as inline tags quantized to what SRT
// carries — foreground color as <font color>, italic/underline as <i>/<u>;
// background is dropped and positioning is dropped (rendered bottom-centered),
// with no {\anX}/coordinate extensions invented (design notes W5/W6). Blocks are
// blank-line separated, ending with a trailing blank line.
func Write(w io.Writer, cues []cue.TimedCue) error {
	bw := bufio.NewWriter(w)
	for i, c := range cues {
		fmt.Fprintf(bw, "%d\n", i+1)
		fmt.Fprintf(bw, "%s --> %s\n", formatTimestamp(c.Start), formatTimestamp(c.End))
		lines := renderLines(c.Content)
		if len(lines) == 0 {
			// A cue with no renderable content still needs a text line so the block
			// stays well-formed; emit one empty line.
			lines = []string{""}
		}
		for _, ln := range lines {
			fmt.Fprintln(bw, ln)
		}
		fmt.Fprintln(bw) // blank line separating blocks
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("srt: write: %w", err)
	}
	return nil
}

// splitBlocks splits normalized SRT text into blocks on blank-line boundaries.
// Each block is the slice of its non-separator lines; runs of blank lines collapse
// to a single separator, and leading/trailing blank lines are ignored.
func splitBlocks(text string) [][]string {
	var blocks [][]string
	var cur []string
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// parseBlock turns one block's lines into a TimedCue. The optional numeric index
// line is skipped, the timing line (the one containing "-->") supplies Start/End,
// and every line after it is caption text. ok is false for a block that carries no
// timing line and only an index/noise (tolerated, skipped); a block with caption
// text but an unparseable timing line is a hard error.
func parseBlock(lines []string) (cue.TimedCue, bool, error) {
	ti := -1
	for i, ln := range lines {
		if strings.Contains(ln, "-->") {
			ti = i
			break
		}
	}
	if ti < 0 {
		// No timing line. A lone index number (or stray blank noise) is tolerated;
		// anything else is a malformed block we refuse rather than silently drop.
		if len(lines) == 1 && isIndexLine(lines[0]) {
			return cue.TimedCue{}, false, nil
		}
		return cue.TimedCue{}, false, fmt.Errorf("srt: block without a timing line: %q", lines[0])
	}

	start, end, err := parseTiming(lines[ti])
	if err != nil {
		return cue.TimedCue{}, false, err
	}
	content := screenFromLines(lines[ti+1:])
	return cue.TimedCue{Start: start, End: end, Content: content}, true, nil
}

// isIndexLine reports whether a line is a bare SRT block index (all digits).
func isIndexLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// timingRe captures the two timestamps of an SRT timing line. It accepts ',' or
// '.' as the millisecond separator and flexible digit counts, and ignores any
// trailing content (e.g. the non-standard "X1:.. X2:.." coordinates some tools
// append) — go-608 never emits or honors SRT positioning extensions (W6).
var timingRe = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}[,.]\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}[,.]\d{1,3})`)

// parseTiming extracts the Start and End of a cue from an SRT timing line.
func parseTiming(line string) (start, end time.Duration, err error) {
	m := timingRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, fmt.Errorf("srt: malformed timing line: %q", line)
	}
	if start, err = parseTimestamp(m[1]); err != nil {
		return 0, 0, err
	}
	if end, err = parseTimestamp(m[2]); err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

// tsRe matches a single SRT timestamp "HH:MM:SS,mmm" (',' or '.' separator).
var tsRe = regexp.MustCompile(`^(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})$`)

// parseTimestamp converts one SRT timestamp to a time.Duration. A fractional part
// shorter than three digits is right-padded (",5" -> 500 ms), matching how the
// field denotes a decimal fraction of a second.
func parseTimestamp(s string) (time.Duration, error) {
	m := tsRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("srt: malformed timestamp: %q", s)
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, _ := strconv.Atoi(m[3])
	frac := m[4]
	for len(frac) < 3 {
		frac += "0"
	}
	ms, _ := strconv.Atoi(frac)
	d := time.Duration(h)*time.Hour +
		time.Duration(min)*time.Minute +
		time.Duration(sec)*time.Second +
		time.Duration(ms)*time.Millisecond
	return d, nil
}

// formatTimestamp renders a time.Duration as an SRT timestamp "HH:MM:SS,mmm".
// Negative durations clamp to zero, and sub-millisecond precision is truncated
// (SRT resolves to whole milliseconds).
func formatTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	h := ms / 3_600_000
	ms -= h * 3_600_000
	m := ms / 60_000
	ms -= m * 60_000
	s := ms / 1_000
	ms -= s * 1_000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// renderLines turns a Screen into SRT text lines, one per non-empty row taken in
// row-index order (top of the grid first). Row and column positions are dropped —
// SRT has no positioning — so only the styled text of each row survives (W6).
func renderLines(s cta608.Screen) []string {
	rows := make([]cta608.Row, len(s.Rows))
	copy(rows, s.Rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	var lines []string
	for _, r := range rows {
		if len(r.Runs) == 0 {
			continue
		}
		lines = append(lines, formatRow(r))
	}
	return lines
}

// screenFromLines builds a cue's Content Screen from a block's caption text lines.
// SRT has no positioning, so the lines are anchored to the bottom of the 15-row
// grid and centered in the 32-column grid (design note W6) by reusing the core
// cta608.CaptionBlock's AnchorBottom + AlignCenter layout — the one place absolute
// row/column placement is computed. Each line's inline styling is parsed to runs
// (W5). Blank/whitespace-only lines that carry no runs are dropped so they do not
// shift the bottom anchor.
func screenFromLines(textLines []string) cta608.Screen {
	var clines []cta608.Line
	for _, tl := range textLines {
		runs := parseLine(tl)
		if len(runs) == 0 {
			continue
		}
		clines = append(clines, cta608.Line{Runs: runs, Align: cta608.AlignCenter})
	}
	if len(clines) == 0 {
		return cta608.Screen{}
	}
	block := cta608.CaptionBlock{Lines: clines, Anchor: cta608.AnchorBottom}
	return block.Screen()
}

// sortedRuns returns the runs sorted by ascending Column without mutating the
// input slice, so serialization order is deterministic regardless of authoring
// order.
func sortedRuns(in []cta608.Run) []cta608.Run {
	out := make([]cta608.Run, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Column < out[j].Column })
	return out
}

// mergeAdjacent coalesces neighbouring runs that share a Pen into a single run,
// concatenating their text. Runs must already be in column order. This keeps the
// emitted markup minimal (<i>AB</i> rather than <i>A</i><i>B</i>) and makes the
// serialized text re-parse to the same maximal run set.
func mergeAdjacent(runs []cta608.Run) []cta608.Run {
	if len(runs) == 0 {
		return runs
	}
	out := []cta608.Run{runs[0]}
	for _, r := range runs[1:] {
		last := &out[len(out)-1]
		if last.Pen == r.Pen {
			last.Text += r.Text
			continue
		}
		out = append(out, r)
	}
	return out
}
