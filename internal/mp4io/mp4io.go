// Package mp4io holds the shared fragmented-mp4 read/write and NAL-splice glue
// used by the go-608 commands: go608-clock spliced-in the wall-clock 608 SEI with
// it today, and go608-info / go608-extract reuse the read side (locating the video
// track and splitting a sample into NAL units). It is deliberately internal — it
// wraps mp4ff muxing details that are not part of go-608's public API (SPEC §9,
// package-layout note P5).
//
// The NAL helpers are codec-aware only where the codec matters: splitting and
// length-prefixing a sample are identical for AVC and HEVC (a 4-byte big-endian
// length before each NAL unit), while VCL detection and the splice point depend on
// the codec, so those dispatch on the carriage.Codec the caller already tracks.
package mp4io

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Track describes the elementary video track the tools operate on: its track ID,
// media timescale, and codec.
type Track struct {
	ID        uint32
	Timescale uint32
	Codec     carriage.Codec
}

// VideoTrack locates the video track of a decoded fragmented mp4 and returns its
// ID, media timescale, and codec, plus the trex needed to expand each fragment's
// samples (default sample flags/duration live in the trex, not the fragment). It
// returns the first "vide"-handler track whose sample entry is AVC or HEVC.
//
// It errors when the file has no moov (an uninitialized segment), is not
// fragmented (no mvex/trex), or has no AVC/HEVC video track.
func VideoTrack(f *mp4.File) (Track, *mp4.TrexBox, error) {
	moov := f.Moov
	if moov == nil && f.Init != nil {
		moov = f.Init.Moov
	}
	if moov == nil {
		return Track{}, nil, fmt.Errorf("mp4io: no moov box (not an initialized mp4)")
	}
	if moov.Mvex == nil {
		return Track{}, nil, fmt.Errorf("mp4io: no mvex box (not a fragmented mp4)")
	}
	for _, trak := range moov.Traks {
		if trak.Tkhd == nil || trak.Mdia == nil || trak.Mdia.Hdlr == nil || trak.Mdia.Hdlr.HandlerType != "vide" {
			continue
		}
		if trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stsd == nil {
			continue
		}
		stsd := trak.Mdia.Minf.Stbl.Stsd
		var codec carriage.Codec
		switch {
		case stsd.AvcX != nil:
			codec = carriage.CodecAVC
		case stsd.HvcX != nil:
			codec = carriage.CodecHEVC
		default:
			return Track{}, nil, fmt.Errorf("mp4io: video track %d is neither AVC nor HEVC", trak.Tkhd.TrackID)
		}
		id := trak.Tkhd.TrackID
		trex, ok := moov.Mvex.GetTrex(id)
		if !ok {
			return Track{}, nil, fmt.Errorf("mp4io: no trex for video track %d", id)
		}
		var timescale uint32
		if trak.Mdia.Mdhd != nil {
			timescale = trak.Mdia.Mdhd.Timescale
		}
		return Track{ID: id, Timescale: timescale, Codec: codec}, trex, nil
	}
	return Track{}, nil, fmt.Errorf("mp4io: no AVC/HEVC video track found")
}

// Samples flattens every fragment's samples across all media segments into one
// decode-order slice — the read unit the extract/info tools iterate. Each
// FullSample carries its Data (length-prefixed NAL units) and DecodeTime.
func Samples(f *mp4.File, trex *mp4.TrexBox) ([]mp4.FullSample, error) {
	var out []mp4.FullSample
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			s, err := frag.GetFullSamples(trex)
			if err != nil {
				return nil, fmt.Errorf("mp4io: expanding fragment samples: %w", err)
			}
			out = append(out, s...)
		}
	}
	return out, nil
}

// SampleInfo identifies one sample being rewritten by SpliceFragmented: its
// 0-based index across the whole file in decode order, and its absolute decode
// time in the track's media timescale. The SEIFunc uses these to pick the frame's
// 608 payload (by frame index or by media time).
type SampleInfo struct {
	Index      int
	DecodeTime uint64
}

// SEIFunc returns the bare SEI NAL unit to splice before the first VCL NAL of the
// given sample (already carrying its codec NAL header, e.g. from
// carriage.FrameSEINALU), or nil to leave that sample unchanged.
type SEIFunc func(SampleInfo) ([]byte, error)

// SpliceFragmented rewrites a single-video-track fragmented mp4, splicing the SEI
// that fn returns before the first VCL NAL of every sample, and encodes the result
// (the original init segment followed by the rebuilt media segments) to out. It is
// the shared write engine behind go608-clock and go608-inject: per-fragment
// sequence numbers, styp, and per-sample timing/flags/CTO are preserved, only the
// sample data grows by the spliced SEI. It errors unless f is an initialized
// fragmented mp4 with exactly one track.
func SpliceFragmented(f *mp4.File, track Track, trex *mp4.TrexBox, out io.Writer, fn SEIFunc) error {
	if f.Init == nil || f.Init.Moov == nil {
		return fmt.Errorf("mp4io: no moov in init segment (need an initialized fragmented mp4)")
	}
	if n := len(f.Init.Moov.Traks); n != 1 {
		return fmt.Errorf("mp4io: %d tracks; only single-video-track fragmented mp4 is supported", n)
	}

	frame := 0
	newSegments := make([]*mp4.MediaSegment, 0, len(f.Segments))
	for _, seg := range f.Segments {
		newSeg := mp4.NewMediaSegmentWithoutStyp()
		if seg.Styp != nil {
			newSeg = mp4.NewMediaSegmentWithStyp(seg.Styp)
		}
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				return fmt.Errorf("mp4io: expanding fragment samples: %w", err)
			}
			seq := uint32(1)
			if frag.Moof != nil && frag.Moof.Mfhd != nil {
				seq = frag.Moof.Mfhd.SequenceNumber
			}
			newFrag, err := mp4.CreateFragment(seq, track.ID)
			if err != nil {
				return fmt.Errorf("mp4io: creating fragment: %w", err)
			}
			for _, s := range samples {
				sei, err := fn(SampleInfo{Index: frame, DecodeTime: s.DecodeTime})
				if err != nil {
					return err
				}
				if len(sei) > 0 {
					data, err := SpliceSEIBeforeVCL(s.Data, sei, track.Codec)
					if err != nil {
						return fmt.Errorf("mp4io: splicing SEI into sample %d: %w", frame, err)
					}
					s.Data = data
					s.Size = uint32(len(data))
				}
				newFrag.AddFullSample(s)
				frame++
			}
			newSeg.AddFragment(newFrag)
		}
		newSegments = append(newSegments, newSeg)
	}

	if err := f.Init.Encode(out); err != nil {
		return fmt.Errorf("mp4io: encoding init segment: %w", err)
	}
	for _, seg := range newSegments {
		if err := seg.Encode(out); err != nil {
			return fmt.Errorf("mp4io: encoding media segment: %w", err)
		}
	}
	return nil
}

// SampleNALUs splits a length-prefixed sample — a 4-byte big-endian length before
// each NAL unit, the AVC/HEVC in-mp4 format — into its NAL units. It is the exact
// inverse of PrefixNALUs and is codec-independent (the length framing is the same
// for AVC and HEVC).
func SampleNALUs(sample []byte) ([][]byte, error) {
	var nalus [][]byte
	for pos := 0; pos < len(sample); {
		if pos+4 > len(sample) {
			return nil, fmt.Errorf("mp4io: truncated 4-byte NAL length prefix at byte %d of %d", pos, len(sample))
		}
		n := int64(binary.BigEndian.Uint32(sample[pos:]))
		pos += 4
		if n == 0 {
			return nil, fmt.Errorf("mp4io: zero-length NAL unit at byte %d", pos)
		}
		if int64(pos)+n > int64(len(sample)) {
			return nil, fmt.Errorf("mp4io: NAL length %d at byte %d overruns %d-byte sample", n, pos, len(sample))
		}
		nalus = append(nalus, sample[pos:pos+int(n)])
		pos += int(n)
	}
	return nalus, nil
}

// PrefixNALUs concatenates NAL units into a sample, writing a 4-byte big-endian
// length before each — the length-prefixed sample format mp4ff reads and writes.
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

// SpliceSEIBeforeVCL inserts a bare SEI NAL unit (already carrying its codec NAL
// header — e.g. from carriage.FrameSEINALU) immediately before the first VCL NAL
// unit of a length-prefixed sample, returning the rewritten sample. This is the
// encode-side splice of SPEC §6: the caption SEI rides ahead of the picture data
// in the same access unit.
//
// If the sample carries no VCL NAL unit, the SEI is placed first so no data is
// lost. Any SEI already present in the sample is preserved.
func SpliceSEIBeforeVCL(sample, seiNALU []byte, codec carriage.Codec) ([]byte, error) {
	nalus, err := SampleNALUs(sample)
	if err != nil {
		return nil, err
	}
	insertAt := 0
	for i, n := range nalus {
		if isVCL(n, codec) {
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

// isVCL reports whether a NAL unit is a video coding layer (picture) NAL for the
// codec — the anchor the SEI splices before.
func isVCL(nalu []byte, codec carriage.Codec) bool {
	if len(nalu) == 0 {
		return false
	}
	switch codec {
	case carriage.CodecAVC:
		return avc.IsVideoNaluType(avc.GetNaluType(nalu[0]))
	case carriage.CodecHEVC:
		return hevc.IsVideoNaluType(hevc.GetNaluType(nalu[0]))
	default:
		return false
	}
}
