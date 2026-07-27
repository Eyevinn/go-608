package carriage

import (
	"encoding/binary"
	"fmt"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
)

// This file is the sample-level half of the carriage seam: getting an SEI NAL unit
// into, and back out of, one mp4 video sample. FrameSEINALU builds the NAL unit and
// FieldPairs reads them back, but a consumer also has to split a sample into its NAL
// units and splice the SEI into the right place — logic that is identical for every
// consumer and was, before this, copied into each of them (go-608's own commands,
// livesim2 and moqlivemock). It lives here so there is one implementation.
//
// Length framing is the AVC/HEVC in-mp4 form: a 4-byte big-endian length before each
// NAL unit. It is the same for both codecs, so only VCL detection takes a Codec.

// SampleNALUs splits a length-prefixed sample — a 4-byte big-endian length before
// each NAL unit, the AVC/HEVC in-mp4 format — into its NAL units. It is the exact
// inverse of PrefixNALUs.
//
// The returned slices alias sample; do not modify them in place. A truncated length
// prefix, a length that overruns the sample, or a zero-length NAL unit is an error
// rather than a silent truncation, so a non-video or corrupt sample is reported
// instead of decoding as empty.
func SampleNALUs(sample []byte) ([][]byte, error) {
	var nalus [][]byte
	for pos := 0; pos < len(sample); {
		if pos+4 > len(sample) {
			return nil, fmt.Errorf("carriage: truncated 4-byte NAL length prefix at byte %d of %d", pos, len(sample))
		}
		n := int64(binary.BigEndian.Uint32(sample[pos:]))
		pos += 4
		if n == 0 {
			return nil, fmt.Errorf("carriage: zero-length NAL unit at byte %d", pos)
		}
		if int64(pos)+n > int64(len(sample)) {
			return nil, fmt.Errorf("carriage: NAL length %d at byte %d overruns %d-byte sample", n, pos, len(sample))
		}
		nalus = append(nalus, sample[pos:pos+int(n)])
		pos += int(n)
	}
	return nalus, nil
}

// PrefixNALUs concatenates bare NAL units into a sample, writing a 4-byte big-endian
// length before each. It is the inverse of SampleNALUs and the form mp4ff reads and
// writes as mp4.FullSample.Data.
func PrefixNALUs(nalus ...[]byte) []byte {
	size := 0
	for _, n := range nalus {
		size += 4 + len(n)
	}
	out := make([]byte, 0, size)
	var l [4]byte
	for _, n := range nalus {
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// SpliceSEIBeforeVCL inserts a bare SEI NAL unit into a length-prefixed sample
// immediately before its first VCL (coded-slice) NAL unit, returning the rewritten
// sample. This is the encode-side splice of SPEC §6: the caption SEI rides ahead of
// the picture data in the same access unit.
//
// seiNALU is a bare NAL unit already carrying its codec NAL header — what
// FrameSEINALU returns — with no length prefix; SpliceSEIBeforeVCL adds the prefix.
// codec selects the VCL-detection rules. Any NAL units already in the sample,
// including other SEI messages, are preserved in order.
//
// A sample with no VCL NAL unit carries no picture for the SEI to precede. The SEI is
// then appended at the end, which preserves the existing NAL order exactly — putting
// it first would place it ahead of an access-unit delimiter or parameter set, which
// is a worse guess than the end. Nothing is dropped either way.
//
// The typical caller loops over a fragment's samples, and must grow the sample size
// alongside the data:
//
//	data, err := carriage.SpliceSEIBeforeVCL(s.Data, seiNALU, codec)
//	if err != nil {
//		return err
//	}
//	s.Data, s.Size = data, uint32(len(data))
func SpliceSEIBeforeVCL(sample, seiNALU []byte, codec Codec) ([]byte, error) {
	nalus, err := SampleNALUs(sample)
	if err != nil {
		return nil, err
	}
	insertAt := len(nalus)
	for i, n := range nalus {
		if IsVCL(n, codec) {
			insertAt = i
			break
		}
	}
	out := make([][]byte, 0, len(nalus)+1)
	out = append(out, nalus[:insertAt]...)
	out = append(out, seiNALU)
	out = append(out, nalus[insertAt:]...)
	return PrefixNALUs(out...), nil
}

// IsVCL reports whether a bare NAL unit (no length prefix) is a video coding layer
// (coded-slice) NAL for the codec — the anchor SpliceSEIBeforeVCL splices before. An
// empty NAL unit, or a Codec outside CodecAVC/CodecHEVC, is not a VCL NAL.
func IsVCL(nalu []byte, codec Codec) bool {
	if len(nalu) == 0 {
		return false
	}
	switch codec {
	case CodecAVC:
		return avc.IsVideoNaluType(avc.GetNaluType(nalu[0]))
	case CodecHEVC:
		return hevc.IsVideoNaluType(hevc.GetNaluType(nalu[0]))
	default:
		return false
	}
}
