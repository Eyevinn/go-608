package cue

import (
	"io"
	"sort"
	"time"

	"github.com/Eyevinn/go-608/cta608"
)

// TimedCue is the shared timed-text intermediate: a presentation window
// [Start, End) whose Content is a rendered caption. It is the single public
// pivot both directions of the 608<->timed-text mapping turn on (SPEC §4.5,
// design note W2).
//
// Content REUSES the core cta608.Screen rather than introducing a parallel
// styled-text type: a cue's rendered content IS positioned, styled rows —
// precisely Row{Index, Displayed, Runs} / Run{Column, Text, Pen}. A consequence
// is that the cue model is "608-flavoured": its coordinate system is the 15x32
// grid and its palette is the 8 CTA-608 colors, so a format's richer
// positioning and styling is quantized to the grid/palette at the serializer
// edge (webvtt/srt), not carried losslessly through the middle (W2, W5, W6).
type TimedCue struct {
	// Start and End bound the cue's presentation window; the caption is shown
	// for Start <= t < End.
	Start, End time.Duration
	// Content is the rendered caption — positioned, styled rows. It reuses the
	// core Screen so timed-text formats pivot on the same value the encoder
	// diffs and the decoder materializes.
	Content cta608.Screen
}

// Reader reads a timed-text document into the shared cue list. It is the
// published, pluggable read half of the format seam (SPEC §4.5 / §8.3, design
// note W8): the 608<->cue mapping is written once here in cue, and each format
// is a thin serializer over []TimedCue.
//
// The in-tree implementors are webvtt.Reader and srt.Reader; TTML and
// third-party formats plug in by implementing this interface, with zero change
// to the mapping in Segment/Compile. A Reader is expected to quantize the
// format's positioning and styling onto the 608 grid/palette as it builds each
// TimedCue.Content (W5, W6).
type Reader interface {
	ReadCues(r io.Reader) ([]TimedCue, error)
}

// Writer writes a cue list out as a timed-text document. It is the published,
// pluggable write half of the format seam (the mirror of Reader; SPEC §4.5 /
// §8.3, design note W8). In-tree implementors are webvtt.Writer and srt.Writer;
// a Writer serializes each TimedCue.Content's rows/runs into the target format's
// styling and positioning syntax.
type Writer interface {
	WriteCues(w io.Writer, cues []TimedCue) error
}

// TimedScreen is one displayed-Screen state at a wall-clock instant — the input
// unit for the 608->text direction (SPEC §4.5, design note W3/W9). A decoder
// driven by timed byte-pairs emits a TimedScreen whenever its displayed Screen
// changes (cta608.Decoder.Changed reports exactly this boundary); Segment cuts
// that timeline into cues.
type TimedScreen struct {
	// Time is the instant at which the displayed screen became Screen.
	Time time.Duration
	// Screen is the displayed caption at Time; an empty Screen (no rows) marks
	// an erase (a gap, no caption on screen).
	Screen cta608.Screen
	// Mode is the caption mode in force when the screen changed, from
	// cta608.Decoder.Mode. Segment needs it to coalesce the direct-write modes
	// without merging distinct pop-on captions: in roll-up and paint-on every byte
	// pair changes the displayed screen, whereas a pop-on caption changes it once,
	// at its EOC. The zero value is cta608.PopOn, which never coalesces — so a
	// producer that does not set it keeps the one-cue-per-change behaviour.
	Mode cta608.Mode
}

// TimedTokens is a wall-time-tagged batch of token transitions — the output
// unit for the text->608 direction (SPEC §4.5, design note W9). Compile emits a
// TimedTokens each time the merged target Screen changes at a cue boundary; the
// Tokens are the []cta608.Token that flip the pop-on caption from the previous
// state to the new one.
//
// The mapping layer stops here, at timed tokens: turning this stream into
// specific frames with per-frame cc_count and field cadence is the schedule
// package's job, not cue's (SPEC §4.3, design note W9). schedule defines its own
// input type (schedule.TimedTokens, keyed on wall-clock milliseconds) because it
// may not import cue without breaking the layering rule; a caller adapts this
// value to schedule.Push.
type TimedTokens struct {
	// Time is the instant at which these tokens take effect.
	Time time.Duration
	// Tokens is the ordered transition to transmit at Time.
	Tokens []cta608.Token
}

// --- shared screen helpers --------------------------------------------------
//
// cta608 has equivalent unexported helpers, but they are private to that
// package; cue needs its own screen-equality and normalization to detect
// displayed-screen boundaries (Segment) and to keep the round-trip comparison
// semantic. Normalization mirrors cta608.normalizeScreen so the two packages
// agree on what "equal" and "empty" mean: empty runs/rows dropped, the
// ColDefault foreground folded to White (both render white on the wire), and
// rows/runs put in a canonical order.

// normalizeScreen returns a copy of s with empty runs and rows dropped, the
// ColDefault foreground folded to White, and rows sorted by Index and runs by
// Column, so screens compare by value regardless of authoring order.
func normalizeScreen(s cta608.Screen) cta608.Screen {
	rows := make([]cta608.Row, 0, len(s.Rows))
	for _, r := range s.Rows {
		runs := make([]cta608.Run, 0, len(r.Runs))
		for _, run := range r.Runs {
			if run.Text == "" {
				continue
			}
			if run.Pen.Color == cta608.ColDefault {
				run.Pen.Color = cta608.White
			}
			runs = append(runs, run)
		}
		if len(runs) == 0 {
			continue
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].Column < runs[j].Column })
		rows = append(rows, cta608.Row{Index: r.Index, Displayed: true, Runs: runs})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	return cta608.Screen{Rows: rows}
}

// screenEqual reports whether two screens render the same caption, ignoring
// authoring order and the ColDefault/White and Displayed distinctions.
func screenEqual(a, b cta608.Screen) bool {
	na, nb := normalizeScreen(a), normalizeScreen(b)
	if len(na.Rows) != len(nb.Rows) {
		return false
	}
	for i := range na.Rows {
		if na.Rows[i].Index != nb.Rows[i].Index || !runsEqual(na.Rows[i].Runs, nb.Rows[i].Runs) {
			return false
		}
	}
	return true
}

// runsEqual compares two run slices by value (Run is a comparable value type).
func runsEqual(a, b []cta608.Run) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
