package scc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Canonical time in this package is an absolute integer frame number counted
// from 00:00:00:00 (SPEC §8.1 / design note S2): exact and drop/non-drop
// agnostic once parsed, with no float drift. The timecode string is the only
// place the drop-frame convention appears, so every bit of drop-frame reasoning
// is confined to the two conversion functions below.

// fpsNTSC30 and fpsNTSC60 are the two fractional NTSC rates, stored verbatim in
// SCCFile.FPS so timecode conversion (and any real-time base a caller derives)
// stays exact. Drop-frame timecode applies only to these.
const (
	fpsNTSC30 = 30000.0 / 1001.0 // 29.97
	fpsNTSC60 = 60000.0 / 1001.0 // 59.94
)

// nominalRate is the integer label rate used to lay out a timecode's frame field
// — round(fps). 29.97 and 30 both label with 30 frames per second, 59.94 and 60
// with 60. It is the divisor of the HH:MM:SS:FF decomposition and the modulus
// the frame field must stay below.
func nominalRate(fps float64) int {
	return int(math.Round(fps))
}

// supportsDropFrame reports whether fps is one of the fractional NTSC rates that
// SMPTE drop-frame timecode applies to (29.97 or 59.94). Drop-frame exists only
// to reconcile a fractional rate with its integer nominal label rate, so PAL (25)
// and the exact integer rates (24/30/50/60) are always non-drop (S2/S3).
func supportsDropFrame(fps float64) bool {
	n := nominalRate(fps)
	if n != 30 && n != 60 {
		return false
	}
	// A fractional rate differs from its nominal (29.97 vs 30, 59.94 vs 60); an
	// exact 30.0 or 60.0 does not, and stays non-drop.
	return math.Abs(fps-float64(n)) > 1e-6
}

// dropFramesPerMinute is the count of frame labels skipped at each dropped-minute
// boundary: 2 for the 30-nominal family, 4 for the 60-nominal family
// (round(nominal × 0.066666), the SMPTE ratio that keeps label time tracking real
// time to within a frame). Only meaningful for drop-frame rates.
func dropFramesPerMinute(fps float64) int {
	return int(math.Round(float64(nominalRate(fps)) * 0.066666))
}

// FrameToTimecode formats an absolute frame number as an SCC timecode string.
// When drop is true and fps is a fractional NTSC rate (29.97/59.94) it emits a
// true SMPTE drop-frame timecode with a ';' before the frame field; otherwise it
// emits a non-drop timecode using ':' throughout. drop is ignored (treated as
// false) for rates that cannot be drop-frame — PAL (25) and the integer rates —
// so the result always denotes a real, existing label (SPEC §8.1).
//
// True drop-frame: the frame labels 0 and 1 (0..3 at 59.94) at the top of every
// minute are skipped, except at every tenth minute (00, 10, 20, …). This is the
// conversion the media-tools/SVTA prior art gets wrong; go-608 implements it
// exactly, so frame↔timecode round-trips at and across every minute boundary.
func FrameToTimecode(frame int, fps float64, drop bool) string {
	nominal := nominalRate(fps)
	if !drop || !supportsDropFrame(fps) {
		// Non-drop: the label count is the frame count itself.
		return formatTimecode(frame, nominal, false)
	}
	dropFrames := dropFramesPerMinute(fps)
	// Real frames in ten nominal minutes (nine of them dropped) and in one dropped
	// minute. For the 30-nominal family: 18000-18 = 17982 per 10 min, 1800-2 =
	// 1798 per min.
	framesPer10Min := nominal*600 - dropFrames*9
	framesPerMin := nominal*60 - dropFrames
	blocks := frame / framesPer10Min
	rem := frame % framesPer10Min
	// Labels skipped so far = nine dropped minutes per complete 10-minute block,
	// plus one dropped minute per whole minute elapsed in the current block. The
	// block's first minute (frames 0..nominal*60) is the undropped tenth minute.
	labelsAdded := blocks * 9 * dropFrames
	if rem >= nominal*60 {
		labelsAdded += dropFrames * (1 + (rem-nominal*60)/framesPerMin)
	}
	return formatTimecode(frame+labelsAdded, nominal, true)
}

// TimecodeToFrame parses an SCC timecode string into an absolute frame number.
// It accepts both separators: a ';' before the frame field marks a drop-frame
// timecode and ':' a non-drop one (real files come both ways — media-tools writes
// non-drop, SVTA drop). The returned drop reports whether drop-frame decoding was
// applied, which is true only when the source used ';' and fps is a fractional
// NTSC rate that supports it; for any other rate the timecode is read as non-drop
// regardless of separator (SPEC §8.1). It is the exact inverse of FrameToTimecode
// for well-formed input.
func TimecodeToFrame(tc string, fps float64) (frame int, drop bool, err error) {
	hh, mm, ss, ff, sepDrop, err := parseTimecodeFields(tc)
	if err != nil {
		return 0, false, err
	}
	nominal := nominalRate(fps)
	if ff >= nominal {
		return 0, false, fmt.Errorf("scc: timecode %q frame field %d out of range for %g fps (max %d)",
			tc, ff, fps, nominal-1)
	}
	label := ((hh*60+mm)*60+ss)*nominal + ff
	drop = sepDrop && supportsDropFrame(fps)
	if !drop {
		return label, drop, nil
	}
	dropFrames := dropFramesPerMinute(fps)
	totalMinutes := hh*60 + mm
	// Subtract the labels skipped before this minute: dropFrames per elapsed
	// minute, except the every-tenth minute (totalMinutes/10 of them undropped).
	skipped := dropFrames * (totalMinutes - totalMinutes/10)
	return label - skipped, drop, nil
}

// formatTimecode decomposes a nominal-rate label count into HH:MM:SS<sep>FF, with
// sep ';' for drop-frame and ':' for non-drop.
func formatTimecode(label, nominal int, drop bool) string {
	ff := label % nominal
	totalSeconds := label / nominal
	ss := totalSeconds % 60
	mm := (totalSeconds / 60) % 60
	hh := totalSeconds / 3600
	sep := byte(':')
	if drop {
		sep = ';'
	}
	return fmt.Sprintf("%02d:%02d:%02d%c%02d", hh, mm, ss, sep, ff)
}

// parseTimecodeFields splits "HH:MM:SS:FF" / "HH:MM:SS;FF" into its four integer
// fields, reporting whether a ';' (the drop-frame marker) was present anywhere.
func parseTimecodeFields(tc string) (hh, mm, ss, ff int, sepDrop bool, err error) {
	tc = strings.TrimSpace(tc)
	sepDrop = strings.ContainsRune(tc, ';')
	fields := strings.FieldsFunc(tc, func(r rune) bool { return r == ':' || r == ';' })
	if len(fields) != 4 {
		return 0, 0, 0, 0, false, fmt.Errorf("scc: malformed timecode %q: want HH:MM:SS:FF", tc)
	}
	vals := make([]int, 4)
	for i, f := range fields {
		v, perr := strconv.Atoi(f)
		if perr != nil || v < 0 {
			return 0, 0, 0, 0, false, fmt.Errorf("scc: malformed timecode %q: bad field %q", tc, f)
		}
		vals[i] = v
	}
	return vals[0], vals[1], vals[2], vals[3], sepDrop, nil
}
