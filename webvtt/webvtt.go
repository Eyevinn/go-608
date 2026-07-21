package webvtt

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
)

// Reader and Writer are the cue.Reader / cue.Writer implementations for WebVTT.
// They are stateless wrappers over the package-level Read/Write so callers can
// treat WebVTT as one interchangeable format behind the published plugin seam
// (SPEC §4.6 / §8.2, design note W8): a Reader/Writer value can be stored where a
// cue.Reader/cue.Writer is expected, alongside srt and future TTML.
type Reader struct{}

// Writer implements cue.Writer for WebVTT; see Reader.
type Writer struct{}

// Compile-time proof that the WebVTT serializer satisfies the published seam.
var (
	_ cue.Reader = Reader{}
	_ cue.Writer = Writer{}
)

// ReadCues implements cue.Reader by delegating to Read.
func (Reader) ReadCues(r io.Reader) ([]cue.TimedCue, error) { return Read(r) }

// WriteCues implements cue.Writer by delegating to Write.
func (Writer) WriteCues(w io.Writer, cues []cue.TimedCue) error { return Write(w, cues) }

// header is the WebVTT magic that every document must begin with; it is also how
// the format is detected (design note W-detection). Read requires it and Write
// emits it.
const header = "WEBVTT"

// Read parses a WebVTT document into the shared cue list, implementing cue.Reader
// (SPEC §4.6 / §8.2). It is a thin serializer: all 608 semantics live in the cue
// package, so Read only turns WebVTT syntax — the WEBVTT magic, STYLE/NOTE blocks,
// cue timings with line:/position:/align: settings, and styled payload text — into
// []cue.TimedCue, quantizing styling to the 8-color palette and positioning to the
// 15x32 grid at this edge (design notes W5/W6).
//
// One WebVTT cue maps to one TimedCue whose Content is a cta608.Screen: each
// payload line becomes a Row (placed by line:), and each maximal same-style span
// becomes a Run (colored/italic/underlined per its tags; bold is dropped, unknown
// tags stripped). The mapping is lossy and quantized — it is a semantic, not a
// byte-exact, round-trip, in contrast to the SCC/SEI containers.
func Read(r io.Reader) ([]cue.TimedCue, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("webvtt: reading input: %w", err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimPrefix(text, "\ufeff") // strip a leading UTF-8 BOM
	if !strings.HasPrefix(text, header) {
		return nil, fmt.Errorf("webvtt: missing %q header", header)
	}

	sc := styleContext{fg: map[string]rgb{}, bg: map[string]rgb{}}
	var cues []cue.TimedCue
	for i, block := range splitBlocks(text) {
		head := strings.TrimSpace(block[0])
		switch {
		case i == 0 && strings.HasPrefix(head, header):
			continue // the WEBVTT header block (plus any Kind:/Language: lines)
		case head == "STYLE":
			parseStyleBlock(strings.Join(block[1:], "\n"), sc)
		case head == "NOTE" || strings.HasPrefix(head, "NOTE "), head == "REGION":
			continue // comments and regions carry nothing 608 can hold
		default:
			c, ok, err := parseCueBlock(block, sc)
			if err != nil {
				return nil, err
			}
			if ok {
				cues = append(cues, c)
			}
		}
	}
	return cues, nil
}

// Write serializes a cue list as a WebVTT document, implementing cue.Writer (SPEC
// §4.6 / §8.2). It emits the WEBVTT header, a single STYLE block declaring every
// color class the cues use, then one cue block per TimedCue: a timing line with
// line:/position:/align: derived from the Screen's grid placement, and payload
// lines built from each Row's Runs with <c.name>/<i>/<u> styling.
//
// A cue whose Content has no non-empty rows is a gap and is skipped. Styling and
// positioning are emitted so that Read reproduces the same Screen within the
// shared quantization (a stable semantic round-trip), not byte-for-byte (W5/W6).
func Write(w io.Writer, cues []cue.TimedCue) error {
	usedFg := map[cta608.Color]bool{}
	usedBg := map[cta608.Color]bool{}

	// block holds one already-rendered cue. Payloads are built first (in a single
	// pass that records the color classes in usedFg/usedBg) so the STYLE block,
	// which must precede every cue, can be written with the complete class set.
	type block struct {
		start, end        time.Duration
		settings, payload string
	}
	var blocks []block
	for _, c := range cues {
		rows := renderRows(c.Content)
		if len(rows) == 0 {
			continue // empty screen: a gap, no cue
		}
		topRow := rows[0].Index
		blockLeft := blockLeftColumn(rows)
		lines := make([]string, 0, len(rows))
		for _, row := range rows {
			lines = append(lines, styledRow(row, blockLeft, usedFg, usedBg))
		}
		blocks = append(blocks, block{
			start:    c.Start,
			end:      c.End,
			settings: formatSettings(topRow, blockLeft),
			payload:  strings.Join(lines, "\n"),
		})
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	writeStyleBlock(&sb, usedFg, usedBg)
	for i, b := range blocks {
		fmt.Fprintf(&sb, "%d\n%s --> %s %s\n%s\n\n",
			i+1, formatTimestamp(b.start), formatTimestamp(b.end), b.settings, b.payload)
	}
	if _, err := io.WriteString(w, sb.String()); err != nil {
		return fmt.Errorf("webvtt: writing output: %w", err)
	}
	return nil
}

// splitBlocks groups the document's lines into blank-line-separated blocks (the
// WebVTT block structure). Payload lines are kept verbatim — leading spaces there
// are significant indentation — so only whitespace-only lines act as separators.
func splitBlocks(text string) [][]string {
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// parseCueBlock turns one cue block into a TimedCue. A leading identifier line
// (anything before the "-->" timing line) is ignored; the timing line yields the
// window and the cue settings, and the remaining lines are the payload. ok is
// false for a block with no timing line (a stray identifier or unrecognized
// block), which Read skips.
func parseCueBlock(block []string, sc styleContext) (cue.TimedCue, bool, error) {
	timingIdx := -1
	for i, line := range block {
		if strings.Contains(line, "-->") {
			timingIdx = i
			break
		}
	}
	if timingIdx < 0 {
		return cue.TimedCue{}, false, nil
	}
	start, end, settings, err := parseTiming(block[timingIdx])
	if err != nil {
		return cue.TimedCue{}, false, err
	}
	payload := block[timingIdx+1:]
	return cue.TimedCue{Start: start, End: end, Content: buildScreen(payload, settings, sc)}, true, nil
}

// parseTiming parses a "START --> END settings..." line into its window and cue
// settings. The tokens before "-->" are the start timestamp and those after are
// the end timestamp followed by any setting tokens.
func parseTiming(line string) (start, end time.Duration, settings cueSettings, err error) {
	fields := strings.Fields(line)
	arrow := -1
	for i, f := range fields {
		if f == "-->" {
			arrow = i
			break
		}
	}
	if arrow < 1 || arrow+1 >= len(fields) {
		return 0, 0, cueSettings{}, fmt.Errorf("webvtt: malformed timing line %q", line)
	}
	if start, err = parseTimestamp(fields[arrow-1]); err != nil {
		return 0, 0, cueSettings{}, err
	}
	if end, err = parseTimestamp(fields[arrow+1]); err != nil {
		return 0, 0, cueSettings{}, err
	}
	return start, end, parseSettings(fields[arrow+2:]), nil
}

// buildScreen turns a cue's payload lines and settings into a Screen. Each line
// is parsed into runs, placed on its grid Row (from line:, stacking downward) and
// shifted to its left Column (from position:/align: and the line's width). Lines
// that collide on the same row after clamping are merged, and empty lines are
// dropped.
func buildScreen(payload []string, settings cueSettings, sc styleContext) cta608.Screen {
	byRow := map[int][]cta608.Run{}
	order := []int{}
	nLines := len(payload)
	for i, line := range payload {
		runs, width := parseLine(line, sc)
		if len(runs) == 0 {
			continue
		}
		left := settings.leftColumn(width)
		for j := range runs {
			runs[j].Column = clampInt(runs[j].Column+left, 0, gridCols-1)
		}
		row := settings.row(nLines, i)
		if _, seen := byRow[row]; !seen {
			order = append(order, row)
		}
		byRow[row] = append(byRow[row], runs...)
	}
	sort.Ints(order)
	rows := make([]cta608.Row, 0, len(order))
	for _, idx := range order {
		runs := byRow[idx]
		sort.SliceStable(runs, func(i, j int) bool { return runs[i].Column < runs[j].Column })
		rows = append(rows, cta608.Row{Index: idx, Displayed: true, Runs: runs})
	}
	return cta608.Screen{Rows: rows}
}

// renderRows returns the Screen's displayable rows for writing: rows with at least
// one non-empty run, sorted by Index, each row's runs sorted by Column and stripped
// of empty text. It mirrors the cue package's normalization so the writer starts
// from a canonical, ordered Screen.
func renderRows(s cta608.Screen) []cta608.Row {
	var rows []cta608.Row
	for _, r := range s.Rows {
		var runs []cta608.Run
		for _, run := range r.Runs {
			if run.Text != "" {
				runs = append(runs, run)
			}
		}
		if len(runs) == 0 {
			continue
		}
		sort.SliceStable(runs, func(i, j int) bool { return runs[i].Column < runs[j].Column })
		rows = append(rows, cta608.Row{Index: r.Index, Displayed: true, Runs: runs})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	return rows
}

// blockLeftColumn is the leftmost run column across all rows — the cue's shared
// indent, carried in the position: setting. Each row's own extra indent beyond
// this is emitted as leading spaces by styledRow, so per-row columns survive.
func blockLeftColumn(rows []cta608.Row) int {
	left := gridCols
	for _, r := range rows {
		if len(r.Runs) > 0 && r.Runs[0].Column < left {
			left = r.Runs[0].Column
		}
	}
	if left == gridCols {
		return 0
	}
	return left
}

// styledRow renders one Row as a payload line: runs laid out left to right from
// the cue's blockLeft, with column gaps (and this row's indent beyond blockLeft)
// filled by spaces and each run wrapped in its styling markup. Reconstructing gaps
// as spaces keeps absolute columns intact through the grid quantization.
func styledRow(r cta608.Row, blockLeft int, usedFg, usedBg map[cta608.Color]bool) string {
	var sb strings.Builder
	col := blockLeft
	for _, run := range r.Runs {
		if run.Column > col {
			sb.WriteString(strings.Repeat(" ", run.Column-col))
			col = run.Column
		}
		sb.WriteString(styledText(run.Text, run.Pen, usedFg, usedBg))
		col += utf8.RuneCountInString(run.Text)
	}
	return sb.String()
}

// parseTimestamp parses a WebVTT timestamp, "HH:MM:SS.mmm" or the hour-less
// "MM:SS.mmm" (the '.' decimal separator is what distinguishes WebVTT from SRT's
// ','). The result is a time.Duration offset from the start of the document.
func parseTimestamp(s string) (time.Duration, error) {
	secPart, msPart, ok := strings.Cut(s, ".")
	if !ok {
		return 0, fmt.Errorf("webvtt: timestamp %q missing '.mmm'", s)
	}
	fields := strings.Split(secPart, ":")
	var h, m, sec int
	switch len(fields) {
	case 3:
		if _, err := fmt.Sscanf(secPart, "%d:%d:%d", &h, &m, &sec); err != nil {
			return 0, fmt.Errorf("webvtt: bad timestamp %q: %w", s, err)
		}
	case 2:
		if _, err := fmt.Sscanf(secPart, "%d:%d", &m, &sec); err != nil {
			return 0, fmt.Errorf("webvtt: bad timestamp %q: %w", s, err)
		}
	default:
		return 0, fmt.Errorf("webvtt: bad timestamp %q", s)
	}
	ms, err := parseMillis(msPart)
	if err != nil {
		return 0, fmt.Errorf("webvtt: bad timestamp %q: %w", s, err)
	}
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
	return d, nil
}

// parseMillis parses the fractional milliseconds field, tolerating 1..3 digits by
// right-padding to three (".5" -> 500 ms), the common lenient reading.
func parseMillis(s string) (int, error) {
	if len(s) == 0 || len(s) > 3 {
		return 0, fmt.Errorf("milliseconds %q not 1..3 digits", s)
	}
	var ms int
	if _, err := fmt.Sscanf(s, "%d", &ms); err != nil {
		return 0, err
	}
	for i := len(s); i < 3; i++ {
		ms *= 10
	}
	return ms, nil
}

// formatTimestamp renders a Duration as the canonical WebVTT "HH:MM:SS.mmm" form
// (always with hours, and the '.' millisecond separator). Negative inputs clamp
// to zero.
func formatTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	h := ms / 3_600_000
	ms %= 3_600_000
	m := ms / 60_000
	ms %= 60_000
	s := ms / 1_000
	ms %= 1_000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
