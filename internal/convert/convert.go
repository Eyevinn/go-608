// Package convert is the shared timed-text conversion core behind go608-extract
// and go608-inject. Format-only conversion (SCC ↔ WebVTT ↔ SRT, no mp4) is a mode
// of both commands rather than a separate binary (SPEC §9, package-layout note
// P4), so the format dispatch lives here once and each command is a thin entry
// point over it.
//
// Everything pivots on the shared cue model: ReadCues turns any supported format
// into []cue.TimedCue and WriteCues turns cues back out. WebVTT and SRT map
// directly (they are cue serializers); SCC is a byte-pair container, so it is
// bridged through the 608 core — decoded pair-by-pair into displayed-Screen
// changes for the read side, and compiled through schedule into per-frame pairs
// for the write side. The mp4 extract path reuses CuesFromUnits, the same
// decode-to-cues helper the SCC read path uses.
package convert

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/cue"
	"github.com/Eyevinn/go-608/scc"
	"github.com/Eyevinn/go-608/schedule"
	"github.com/Eyevinn/go-608/srt"
	"github.com/Eyevinn/go-608/webvtt"
)

// Format identifies a timed-text container go-608 can read and write.
type Format int

const (
	FormatWebVTT Format = iota
	FormatSRT
	FormatSCC
)

func (f Format) String() string {
	switch f {
	case FormatWebVTT:
		return "webvtt"
	case FormatSRT:
		return "srt"
	case FormatSCC:
		return "scc"
	default:
		return fmt.Sprintf("Format(%d)", int(f))
	}
}

// ParseFormat maps a format name or a file extension (with or without a leading
// dot) to a Format: "webvtt"/"vtt", "srt", "scc".
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), ".")) {
	case "webvtt", "vtt":
		return FormatWebVTT, nil
	case "srt":
		return FormatSRT, nil
	case "scc":
		return FormatSCC, nil
	default:
		return 0, fmt.Errorf("convert: unknown format %q (want webvtt, srt, or scc)", s)
	}
}

// FormatFromPath infers a Format from a file path's extension, reporting ok=false
// when the extension is not a recognized timed-text format.
func FormatFromPath(path string) (Format, bool) {
	f, err := ParseFormat(filepath.Ext(path))
	return f, err == nil
}

// Options carries the framing and policy knobs the SCC and decode paths need; the
// cue-native formats (WebVTT/SRT) ignore them. The zero value is not usable for
// SCC (FPS must be positive).
type Options struct {
	// FPS frames the SCC path: it maps SCC frame numbers to time on read and times
	// to frames on write. For a WebVTT/SRT-only conversion it is unused.
	FPS float64
	// DropFrame selects drop-frame SCC timecodes when writing SCC. On read the
	// scc package infers it; this field only shapes the write side.
	DropFrame bool
	// Segment is the dangling-cue end policy applied when decoding a 608 stream
	// (mp4 or SCC) into cues (SPEC §8.2, cue.Segment).
	Segment cue.SegmentOptions
	// CCCount is the cc_count policy for the schedule step of the SCC write path
	// (it does not affect the extracted pair bytes, only would-be cc_data padding).
	CCCount schedule.CCCountPolicy
	// FlipTiming decides whether a cue's Start is when its caption becomes visible
	// (schedule.FlipOnTime, the zero value and default) or when its transmission
	// begins (schedule.FlipAfterBuild, the pre-v0.8.0 behaviour). It shapes the
	// text -> SCC write path, whose timecodes are otherwise ~0.2-0.5 s late.
	FlipTiming schedule.FlipTiming
}

// ReadCues reads a timed-text document of format f into the shared cue list.
// WebVTT and SRT parse directly; SCC is decoded through the 608 core — its byte
// pairs drive a cta608.Decoder and each displayed-Screen change becomes a
// TimedScreen that cue.Segment cuts into cues (framed by opts.FPS, or the fps the
// scc reader infers when opts.FPS is zero).
func ReadCues(f Format, r io.Reader, opts Options) ([]cue.TimedCue, error) {
	switch f {
	case FormatWebVTT:
		return webvtt.Read(r)
	case FormatSRT:
		return srt.Read(r)
	case FormatSCC:
		var ropts []scc.ReadOption
		if opts.FPS > 0 {
			ropts = append(ropts, scc.WithFPS(opts.FPS))
		}
		file, err := scc.Read(r, ropts...)
		if err != nil {
			return nil, err
		}
		return CuesFromUnits(unitsFromPairs(file.TimedPairs(), file.FPS), opts.Segment)
	default:
		return nil, fmt.Errorf("convert: unknown format %d", int(f))
	}
}

// WriteCues writes cues out as format f. WebVTT and SRT serialize directly; SCC
// goes through the encode stack — cue.Compile turns the cues into timed token
// transitions, a schedule.Scheduler drains them one pair per frame at opts.FPS,
// and the per-frame pairs are grouped into SCC entries (SPEC §6/§8).
func WriteCues(f Format, w io.Writer, cues []cue.TimedCue, opts Options) error {
	switch f {
	case FormatWebVTT:
		return webvtt.Write(w, cues)
	case FormatSRT:
		return srt.Write(w, cues)
	case FormatSCC:
		if opts.FPS <= 0 {
			return fmt.Errorf("convert: writing SCC needs a positive fps")
		}
		entries, err := sccEntriesFromCues(cues, opts)
		if err != nil {
			return err
		}
		return scc.Write(w, &scc.SCCFile{FPS: opts.FPS, DropFrame: opts.DropFrame, Entries: entries})
	default:
		return fmt.Errorf("convert: unknown format %d", int(f))
	}
}

// WriteSCCPairs writes raw per-frame 608 byte pairs out as a byte-exact SCC file.
// It is the byte-pair-to-byte-pair path (mp4 SEI → SCC): the wire bytes are
// carried verbatim — pairs on successive frames coalesce into one entry
// (scc.GroupPairs) with no decode/recompile — so an SCC extracted from an mp4's
// 608 is byte-exact to the mp4's field-1 stream (SPEC §8.1). This is distinct from
// WriteCues(FormatSCC, …), which re-encodes cues (the lossy WebVTT/SRT → SCC path).
func WriteSCCPairs(w io.Writer, pairs []scc.TimedPair, opts Options) error {
	if opts.FPS <= 0 {
		return fmt.Errorf("convert: writing SCC needs a positive fps")
	}
	return scc.Write(w, &scc.SCCFile{FPS: opts.FPS, DropFrame: opts.DropFrame, Entries: scc.GroupPairs(pairs)})
}

// DecodeUnit is one video frame's field-1 byte pair at a wall-clock instant — the
// unit the mp4 and SCC decode paths share to drive a cta608.Decoder. Field1 is
// normally 0 or 2 bytes (a single 608 pair); an idle frame carries 0.
type DecodeUnit struct {
	TimeMS int64
	Field1 []byte
}

// CuesFromUnits drives a cta608.Decoder over timed field-1 pairs and Segments the
// displayed-Screen changes into cues (SPEC §8.2). It feeds one unit at a time and
// samples the Screen whenever cta608.Decoder.Changed reports a boundary, tagging
// the cut with that unit's time.
//
// Feeding pair-by-pair keeps per-frame timing. cta608.Decoder carries its parse state
// across Feed calls, so this decodes identically to feeding the whole stream at once —
// which matters for the two constructs that straddle a pair boundary: a doubled control
// code (a doubled roll-up CR must scroll once, not twice) and an extended character,
// whose fallback and glyph are always in different units here.
func CuesFromUnits(units []DecodeUnit, opts cue.SegmentOptions) ([]cue.TimedCue, error) {
	var dec cta608.Decoder
	var changes []cue.TimedScreen
	for _, u := range units {
		if len(u.Field1) == 0 {
			continue
		}
		if err := dec.Feed(u.Field1); err != nil {
			return nil, fmt.Errorf("convert: decoding field-1 pairs: %w", err)
		}
		if dec.Changed() {
			changes = append(changes, cue.TimedScreen{
				Time:   time.Duration(u.TimeMS) * time.Millisecond,
				Screen: dec.Screen(),
				// The mode lets Segment coalesce roll-up and paint-on, which change
				// the displayed screen on every byte pair, without merging distinct
				// pop-on captions.
				Mode: dec.Mode(),
			})
		}
	}
	return cue.Segment(changes, opts), nil
}

// unitsFromPairs converts SCC per-frame pairs into timed decode units, mapping a
// frame number to time at the given fps.
func unitsFromPairs(pairs []scc.TimedPair, fps float64) []DecodeUnit {
	frameDurMS := 1000.0 / fps
	units := make([]DecodeUnit, 0, len(pairs))
	for _, p := range pairs {
		units = append(units, DecodeUnit{
			TimeMS: int64(math.Round(float64(p.Frame) * frameDurMS)),
			Field1: p.Pair,
		})
	}
	return units
}

// sccEntriesFromCues compiles cues to pop-on token transitions, schedules them at
// opts.FPS draining one field-1 pair per frame, and groups the successive-frame
// pairs into SCC entries.
func sccEntriesFromCues(cues []cue.TimedCue, opts Options) ([]scc.Entry, error) {
	tokens := cue.Compile(cues)
	sched := schedule.NewScheduler(opts.FPS,
		schedule.WithCCCountPolicy(opts.CCCount), schedule.WithFlipTiming(opts.FlipTiming))
	var lastMS int64
	for _, tt := range tokens {
		ms := tt.Time.Milliseconds()
		sched.Push(schedule.TimedTokens{TimeMS: ms, Field: 1, Tokens: tt.Tokens})
		if ms > lastMS {
			lastMS = ms
		}
	}

	frameDurMS := 1000.0 / opts.FPS
	// Safety cap: bound the drain far above any real caption so a scheduler bug
	// can't loop forever. The tail drains one pair per frame after the last push.
	maxFrames := int(math.Round(float64(lastMS)/frameDurMS)) + 100_000
	var timed []scc.TimedPair
	for frame := 0; frame <= maxFrames; frame++ {
		frameMS := int64(math.Round(float64(frame) * frameDurMS))
		fr := sched.Frame(frameMS)
		if len(fr.Field1) == 2 {
			timed = append(timed, scc.TimedPair{Frame: frame, Pair: fr.Field1})
			continue
		}
		if frameMS >= lastMS {
			break // past the last push with nothing left to drain: queue is empty
		}
	}
	return scc.GroupPairs(timed), nil
}
