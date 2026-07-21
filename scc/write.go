package scc

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// sccHeader is the Scenarist SCC v1.0 signature every file begins with. Write
// always emits it and Read requires it, so the header is part of the byte-exact
// contract.
const sccHeader = "Scenarist_SCC V1.0"

// Write serializes an SCCFile to Scenarist SCC text. It is deliberately dumb: it
// emits the header, then one line per Entry — the entry's timecode (derived from
// its absolute Frame via the file's FPS/DropFrame) then its byte pairs, formatted
// verbatim as space-separated lowercase 4-hex-digit groups. It applies no
// grouping, idle-gap, or one-pair-per-frame policy: the caller already decided
// what pairs sit on each line when it built the Entries (S4; GroupPairs is the
// optional helper for that). A blank line separates the header and every entry,
// matching conventional SCC and making Read→Write byte-exact.
func Write(w io.Writer, f *SCCFile) error {
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(bw, "%s\n", sccHeader); err != nil {
		return err
	}
	for _, e := range f.Entries {
		tc := FrameToTimecode(e.Frame, f.FPS, f.DropFrame)
		if _, err := fmt.Fprintf(bw, "\n%s\t%s\n", tc, formatPairs(e.Pairs)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// formatPairs renders raw bytes as the SCC pair format: space-separated lowercase
// 4-hex-digit groups, two bytes each. A lone trailing byte (which a well-formed
// Entry never has) is emitted as a 2-digit group rather than dropped, so no data
// is silently lost.
func formatPairs(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 2 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		if i+1 < len(b) {
			fmt.Fprintf(&sb, "%02x%02x", b[i], b[i+1])
		} else {
			fmt.Fprintf(&sb, "%02x", b[i])
		}
	}
	return sb.String()
}
