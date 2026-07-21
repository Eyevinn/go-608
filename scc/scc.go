package scc

// Entry is one SCC line: a run of raw CTA-608 byte pairs whose first pair sits at
// absolute frame Frame and whose subsequent pairs sit on the immediately
// following frames (pair i at Frame+i — the standard SCC "successive frames"
// reading). Pairs is the verbatim wire bytes, parity and all; this package never
// interprets them (the cta608 core owns all 608 semantics), which is what makes
// the round-trip byte-exact. len(Pairs) is normally even (whole 2-byte pairs).
type Entry struct {
	Frame int
	Pairs []byte
}

// SCCFile is a parsed Scenarist SCC file: its frame rate, whether its timecodes
// are drop-frame, and its ordered entries. FPS and DropFrame make formatting
// deterministic — Read fills them by inference or override (S3) and Write consumes
// them — so that (FPS, DropFrame, Entries) alone reproduce the file byte-exact.
type SCCFile struct {
	FPS       float64
	DropFrame bool
	Entries   []Entry
}

// TimedPair is one CTA-608 byte pair at an absolute frame number — the flattened,
// per-frame unit the cta608 core consumes. Pair is two bytes (verbatim wire).
type TimedPair struct {
	Frame int
	Pair  []byte
}

// TimedPairs flattens every entry into per-frame byte pairs, assigning pair i of
// an entry to frame Entry.Frame+i (the successive-frames reading, S5). The raw
// Entry bytes are what round-trip byte-exact; this flatten is the one place the
// successive-frame interpretation is imposed, so a caller who deliberately packs
// several pairs onto one line still round-trips via the raw entries. Concatenate
// the Pair bytes of one channel and feed them to cta608.Parse (or a
// cta608.Decoder) for tokens / a Screen carrying per-frame timing.
func (f *SCCFile) TimedPairs() []TimedPair {
	var out []TimedPair
	for _, e := range f.Entries {
		for i := 0; i+1 < len(e.Pairs); i += 2 {
			// A three-index slice caps the shared window so a caller appending to
			// Pair cannot clobber the entry's backing array.
			out = append(out, TimedPair{Frame: e.Frame + i/2, Pair: e.Pairs[i : i+2 : i+2]})
		}
	}
	return out
}

// GroupPairs coalesces a flat stream of timed byte pairs into sparse Entries,
// starting a new Entry wherever a pair's frame is not the immediate successor of
// the current run (an idle gap). It is the practical inverse of TimedPairs and a
// convenience for turning a scheduled cta608.Serialize stream into SCC lines; it
// is deliberately NOT the writer (Write stays dumb and verbatim, S4), so a caller
// who needs a different pairs-per-line policy can build Entries directly instead.
//
// It expects pairs ordered by frame with one 2-byte pair per successive frame;
// two adjacent runs that happen to be frame-contiguous are therefore merged into
// one Entry (line grouping is not recovered — that is why Write is separate).
func GroupPairs(pairs []TimedPair) []Entry {
	var entries []Entry
	for _, p := range pairs {
		if n := len(entries); n > 0 {
			last := &entries[n-1]
			if p.Frame == last.Frame+len(last.Pairs)/2 {
				last.Pairs = append(last.Pairs, p.Pair...)
				continue
			}
		}
		entries = append(entries, Entry{Frame: p.Frame, Pairs: append([]byte(nil), p.Pair...)})
	}
	return entries
}
